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

package batchrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/openkruise/rollouts/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func HasTerminatingCondition(status v1alpha1.BatchReleaseStatus) bool {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Status == v1.ConditionTrue && c.Reason == v1alpha1.TerminatingReasonInTerminating {
			return true
		}
	}
	return false
}

func hashReleasePlanBatches(releasePlan *v1alpha1.ReleasePlan) string {
	by, _ := json.Marshal(releasePlan.Batches)
	md5Hash := sha256.Sum256(by)
	return hex.EncodeToString(md5Hash[:])
}

func initializeStatusIfNeeds(status *v1alpha1.BatchReleaseStatus) {
	if len(status.Phase) == 0 {
		resetStatus(status)
	}
}

func signalStart(status *v1alpha1.BatchReleaseStatus) bool {
	if status.Phase == v1alpha1.RolloutPhaseHealthy {
		return false
	}
	status.Phase = v1alpha1.RolloutPhaseHealthy
	return true
}

func signalRestart(status *v1alpha1.BatchReleaseStatus) bool {
	if status.Phase == v1alpha1.RolloutPhaseInitial {
		return false
	}
	resetStatus(status)
	return true
}

func signalRecalculate(status *v1alpha1.BatchReleaseStatus) bool {
	if status.CanaryStatus.CurrentBatchState == v1alpha1.InitializeBatchState {
		return false
	}
	status.CanaryStatus.CurrentBatchState = v1alpha1.InitializeBatchState
	return true
}

func signalTerminating(status *v1alpha1.BatchReleaseStatus) bool {
	if status.Phase == v1alpha1.RolloutPhaseTerminating {
		return false
	}
	status.Phase = v1alpha1.RolloutPhaseTerminating
	return true
}

func signalFinalize(status *v1alpha1.BatchReleaseStatus) bool {
	if status.Phase == v1alpha1.RolloutPhaseFinalizing {
		return false
	}
	status.Phase = v1alpha1.RolloutPhaseFinalizing
	return true
}

func signalAbort(status *v1alpha1.BatchReleaseStatus) bool {
	if status.Phase == v1alpha1.RolloutPhaseAbort {
		return false
	}
	status.Phase = v1alpha1.RolloutPhaseAbort
	return true
}

func resetStatus(status *v1alpha1.BatchReleaseStatus) {
	status.Phase = v1alpha1.RolloutPhaseInitial
	status.StableRevision = ""
	status.UpdateRevision = ""
	status.ObservedReleasePlanHash = ""
	status.ObservedWorkloadReplicas = -1
	status.CanaryStatus = v1alpha1.BatchReleaseCanaryStatus{}
}

func setCondition(status *v1alpha1.BatchReleaseStatus, reason, message string, conditionStatusType v1.ConditionStatus) {
	if status == nil {
		return
	}

	var suitableCondition *v1alpha1.RolloutCondition
	for i := range status.Conditions {
		condition := &status.Conditions[i]
		if condition.Type == getConditionType(status.Phase) {
			suitableCondition = condition
		}
	}

	if suitableCondition == nil {
		status.Conditions = append(status.Conditions, v1alpha1.RolloutCondition{
			Type:           getConditionType(status.Phase),
			Status:         conditionStatusType,
			Reason:         reason,
			Message:        message,
			LastUpdateTime: metav1.Now(),
		})
	} else {
		suitableCondition.Reason = reason
		suitableCondition.Message = message
		suitableCondition.LastUpdateTime = metav1.Now()
		if suitableCondition.Status != conditionStatusType {
			suitableCondition.LastTransitionTime = metav1.Now()
		}
		suitableCondition.Status = conditionStatusType
	}
}

func cleanupConditions(status *v1alpha1.BatchReleaseStatus) {
	status.Conditions = nil
}

func getConditionType(phase v1alpha1.RolloutPhase) v1alpha1.RolloutConditionType {
	return v1alpha1.RolloutConditionType(fmt.Sprintf("%sPhaseCompleted", phase))
}
