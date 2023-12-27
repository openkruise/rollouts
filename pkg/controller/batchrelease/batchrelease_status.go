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
	"reflect"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *Executor) syncStatusBeforeExecuting(release *v1beta1.BatchRelease, newStatus *v1beta1.BatchReleaseStatus, controller control.Interface) (bool, reconcile.Result, error) {
	var err error
	var message string
	var needRetry bool
	needStopThisRound := false
	result := reconcile.Result{}
	// sync the workload info and watch the workload change event
	workloadEvent, workloadInfo, err := controller.SyncWorkloadInformation()

	// Note: must keep the order of the following cases:
	switch {
	/**************************************************************************
		          SPECIAL CASES ABOUT THE BATCH RELEASE PLAN
	 *************************************************************************/
	//The following special cases are about the **batch release plan**, include:
	//  (1). Plan has been terminated
	//  (2). Plan is deleted or cancelled
	//  (3). Plan is changed during rollout
	//  (4). Plan status is unexpected/unhealthy
	case isPlanCompleted(release):
		message = "release plan has been completed, will do nothing"
		needStopThisRound = true

	case isPlanFinalizing(release):
		// handle the case that the plan is deleted or is terminating
		message = "release plan is deleted or cancelled, then finalize"
		signalFinalizing(newStatus)

	case isPlanChanged(release):
		// handle the case that release plan is changed during progressing
		message = "release plan is changed, then recalculate status"
		signalRecalculate(release, newStatus)

	case isPlanUnhealthy(release):
		// handle the case that release status is chaos which may lead to panic
		message = "release plan is unhealthy, then restart"
		signalRestartAll(newStatus)

	/**************************************************************************
			          SPECIAL CASES ABOUT THE WORKLOAD
	*************************************************************************/
	// The following special cases are about the **workload**, include:
	//   (1). Get workload info err
	//   (2). Workload is deleted
	//   (3). Workload is created
	//   (4). Workload scale when rollout
	//   (5). Workload rollback when rollout
	//   (6). Workload revision changed when rollout
	//   (7). Workload is at unexpected/unhealthy state
	//   (8). Workload is at unstable state, its workload info is untrustworthy
	case isGetWorkloadInfoError(err):
		// handle the case of IgnoreNotFound(err) != nil
		message = err.Error()

	case isWorkloadGone(workloadEvent, release):
		// handle the case that the workload is deleted
		message = "target workload has gone, then finalize"
		signalFinalizing(newStatus)

	case isWorkloadScaling(workloadEvent, release):
		// handle the case that workload is scaling during progressing
		message = "workload is scaling, then reinitialize batch status"
		signalRestartBatch(newStatus)
		// we must ensure that this field is updated only when we have observed
		// the workload scaling event, otherwise this event may be lost.
		newStatus.ObservedWorkloadReplicas = workloadInfo.Replicas

	case isWorkloadRevisionChanged(workloadEvent, release):
		// handle the case of continuous release
		message = "workload revision was changed, then abort"
		newStatus.UpdateRevision = workloadInfo.Status.UpdateRevision
		needStopThisRound = true

	case isWorkloadUnstable(workloadEvent, release):
		// handle the case that workload.Generation != workload.Status.ObservedGeneration
		message = "workload status is not stable, then wait"
		needStopThisRound = true

	case isWorkloadRollbackInBatch(workloadEvent, release):
		// handle the case of rollback in batches
		if isRollbackInBatchSatisfied(workloadInfo, release) {
			message = "workload is rollback in batch"
			signalRePrepareRollback(newStatus)
			newStatus.UpdateRevision = workloadInfo.Status.UpdateRevision
		} else {
			message = "workload is preparing rollback, wait condition to be satisfied"
			needStopThisRound = true
		}
	}

	// log the special event info
	if len(message) > 0 {
		klog.Warningf("Special case occurred in BatchRelease(%v), message: %v", klog.KObj(release), message)
	}

	// sync workload info with status
	refreshStatus(release, newStatus, workloadInfo)

	// If it needs to retry or status phase or state changed, should
	// stop and retry. This is because we must ensure that the phase
	// and state is persistent in ETCD, or will lead to the chaos of
	// state machine.
	if !reflect.DeepEqual(&release.Status, newStatus) {
		needRetry = true
	}

	// will retry after 50ms
	err = client.IgnoreNotFound(err)
	if needRetry && err == nil {
		needStopThisRound = true
		result = reconcile.Result{RequeueAfter: DefaultDuration}
	}

	return needStopThisRound, result, err
}

func refreshStatus(release *v1beta1.BatchRelease, newStatus *v1beta1.BatchReleaseStatus, workloadInfo *util.WorkloadInfo) {
	// refresh workload info for status
	if workloadInfo != nil {
		newStatus.CanaryStatus.UpdatedReplicas = workloadInfo.Status.UpdatedReplicas
		newStatus.CanaryStatus.UpdatedReadyReplicas = workloadInfo.Status.UpdatedReadyReplicas
	}
	if len(newStatus.ObservedReleasePlanHash) == 0 {
		newStatus.ObservedReleasePlanHash = util.HashReleasePlanBatches(&release.Spec.ReleasePlan)
	}
	newStatus.ObservedRolloutID = release.Spec.ReleasePlan.RolloutID
}

func isPlanFinalizing(release *v1beta1.BatchRelease) bool {
	if release.DeletionTimestamp != nil || release.Status.Phase == v1beta1.RolloutPhaseFinalizing {
		return true
	}
	return release.Spec.ReleasePlan.BatchPartition == nil
}

func isPlanCompleted(release *v1beta1.BatchRelease) bool {
	return release.Status.Phase == v1beta1.RolloutPhaseCompleted
}

func isPlanChanged(release *v1beta1.BatchRelease) bool {
	return release.Status.ObservedReleasePlanHash != util.HashReleasePlanBatches(&release.Spec.ReleasePlan) && release.Status.Phase == v1beta1.RolloutPhaseProgressing
}

func isPlanUnhealthy(release *v1beta1.BatchRelease) bool {
	return int(release.Status.CanaryStatus.CurrentBatch) >= len(release.Spec.ReleasePlan.Batches) && release.Status.Phase == v1beta1.RolloutPhaseProgressing
}

func isGetWorkloadInfoError(err error) bool {
	return err != nil && !errors.IsNotFound(err)
}

func isWorkloadGone(event control.WorkloadEventType, release *v1beta1.BatchRelease) bool {
	return event == control.WorkloadHasGone && release.Status.Phase != v1beta1.RolloutPhaseInitial && release.Status.Phase != ""
}

func isWorkloadScaling(event control.WorkloadEventType, release *v1beta1.BatchRelease) bool {
	return event == control.WorkloadReplicasChanged && release.Status.Phase == v1beta1.RolloutPhaseProgressing
}

func isWorkloadRevisionChanged(event control.WorkloadEventType, release *v1beta1.BatchRelease) bool {
	return event == control.WorkloadPodTemplateChanged && release.Status.Phase == v1beta1.RolloutPhaseProgressing
}

func isWorkloadRollbackInBatch(event control.WorkloadEventType, release *v1beta1.BatchRelease) bool {
	return (event == control.WorkloadRollbackInBatch || release.Annotations[v1alpha1.RollbackInBatchAnnotation] != "") &&
		release.Status.CanaryStatus.NoNeedUpdateReplicas == nil && release.Status.Phase == v1beta1.RolloutPhaseProgressing
}

func isWorkloadUnstable(event control.WorkloadEventType, _ *v1beta1.BatchRelease) bool {
	return event == control.WorkloadStillReconciling
}

func isRollbackInBatchSatisfied(workloadInfo *util.WorkloadInfo, release *v1beta1.BatchRelease) bool {
	return workloadInfo.Status.StableRevision == workloadInfo.Status.UpdateRevision && release.Annotations[v1alpha1.RollbackInBatchAnnotation] != ""
}

func signalRePrepareRollback(newStatus *v1beta1.BatchReleaseStatus) {
	newStatus.Phase = v1beta1.RolloutPhasePreparing
	newStatus.CanaryStatus.BatchReadyTime = nil
	newStatus.CanaryStatus.CurrentBatchState = v1beta1.UpgradingBatchState
}

func signalRestartBatch(status *v1beta1.BatchReleaseStatus) {
	status.CanaryStatus.BatchReadyTime = nil
	status.CanaryStatus.CurrentBatchState = v1beta1.UpgradingBatchState
}

func signalRestartAll(status *v1beta1.BatchReleaseStatus) {
	emptyStatus := v1beta1.BatchReleaseStatus{}
	resetStatus(&emptyStatus)
	*status = emptyStatus
}

func signalFinalizing(status *v1beta1.BatchReleaseStatus) {
	status.Phase = v1beta1.RolloutPhaseFinalizing
}

func signalRecalculate(release *v1beta1.BatchRelease, newStatus *v1beta1.BatchReleaseStatus) {
	// When BatchRelease plan was changed, rollout controller will update this batchRelease cr,
	// and rollout controller will set BatchPartition as its expected current batch index.
	currentBatch := int32(0)
	// if rollout-id is not changed, just use batchPartition;
	// if rollout-id is changed, we should patch pod batch id from batch 0.
	observedRolloutID := release.Status.ObservedRolloutID
	if release.Spec.ReleasePlan.BatchPartition != nil && release.Spec.ReleasePlan.RolloutID == observedRolloutID {
		// ensure current batch upper bound
		currentBatch = integer.Int32Min(*release.Spec.ReleasePlan.BatchPartition, int32(len(release.Spec.ReleasePlan.Batches)-1))
	}

	klog.Infof("BatchRelease(%v) canary batch changed from %v to %v when the release plan changed, observed-rollout-id: %s, current-rollout-id: %s",
		client.ObjectKeyFromObject(release), newStatus.CanaryStatus.CurrentBatch, currentBatch, observedRolloutID, release.Spec.ReleasePlan.RolloutID)
	newStatus.CanaryStatus.BatchReadyTime = nil
	newStatus.CanaryStatus.CurrentBatch = currentBatch
	newStatus.ObservedRolloutID = release.Spec.ReleasePlan.RolloutID
	newStatus.CanaryStatus.CurrentBatchState = v1beta1.UpgradingBatchState
	newStatus.ObservedReleasePlanHash = util.HashReleasePlanBatches(&release.Spec.ReleasePlan)
}

func getInitializedStatus(status *v1beta1.BatchReleaseStatus) *v1beta1.BatchReleaseStatus {
	newStatus := status.DeepCopy()
	if len(status.Phase) == 0 {
		resetStatus(newStatus)
	}
	return newStatus
}

func resetStatus(status *v1beta1.BatchReleaseStatus) {
	status.Phase = v1beta1.RolloutPhasePreparing
	status.StableRevision = ""
	status.UpdateRevision = ""
	status.ObservedReleasePlanHash = ""
	status.ObservedWorkloadReplicas = -1
	status.CanaryStatus = v1beta1.BatchReleaseCanaryStatus{}
}
