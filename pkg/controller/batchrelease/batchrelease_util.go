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
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func HasTerminatingCondition(status v1alpha1.BatchReleaseStatus) bool {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == v1alpha1.TerminatedBatchReleaseCondition && c.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

func initializeStatusIfNeeds(status *v1alpha1.BatchReleaseStatus) {
	if len(status.Phase) == 0 {
		resetStatus(status)
	}
}

func signalReinitializeBatch(status *v1alpha1.BatchReleaseStatus) {
	status.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
}

func signalLocated(status *v1alpha1.BatchReleaseStatus) {
	status.Phase = v1alpha1.RolloutPhaseHealthy
	setCondition(status, v1alpha1.VerifyingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is verifying the workload")
}

func signalTerminating(status *v1alpha1.BatchReleaseStatus) {
	status.Phase = v1alpha1.RolloutPhaseTerminating
	setCondition(status, v1alpha1.TerminatingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is terminating")
}

func signalFinalize(status *v1alpha1.BatchReleaseStatus) {
	status.Phase = v1alpha1.RolloutPhaseFinalizing
	setCondition(status, v1alpha1.FinalizingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is finalizing")
}

func signalRecalculate(release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus) {
	// When BatchRelease plan was changed, rollout controller will update this batchRelease cr,
	// and rollout controller will set BatchPartition as its expected current batch index.
	currentBatch := int32(0)
	if release.Spec.ReleasePlan.BatchPartition != nil {
		// ensure current batch upper bound
		currentBatch = integer.Int32Min(*release.Spec.ReleasePlan.BatchPartition, int32(len(release.Spec.ReleasePlan.Batches)-1))
	}

	klog.Infof("BatchRelease(%v) canary batch changed from %v to %v when the release plan changed",
		client.ObjectKeyFromObject(release), newStatus.CanaryStatus.CurrentBatch, currentBatch)
	newStatus.CanaryStatus.CurrentBatch = currentBatch
	newStatus.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
	newStatus.ObservedReleasePlanHash = util.HashReleasePlanBatches(&release.Spec.ReleasePlan)
}

func signalPartitionBack(plan *v1alpha1.ReleasePlan, newStatus *v1alpha1.BatchReleaseStatus) {
	if plan.BatchPartition != nil {
		newStatus.CanaryStatus.CurrentBatch = *plan.BatchPartition
		newStatus.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
	}
}

func resetStatus(status *v1alpha1.BatchReleaseStatus) {
	status.Phase = v1alpha1.RolloutPhaseInitial
	status.StableRevision = ""
	status.UpdateRevision = ""
	status.ObservedReleasePlanHash = ""
	status.ObservedWorkloadReplicas = -1
	status.CanaryStatus = v1alpha1.BatchReleaseCanaryStatus{}
}

func setCondition(status *v1alpha1.BatchReleaseStatus, condType v1alpha1.RolloutConditionType, condStatus v1.ConditionStatus, reason, message string) {
	if status == nil {
		return
	}

	if len(status.Conditions) == 0 {
		status.Conditions = append(status.Conditions, v1alpha1.RolloutCondition{
			Type:               condType,
			Status:             condStatus,
			Reason:             reason,
			Message:            message,
			LastUpdateTime:     metav1.Now(),
			LastTransitionTime: metav1.Now(),
		})
		return
	}

	condition := &status.Conditions[0]
	isConditionChanged := func() bool {
		return condition.Type != condType || condition.Status != condStatus || condition.Reason != reason || condition.Message != message
	}

	if isConditionChanged() {
		condition.Type = condType
		condition.Reason = reason
		condition.Message = message
		condition.LastUpdateTime = metav1.Now()
		if condition.Status != condStatus {
			condition.LastTransitionTime = metav1.Now()
		}
		condition.Status = condStatus
	}
}

func IsPartitioned(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return plan.BatchPartition != nil && *plan.BatchPartition <= status.CanaryStatus.CurrentBatch
}

func IsAllBatchReady(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return len(plan.Batches)-1 == int(status.CanaryStatus.CurrentBatch) && status.CanaryStatus.CurrentBatchState == v1alpha1.ReadyBatchState
}
