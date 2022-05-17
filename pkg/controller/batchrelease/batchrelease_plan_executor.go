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
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
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
	DefaultDuration = (50 * 1000) * time.Microsecond
)

// Executor is the controller that controls the release plan resource
type Executor struct {
	client   client.Client
	recorder record.EventRecorder

	release       *v1alpha1.BatchRelease
	releasePlan   *v1alpha1.ReleasePlan
	releaseStatus *v1alpha1.BatchReleaseStatus
	releaseKey    types.NamespacedName
	workloadKey   types.NamespacedName
}

// NewReleasePlanExecutor creates a RolloutPlanController
func NewReleasePlanExecutor(client client.Client, recorder record.EventRecorder) *Executor {
	return &Executor{
		client:   client,
		recorder: recorder,
	}
}

func (r *Executor) SetReleaseInfo(release *v1alpha1.BatchRelease) {
	r.release = release
	r.releaseStatus = release.Status.DeepCopy()
	r.releasePlan = release.Spec.ReleasePlan.DeepCopy()
	initializeStatusIfNeeds(r.releaseStatus)
	r.releaseKey = client.ObjectKeyFromObject(release)
	if release.Spec.TargetRef.WorkloadRef != nil {
		r.workloadKey = types.NamespacedName{
			Namespace: release.Namespace,
			Name:      release.Spec.TargetRef.WorkloadRef.Name,
		}
	}
}

// Do execute the release plan
func (r *Executor) Do() (reconcile.Result, *v1alpha1.BatchReleaseStatus, error) {
	klog.InfoS("Starting one round of reconciling release plan",
		"BatchRelease", client.ObjectKeyFromObject(r.release),
		"phase", r.releaseStatus.Phase,
		"current-batch", r.releaseStatus.CanaryStatus.CurrentBatch,
		"current-batch-state", r.releaseStatus.CanaryStatus.CurrentBatchState)

	workloadController, err := r.GetWorkloadController()
	if err != nil || workloadController == nil {
		return reconcile.Result{}, r.releaseStatus, nil
	}

	shouldStopThisRound, result, err := r.checkHealthBeforeExecution(workloadController)
	if shouldStopThisRound || err != nil {
		return result, r.releaseStatus, err
	}

	return r.executeBatchReleasePlan(workloadController)
}

func (r *Executor) executeBatchReleasePlan(workloadController workloads.WorkloadController) (reconcile.Result, *v1alpha1.BatchReleaseStatus, error) {
	var err error
	status := r.releaseStatus
	result := reconcile.Result{}

	klog.V(3).Infof("BatchRelease(%v) State Machine into '%s' state", r.releaseKey, status.Phase)

	switch status.Phase {
	case v1alpha1.RolloutPhaseInitial:
		// if this batchRelease was created but workload doest not exist,
		// should keep this phase and do nothing util workload is created.

	case v1alpha1.RolloutPhaseHealthy:
		// verify whether the workload is ready to execute the release plan in this state.
		var verifiedDone bool
		verifiedDone, err = workloadController.VerifyWorkload()
		switch {
		case err != nil:
			setCondition(status, v1alpha1.VerifyingBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case verifiedDone:
			status.Phase = v1alpha1.RolloutPhasePreparing
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			setCondition(status, v1alpha1.PreparingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is preparing for progress")
		}

	case v1alpha1.RolloutPhasePreparing:
		// prepare and initialize something before progressing in this state.
		var preparedDone bool
		preparedDone, err = workloadController.PrepareBeforeProgress()
		switch {
		case err != nil:
			setCondition(status, v1alpha1.PreparingBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case preparedDone:
			status.Phase = v1alpha1.RolloutPhaseProgressing
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			setCondition(status, v1alpha1.ProgressingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is progressing")
		}

	case v1alpha1.RolloutPhaseProgressing:
		// progress the release plan in this state.
		var progressDone bool
		progressDone, result, err = r.progressBatches(workloadController)
		switch {
		case progressDone:
			status.Phase = v1alpha1.RolloutPhaseFinalizing
			setCondition(status, v1alpha1.FinalizingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is finalizing")
		}

	case v1alpha1.RolloutPhaseFinalizing:
		// finalize canary the resources when progressing done.
		// Do not clean the canary resources, because rollout
		// controller should set the traffic routing first.
		var finalizedDone bool
		finalizedDone, err = workloadController.FinalizeProgress(false)
		switch {
		case err != nil:
			setCondition(status, v1alpha1.CompletedBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case finalizedDone:
			if IsAllBatchReady(r.releasePlan, r.releaseStatus) {
				status.Phase = v1alpha1.RolloutPhaseCompleted
				setCondition(status, v1alpha1.CompletedBatchReleaseCondition, v1.ConditionTrue, v1alpha1.SucceededBatchReleaseConditionReason, "BatchRelease is completed")
			} else {
				status.Phase = v1alpha1.RolloutPhaseCancelled
				setCondition(status, v1alpha1.CancelledBatchReleaseCondition, v1.ConditionTrue, v1alpha1.SucceededBatchReleaseConditionReason, "BatchRelease is cancelled")
			}
		default:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
		}

	case v1alpha1.RolloutPhaseTerminating:
		var finalizedDone bool
		finalizedDone, err = workloadController.FinalizeProgress(true)
		switch {
		case err != nil:
			setCondition(status, v1alpha1.CompletedBatchReleaseCondition, v1.ConditionFalse, v1alpha1.FailedBatchReleaseConditionReason, err.Error())
		case finalizedDone:
			setCondition(status, v1alpha1.TerminatedBatchReleaseCondition, v1.ConditionTrue, v1alpha1.SucceededBatchReleaseConditionReason, "BatchRelease is terminated")
		default:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
		}

	case v1alpha1.RolloutPhaseCompleted, v1alpha1.RolloutPhaseCancelled:
		// this state indicates that the plan is executed/cancelled successfully, should do nothing in these states.

	default:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, "Unknown")
		panic(fmt.Sprintf("illegal release status %+v", status))
	}

	return result, status, err
}

// reconcile logic when we are in the middle of release, we have to go through finalizing state before succeed or fail
func (r *Executor) progressBatches(workloadController workloads.WorkloadController) (bool, reconcile.Result, error) {
	var err error
	progressDone := false
	status := r.releaseStatus
	result := reconcile.Result{}

	klog.V(3).Infof("BatchRelease(%v) Canary Batch State Machine into '%s' state", r.releaseKey, status.CanaryStatus.CurrentBatchState)

	switch status.CanaryStatus.CurrentBatchState {
	case "", v1alpha1.UpgradingBatchState:
		// modify workload replicas/partition based on release plan in this state.
		upgradeDone, upgradeErr := workloadController.UpgradeOneBatch()
		switch {
		case upgradeErr != nil:
			err = upgradeErr
			setCondition(status, "Progressing", v1.ConditionFalse, "UpgradeBatchFailed", err.Error())
		case upgradeDone:
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			status.CanaryStatus.CurrentBatchState = v1alpha1.VerifyingBatchState
		}

	case v1alpha1.VerifyingBatchState:
		// TODO: metrics analysis
		// replicas/partition has been modified, should wait pod ready in this state.
		verified, verifiedErr := workloadController.CheckOneBatchReady()
		switch {
		case verifiedErr != nil:
			err = verifiedErr
			setCondition(status, "Progressing", v1.ConditionFalse, "VerifyBatchFailed", err.Error())
		case verified:
			now := metav1.Now()
			status.CanaryStatus.BatchReadyTime = &now
			result = reconcile.Result{RequeueAfter: DefaultDuration}
			status.CanaryStatus.CurrentBatchState = v1alpha1.ReadyBatchState
		default:
			status.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
		}

	case v1alpha1.ReadyBatchState:
		if !IsPartitioned(r.releasePlan, r.releaseStatus) {
			currentTimestamp := time.Now()
			currentBatch := r.releasePlan.Batches[r.releaseStatus.CanaryStatus.CurrentBatch]
			waitDuration := time.Duration(currentBatch.PauseSeconds) * time.Second
			if waitDuration > 0 && r.releaseStatus.CanaryStatus.BatchReadyTime.Time.Add(waitDuration).After(currentTimestamp) {
				restDuration := r.releaseStatus.CanaryStatus.BatchReadyTime.Time.Add(waitDuration).Sub(currentTimestamp)
				result = reconcile.Result{RequeueAfter: restDuration}
				setCondition(status, "Progressing", v1.ConditionFalse, "Paused", fmt.Sprintf("BatchRelease will resume after %v", restDuration))
				klog.Infof("BatchRelease (%v) paused and will continue to reconcile after %v", r.releaseKey, restDuration)
			} else {
				// expected pods in the batch are upgraded and the state is ready, then try to move to the next batch
				progressDone = r.moveToNextBatch()
				result = reconcile.Result{RequeueAfter: DefaultDuration}
				setCondition(status, v1alpha1.ProgressingBatchReleaseCondition, v1.ConditionTrue, "", "BatchRelease is progressing")
			}
		} else {
			setCondition(status, "Progressing", v1.ConditionFalse, "Paused", fmt.Sprintf("BatchRelease is partitioned in %v-th batch", status.CanaryStatus.CurrentBatch))
		}

	default:
		klog.V(3).Infof("ReleasePlan(%v) Batch State Machine into %s state", "Unknown")
		panic(fmt.Sprintf("illegal status %+v", r.releaseStatus))
	}

	return progressDone, result, err
}

// GetWorkloadController pick the right workload controller to work on the workload
func (r *Executor) GetWorkloadController() (workloads.WorkloadController, error) {
	targetRef := r.release.Spec.TargetRef.WorkloadRef
	if targetRef == nil {
		return nil, nil
	}

	gvk := schema.FromAPIVersionAndKind(targetRef.APIVersion, targetRef.Kind)
	targetKey := types.NamespacedName{
		Namespace: r.release.Namespace,
		Name:      targetRef.Name,
	}

	switch targetRef.APIVersion {
	case appsv1alpha1.GroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(appsv1alpha1.CloneSet{}).Name() {
			klog.InfoS("using cloneset batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return workloads.NewCloneSetRolloutController(r.client, r.recorder, r.release, r.releasePlan, r.releaseStatus, targetKey), nil
		}

	case apps.SchemeGroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() {
			klog.InfoS("using deployment batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return workloads.NewDeploymentRolloutController(r.client, r.recorder, r.release, r.releasePlan, r.releaseStatus, targetKey), nil
		}
	}

	klog.InfoS("using statefulset-like batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
	return workloads.NewUnifiedWorkloadRolloutControlPlane(workloads.NewStatefulSetLikeController, r.client, r.recorder, r.release, r.releaseStatus, targetKey, gvk), nil
	//
	//message := fmt.Sprintf("the workload `%v.%v/%v` is not supported", targetRef.APIVersion, targetRef.Kind, targetRef.Name)
	//r.recorder.Event(r.release, v1.EventTypeWarning, "UnsupportedWorkload", message)
	//return nil, fmt.Errorf(message)
}

func (r *Executor) moveToNextBatch() bool {
	currentBatch := int(r.releaseStatus.CanaryStatus.CurrentBatch)
	if currentBatch >= len(r.releasePlan.Batches)-1 {
		klog.V(3).Infof("BatchRelease(%v) finished all batch, release current batch: %v", r.releaseKey, r.releaseStatus.CanaryStatus.CurrentBatch)
		return true
	} else {
		if r.releasePlan.BatchPartition == nil || *r.releasePlan.BatchPartition > r.releaseStatus.CanaryStatus.CurrentBatch {
			r.releaseStatus.CanaryStatus.CurrentBatch++
		}
		r.releaseStatus.CanaryStatus.CurrentBatchState = v1alpha1.UpgradingBatchState
		klog.V(3).Infof("BatchRelease(%v) finished one batch, release current batch: %v", r.releaseKey, r.releaseStatus.CanaryStatus.CurrentBatch)
		return false
	}
}
