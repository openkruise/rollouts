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
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *Executor) checkHealthBeforeExecution(controller workloads.WorkloadController) (bool, reconcile.Result, error) {
	var err error
	var reason string
	var message string
	var needRetry bool
	needStopThisRound := false
	result := reconcile.Result{}
	oldStatus := r.releaseStatus.DeepCopy()

	// sync the workload info and watch the workload change event
	workloadEvent, workloadInfo, err := controller.SyncWorkloadInfo()

	// Note: must keep the order of the following cases:
	switch {
	/**************************************************************************
		          SPECIAL CASES ABOUT THE BATCH RELEASE PLAN
	 *************************************************************************/
	//The following special cases are about the **batch release plan**, include:
	//  (1). Plan is deleted or cancelled
	//  (2). Plan is paused during rollout
	//  (3). Plan is changed during rollout
	//  (4). Plan status is unexpected/unhealthy
	//  (5). Plan batchPartition is less than status.currenBatch
	case isPlanTerminating(r.release, r.releaseStatus):
		// handle the case that the plan is deleted or is terminating
		reason = "PlanTerminating"
		message = "Release plan is deleted or cancelled, then terminate"
		signalTerminating(r.releaseStatus)

	case isPlanPaused(workloadEvent, r.releasePlan, r.releaseStatus):
		// handle the case that releasePlan.paused = true
		reason = "PlanPaused"
		message = "release plan is paused, then stop reconcile"
		needStopThisRound = true

	case isPlanChanged(r.releasePlan, r.releaseStatus):
		// handle the case that release plan is changed during progressing
		reason = "PlanChanged"
		message = "release plan is changed, then recalculate status"
		signalRecalculate(r.release, r.releaseStatus)

	case isPlanUnhealthy(r.releasePlan, r.releaseStatus):
		// handle the case that release status is chaos which may lead to panic
		reason = "PlanStatusUnhealthy"
		message = "release plan is unhealthy, then restart"
		needStopThisRound = true

	case isPlanPartitionModifiedBack(r.releasePlan, r.releaseStatus):
		reason = "BatchPartitionModifiedBack"
		message = "release plan batch partition is less than current status, then modified currentBatch back"
		signalPartitionBack(r.releasePlan, r.releaseStatus)

	/**************************************************************************
			          SPECIAL CASES ABOUT THE WORKLOAD
	*************************************************************************/
	// The following special cases are about the **workload**, include:
	//   (1). Get workload info err
	//   (2). Workload was deleted
	//   (3). Workload is created
	//   (4). Workload scale when rollout
	//   (5). Workload rollback when rollout
	//   (6). Workload revision changed when rollout
	//   (7). Workload is at unexpected/unhealthy state
	//   (8). Workload is at unstable state, its workload info is untrustworthy
	case isGetWorkloadInfoError(err):
		// handle the case of IgnoreNotFound(err) != nil
		reason = "GetWorkloadError"
		message = err.Error()

	case isWorkloadGone(workloadEvent, r.releaseStatus):
		// handle the case that the workload is deleted
		reason = "WorkloadGone"
		message = "target workload has gone, then terminate"
		signalTerminating(r.releaseStatus)

	case isWorkloadLocated(err, r.releaseStatus):
		// handle the case that workload is newly created
		reason = "WorkloadLocated"
		message = "workload is located, then start"
		signalLocated(r.releaseStatus)

	case isWorkloadScaling(workloadEvent, r.releaseStatus):
		// handle the case that workload is scaling during progressing
		reason = "ReplicasChanged"
		message = "workload is scaling, then reinitialize batch status"
		signalReinitializeBatch(r.releaseStatus)
		// we must ensure that this field is updated only when we have observed
		// the workload scaling event, otherwise this event may be lost.
		r.releaseStatus.ObservedWorkloadReplicas = *workloadInfo.Replicas

	case isWorkloadChanged(workloadEvent, r.releaseStatus):
		// handle the case of continuous release v1 -> v2 -> v3
		reason = "TargetRevisionChanged"
		message = "workload revision was changed, then abort"
		signalFinalize(r.releaseStatus)

	case isWorkloadUnhealthy(workloadEvent, r.releaseStatus):
		// handle the case that workload is unhealthy, and rollout plan cannot go on
		reason = "WorkloadUnHealthy"
		message = "workload is UnHealthy, then stop"
		needStopThisRound = true

	case isWorkloadUnstable(workloadEvent, r.releaseStatus):
		// handle the case that workload.Generation != workload.Status.ObservedGeneration
		reason = "WorkloadNotStable"
		message = "workload status is not stable, then wait"
		needStopThisRound = true
	}

	// log the special event info
	if len(message) > 0 {
		r.recorder.Eventf(r.release, v1.EventTypeWarning, reason, message)
		klog.Warningf("Special case occurred in BatchRelease(%v), message: %v", r.releaseKey, message)
	}

	// sync workload info with status
	refreshStatus(r.release, r.releaseStatus, workloadInfo)

	// If it needs to retry or status phase or state changed, should
	// stop and retry. This is because we must ensure that the phase
	// and state is persistent in ETCD, or will lead to the chaos of
	// state machine.
	if !reflect.DeepEqual(oldStatus, r.releaseStatus) {
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

func refreshStatus(release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, workloadInfo *util.WorkloadInfo) {
	// refresh workload info for status
	if workloadInfo != nil {
		if workloadInfo.Status != nil {
			newStatus.CanaryStatus.UpdatedReplicas = workloadInfo.Status.UpdatedReplicas
			newStatus.CanaryStatus.UpdatedReadyReplicas = workloadInfo.Status.UpdatedReadyReplicas
		}
	}
	if len(newStatus.ObservedReleasePlanHash) == 0 {
		newStatus.ObservedReleasePlanHash = util.HashReleasePlanBatches(&release.Spec.ReleasePlan)
	}
}

func isPlanTerminating(release *v1alpha1.BatchRelease, status *v1alpha1.BatchReleaseStatus) bool {
	return release.DeletionTimestamp != nil || status.Phase == v1alpha1.RolloutPhaseTerminating
}

func isPlanChanged(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return status.ObservedReleasePlanHash != util.HashReleasePlanBatches(plan) && status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isPlanUnhealthy(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return int(status.CanaryStatus.CurrentBatch) >= len(plan.Batches) && status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isPlanPaused(event workloads.WorkloadEventType, plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return plan.Paused && status.Phase == v1alpha1.RolloutPhaseProgressing && !isWorkloadGone(event, status)
}

func isPlanPartitionModifiedBack(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	if plan.BatchPartition == nil {
		return false
	}
	return *plan.BatchPartition < status.CanaryStatus.CurrentBatch && status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isGetWorkloadInfoError(err error) bool {
	return err != nil && !errors.IsNotFound(err)
}

func isWorkloadLocated(err error, status *v1alpha1.BatchReleaseStatus) bool {
	return err == nil && status.Phase == v1alpha1.RolloutPhaseInitial
}

func isWorkloadGone(event workloads.WorkloadEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadHasGone && status.Phase != v1alpha1.RolloutPhaseInitial
}

func isWorkloadScaling(event workloads.WorkloadEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadReplicasChanged && status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isWorkloadChanged(event workloads.WorkloadEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadPodTemplateChanged && status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isWorkloadUnhealthy(event workloads.WorkloadEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadUnHealthy && status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isWorkloadUnstable(event workloads.WorkloadEventType, _ *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadStillReconciling
}
