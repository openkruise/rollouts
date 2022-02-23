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
	workloads2 "github.com/openkruise/rollouts/controllers/batchrelease/workloads"
	"k8s.io/utils/pointer"
	"reflect"
	"time"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DefaultLongDuration  = 5 * time.Second
	DefaultShortDuration = (50 * 1000) * time.Microsecond
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
func (r *Executor) Do() (reconcile.Result, *v1alpha1.BatchReleaseStatus) {
	klog.InfoS("Starting one round of reconciling release plan",
		"BatchRelease", client.ObjectKeyFromObject(r.release),
		"phase", r.releaseStatus.Phase,
		"current-batch", r.releaseStatus.CanaryStatus.CurrentBatch,
		"current-batch-state", r.releaseStatus.CanaryStatus.CurrentBatchState)

	workloadController, err := r.GetWorkloadController()
	if err != nil || workloadController == nil {
		return reconcile.Result{}, r.releaseStatus
	}

	shouldStopThisRound, retryDuration := r.checkHealthyBeforeExecution(workloadController)
	if shouldStopThisRound {
		return retryDuration, r.releaseStatus
	}

	return r.executeBatchReleasePlan(workloadController)
}

func (r *Executor) executeBatchReleasePlan(workloadController workloads2.WorkloadController) (reconcile.Result, *v1alpha1.BatchReleaseStatus) {
	status := r.releaseStatus
	retryDuration := reconcile.Result{}

	switch status.Phase {
	case v1alpha1.RolloutPhaseInitial:
		// if this batchRelease was created but workload doest not exist,
		// should keep this phase and do nothing util workload is created.

	case v1alpha1.RolloutPhaseHealthy:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseHealthy)
		// verify whether the workload is ready to execute the release plan in this state.
		needed, err := workloadController.IfNeedToProgress()
		switch {
		case err != nil:
			setCondition(status, "VerifyWorkloadError", err.Error(), v1.ConditionFalse)
		case needed:
			setCondition(status, "VerifyWorkloadSuccessfully", "", v1.ConditionTrue)
			status.Phase = v1alpha1.RolloutPhasePreparing
			fallthrough
		default:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhasePreparing:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhasePreparing)
		// prepare and initialize something before progressing in this state.
		initialized, err := workloadController.PrepareBeforeProgress()
		switch {
		case err != nil:
			setCondition(status, "InitializeError", err.Error(), v1.ConditionFalse)
		case initialized:
			setCondition(status, "InitializeSuccessfully", "", v1.ConditionTrue)
			status.Phase = v1alpha1.RolloutPhaseProgressing
			fallthrough
		default:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhaseProgressing:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseProgressing)
		// progress the release plan in this state.
		var progressDone bool
		if progressDone, retryDuration = r.progressBatches(workloadController); progressDone {
			setCondition(status, "ProgressSuccessfully", "", v1.ConditionTrue)
			status.Phase = v1alpha1.RolloutPhaseFinalizing
		}

	case v1alpha1.RolloutPhaseFinalizing:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseFinalizing)
		// finalize canary the resources when progressing done.
		// Do not clean the canary resources and set 'paused=false' if it is controlled by rollout,
		// because rollout controller should route the traffic firstly.
		clean := false
		pause := pointer.BoolPtr(false)
		if util.IsControlledByRollout(r.release) {
			pause = nil
			clean = false
		}

		if succeed := workloadController.FinalizeProgress(pause, clean); succeed {
			cleanupConditions(status)
			status.Phase = v1alpha1.RolloutPhaseCompleted
		} else {
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhaseAbort:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseAbort)
		// Abort the release plan.
		// do not clean the canary resources if it is controlled by rollout,
		// because rollout controller should route the traffic firstly.
		clean := true
		pause := pointer.BoolPtr(false)
		if util.IsControlledByRollout(r.release) {
			pause = nil
			clean = false
		}

		if succeed := workloadController.FinalizeProgress(pause, clean); succeed {
			cleanupConditions(status)
			status.Phase = v1alpha1.RolloutPhaseCancelled
		} else {
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhaseTerminating:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseTerminating)
		if succeed := workloadController.FinalizeProgress(nil, true); succeed {
			if r.release.DeletionTimestamp != nil {
				setCondition(status, v1alpha1.TerminatingReasonInTerminating, "Release plan was cancelled or deleted", v1.ConditionTrue)
			} else {
				status.Phase = v1alpha1.RolloutPhaseCancelled
			}
		} else {
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhaseCompleted:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseCompleted)
		// this state indicates that the plan is executed successfully, should do nothing in this state.

	case v1alpha1.RolloutPhaseCancelled:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, v1alpha1.RolloutPhaseCancelled)
		// this state indicates that the plan is cancelled successfully, should do nothing in this state.

	default:
		klog.V(3).Infof("BatchRelease(%v) State Machine into %s state", r.releaseKey, "Unknown")
		panic(fmt.Sprintf("illegal release status %+v", status))
	}

	return retryDuration, status
}

// reconcile logic when we are in the middle of release, we have to go through finalizing state before succeed or fail
func (r *Executor) progressBatches(workloadController workloads2.WorkloadController) (bool, reconcile.Result) {
	progressDone := false
	retryDuration := reconcile.Result{}

	switch r.releaseStatus.CanaryStatus.CurrentBatchState {
	case "", v1alpha1.InitializeBatchState:
		klog.V(3).Infof("BatchRelease(%v) Batch State Machine into %s state", r.releaseKey, v1alpha1.InitializeBatchState)
		// prepare something before do canary to modify workload, such as calculating suitable batch index.
		r.releaseStatus.CanaryStatus.CurrentBatchState = v1alpha1.DoCanaryBatchState
		fallthrough

	case v1alpha1.DoCanaryBatchState:
		klog.V(3).Infof("BatchRelease(%v) Batch State Machine into %s state", r.releaseKey, v1alpha1.DoCanaryBatchState)
		// modify workload replicas/partition based on release plan in this state.
		upgradeDone, err := workloadController.ProgressOneBatchReplicas()
		switch {
		case err != nil:
			setCondition(r.releaseStatus, "DoCanaryError", err.Error(), v1.ConditionFalse)
		case upgradeDone:
			r.releaseStatus.CanaryStatus.CurrentBatchState = v1alpha1.VerifyBatchState
			fallthrough
		default:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.VerifyBatchState:
		klog.V(3).Infof("BatchRelease(%v) Batch State Machine into %s state", r.releaseKey, v1alpha1.VerifyBatchState)
		// TODO: metrics analysis
		// replicas/partition has been modified, should wait pod ready in this state.
		verified, err := workloadController.CheckOneBatchReplicas()
		switch {
		case err != nil:
			setCondition(r.releaseStatus, "VerifyBatchReadyError", err.Error(), v1.ConditionFalse)
		case verified:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
			r.releaseStatus.CanaryStatus.BatchReadyTime = metav1.Now()
			r.releaseStatus.CanaryStatus.CurrentBatchState = v1alpha1.ReadyBatchState
		default:
			r.releaseStatus.CanaryStatus.CurrentBatchState = v1alpha1.InitializeBatchState
		}

	case v1alpha1.ReadyBatchState:
		klog.V(3).Infof("BatchRelease(%v) Batch State Machine into %s state", r.releaseKey, v1alpha1.ReadyBatchState)
		// all the pods in the batch are upgraded and their state are ready
		// wait to move to the next batch if there are any
		progressDone = r.moveToNextBatch()
		retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}

	default:
		klog.V(3).Infof("ReleasePlan(%v) Batch State Machine into %s state", "Unknown")
		panic(fmt.Sprintf("illegal status %+v", r.releaseStatus))
	}

	return progressDone, retryDuration
}

// GetWorkloadController pick the right workload controller to work on the workload
func (r *Executor) GetWorkloadController() (workloads2.WorkloadController, error) {
	targetRef := r.release.Spec.TargetRef.WorkloadRef
	if targetRef == nil {
		return nil, nil
	}

	targetKey := types.NamespacedName{
		Namespace: r.release.Namespace,
		Name:      targetRef.Name,
	}

	switch targetRef.APIVersion {
	case kruiseappsv1alpha1.GroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(kruiseappsv1alpha1.CloneSet{}).Name() {
			klog.InfoS("using cloneset batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return workloads2.NewCloneSetRolloutController(r.client, r.recorder, r.release, r.releasePlan, r.releaseStatus, targetKey), nil
		}

	case apps.SchemeGroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() {
			klog.InfoS("using deployment batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return workloads2.NewDeploymentRolloutController(r.client, r.recorder, r.release, r.releasePlan, r.releaseStatus, targetKey), nil
		}
	}
	message := fmt.Sprintf("the workload `%v.%v/%v` is not supported", targetRef.APIVersion, targetRef.Kind, targetRef.Name)
	r.recorder.Event(r.release, v1.EventTypeWarning, "UnsupportedWorkload", message)
	return nil, fmt.Errorf(message)
}

func (r *Executor) moveToNextBatch() bool {
	currentBatch := int(r.releaseStatus.CanaryStatus.CurrentBatch)
	if currentBatch >= len(r.releasePlan.Batches)-1 {
		klog.V(3).Infof("BatchRelease(%v) finished all batch, release current batch: %v", r.releasePlan, r.releaseStatus.CanaryStatus.CurrentBatch)
		return true
	} else {
		if r.releasePlan.BatchPartition == nil ||
			*r.releasePlan.BatchPartition > r.releaseStatus.CanaryStatus.CurrentBatch {
			r.releaseStatus.CanaryStatus.CurrentBatch++
		}
		r.releaseStatus.CanaryStatus.CurrentBatchState = v1alpha1.InitializeBatchState
		klog.V(3).Infof("BatchRelease(%v) finished one batch, release current batch: %v", r.releasePlan, r.releaseStatus.CanaryStatus.CurrentBatch)
		return false
	}
}
