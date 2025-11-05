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
	"sort"
	"strconv"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
)

const (
	// PodDeletionCostAnnotation is the annotation key for pod deletion cost
	PodDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
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

// SortPodsForDeletion sorts pods by deletion priority order
// (highest to lowest priority for deletion):
// 1. Pending pods (if any)
// 2. Pods with Ready=false/unknown (if any)
// 3. Pods with low deletion cost annotation (if any)
// 4. Newer pods (by creation timestamp)
// Ready pods are deleted last
func SortPodsForDeletion(pods []*corev1.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		podI, podJ := pods[i], pods[j]

		// Get pod readiness status
		readyI := GetPodReadiness(podI)
		readyJ := GetPodReadiness(podJ)

		// 1. Pending pods first
		if podI.Status.Phase == corev1.PodPending && podJ.Status.Phase != corev1.PodPending {
			return true
		}
		if podI.Status.Phase != corev1.PodPending && podJ.Status.Phase == corev1.PodPending {
			return false
		}

		// 2. Not ready pods before ready pods
		if !readyI && readyJ {
			return true
		}
		if readyI && !readyJ {
			return false
		}

		// 3. Compare deletion cost (lower cost = higher priority for deletion)
		costI := GetPodDeletionCost(podI)
		costJ := GetPodDeletionCost(podJ)
		if costI != costJ {
			return costI < costJ
		}

		// 4. Newer pods first (newer creation timestamp = higher priority for deletion)
		return podI.CreationTimestamp.After(podJ.CreationTimestamp.Time)
	})
}

// GetPodReadiness returns true if the pod is ready
func GetPodReadiness(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// GetPodDeletionCost returns the deletion cost of a pod
// Lower cost means higher priority for deletion
func GetPodDeletionCost(pod *corev1.Pod) int32 {
	if pod.Annotations == nil {
		return 0
	}

	costStr, exists := pod.Annotations[PodDeletionCostAnnotation]
	if !exists {
		return 0
	}

	cost, err := strconv.ParseInt(costStr, 10, 32)
	if err != nil {
		klog.Infof("Invalid pod deletion cost annotation for pod %s/%s: %s", pod.Namespace, pod.Name, costStr)
		return 0
	}

	return int32(cost)
}

// SlowStartBatch tries to call the provided function a total of 'count' times,
// starting slow to check for errors, then speeding up if calls succeed.
//
// It groups the calls into batches, starting with a group of initialBatchSize.
// Within each batch, it may call the function multiple times concurrently with its index.
//
// If a whole batch succeeds, the next batch may get exponentially larger.
// If there are any failures in a batch, all remaining batches are skipped
// after waiting for the current batch to complete.
//
// It returns the number of successful calls to the function.
func SlowStartBatch(count int, initialBatchSize int, fn func(index int) error) (int, error) {
	remaining := count
	successes := 0
	index := 0

	for batchSize := IntMin(remaining, initialBatchSize); batchSize > 0; batchSize = IntMin(2*batchSize, remaining) {
		errCh := make(chan error, batchSize)
		var wg sync.WaitGroup
		wg.Add(batchSize)

		for i := 0; i < batchSize; i++ {
			go func(idx int) {
				defer wg.Done()
				if err := fn(idx); err != nil {
					errCh <- err
				}
			}(index)
			index++
		}

		wg.Wait()
		curSuccesses := batchSize - len(errCh)
		successes += curSuccesses

		if len(errCh) > 0 {
			return successes, <-errCh
		}

		remaining -= batchSize
	}

	return successes, nil
}

// IntMin returns the minimum of two integers
func IntMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
