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
	"context"
	"fmt"
	"strconv"
	"time"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
)

var defaultGracePeriodSeconds int32 = 3

// parameter1 retryReconcile, parameter2 error
func (r *RolloutReconciler) reconcileRolloutProgressing(rollout *rolloutv1alpha1.Rollout) (*time.Time, error) {
	cond := util.GetRolloutCondition(rollout.Status, rolloutv1alpha1.RolloutConditionProgressing)
	klog.Infof("reconcile rollout(%s/%s) progressing action", rollout.Namespace, rollout.Name)
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	} else if workload == nil {
		klog.Errorf("rollout(%s/%s) workload Not Found", rollout.Namespace, rollout.Name)
		return nil, nil
	} else if !workload.IsStatusConsistent {
		klog.Infof("rollout(%s/%s) workload status isn't consistent, then wait a moment", rollout.Namespace, rollout.Name)
		return nil, nil
	}

	var recheckTime *time.Time
	newStatus := rollout.Status.DeepCopy()
	switch cond.Reason {
	case rolloutv1alpha1.ProgressingReasonInitializing:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		// new canaryStatus
		done, _, err := r.doProgressingInitializing(rollout, newStatus)
		if err != nil {
			klog.Errorf("rollout(%s/%s) doProgressingInitializing error(%s)", rollout.Namespace, rollout.Name, err.Error())
			return nil, err
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonInRolling, "Rollout is in Progressing")
		} else {
			// Incomplete, recheck
			expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
			recheckTime = &expectedTime
			klog.Infof("rollout(%s/%s) doProgressingInitializing is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		}

	case rolloutv1alpha1.ProgressingReasonInRolling:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		recheckTime, err = r.doProgressingInRolling(rollout, workload, newStatus)
		if err != nil {
			return nil, err
		}

	case rolloutv1alpha1.ProgressingReasonFinalising:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		var done bool
		done, recheckTime, err = r.doFinalising(rollout, newStatus, true)
		if err != nil {
			return nil, err
			// finalizer is finished
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionTrue, rolloutv1alpha1.ProgressingReasonSucceeded, "Rollout has been completed, and succeed")
		}

	case rolloutv1alpha1.ProgressingReasonPaused:
		if workload.IsInRollback {
			newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
			r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload has been rollback, then rollout is canceled")
			klog.Infof("rollout(%s/%s) workload has been rollback, then rollout canceled", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonCancelling, "The workload has been rolled back and the rollout process will be cancelled")
			// from paused to inRolling
		} else if !rollout.Spec.Strategy.Paused {
			klog.Infof("rollout(%s/%s) is Progressing, but paused", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonInRolling, "")
		}

	case rolloutv1alpha1.ProgressingReasonCancelling:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		var done bool
		done, recheckTime, err = r.doFinalising(rollout, newStatus, false)
		if err != nil {
			return nil, err
			// finalizer is finished
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonCanceled, "")
		}

	case rolloutv1alpha1.ProgressingReasonSucceeded, rolloutv1alpha1.ProgressingReasonCanceled:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
	}

	err = r.updateRolloutStatusInternal(rollout, *newStatus)
	if err != nil {
		klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	return recheckTime, nil
}

func progressingStateTransition(status *rolloutv1alpha1.RolloutStatus, condStatus corev1.ConditionStatus, reason, message string) {
	cond := util.GetRolloutCondition(*status, rolloutv1alpha1.RolloutConditionProgressing)
	if cond == nil {
		cond = util.NewRolloutCondition(rolloutv1alpha1.RolloutConditionProgressing, condStatus, reason, message)
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

func (r *RolloutReconciler) doProgressingInitializing(rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus) (bool, string, error) {
	// canary release
	resetCanaryStatus(newStatus)
	return r.verifyCanaryStrategy(rollout, newStatus)
}

func (r *RolloutReconciler) doProgressingReset(rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus) (bool, error) {
	rolloutCon := newRolloutContext(r.Client, r.Recorder, rollout, newStatus, nil)
	if rolloutCon.rollout.Spec.Strategy.Canary.TrafficRoutings != nil {
		// 1. remove stable service podRevision selector
		done, err := rolloutCon.restoreStableService()
		if err != nil || !done {
			return done, err
		}
		// 2. route all traffic to stable service
		done, err = rolloutCon.doFinalisingTrafficRouting()
		if err != nil || !done {
			return done, err
		}
	}

	// 3. delete batchRelease CRD
	done, err := rolloutCon.batchControl.Finalize()
	if err != nil {
		klog.Errorf("rollout(%s/%s) DoFinalising batchRelease failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, err
	} else if !done {
		return false, nil
	}
	return true, nil
}

func (r *RolloutReconciler) verifyCanaryStrategy(rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus) (bool, string, error) {
	canary := rollout.Spec.Strategy.Canary
	// Traffic routing
	if canary.TrafficRoutings != nil && len(canary.TrafficRoutings) > 0 {
		rolloutCon := newRolloutContext(r.Client, r.Recorder, rollout, newStatus, nil)
		trController, err := rolloutCon.newTrafficRoutingController(rolloutCon)
		if err != nil {
			return false, "", err
		}
		if ok, msg, err := r.verifyTrafficRouting(rollout.Namespace, canary.TrafficRoutings[0], trController); !ok {
			return ok, msg, err
		}
	}

	// It is not allowed to modify the rollout.spec in progressing phase (validate webhook rollout),
	// but in many scenarios the user may modify the workload and rollout spec at the same time,
	// and there is a possibility that the workload is released first, and due to some network or other reasons the rollout spec is delayed by a few seconds,
	// so this is mainly compatible with this scenario.
	cond := util.GetRolloutCondition(*newStatus, rolloutv1alpha1.RolloutConditionProgressing)
	if verifyTime := cond.LastUpdateTime.Add(time.Second * time.Duration(defaultGracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("verify rollout(%s/%s) TrafficRouting done, and wait a moment", rollout.Namespace, rollout.Name)
		return false, "", nil
	}
	return true, "", nil
}

func (r *RolloutReconciler) verifyTrafficRouting(ns string, tr *rolloutv1alpha1.TrafficRouting, c trafficrouting.Controller) (bool, string, error) {
	// check service
	service := &corev1.Service{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: ns, Name: tr.Service}, service)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Sprintf("Service(%s/%s) is Not Found", ns, tr.Service), nil
		}
		return false, "", err
	}

	// check the traffic routing configuration
	err = c.Initialize(context.TODO())
	if err != nil {
		return false, "", err
	}
	return true, "", nil
}

func (r *RolloutReconciler) reCalculateCanaryStepIndex(rollout *rolloutv1alpha1.Rollout, batchControl batchrelease.BatchRelease) (int32, error) {
	batch, err := batchControl.FetchBatchRelease()
	if errors.IsNotFound(err) {
		return 1, nil
	} else if err != nil {
		return 0, err
	}
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return 0, err
	}
	currentReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.Spec.ReleasePlan.Batches[*batch.Spec.ReleasePlan.BatchPartition].CanaryReplicas, int(workload.Replicas), true)

	var stepIndex int32
	for i := range rollout.Spec.Strategy.Canary.Steps {
		step := rollout.Spec.Strategy.Canary.Steps[i]
		var desiredReplicas int
		if step.Replicas != nil {
			desiredReplicas, _ = intstr.GetScaledValueFromIntOrPercent(step.Replicas, int(workload.Replicas), true)
		} else {
			replicas := intstr.FromString(strconv.Itoa(int(*step.Weight)) + "%")
			desiredReplicas, _ = intstr.GetScaledValueFromIntOrPercent(&replicas, int(workload.Replicas), true)
		}
		stepIndex = int32(i + 1)
		if currentReplicas <= desiredReplicas {
			break
		}
	}
	return stepIndex, nil
}

func resetCanaryStatus(newStatus *rolloutv1alpha1.RolloutStatus) {
	// Message cannot be cleaned up here
	canaryStatus := &rolloutv1alpha1.CanaryStatus{}
	canaryStatus.Message = newStatus.CanaryStatus.Message
	newStatus.CanaryStatus = canaryStatus
}
