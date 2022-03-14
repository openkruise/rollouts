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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	Keep         = "Keep"
	Start        = "Start"
	Restart      = "Restart"
	Finalize     = "Finalize"
	Terminating  = "Terminating"
	Recalculate  = "Recalculate"
	Reinitialize = "Reinitialize"
)

func (r *Executor) checkHealthBeforeExecution(controller workloads.WorkloadController) (needStopThisRound bool, result reconcile.Result) {
	var reason string
	var message string
	var needRetry bool
	var stateTrans string

	// sync the workload info and watch the workload change event
	workloadEvent, workloadInfo, err := controller.SyncWorkloadInfo()

	// Handle the special cases about the workload, include:
	//   (1). Get workload info err
	//   (2). Workload was deleted
	//   (3). Workload is created
	//   (4). Workload scale when rollout
	//   (5). Workload rollback when rollout
	//   (6). Workload revision changed when rollout
	//   (7). Workload is at unexpected/unhealthy state
	//   (8). Workload is at unstable state, its workload info is untrustworthy
	switch {
	// handle the case of IgnoreNotFound(err) != nil
	case isGetWorkloadInfoError(err):
		reason = "GetWorkloadError"
		message = err.Error()
		needRetry = true
		stateTrans = Keep

	// handle the case that the workload is deleted
	case isWorkloadGone(err, r.releaseStatus):
		reason = "WorkloadGone"
		message = "target workload has gone, then terminating"
		stateTrans = Terminating

	// handle the case that workload is newly created
	case isWorkloadLocated(err, r.releaseStatus):
		reason = "WorkloadLocated"
		message = "workload is located, then start"
		stateTrans = Start

	// handle the case that workload is scaling during progressing
	case isWorkloadScaling(workloadEvent, r.releaseStatus):
		reason = "ReplicasChanged"
		message = "workload is scaling, then recalculate"
		stateTrans = Reinitialize

	// handle the case that workload is rolling back during progressing
	case isWorkloadRollback(workloadEvent, r.releaseStatus):
		reason = "StableOrRollback"
		message = "workload is table or rolling back, then abort"
		stateTrans = Finalize

	// handle the case that workload is unhealthy, and rollout plan cannot go on
	case isWorkloadUnhealthy(workloadEvent, r.releaseStatus):
		reason = "WorkloadUnHealthy"
		message = "workload is UnHealthy, then stop"
		stateTrans = Keep
		needStopThisRound = true

	// handle the case that workload.Generation != workload.Status.ObservedGeneration
	case isWorkloadUnStable(workloadEvent, r.releaseStatus):
		if r.releaseStatus.Phase != v1alpha1.RolloutPhaseCompleted {
			reason = "WorkloadNotStable"
			message = "workload status is not stable, then retry"
		}
		needRetry = true
		stateTrans = Keep

	// handle the case of continuous release v1 -> v2 (UnCompleted) -> v3
	case isWorkloadChanged(workloadEvent, r.releaseStatus):
		reason = "TargetRevisionChanged"
		// Rollout controller needs route traffic firstly
		if !util.IsControlledByRollout(r.release) {
			message = "workload revision was changed, then restart"
			stateTrans = Restart
		} else {
			message = "workload revision was changed, then finalize"
			stateTrans = Finalize
		}
	}

	// Handle the special cases about the batch release plan.
	// Note: must keep the order of the following cases:
	//   (1). Plan is deleted or cancelled
	//   (2). Plan is paused during rollout
	//   (3). Plan is changed during rollout
	//   (4). Plan status is unexpected/unhealthy
	switch {
	// handle the case that the plan is deleted or is terminating
	case isPlanTerminating(r.release, r.releaseStatus):
		reason = "PlanTerminating"
		message = "Release plan is terminating, then terminating"
		stateTrans = Terminating

	// handle the case that releasePlan.paused = true
	case isPlanPaused(r.releasePlan, r.releaseStatus):
		reason = "PlanPaused"
		message = "release plan is paused, then paused"
		stateTrans = Keep
		needStopThisRound = true

	// handle the case that release plan is changed during progressing
	case isPlanChanged(r.releasePlan, r.releaseStatus):
		reason = "PlanChanged"
		message = "release plan is changed, then recalculate"
		stateTrans = Recalculate

	// handle the case that release status is chaos which may lead to panic
	case isPlanUnhealthy(r.releasePlan, r.releaseStatus):
		reason = "PlanStatusUnhealthy"
		message = "release plan is unhealthy, then restart"
		stateTrans = Restart
	}

	// log the special event info
	if len(message) > 0 {
		r.recorder.Eventf(r.release, v1.EventTypeWarning, reason, message)
		klog.Warningf("Special case occurred in BatchRelease(%v), message: %v", r.releaseKey, message)
	}

	// 1. sync workload info with status
	// 2. transform status state according to stateTrans
	oldStatus := r.releaseStatus.DeepCopy()
	refreshStatus(r.release, r.releaseStatus, workloadInfo, stateTrans)

	// If it needs to retry or status phase or state changed, should
	// stop and retry. This is because we must ensure that the phase
	// and state is persistent in ETCD, or will lead to the chaos of
	// state machine.
	if !reflect.DeepEqual(oldStatus, r.releaseStatus) {
		needRetry = true
	}

	// will retry after 50ms
	if needRetry {
		needStopThisRound = true
		result = reconcile.Result{RequeueAfter: DefaultDuration}
	}

	return needStopThisRound, result
}

func refreshStatus(release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, workloadInfo *workloads.WorkloadInfo, stateTrans string) {
	// refresh workload info for status
	if workloadInfo != nil {
		if workloadInfo.Replicas != nil {
			newStatus.ObservedWorkloadReplicas = *workloadInfo.Replicas
		}
		if workloadInfo.UpdateRevision != nil {
			newStatus.UpdateRevision = *workloadInfo.UpdateRevision
		}
		if workloadInfo.Status != nil {
			newStatus.CanaryStatus.UpdatedReplicas = workloadInfo.Status.UpdatedReplicas
			newStatus.CanaryStatus.UpdatedReadyReplicas = workloadInfo.Status.UpdatedReadyReplicas
		}
		planHash := hashReleasePlanBatches(&release.Spec.ReleasePlan)
		if newStatus.ObservedReleasePlanHash != planHash {
			newStatus.ObservedReleasePlanHash = planHash
		}
	}

	// transform status state
	switch stateTrans {
	case Keep:
		// keep current state, do nothing
	case Start:
		signalStart(newStatus)
	case Restart:
		signalRestart(newStatus)
	case Finalize:
		signalFinalize(newStatus)
	case Terminating:
		signalTerminating(newStatus)
	case Reinitialize:
		signalReinitialize(newStatus)
	case Recalculate:
		signalRecalculate(release, newStatus, workloadInfo)
	}
}

func isPlanUnhealthy(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return isProgressingState(status) && int(status.CanaryStatus.CurrentBatch) >= len(plan.Batches)
}

func isPlanChanged(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return isProgressingState(status) && status.ObservedReleasePlanHash != hashReleasePlanBatches(plan)
}

func isWorkloadLocated(err error, status *v1alpha1.BatchReleaseStatus) bool {
	return err == nil && status.Phase == v1alpha1.RolloutPhaseInitial
}

func isWorkloadGone(err error, status *v1alpha1.BatchReleaseStatus) bool {
	return errors.IsNotFound(err) && status.Phase != v1alpha1.RolloutPhaseInitial
}

func isPlanPaused(plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus) bool {
	return isProgressingState(status) && plan.Paused
}

func isPlanTerminating(release *v1alpha1.BatchRelease, status *v1alpha1.BatchReleaseStatus) bool {
	return release.DeletionTimestamp != nil || status.Phase == v1alpha1.RolloutPhaseTerminating
}

func isProgressingState(status *v1alpha1.BatchReleaseStatus) bool {
	return status.Phase == v1alpha1.RolloutPhaseProgressing
}

func isGetWorkloadInfoError(err error) bool {
	return err != nil && !errors.IsNotFound(err)
}

func isWorkloadScaling(event workloads.WorkloadChangeEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadReplicasChanged && isProgressingState(status)
}

func isWorkloadRollback(event workloads.WorkloadChangeEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadRollback && isProgressingState(status)
}

func isWorkloadUnhealthy(event workloads.WorkloadChangeEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadUnHealthy
}

func isWorkloadChanged(event workloads.WorkloadChangeEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadPodTemplateChanged
}

func isWorkloadUnStable(event workloads.WorkloadChangeEventType, status *v1alpha1.BatchReleaseStatus) bool {
	return event == workloads.WorkloadStillReconciling
}
