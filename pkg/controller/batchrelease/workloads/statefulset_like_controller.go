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

package workloads

import (
	"context"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type StatefulSetLikeController struct {
	client.Client
	recorder       record.EventRecorder
	release        *appsv1alpha1.BatchRelease
	namespacedName types.NamespacedName
	workloadObj    client.Object
	gvk            schema.GroupVersionKind
	pods           []*v1.Pod
}

func NewStatefulSetLikeController(c client.Client, r record.EventRecorder, b *appsv1alpha1.BatchRelease, n types.NamespacedName, gvk schema.GroupVersionKind) UnifiedWorkloadController {
	return &StatefulSetLikeController{
		Client:         c,
		recorder:       r,
		release:        b,
		namespacedName: n,
		gvk:            gvk,
	}
}

func (c *StatefulSetLikeController) GetWorkloadObject() (client.Object, error) {
	if c.workloadObj == nil {
		workloadObj := util.GetEmptyWorkloadObject(c.gvk)
		if workloadObj == nil {
			return nil, errors.NewNotFound(schema.GroupResource{Group: c.gvk.Group, Resource: c.gvk.Kind}, c.namespacedName.Name)
		}
		if err := c.Get(context.TODO(), c.namespacedName, workloadObj); err != nil {
			return nil, err
		}
		c.workloadObj = workloadObj
	}
	return c.workloadObj, nil
}

func (c *StatefulSetLikeController) GetWorkloadInfo() (*util.WorkloadInfo, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return nil, err
	}

	workloadInfo := util.ParseStatefulSetInfo(set, c.namespacedName)
	workloadInfo.Paused = true
	if workloadInfo.Status.UpdatedReadyReplicas <= 0 {
		pods, err := c.ListOwnedPods()
		if err != nil {
			return nil, err
		}
		updatedReadyReplicas, err := c.countUpdatedReadyPods(pods, workloadInfo.Status.UpdateRevision)
		if err != nil {
			return nil, err
		}
		workloadInfo.Status.UpdatedReadyReplicas = updatedReadyReplicas
	}

	return workloadInfo, nil
}

func (c *StatefulSetLikeController) ClaimWorkload() (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	err = claimWorkload(c.Client, c.release, set, map[string]interface{}{
		"type": apps.RollingUpdateStatefulSetStrategyType,
		"rollingUpdate": map[string]interface{}{
			"partition": pointer.Int32(util.GetReplicas(set)),
		},
	})
	if err != nil {
		return false, err
	}

	klog.V(3).Infof("Claim StatefulSet(%v) Successfully", c.namespacedName)
	return true, nil
}

func (c *StatefulSetLikeController) ReleaseWorkload(cleanup bool) (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	err = releaseWorkload(c.Client, set)
	if err != nil {
		c.recorder.Eventf(c.release, v1.EventTypeWarning, "ReleaseFailed", err.Error())
		return false, err
	}

	klog.V(3).Infof("Release StatefulSet(%v) Successfully", c.namespacedName)
	return true, nil
}

func (c *StatefulSetLikeController) UpgradeBatch(canaryReplicasGoal, stableReplicasGoal int32) (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	// if no needs to patch partition
	partition := util.GetStatefulSetPartition(set)
	if partition <= stableReplicasGoal {
		return true, nil
	}

	err = patchSpec(c.Client, set, map[string]interface{}{
		"updateStrategy": map[string]interface{}{
			"rollingUpdate": map[string]interface{}{
				"partition": pointer.Int32(stableReplicasGoal),
			},
		},
	})
	if err != nil {
		return false, err
	}

	klog.V(3).Infof("Upgrade StatefulSet(%v) Partition to %v Successfully", c.namespacedName, stableReplicasGoal)
	return true, nil
}

func (c *StatefulSetLikeController) IsOrderedUpdate() (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	return !util.IsStatefulSetUnorderedUpdate(set), nil
}

func (c *StatefulSetLikeController) ListOwnedPods() ([]*v1.Pod, error) {
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

func (c *StatefulSetLikeController) countUpdatedReadyPods(pods []*v1.Pod, updateRevision string) (int32, error) {
	activePods := util.FilterActivePods(pods)
	updatedReadyReplicas := int32(0)
	for _, pod := range activePods {
		if util.IsConsistentWithRevision(pod, updateRevision) && util.IsPodReady(pod) {
			updatedReadyReplicas++
		}
	}
	return updatedReadyReplicas, nil
}
