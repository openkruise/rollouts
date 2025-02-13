/*
Copyright 2022 The Kruise Authors.

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

package util

import (
	"context"
	"strings"

	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IsPodReady returns true if a pod is ready; false otherwise.
func IsPodReady(pod *v1.Pod) bool {
	return IsPodReadyConditionTrue(pod.Status)
}

// IsPodReadyConditionTrue returns true if a pod is ready; false otherwise.
func IsPodReadyConditionTrue(status v1.PodStatus) bool {
	condition := GetPodReadyCondition(status)
	return condition != nil && condition.Status == v1.ConditionTrue
}

// GetPodReadyCondition extracts the pod ready condition from the given status and returns that.
// Returns nil if the condition is not present.
func GetPodReadyCondition(status v1.PodStatus) *v1.PodCondition {
	_, condition := GetPodCondition(&status, v1.PodReady)
	return condition
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	return GetPodConditionFromList(status.Conditions, conditionType)
}

// GetPodConditionFromList extracts the provided condition from the given list of condition and
// returns the index of the condition and the condition. Returns -1 and nil if the condition is not present.
func GetPodConditionFromList(conditions []v1.PodCondition, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if conditions == nil {
		return -1, nil
	}
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return i, &conditions[i]
		}
	}
	return -1, nil
}

// IsConsistentWithRevision return true if pod is match the revision
func IsConsistentWithRevision(labels map[string]string, revision string) bool {
	if labels[appsv1.DefaultDeploymentUniqueLabelKey] != "" &&
		strings.HasSuffix(revision, labels[appsv1.DefaultDeploymentUniqueLabelKey]) {
		return true
	}

	if labels[appsv1.ControllerRevisionHashLabelKey] != "" &&
		strings.HasSuffix(revision, labels[appsv1.ControllerRevisionHashLabelKey]) {
		return true
	}
	return false
}

// IsEqualRevision return true if a and b have equal revision label
func IsEqualRevision(a, b *v1.Pod) bool {
	if a.Labels[appsv1.DefaultDeploymentUniqueLabelKey] != "" &&
		a.Labels[appsv1.DefaultDeploymentUniqueLabelKey] == b.Labels[appsv1.DefaultDeploymentUniqueLabelKey] {
		return true
	}

	if a.Labels[appsv1.ControllerRevisionHashLabelKey] != "" &&
		a.Labels[appsv1.ControllerRevisionHashLabelKey] == b.Labels[appsv1.ControllerRevisionHashLabelKey] {
		return true
	}
	return false
}

// FilterActivePods will filter out terminating pods
func FilterActivePods(pods []*v1.Pod) []*v1.Pod {
	var activePods []*v1.Pod
	for _, pod := range pods {
		if pod.DeletionTimestamp.IsZero() {
			activePods = append(activePods, pod)
		}
	}
	return activePods
}

// IsCompletedPod return true if pod is at Failed or Succeeded phase
func IsCompletedPod(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded
}

// ListOwnedPods will list all pods belong to workload, including terminating pods
func ListOwnedPods(c client.Client, workload client.Object) ([]*v1.Pod, error) {
	selector, err := getSelector(workload)
	if err != nil {
		return nil, err
	}

	podLister := &v1.PodList{}
	err = c.List(context.TODO(), podLister, &client.ListOptions{LabelSelector: selector, Namespace: workload.GetNamespace()}, utilclient.DisableDeepCopy)
	if err != nil {
		return nil, err
	}
	pods := make([]*v1.Pod, 0, len(podLister.Items))
	for i := range podLister.Items {
		pod := &podLister.Items[i]
		if IsCompletedPod(pod) {
			continue
		}
		// we should find their indirect owner-relationship,
		// such as pod -> replicaset -> deployment
		owned, err := IsOwnedBy(c, pod, workload)
		if err != nil {
			return nil, err
		} else if !owned {
			continue
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

// WrappedPodCount return the number of pods which satisfy the filter
func WrappedPodCount(pods []*v1.Pod, filter func(pod *v1.Pod) bool) int {
	count := 0
	for _, pod := range pods {
		if filter(pod) {
			count++
		}
	}
	return count
}
