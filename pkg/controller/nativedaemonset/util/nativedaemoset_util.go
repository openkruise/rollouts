/*
Copyright 2025 The Kruise Authors.

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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
)

// HasPodsBeingDeleted checks if there are any pods currently being deleted
func HasPodsBeingDeleted(pods []*corev1.Pod) bool {
	podsBeingDeleted := int32(0)
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			klog.Infof("Pod %s/%s is being deleted", pod.Namespace, pod.Name)
			podsBeingDeleted++
		}
	}

	if podsBeingDeleted > 0 {
		klog.Infof("Found %d pods being deleted, skipping this reconcile", podsBeingDeleted)
		return true
	}
	return false
}

// CalculateDesiredUpdatedReplicas parses partitionStr and returns desiredUpdatedReplicas based on totalPods
func CalculateDesiredUpdatedReplicas(partitionStr string, totalPods int) (int32, error) {
	var partition int32
	_, err := fmt.Sscanf(partitionStr, "%d", &partition)
	if err != nil {
		return 0, fmt.Errorf("invalid partition value %s: %v", partitionStr, err)
	}
	desiredUpdatedReplicas := int32(totalPods) - partition
	if desiredUpdatedReplicas < 0 {
		desiredUpdatedReplicas = 0
	}
	return desiredUpdatedReplicas, nil
}

// GetMaxUnavailable gets the maxUnavailable value from DaemonSet spec
func GetMaxUnavailable(daemon *appsv1.DaemonSet) int32 {
	maxUnavailable := int32(1) // Default value
	if daemon.Spec.UpdateStrategy.RollingUpdate != nil && daemon.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable != nil {
		maxUnavailableValue, err := intstr.GetScaledValueFromIntOrPercent(daemon.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable, int(daemon.Status.DesiredNumberScheduled), false)
		if err == nil && maxUnavailableValue > 0 {
			maxUnavailable = int32(maxUnavailableValue)
		}
	}
	return maxUnavailable
}

// ApplyDeletionConstraints applies maxUnavailable and available pod constraints
func ApplyDeletionConstraints(needToDelete int32, maxUnavailable int32, availablePodCount int) int32 {
	originalNeedToDelete := needToDelete

	// Ensure we don't exceed the maxUnavailable limit
	if needToDelete > maxUnavailable {
		needToDelete = maxUnavailable
		klog.Infof("Limited needToDelete from %d to %d due to maxUnavailable constraint",
			originalNeedToDelete, needToDelete)
	}

	// Limit deletion count to available pod count
	if needToDelete > int32(availablePodCount) {
		needToDelete = int32(availablePodCount)
		klog.Infof("Limited needToDelete to available pods: %d", needToDelete)
	}

	return needToDelete
}
