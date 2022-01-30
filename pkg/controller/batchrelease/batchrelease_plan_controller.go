package batchrelease

import (
	"fmt"
	"reflect"
	"time"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
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
}

// Do execute the release plan
func (r *Executor) Do() (reconcile.Result, *v1alpha1.BatchReleaseStatus) {
	klog.V(3).InfoS("Reconcile the release plan",
		"target-workload", r.release.Spec.TargetRef.Name)

	klog.V(3).InfoS("release-status:",
		"release-phase", r.releaseStatus.Phase,
		"batch-rolling-state", r.releaseStatus.CanaryStatus.ReleasingBatchState,
		"current-batch", r.releaseStatus.CanaryStatus.CurrentBatch)

	workloadController, err := r.GetWorkloadController()
	if err != nil {
		return reconcile.Result{}, r.releaseStatus
	}

	shouldStopThisRound, retryDuration := r.handleSpecialCases(workloadController)
	if shouldStopThisRound {
		return retryDuration, r.releaseStatus
	}

	return r.executeBatchReleasePlan(workloadController)
}

func (r *Executor) executeBatchReleasePlan(workloadController workloads.WorkloadController) (reconcile.Result, *v1alpha1.BatchReleaseStatus) {
	status := r.releaseStatus
	retryDuration := reconcile.Result{}

	switch status.Phase {
	case v1alpha1.RolloutPhaseHealthy:
		r.releaseStatus.Phase = v1alpha1.RolloutPhaseVerify
		fallthrough

	case v1alpha1.RolloutPhaseVerify:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseVerify)
		// verify whether the workload is ready to execute the release plan in this state.
		verified, err := workloadController.VerifySpec()
		switch {
		case err != nil:
			setCondition(r.releaseStatus, "VerifyWorkloadError", err.Error(), v1.ConditionFalse)
		case verified:
			setCondition(r.releaseStatus, "VerifyWorkloadSuccessfully", "", v1.ConditionTrue)
			status.Phase = v1alpha1.RolloutPhaseInitial
			fallthrough
		default:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhaseInitial:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseInitial)
		r.releaseStatus.Phase = v1alpha1.RolloutPhasePreparing
		fallthrough

	case v1alpha1.RolloutPhasePreparing:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhasePreparing)
		// prepare and initialize something before progressing in this state.
		initialized, err := workloadController.Initialize()
		switch {
		case err != nil:
			setCondition(r.releaseStatus, "InitializeError", err.Error(), v1.ConditionFalse)
		case initialized:
			setCondition(r.releaseStatus, "InitializeSuccessfully", "", v1.ConditionTrue)
			status.Phase = v1alpha1.RolloutPhaseProgressing
			fallthrough
		default:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.RolloutPhaseProgressing:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseProgressing)
		// progress the release plan in this state.
		var progressDone bool
		progressDone, retryDuration = r.progressBatches(workloadController)
		if progressDone {
			setCondition(r.releaseStatus, "ProgressSuccessfully", "", v1.ConditionTrue)
			status.Phase = v1alpha1.RolloutPhaseFinalizing
		}

	case v1alpha1.RolloutPhaseFinalizing:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseFinalizing)
		// restore the workload in this state
		if succeed := workloadController.Finalize(false, false); succeed {
			cleanupConditions(status)
			status.Phase = v1alpha1.RolloutPhaseCompleted
		}
		retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}

	case v1alpha1.RolloutPhaseRollingBack:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseRollingBack)
		// restore the workload in this state
		cleanup := metav1.GetControllerOf(r.release) == nil
		if succeed := workloadController.Finalize(false, cleanup); succeed {
			cleanupConditions(status)
			status.Phase = v1alpha1.RolloutPhaseCancelled
		}
		retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}

	case v1alpha1.RolloutPhaseCompleted:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseCompleted)
		// this state indicates that the plan is executed successfully, should do nothing in this state.

	case v1alpha1.RolloutPhaseTerminating:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseTerminating)
		if succeed := workloadController.Finalize(true, true); succeed {
			if r.release.DeletionTimestamp != nil {
				setCondition(status, v1alpha1.TerminatingReasonInTerminating, "Release plan was cancelled or deleted", v1.ConditionTrue)
			} else {
				status.Phase = v1alpha1.RolloutPhaseCancelled
			}
		}
		retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}

	case v1alpha1.RolloutPhaseCancelled:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", v1alpha1.RolloutPhaseCancelled)
		// this state indicates that the plan is cancelled successfully, should do nothing in this state.

	default:
		klog.V(3).Infof("ReleasePlan State Machine into %s state", "Unknown")
		panic(fmt.Sprintf("illegal release status %+v", status))
	}

	return retryDuration, status
}

// reconcile logic when we are in the middle of release, we have to go through finalizing state before succeed or fail
func (r *Executor) progressBatches(workloadController workloads.WorkloadController) (bool, reconcile.Result) {
	progressDone := false
	retryDuration := reconcile.Result{}

	switch r.releaseStatus.CanaryStatus.ReleasingBatchState {
	case "", v1alpha1.InitializeBatchState:
		klog.V(3).Infof("ReleaseBatch State Machine into %s state", v1alpha1.InitializeBatchState)
		// prepare something before do canary to modify workload, such as calculating suitable batch index.
		r.releaseStatus.CanaryStatus.ReleasingBatchState = v1alpha1.DoCanaryBatchState
		fallthrough

	case v1alpha1.DoCanaryBatchState:
		klog.V(3).Infof("ReleaseBatch State Machine into %s state", v1alpha1.DoCanaryBatchState)
		// modify workload replicas/partition based on release plan in this state.
		upgradeDone, err := workloadController.RolloutOneBatchPods()
		switch {
		case err != nil:
			setCondition(r.releaseStatus, "DoCanaryError", err.Error(), v1.ConditionFalse)
		case upgradeDone:
			r.releaseStatus.CanaryStatus.ReleasingBatchState = v1alpha1.VerifyBatchState
			fallthrough
		default:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
		}

	case v1alpha1.VerifyBatchState:
		klog.V(3).Infof("ReleaseBatch State Machine into %s state", v1alpha1.VerifyBatchState)
		// TODO: metrics analysis
		// replicas/partition has been modified, should wait pod ready in this state.
		verified, err := workloadController.CheckOneBatchPods()
		switch {
		case err != nil:
			setCondition(r.releaseStatus, "VerifyBatchReadyError", err.Error(), v1.ConditionFalse)
		case verified:
			retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}
			r.releaseStatus.CanaryStatus.LastBatchReadyTime = metav1.Now()
			r.releaseStatus.CanaryStatus.ReleasingBatchState = v1alpha1.ReadyBatchState
		default:
			r.releaseStatus.CanaryStatus.ReleasingBatchState = v1alpha1.InitializeBatchState
		}

	case v1alpha1.ReadyBatchState:
		klog.V(3).Infof("ReleaseBatch State Machine into %s state", v1alpha1.ReadyBatchState)
		// all the pods in the batch are upgraded and their state are ready
		// wait to move to the next batch if there are any
		progressDone = r.moveToNextBatch()
		retryDuration = reconcile.Result{RequeueAfter: DefaultShortDuration}

	default:
		klog.V(3).Infof("ReleaseBatch State Machine into %s state", "Unknown")
		panic(fmt.Sprintf("illegal status %+v", r.releaseStatus))
	}

	return progressDone, retryDuration
}

// GetWorkloadController pick the right workload controller to work on the workload
func (r *Executor) GetWorkloadController() (workloads.WorkloadController, error) {
	targetRef := r.release.Spec.TargetRef
	targetKey := types.NamespacedName{
		Namespace: r.release.Namespace,
		Name:      targetRef.Name,
	}

	switch targetRef.APIVersion {
	case kruiseappsv1alpha1.GroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(kruiseappsv1alpha1.CloneSet{}).Name() {
			klog.InfoS("using cloneset batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return workloads.NewCloneSetRolloutController(r.client, r.recorder, r.release, r.releasePlan, r.releaseStatus, targetKey), nil
		}

	case apps.SchemeGroupVersion.String():
		if targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() {
			klog.InfoS("using deployment batch release controller for this batch release", "workload name", targetKey.Name, "namespace", targetKey.Namespace)
			return workloads.NewDeploymentRolloutController(r.client, r.recorder, r.release, r.releasePlan, r.releaseStatus, targetKey), nil
		}
	}
	message := fmt.Sprintf("the workload `%v/%v` is not supported", targetRef.APIVersion, targetRef.Kind)
	r.recorder.Event(r.release, v1.EventTypeWarning, "UnsupportedWorkload", message)
	return nil, fmt.Errorf(message)
}

func (r *Executor) moveToNextBatch() bool {
	currentBatch := int(r.releaseStatus.CanaryStatus.CurrentBatch)
	if currentBatch >= len(r.releasePlan.Batches)-1 {
		klog.V(3).InfoS("Finished all batch release", "current batch", r.releaseStatus.CanaryStatus.CurrentBatch)
		return true
	} else {
		if r.releasePlan.BatchPartition == nil ||
			*r.releasePlan.BatchPartition > r.releaseStatus.CanaryStatus.CurrentBatch {
			r.releaseStatus.CanaryStatus.CurrentBatch++
		}
		r.releaseStatus.CanaryStatus.ReleasingBatchState = v1alpha1.InitializeBatchState
		klog.V(3).InfoS("Finished one batch release", "current batch", r.releaseStatus.CanaryStatus.CurrentBatch)
		return false
	}
}
