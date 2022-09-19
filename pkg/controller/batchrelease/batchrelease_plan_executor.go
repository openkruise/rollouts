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
	"fmt"
	"reflect"
	"time"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads/cloneset"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads/deployment/canary_strategy"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads/statefulset"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DefaultDuration = 2 * time.Second
)

// Executor is the controller that controls the release plan resource
type Executor struct {
	client   client.Client
	recorder record.EventRecorder
}

// NewReleasePlanExecutor creates a RolloutPlanController
func NewReleasePlanExecutor(cli client.Client, recorder record.EventRecorder) *Executor {
	return &Executor{
		client:   cli,
		recorder: recorder,
	}
}

// Do execute the release plan
func (r *Executor) Do(release *v1alpha1.BatchRelease) (reconcile.Result, *v1alpha1.BatchReleaseStatus, error) {
	klog.InfoS("Starting one round of reconciling release plan",
		"BatchRelease", client.ObjectKeyFromObject(release),
		"phase", release.Status.Phase,
		"current-batch", release.Status.CanaryStatus.CurrentBatch,
		"current-batch-state", release.Status.CanaryStatus.CurrentBatchState)

	newStatus := getInitializedStatus(&release.Status)
	workloadController, err := r.getReleaseController(release, newStatus)
	if err != nil || workloadController == nil {
		return reconcile.Result{}, nil, nil
	}

	stop, result, err := r.syncStatusBeforeExecuting(release, newStatus, workloadController)
	if stop || err != nil {
		return result, newStatus, err
	}

	return r.executeBatchReleasePlan(release, newStatus, workloadController)
}

func (r *Executor) executeBatchReleasePlan(release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, workloadController workloads.ReleaseController) (reconcile.Result, *v1alpha1.BatchReleaseStatus, error) {
	var err error
	result := reconcile.Result{}

	klog.V(3).Infof("BatchRelease(%v) State Machine into '%s' state", klog.KObj(release), newStatus.Phase)

	switch newStatus.Phase {
	case v1alpha1.RolloutPhaseInitial:
		// if this batchRelease was created but workload doest not exist,
		// should keep this phase and do nothing util workload is created.

	case v1alpha1.RolloutPhaseHealthy:
		// verify whether the workload is ready to execute the release plan in this state.
		var verifiedDone bool
		verifiedDone, err = workloadController.VerifyWorkload()
		switch {
		case err != nil:
			setCondition(newStatus, v1alpha1.VerifyingBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case verifiedDone:
			newStatus.Phase = v1alpha1.RolloutPhasePreparing
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			setCondition(newStatus, v1alpha1.PreparingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is preparing for progress")
		}

	case v1alpha1.RolloutPhasePreparing:
		// prepare and initialize something before progressing in this state.
		var preparedDone bool
		var replicasNoNeedToRollback *int32
		preparedDone, replicasNoNeedToRollback, err = workloadController.PrepareBeforeProgress()
		switch {
		case err != nil:
			setCondition(newStatus, v1alpha1.PreparingBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case preparedDone:
			newStatus.Phase = v1alpha1.RolloutPhaseProgressing
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			newStatus.CanaryStatus.NoNeedUpdateReplicas = replicasNoNeedToRollback
			setCondition(newStatus, v1alpha1.ProgressingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is progressing")
		default:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
		}

	case v1alpha1.RolloutPhaseProgressing:
		// progress the release plan in this state.
		var progressDone bool
		progressDone, result, err = r.progressBatches(release, newStatus, workloadController)
		switch {
		case progressDone:
			newStatus.Phase = v1alpha1.RolloutPhaseFinalizing
			setCondition(newStatus, v1alpha1.FinalizingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is finalizing")
		}

	case v1alpha1.RolloutPhaseFinalizing:
		// finalize canary the resources when progressing done.
		// Do not clean the canary resources, because rollout
		// controller should set the traffic routing first.
		var finalizedDone bool
		finalizedDone, err = workloadController.FinalizeProgress(false)
		switch {
		case err != nil:
			setCondition(newStatus, v1alpha1.CompletedBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case finalizedDone:
			if IsAllBatchReady(release) {
				newStatus.Phase = v1alpha1.RolloutPhaseCompleted
				setCondition(newStatus, v1alpha1.CompletedBatchReleaseCondition, v1.ConditionTrue, v1alpha1.SucceededBatchReleaseConditionReason, "BatchRelease is completed")
			} else {
				newStatus.Phase = v1alpha1.RolloutPhaseCancelled
				setCondition(newStatus, v1alpha1.CancelledBatchReleaseCondition, v1.ConditionTrue, v1alpha1.SucceededBatchReleaseConditionReason, "BatchRelease is cancelled")
			}
		default:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
		}

	case v1alpha1.RolloutPhaseTerminating:
		var finalizedDone bool
		finalizedDone, err = workloadController.FinalizeProgress(true)
		switch {
		case err != nil:
			setCondition(newStatus, v1alpha1.CompletedBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case finalizedDone:
			setCondition(newStatus, v1alpha1.TerminatedBatchReleaseCondition, v1.ConditionTrue, v1alpha1.SucceededBatchReleaseConditionReason, "BatchRelease is terminated")
		default:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
		}

	case v1alpha1.RolloutPhaseCompleted, v1alpha1.RolloutPhaseCancelled:
		// this state indicates that the plan is executed/cancelled successfully, should do nothing in these states.

	default:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", klog.KObj(release), "Unknown")
		panic(fmt.Sprintf("illegal release status %+v", newStatus))
	}

	return result, newStatus, err
}

// reconcile logic when we are in the middle of release, we have to go through finalizing state before succeed or fail
func (r *Executor) progressBatches(release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, workloadController workloads.ReleaseController) (bool, reconcile.Result, error) {
	var err error
	progressDone := false
	result := reconcile.Result{}

	klog.V(3).Infof("BatchRelease(%v) Canary Batch State Machine into '%s' state", klog.KObj(release), newStatus.CanaryStatus.CurrentBatchState)

	switch newStatus.CanaryStatus.CurrentBatchState {
	case "", v1alpha1.UpgradingBatchState:
		// modify workload replicas/partition based on release plan in this state.
		upgradeDone, upgradeErr := workloadController.UpgradeOneBatch()
		switch {
		case upgradeErr != nil:
			err = upgradeErr
			setCondition(newStatus, "Progressing", v1.ConditionFalse, "UpgradeBatchFailed", err.Error())
		case upgradeDone:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			newStatus.CanaryStatus.CurrentBatchState = v1alpha1.VerifyingBatchState
		}

	case v1alpha1.VerifyingBatchState:
		// replicas/partition has been modified, should wait pod ready in this state.
		verified, verifiedErr := workloadController.CheckOneBatchReady()
		switch {
		case verifiedErr != nil:
			err = verifiedErr
			setCondition(newStatus, "Progressing", v1.ConditionFalse, "VerifyBatchFailed", err.Error())
		case verified:
			now := metav1.Now()
			newStatus.CanaryStatus.BatchReadyTime = &now
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			newStatus.CanaryStatus.CurrentBatchState = v1alpha1.ReadyBatchState
		default:
			newStatus.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
		}

	case v1alpha1.ReadyBatchState:
		if !IsPartitioned(release) {
			// expected pods in the batch are upgraded and the state is ready, then try to move to the next batch
			progressDone = r.moveToNextBatch(release, newStatus)
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			setCondition(newStatus, v1alpha1.ProgressingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is progressing")
		} else {
			setCondition(newStatus, "Progressing", v1.ConditionFalse, "Paused", fmt.Sprintf("BatchRelease is partitioned in %v-th batch", newStatus.CanaryStatus.CurrentBatch))
		}

	default:
		klog.V(3).Infof("ReleasePlan(%v) Batch State Machine into %s state", "Unknown")
		panic(fmt.Sprintf("illegal status %+v", newStatus))
	}

	return progressDone, result, err
}

// GetWorkloadController pick the right workload controller to work on the workload
func (r *Executor) getReleaseController(release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus) (workloads.ReleaseController, error) {
	targetRef := release.Spec.TargetRef.WorkloadRef
	if targetRef == nil {
		return nil, nil
	}

	gvk := schema.FromAPIVersionAndKind(targetRef.APIVersion, targetRef.Kind)
	if !util.IsSupportedWorkload(gvk) {
		message := fmt.Sprintf("the workload type '%v' is not supported", gvk)
		r.recorder.Event(release, v1.EventTypeWarning, "UnsupportedWorkload", message)
		return nil, fmt.Errorf(message)
	}

	targetKey := types.NamespacedName{
		Namespace: release.Namespace,
		Name:      targetRef.Name,
	}

	switch targetRef.APIVersion {
	case appsv1alpha1.GroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(appsv1alpha1.CloneSet{}).Name() {
			klog.InfoS("using cloneset batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return cloneset.NewCloneSetReleaseController(r.client, r.recorder, release, newStatus, targetKey), nil
		}
		if targetRef.Kind == reflect.TypeOf(appsv1beta1.StatefulSet{}).Name() {
			klog.InfoS("using statefulset-like batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return statefulset.NewStatefulSetReleaseController(r.client, r.recorder, release, newStatus, targetKey, gvk), nil
		}

	case apps.SchemeGroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() {
			klog.InfoS("using deployment batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return canary_strategy.NewDeploymentReleaseController(r.client, r.recorder, release, newStatus, targetKey), nil
		}
		if targetRef.Kind == reflect.TypeOf(apps.StatefulSet{}).Name() {
			klog.InfoS("using statefulset-like batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return statefulset.NewStatefulSetReleaseController(r.client, r.recorder, release, newStatus, targetKey, gvk), nil
		}
	}

	// try to use statefulset-like rollout controller by default
	klog.InfoS("using statefulset-like batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
	return statefulset.NewStatefulSetReleaseController(r.client, r.recorder, release, newStatus, targetKey, gvk), nil
}

func (r *Executor) moveToNextBatch(release *v1alpha1.BatchRelease, status *v1alpha1.BatchReleaseStatus) bool {
	currentBatch := int(status.CanaryStatus.CurrentBatch)
	if currentBatch >= len(release.Spec.ReleasePlan.Batches)-1 {
		klog.V(3).Infof("BatchRelease(%v) finished all batch, release current batch: %v", klog.KObj(release), status.CanaryStatus.CurrentBatch)
		return true
	} else {
		if release.Spec.ReleasePlan.BatchPartition == nil || *release.Spec.ReleasePlan.BatchPartition > status.CanaryStatus.CurrentBatch {
			status.CanaryStatus.CurrentBatch++
		}
		status.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
		klog.V(3).Infof("BatchRelease(%v) finished one batch, release current batch: %v", klog.KObj(release), status.CanaryStatus.CurrentBatch)
		return false
	}
}
