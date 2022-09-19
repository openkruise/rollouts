/*
Copyright 2019 The Kruise Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package statefulset

import (
	"context"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads/helper"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type statefulSetLikeController struct {
	client.Client
	recorder    record.EventRecorder
	pods        []*v1.Pod
	release     *appsv1alpha1.BatchRelease
	workloadObj client.Object
	workloadGvk schema.GroupVersionKind
	workloadKey types.NamespacedName
}

func newStatefulSetLikeController(c client.Client, r record.EventRecorder, br *appsv1alpha1.BatchRelease, key types.NamespacedName, gvk schema.GroupVersionKind) *statefulSetLikeController {
	return &statefulSetLikeController{
		Client:      c,
		recorder:    r,
		release:     br,
		workloadKey: key,
		workloadGvk: gvk,
	}
}

func (c *statefulSetLikeController) GetWorkloadObject() (client.Object, error) {
	if c.workloadObj == nil {
		workloadObj := util.GetEmptyWorkloadObject(c.workloadGvk)
		if workloadObj == nil {
			return nil, errors.NewNotFound(schema.GroupResource{Group: c.workloadGvk.Group, Resource: c.workloadGvk.Kind}, c.workloadKey.Name)
		}
		if err := c.Get(context.TODO(), c.workloadKey, workloadObj); err != nil {
			return nil, err
		}
		c.workloadObj = workloadObj
	}
	return c.workloadObj, nil
}

func (c *statefulSetLikeController) GetWorkloadInfo() (*util.WorkloadInfo, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return nil, err
	}

	workloadInfo := util.ParseStatefulSetInfo(set, c.workloadKey)
	workloadInfo.Paused = true
	if workloadInfo.Status.UpdatedReadyReplicas <= 0 {
		updatedReadyReplicas, err := c.countUpdatedReadyPods(workloadInfo.Status.UpdateRevision)
		if err != nil {
			return nil, err
		}
		workloadInfo.Status.UpdatedReadyReplicas = updatedReadyReplicas
	}

	return workloadInfo, nil
}

func (c *statefulSetLikeController) ClaimWorkload() (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	err = helper.ClaimWorkload(c.Client, c.release, set, map[string]interface{}{
		"type": apps.RollingUpdateStatefulSetStrategyType,
		"rollingUpdate": map[string]interface{}{
			"partition": pointer.Int32(util.GetReplicas(set)),
		},
	})
	if err != nil {
		return false, err
	}

	klog.V(3).Infof("Claim StatefulSet(%v) Successfully", c.workloadKey)
	return true, nil
}

func (c *statefulSetLikeController) ReleaseWorkload(cleanup bool) (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	err = helper.ReleaseWorkload(c.Client, set)
	if err != nil {
		c.recorder.Eventf(c.release, v1.EventTypeWarning, "ReleaseFailed", err.Error())
		return false, err
	}

	klog.V(3).Infof("Release StatefulSet(%v) Successfully", c.workloadKey)
	return true, nil
}

func (c *statefulSetLikeController) UpgradeBatch(canaryReplicasGoal, stableReplicasGoal int32) (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	// if no needs to patch partition
	partition := util.GetStatefulSetPartition(set)
	if partition <= stableReplicasGoal {
		return true, nil
	}

	err = helper.PatchSpec(c.Client, set, map[string]interface{}{
		"updateStrategy": map[string]interface{}{
			"rollingUpdate": map[string]interface{}{
				"partition": pointer.Int32(stableReplicasGoal),
			},
		},
	})
	if err != nil {
		return false, err
	}

	klog.V(3).Infof("Upgrade StatefulSet(%v) Partition to %v Successfully", c.workloadKey, stableReplicasGoal)
	return true, nil
}

func (c *statefulSetLikeController) IsOrderedUpdate() (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	return !util.IsStatefulSetUnorderedUpdate(set), nil
}

func (c *statefulSetLikeController) IsBatchReady(canaryReplicasGoal, stableReplicasGoal int32) (bool, error) {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	// ignore this corner case
	if canaryReplicasGoal <= 0 {
		return true, nil
	}

	// first: make sure that the canary goal is met
	firstCheckPointReady := workloadInfo.Status.UpdatedReplicas >= canaryReplicasGoal
	if !firstCheckPointReady {
		return false, nil
	}

	updatedReadyReplicas := int32(0)
	// TODO: add updatedReadyReplicas for advanced statefulset
	if workloadInfo.Status.UpdatedReadyReplicas > 0 {
		updatedReadyReplicas = workloadInfo.Status.UpdatedReadyReplicas
	} else {
		updatedReadyReplicas, err = c.countUpdatedReadyPods(workloadInfo.Status.UpdateRevision)
		if err != nil {
			return false, err
		}
	}

	maxUnavailable := 0
	if workloadInfo.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetScaledValueFromIntOrPercent(workloadInfo.MaxUnavailable, int(canaryReplicasGoal), true)
	}

	secondCheckPointReady := func() bool {
		// 1. make sure updatedReadyReplicas has achieved the goal
		return updatedReadyReplicas+int32(maxUnavailable) >= canaryReplicasGoal &&
			// 2. make sure at latest one updated pod is available if canaryReplicasGoal != 0
			(canaryReplicasGoal == 0 || updatedReadyReplicas >= 1)
	}
	return secondCheckPointReady(), nil
}

func (c *statefulSetLikeController) ListOwnedPods() ([]*v1.Pod, error) {
	if c.pods != nil {
		return c.pods, nil
	}
	set, err := c.GetWorkloadObject()
	if err != nil {
		return nil, err
	}
	c.pods, err = util.ListOwnedPods(c.Client, set)
	return c.pods, err
}

func (c *statefulSetLikeController) countUpdatedReadyPods(updateRevision string) (int32, error) {
	pods, err := c.ListOwnedPods()
	if err != nil {
		return 0, err
	}
	activePods := util.FilterActivePods(pods)
	updatedReadyReplicas := int32(0)
	for _, pod := range activePods {
		if util.IsConsistentWithRevision(pod, updateRevision) && util.IsPodReady(pod) {
			updatedReadyReplicas++
		}
	}
	return updatedReadyReplicas, nil
}
