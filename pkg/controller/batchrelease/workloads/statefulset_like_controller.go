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
	"fmt"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type StatefulSetLikeController struct {
	client.Client
	recorder       record.EventRecorder
	planController *appsv1alpha1.BatchRelease
	namespacedName types.NamespacedName
	gvk            schema.GroupVersionKind
	statefulSet    *unstructured.Unstructured
	pods           []*v1.Pod
}

func NewStatefulSetLikeController(c client.Client, r record.EventRecorder, p *appsv1alpha1.BatchRelease, n types.NamespacedName, gvk schema.GroupVersionKind) UnifiedWorkloadController {
	return &StatefulSetLikeController{
		Client:         c,
		recorder:       r,
		planController: p,
		namespacedName: n,
		gvk:            gvk,
	}
}

func (c *StatefulSetLikeController) GetWorkloadObject() (*unstructured.Unstructured, error) {
	if c.statefulSet == nil {
		c.statefulSet = &unstructured.Unstructured{}
		c.statefulSet.SetGroupVersionKind(c.gvk)
		if err := c.Get(context.TODO(), c.namespacedName, c.statefulSet); err != nil {
			return nil, err
		}
	}
	return c.statefulSet, nil
}

func (c *StatefulSetLikeController) GetWorkloadInfo() (*util.WorkloadInfo, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return nil, err
	}

	workloadInfo := util.ParseWorkloadInfo(set, c.namespacedName)
	workloadInfo.Paused = true
	return workloadInfo, nil
}

func (c *StatefulSetLikeController) ClaimWorkload() (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	err = util.ClaimWorkload(c.Client, c.planController, set, map[string]interface{}{
		"rollingUpdate": map[string]interface{}{
			"partition": pointer.Int32(util.ParseReplicasFrom(set)),
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

	err = util.ReleaseWorkload(c.Client, set)
	if err != nil {
		c.recorder.Eventf(c.planController, v1.EventTypeWarning, "ReleaseCloneSetFailed", err.Error())
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

	observedReplicas := canaryReplicasGoal + stableReplicasGoal
	if observedReplicas != util.ParseReplicasFrom(set) {
		return false, fmt.Errorf("StatefulSet(%v) scaled, should handle scale event first", c.namespacedName)
	}

	err = util.PatchUpdateStrategy(c.Client, set, map[string]interface{}{
		"rollingUpdate": map[string]interface{}{
			"partition": pointer.Int32(stableReplicasGoal),
		},
	})
	if err != nil {
		return false, err
	}

	klog.V(3).Infof("Upgrade StatefulSet(%v) Partition to %v Successfully", c.namespacedName, stableReplicasGoal)
	return true, nil
}

func (c *StatefulSetLikeController) IsBatchUpgraded(canaryReplicasGoal, stableReplicasGoal int32) (bool, error) {
	set, err := c.GetWorkloadObject()
	if err != nil {
		return false, err
	}

	if !util.IsRollingUpdateStrategy(set) {
		return false, fmt.Errorf("StatefulSet(%v) rollingUpdate configuration is nil, should check it maully", c.namespacedName)
	}

	partition := util.ParseInt32PartitionFrom(set)
	return partition <= stableReplicasGoal, nil
}

func (c *StatefulSetLikeController) IsBatchReady(canaryReplicasGoal, stableReplicasGoal int32) (bool, error) {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	// ignore this corner case
	if canaryReplicasGoal <= 0 {
		return true, nil
	}

	// first: make sure all pod is available, and the canary goal is met
	firstCheckPointReady := workloadInfo.Status.ReadyReplicas == workloadInfo.Status.Replicas &&
		workloadInfo.Status.UpdatedReplicas >= canaryReplicasGoal
	if !firstCheckPointReady {
		return false, nil
	}

	updatedReadyReplicas := int32(0)
	if workloadInfo.Status.UpdatedReadyReplicas > 0 {
		updatedReadyReplicas = workloadInfo.Status.UpdatedReadyReplicas
	} else {
		updatedReadyReplicas, err = c.CountUpdatedReadyPods(workloadInfo.Status.UpdateRevision)
		if err != nil {
			return false, err
		}
	}

	maxUnavailable := 0
	if workloadInfo.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetScaledValueFromIntOrPercent(workloadInfo.MaxUnavailable, int(canaryReplicasGoal), true)
	}

	// second: make sure the updated replicas are available
	secondCheckPointReady := updatedReadyReplicas+int32(maxUnavailable) >= canaryReplicasGoal
	return secondCheckPointReady, nil
}

func (c *StatefulSetLikeController) listOwnedPods() error {
	if c.pods != nil {
		return nil
	}
	set, err := c.GetWorkloadObject()
	if err != nil {
		return err
	}
	selector, err := util.ParseSelector(set)
	if err != nil || selector == nil {
		return err
	}
	podLister := &v1.PodList{}
	err = c.List(context.TODO(), podLister, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	c.pods = make([]*v1.Pod, 0)
	for i := range podLister.Items {
		pod := &podLister.Items[i]
		owner := metav1.GetControllerOf(pod)
		if owner == nil || owner.UID != set.GetUID() {
			continue
		}
		c.pods = append(c.pods, pod)
	}
	return nil
}

func (c *StatefulSetLikeController) CountUpdatedReadyPods(updateRevision string) (int32, error) {
	err := c.listOwnedPods()
	if err != nil {
		return 0, err
	}
	updatedReadyReplicas := int32(0)
	for _, pod := range c.pods {
		switch updateRevision {
		case pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey], pod.Labels[appsv1.ControllerRevisionHashLabelKey]:
			updatedReadyReplicas++
			continue
		}

		switch fmt.Sprintf("%v-%v", c.statefulSet.GetName(), updateRevision) {
		case pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey], pod.Labels[appsv1.ControllerRevisionHashLabelKey]:
			updatedReadyReplicas++
			continue
		}
	}
	return updatedReadyReplicas, nil
}
