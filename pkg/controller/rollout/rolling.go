package rollout

import (
	"time"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

func (r *RolloutReconciler) doProgressingInRolling(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	// Handle the 5 special cases firstly, and we had better keep the order of following cases:

	switch {
	// 1. In case of rollback in a quick way, un-paused and just use workload rolling strategy
	case isRollingBackDirectly(rollout, workload):
		return r.handleRollbackDirectly(rollout, workload, newStatus)

	// 2. In case of rollout paused, just stop reconcile
	case isRolloutPaused(rollout):
		return r.handleRolloutPaused(rollout, newStatus)

	// 3. In case of rollback in a batch way, use rollout step strategy
	case isRollingBackInBatches(rollout, workload):
		return r.handleRollbackInBatches(rollout, workload, newStatus)

	// 4. In case of continuous publishing(v1 -> v2 -> v3), restart publishing
	case isContinuousRelease(rollout, workload):
		return r.handleContinuousRelease(rollout, workload, newStatus)

	// 5. In case of rollout plan changed, recalculate and publishing
	case isRolloutPlanChanged(rollout):
		return r.handleRolloutPlanChanged(rollout, workload, newStatus)
	}

	return r.handleNormalRolling(rollout, workload, newStatus)
}

func (r *RolloutReconciler) handleRolloutPaused(rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	klog.Infof("rollout(%s/%s) is Progressing, but paused", rollout.Namespace, rollout.Name)
	progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonPaused, "Rollout has been paused, you can resume it by kube-cli")
	return nil, nil
}

func (r *RolloutReconciler) handleContinuousRelease(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload continuous publishing canaryRevision, then restart publishing")
	klog.Infof("rollout(%s/%s) workload continuous publishing canaryRevision from(%s) -> to(%s), then restart publishing",
		rollout.Namespace, rollout.Name, newStatus.CanaryStatus.CanaryRevision, workload.CanaryRevision)

	var recheckTime *time.Time
	done, err := r.doProgressingReset(rollout, newStatus)
	if err != nil {
		klog.Errorf("rollout(%s/%s) doProgressingReset failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	} else if done {
		progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonInitializing, "Workload is continuous release")
		klog.Infof("rollout(%s/%s) workload is continuous publishing, reset complete", rollout.Namespace, rollout.Name)
	} else {
		// Incomplete, recheck
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		recheckTime = &expectedTime
		klog.Infof("rollout(%s/%s) workload is continuous publishing, reset incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
	}
	return recheckTime, nil
}

func (r *RolloutReconciler) handleRollbackDirectly(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
	r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload has been rollback, then rollout is canceled")
	klog.Infof("rollout(%s/%s) workload has been rollback directly, then rollout canceled", rollout.Namespace, rollout.Name)
	progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonCancelling, "The workload has been rolled back and the rollout process will be cancelled")
	return nil, nil
}

func (r *RolloutReconciler) handleRollbackInBatches(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	// restart from the beginning
	newStatus.CanaryStatus.CurrentStepIndex = 1
	newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
	newStatus.CanaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
	newStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	newStatus.CanaryStatus.RolloutHash = rollout.Annotations[util.RolloutHashAnnotation]
	klog.Infof("rollout(%s/%s) workload has been rollback in batches, then restart from beginning", rollout.Namespace, rollout.Name)
	return nil, nil
}

func (r *RolloutReconciler) handleRolloutPlanChanged(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	batchControl := batchrelease.NewInnerBatchController(r.Client, rollout, getRolloutID(workload, rollout))
	newStepIndex, err := r.reCalculateCanaryStepIndex(rollout, batchControl)
	if err != nil {
		klog.Errorf("rollout(%s/%s) reCalculate Canary StepIndex failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	// canary step configuration change causes current step index change
	newStatus.CanaryStatus.CurrentStepIndex = newStepIndex
	newStatus.CanaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
	newStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	newStatus.CanaryStatus.RolloutHash = rollout.Annotations[util.RolloutHashAnnotation]
	klog.Infof("rollout(%s/%s) canary step configuration change, and stepIndex(%d) state(%s)",
		rollout.Namespace, rollout.Name, newStatus.CanaryStatus.CurrentStepIndex, newStatus.CanaryStatus.CurrentStepState)
	return nil, nil
}

func (r *RolloutReconciler) handleNormalRolling(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	//check if canary is done
	if newStatus.CanaryStatus.CurrentStepState == rolloutv1alpha1.CanaryStepStateCompleted {
		klog.Infof("rollout(%s/%s) progressing rolling done", rollout.Namespace, rollout.Name)
		progressingStateTransition(newStatus, corev1.ConditionTrue, rolloutv1alpha1.ProgressingReasonFinalising, "Rollout has been completed and some closing work is being done")
	} else { // rollout is in rolling
		newStatus.CanaryStatus.PodTemplateHash = workload.PodTemplateHash
		return r.doNormalRolling(rollout, workload, newStatus)
	}
	return nil, nil
}

func (r *RolloutReconciler) doNormalRolling(rollout *rolloutv1alpha1.Rollout, workload *util.Workload, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	rolloutCon := newRolloutContext(r.Client, r.Recorder, rollout, newStatus, workload)
	err := rolloutCon.reconcile()
	if err != nil {
		klog.Errorf("rollout(%s/%s) Progressing failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	return rolloutCon.recheckTime, nil
}

/* **********************************************************************
	help functions
*********************************************************************** */
func isRolloutPaused(rollout *rolloutv1alpha1.Rollout) bool {
	return rollout.Spec.Strategy.Paused
}

func isRolloutPlanChanged(rollout *rolloutv1alpha1.Rollout) bool {
	status := &rollout.Status
	return status.CanaryStatus.RolloutHash != "" && status.CanaryStatus.RolloutHash != rollout.Annotations[util.RolloutHashAnnotation]
}

func isContinuousRelease(rollout *rolloutv1alpha1.Rollout, workload *util.Workload) bool {
	status := &rollout.Status
	return status.CanaryStatus.CanaryRevision != "" && workload.CanaryRevision != status.CanaryStatus.CanaryRevision && !workload.IsInRollback
}

func isRollingBackDirectly(rollout *rolloutv1alpha1.Rollout, workload *util.Workload) bool {
	status := &rollout.Status
	inBatch := util.IsRollbackInBatchPolicy(rollout, workload.Labels)
	return workload.IsInRollback && workload.CanaryRevision != status.CanaryStatus.CanaryRevision && !inBatch
}

func isRollingBackInBatches(rollout *rolloutv1alpha1.Rollout, workload *util.Workload) bool {
	// currently, only support the case of no traffic routing
	if len(rollout.Spec.Strategy.Canary.TrafficRoutings) > 0 {
		return false
	}
	status := &rollout.Status
	inBatch := util.IsRollbackInBatchPolicy(rollout, workload.Labels)
	return workload.IsInRollback && workload.CanaryRevision != status.CanaryStatus.CanaryRevision && inBatch
}
