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

package rollout

import (
	"strconv"
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
)

var defaultGracePeriodSeconds int32 = 3

// parameter1 retryReconcile, parameter2 error
func (r *RolloutReconciler) reconcileRolloutProgressing(rollout *v1alpha1.Rollout, newStatus *v1alpha1.RolloutStatus) (*time.Time, error) {
	cond := util.GetRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
	klog.Infof("reconcile rollout(%s/%s) progressing action...", rollout.Namespace, rollout.Name)
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	} else if workload == nil {
		klog.Errorf("rollout(%s/%s) workload Not Found", rollout.Namespace, rollout.Name)
		return nil, nil
	} else if !workload.IsStatusConsistent {
		klog.Infof("rollout(%s/%s) workload status is inconsistent, then wait a moment", rollout.Namespace, rollout.Name)
		return nil, nil
	}
	rolloutContext := &util.RolloutContext{Rollout: rollout, NewStatus: newStatus, Workload: workload}
	switch cond.Reason {
	case v1alpha1.ProgressingReasonInitializing:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		// new canaryStatus
		newStatus.CanaryStatus = &v1alpha1.CanaryStatus{
			ObservedWorkloadGeneration: rolloutContext.Workload.Generation,
			RolloutHash:                rolloutContext.Rollout.Annotations[util.RolloutHashAnnotation],
			ObservedRolloutID:          getRolloutID(rolloutContext.Workload),
			StableRevision:             rolloutContext.Workload.StableRevision,
			CanaryRevision:             rolloutContext.Workload.CanaryRevision,
			CurrentStepIndex:           1,
			CurrentStepState:           v1alpha1.CanaryStepStateUpgrade,
			LastUpdateTime:             &metav1.Time{Time: time.Now()},
		}
		done, err := r.doProgressingInitializing(rolloutContext)
		if err != nil {
			klog.Errorf("rollout(%s/%s) doProgressingInitializing error(%s)", rollout.Namespace, rollout.Name, err.Error())
			return nil, err
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionTrue, v1alpha1.ProgressingReasonInRolling, "Rollout is in Progressing")
		} else {
			// Incomplete, recheck
			expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
			rolloutContext.RecheckTime = &expectedTime
			klog.Infof("rollout(%s/%s) doProgressingInitializing is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		}

	case v1alpha1.ProgressingReasonInRolling:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		err = r.doProgressingInRolling(rolloutContext)
		if err != nil {
			return nil, err
		}

	case v1alpha1.ProgressingReasonFinalising:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		var done bool
		rolloutContext.WaitReady = true
		done, err = r.doFinalising(rolloutContext)
		if err != nil {
			return nil, err
			// finalizer is finished
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionFalse, v1alpha1.ProgressingReasonCompleted, "Rollout progressing has been completed")
			setRolloutSucceededCondition(newStatus, corev1.ConditionTrue)
		} else {
			// Incomplete, recheck
			expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
			rolloutContext.RecheckTime = &expectedTime
			klog.Infof("rollout(%s/%s) doProgressingFinalising is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		}

	case v1alpha1.ProgressingReasonPaused:
		// from paused to rolling progressing
		if !rollout.Spec.Strategy.Paused {
			klog.Infof("rollout(%s/%s) is Progressing, from paused to rolling", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionTrue, v1alpha1.ProgressingReasonInRolling, "")
		}

	case v1alpha1.ProgressingReasonCancelling:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		var done bool
		done, err = r.doFinalising(rolloutContext)
		if err != nil {
			return nil, err
			// finalizer is finished
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionFalse, v1alpha1.ProgressingReasonCompleted, "Rollout progressing has been cancelled")
			setRolloutSucceededCondition(newStatus, corev1.ConditionFalse)
		} else {
			// Incomplete, recheck
			expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
			rolloutContext.RecheckTime = &expectedTime
			klog.Infof("rollout(%s/%s) doProgressingCancelling is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		}

	case v1alpha1.ProgressingReasonCompleted:
		// rollout phase from progressing to healthy
		klog.Infof("rollout(%s/%s) phase is from progressing to healthy", rollout.Namespace, rollout.Name)
		newStatus.Phase = v1alpha1.RolloutPhaseHealthy
	}

	return rolloutContext.RecheckTime, nil
}

func (r *RolloutReconciler) doProgressingInitializing(c *util.RolloutContext) (bool, error) {
	// Traffic routing
	if len(c.Rollout.Spec.Strategy.Canary.TrafficRoutings) > 0 {
		if err := r.trafficRoutingManager.InitializeTrafficRouting(c); err != nil {
			return false, err
		}
	}

	// It is not allowed to modify the rollout.spec in progressing phase (validate webhook rollout),
	// but in many scenarios the user may modify the workload and rollout spec at the same time,
	// and there is a possibility that the workload is released first, and due to some network or other reasons the rollout spec is delayed by a few seconds,
	// so this is mainly compatible with this scenario.
	cond := util.GetRolloutCondition(*c.NewStatus, v1alpha1.RolloutConditionProgressing)
	if verifyTime := cond.LastUpdateTime.Add(time.Second * time.Duration(defaultGracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("verify rollout(%s/%s) TrafficRouting, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
		return false, nil
	}
	return true, nil
}

func (r *RolloutReconciler) doProgressingInRolling(c *util.RolloutContext) error {
	// Handle the 5 special cases firstly, and we had better keep the order of following cases:

	switch {
	// 1. In case of rollback in a quick way, un-paused and just use workload rolling strategy
	case isRollingBackDirectly(c.Rollout, c.Workload):
		return r.handleRollbackDirectly(c.Rollout, c.Workload, c.NewStatus)

	// 2. In case of rollout paused, just stop reconcile
	case isRolloutPaused(c.Rollout):
		return r.handleRolloutPaused(c.Rollout, c.NewStatus)

	// 3. In case of rollback in a batch way, use rollout step strategy
	case isRollingBackInBatches(c.Rollout, c.Workload):
		return r.handleRollbackInBatches(c.Rollout, c.Workload, c.NewStatus)

	// 4. In case of continuous publishing(v1 -> v2 -> v3), restart publishing
	case isContinuousRelease(c.Rollout, c.Workload):
		return r.handleContinuousRelease(c)

	// 5. In case of rollout plan changed, recalculate and publishing
	case isRolloutPlanChanged(c.Rollout):
		return r.handleRolloutPlanChanged(c)
	}
	return r.handleNormalRolling(c)
}

func (r *RolloutReconciler) handleRolloutPaused(rollout *v1alpha1.Rollout, newStatus *v1alpha1.RolloutStatus) error {
	klog.Infof("rollout(%s/%s) is Progressing, but paused", rollout.Namespace, rollout.Name)
	progressingStateTransition(newStatus, corev1.ConditionTrue, v1alpha1.ProgressingReasonPaused, "Rollout has been paused, you can resume it by kube-cli")
	return nil
}

func (r *RolloutReconciler) handleContinuousRelease(c *util.RolloutContext) error {
	r.Recorder.Eventf(c.Rollout, corev1.EventTypeNormal, "Progressing", "workload continuous publishing canaryRevision, then restart publishing")
	klog.Infof("rollout(%s/%s) workload continuous publishing canaryRevision from(%s) -> to(%s), then restart publishing",
		c.Rollout.Namespace, c.Rollout.Name, c.NewStatus.CanaryStatus.CanaryRevision, c.Workload.CanaryRevision)

	done, err := r.doProgressingReset(c)
	if err != nil {
		klog.Errorf("rollout(%s/%s) doProgressingReset failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return err
	} else if done {
		c.NewStatus.CanaryStatus = nil
		progressingStateTransition(c.NewStatus, corev1.ConditionTrue, v1alpha1.ProgressingReasonInitializing, "Workload is continuous release")
		klog.Infof("rollout(%s/%s) workload is continuous publishing, reset complete", c.Rollout.Namespace, c.Rollout.Name)
	} else {
		// Incomplete, recheck
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		c.RecheckTime = &expectedTime
		klog.Infof("rollout(%s/%s) workload is continuous publishing, reset incomplete, and recheck(%s)", c.Rollout.Namespace, c.Rollout.Name, expectedTime.String())
	}
	return nil
}

func (r *RolloutReconciler) handleRollbackDirectly(rollout *v1alpha1.Rollout, workload *util.Workload, newStatus *v1alpha1.RolloutStatus) error {
	newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
	r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload has been rollback, then rollout is canceled")
	klog.Infof("rollout(%s/%s) workload has been rollback directly, then rollout canceled", rollout.Namespace, rollout.Name)
	progressingStateTransition(newStatus, corev1.ConditionTrue, v1alpha1.ProgressingReasonCancelling, "The workload has been rolled back and the rollout process will be cancelled")
	return nil
}

func (r *RolloutReconciler) handleRollbackInBatches(rollout *v1alpha1.Rollout, workload *util.Workload, newStatus *v1alpha1.RolloutStatus) error {
	// restart from the beginning
	newStatus.CanaryStatus.CurrentStepIndex = 1
	newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
	newStatus.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
	newStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	newStatus.CanaryStatus.RolloutHash = rollout.Annotations[util.RolloutHashAnnotation]
	klog.Infof("rollout(%s/%s) workload has been rollback in batches, then restart from beginning", rollout.Namespace, rollout.Name)
	return nil
}

func (r *RolloutReconciler) handleRolloutPlanChanged(c *util.RolloutContext) error {
	newStepIndex, err := r.recalculateCanaryStep(c)
	if err != nil {
		klog.Errorf("rollout(%s/%s) reCalculate Canary StepIndex failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return err
	}
	// canary step configuration change causes current step index change
	c.NewStatus.CanaryStatus.CurrentStepIndex = newStepIndex
	c.NewStatus.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
	c.NewStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	c.NewStatus.CanaryStatus.RolloutHash = c.Rollout.Annotations[util.RolloutHashAnnotation]
	klog.Infof("rollout(%s/%s) canary step configuration change, and stepIndex(%d) state(%s)",
		c.Rollout.Namespace, c.Rollout.Name, c.NewStatus.CanaryStatus.CurrentStepIndex, c.NewStatus.CanaryStatus.CurrentStepState)
	return nil
}

func (r *RolloutReconciler) handleNormalRolling(c *util.RolloutContext) error {
	//check if canary is done
	if c.NewStatus.CanaryStatus.CurrentStepState == v1alpha1.CanaryStepStateCompleted {
		klog.Infof("rollout(%s/%s) progressing rolling done", c.Rollout.Namespace, c.Rollout.Name)
		progressingStateTransition(c.NewStatus, corev1.ConditionTrue, v1alpha1.ProgressingReasonFinalising, "Rollout has been completed and some closing work is being done")
	} else { // rollout is in rolling
		return r.canaryManager.runCanary(c)
	}
	return nil
}

/* **********************************************************************
	help functions
*********************************************************************** */
func isRolloutPaused(rollout *v1alpha1.Rollout) bool {
	return rollout.Spec.Strategy.Paused
}

func isRolloutPlanChanged(rollout *v1alpha1.Rollout) bool {
	status := &rollout.Status
	return status.CanaryStatus.RolloutHash != "" && status.CanaryStatus.RolloutHash != rollout.Annotations[util.RolloutHashAnnotation]
}

func isContinuousRelease(rollout *v1alpha1.Rollout, workload *util.Workload) bool {
	status := &rollout.Status
	return status.CanaryStatus.CanaryRevision != "" && workload.CanaryRevision != status.CanaryStatus.CanaryRevision && !workload.IsInRollback
}

func isRollingBackDirectly(rollout *v1alpha1.Rollout, workload *util.Workload) bool {
	status := &rollout.Status
	inBatch := util.IsRollbackInBatchPolicy(rollout, workload.Labels)
	return workload.IsInRollback && workload.CanaryRevision != status.CanaryStatus.CanaryRevision && !inBatch
}

func isRollingBackInBatches(rollout *v1alpha1.Rollout, workload *util.Workload) bool {
	status := &rollout.Status
	inBatch := util.IsRollbackInBatchPolicy(rollout, workload.Labels)
	return workload.IsInRollback && workload.CanaryRevision != status.CanaryStatus.CanaryRevision && inBatch
}

// 1. modify network api(ingress or gateway api) configuration, and route 100% traffic to stable pods
// 2. remove batchRelease CR.
func (r *RolloutReconciler) doProgressingReset(c *util.RolloutContext) (bool, error) {
	if len(c.Rollout.Spec.Strategy.Canary.TrafficRoutings) > 0 {
		// modify network api(ingress or gateway api) configuration, and route 100% traffic to stable pods
		done, err := r.trafficRoutingManager.FinalisingTrafficRouting(c, false)
		if err != nil || !done {
			return done, err
		}
	}
	done, err := r.canaryManager.removeBatchRelease(c)
	if err != nil {
		klog.Errorf("rollout(%s/%s) DoFinalising batchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return false, err
	} else if !done {
		return false, nil
	}
	return true, nil
}

func (r *RolloutReconciler) recalculateCanaryStep(c *util.RolloutContext) (int32, error) {
	batch, err := r.canaryManager.fetchBatchRelease(c.Rollout.Namespace, c.Rollout.Name)
	if errors.IsNotFound(err) {
		return 1, nil
	} else if err != nil {
		return 0, err
	}
	currentReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.Spec.ReleasePlan.Batches[*batch.Spec.ReleasePlan.BatchPartition].CanaryReplicas, int(c.Workload.Replicas), true)
	var stepIndex int32
	for i := range c.Rollout.Spec.Strategy.Canary.Steps {
		step := c.Rollout.Spec.Strategy.Canary.Steps[i]
		var desiredReplicas int
		if step.Replicas != nil {
			desiredReplicas, _ = intstr.GetScaledValueFromIntOrPercent(step.Replicas, int(c.Workload.Replicas), true)
		} else {
			replicas := intstr.FromString(strconv.Itoa(int(*step.Weight)) + "%")
			desiredReplicas, _ = intstr.GetScaledValueFromIntOrPercent(&replicas, int(c.Workload.Replicas), true)
		}
		stepIndex = int32(i + 1)
		if currentReplicas <= desiredReplicas {
			break
		}
	}
	return stepIndex, nil
}

func (r *RolloutReconciler) doFinalising(c *util.RolloutContext) (bool, error) {
	klog.Infof("reconcile rollout(%s/%s) doFinalising", c.Rollout.Namespace, c.Rollout.Name)
	done, err := r.canaryManager.doCanaryFinalising(c)
	if err != nil {
		klog.Errorf("rollout(%s/%s) Progressing failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return false, err
	} else if !done {
		klog.Infof("rollout(%s/%s) finalizer is not finished, and retry reconcile", c.Rollout.Namespace, c.Rollout.Name)
		return false, nil
	}
	klog.Infof("run rollout(%s/%s) Progressing Finalising done", c.Rollout.Namespace, c.Rollout.Name)
	return true, nil
}

func progressingStateTransition(status *v1alpha1.RolloutStatus, condStatus corev1.ConditionStatus, reason, message string) {
	cond := util.GetRolloutCondition(*status, v1alpha1.RolloutConditionProgressing)
	if cond == nil {
		cond = util.NewRolloutCondition(v1alpha1.RolloutConditionProgressing, condStatus, reason, message)
	} else {
		cond.Status = condStatus
		cond.Reason = reason
		if message != "" {
			cond.Message = message
		}
	}
	util.SetRolloutCondition(status, *cond)
	status.Message = cond.Message
}

func setRolloutSucceededCondition(status *v1alpha1.RolloutStatus, condStatus corev1.ConditionStatus) {
	cond := util.GetRolloutCondition(*status, v1alpha1.RolloutConditionSucceeded)
	if cond == nil {
		cond = util.NewRolloutCondition(v1alpha1.RolloutConditionSucceeded, condStatus, "", "")
	} else {
		cond.Status = condStatus
	}
	util.SetRolloutCondition(status, *cond)
}
