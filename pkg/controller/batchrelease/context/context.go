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

package context

import (
	"encoding/json"
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type BatchContext struct {
	RolloutID string `json:"rolloutID,omitempty"`
	// current batch index, start from 0
	CurrentBatch int32 `json:"currentBatchIndex"`
	// workload update revision
	UpdateRevision string `json:"updateRevision,omitempty"`

	// workload replicas
	Replicas int32 `json:"replicas"`
	// Updated replicas
	UpdatedReplicas int32 `json:"updatedReplicas"`
	// Updated ready replicas
	UpdatedReadyReplicas int32 `json:"updatedReadyReplicas"`
	// no need update replicas that marked before rollout
	NoNeedUpdatedReplicas *int32 `json:"noNeedUpdatedReplicas,omitempty"`
	// the planned number of Pods should be upgrade in current batch
	// this field corresponds to releasePlan.Batches[currentBatch]
	PlannedUpdatedReplicas int32 `json:"plannedUpdatedReplicas,omitempty"`
	// the total number of the really updated pods you desired in current batch.
	// In most normal cases, this field will equal to PlannedUpdatedReplicas,
	// but in some scene, e.g., rolling back in batches, the really desired updated
	// replicas will not equal to planned update replicas, because we just roll the
	// pods that really need update back in batches.
	DesiredUpdatedReplicas int32 `json:"desiredUpdatedReplicas,omitempty"`
	// workload current partition
	CurrentPartition intstr.IntOrString `json:"currentPartition,omitempty"`
	// desired partition replicas in current batch
	DesiredPartition intstr.IntOrString `json:"desiredPartition,omitempty"`
	// failureThreshold to tolerate unready updated replicas;
	FailureThreshold *intstr.IntOrString `json:"failureThreshold,omitempty"`

	// the pods owned by workload
	Pods []*corev1.Pod `json:"-"`
	// filter or sort pods before patch label
	FilterFunc FilterFuncType `json:"-"`
	// the next two fields are only used for bluegreen style
	CurrentSurge intstr.IntOrString `json:"currentSurge,omitempty"`
	DesiredSurge intstr.IntOrString `json:"desiredSurge,omitempty"`
}

type FilterFuncType func(pods []*corev1.Pod, ctx *BatchContext) []*corev1.Pod

func (bc *BatchContext) Log() string {
	marshal, _ := json.Marshal(bc)
	return fmt.Sprintf("%s with %d pods", string(marshal), len(bc.Pods))
}

// IsBatchReady return nil if the batch is ready
func (bc *BatchContext) IsBatchReady() error {
	if bc.UpdatedReplicas < bc.DesiredUpdatedReplicas {
		return fmt.Errorf("current batch not ready: updated replicas not satisfied, UpdatedReplicas %d < DesiredUpdatedReplicas %d", bc.UpdatedReplicas, bc.DesiredUpdatedReplicas)
	}

	unavailableToleration := allowedUnavailable(bc.FailureThreshold, bc.UpdatedReplicas)
	if unavailableToleration+bc.UpdatedReadyReplicas < bc.DesiredUpdatedReplicas {
		return fmt.Errorf("current batch not ready: updated ready replicas not satisfied, allowedUnavailable + UpdatedReadyReplicas %d < DesiredUpdatedReplicas %d", unavailableToleration+bc.UpdatedReadyReplicas, bc.DesiredUpdatedReplicas)
	}

	if bc.DesiredUpdatedReplicas > 0 && bc.UpdatedReadyReplicas == 0 {
		return fmt.Errorf("current batch not ready: no updated ready replicas, DesiredUpdatedReplicas %d > 0 and UpdatedReadyReplicas %d = 0", bc.DesiredUpdatedReplicas, bc.UpdatedReadyReplicas)
	}

	if !batchLabelSatisfied(bc.Pods, bc.RolloutID, bc.PlannedUpdatedReplicas) {
		return fmt.Errorf("current batch not ready: pods with batch label not satisfied, RolloutID %s, PlannedUpdatedReplicas %d", bc.RolloutID, bc.PlannedUpdatedReplicas)
	}
	return nil
}

// batchLabelSatisfied return true if the expected batch label has been patched
func batchLabelSatisfied(pods []*corev1.Pod, rolloutID string, targetCount int32) bool {
	if rolloutID == "" || len(pods) == 0 {
		return true
	}
	patchedCount := util.WrappedPodCount(pods, func(pod *corev1.Pod) bool {
		if !pod.DeletionTimestamp.IsZero() {
			return false
		}
		return pod.Labels[v1beta1.RolloutIDLabel] == rolloutID
	})
	return patchedCount >= int(targetCount)
}

// allowedUnavailable return absolute number of failure threshold
func allowedUnavailable(threshold *intstr.IntOrString, replicas int32) int32 {
	failureThreshold := 0
	if threshold != nil {
		failureThreshold, _ = intstr.GetScaledValueFromIntOrPercent(threshold, int(replicas), true)
	}
	return int32(failureThreshold)
}
