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
	"time"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/controllers/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// parameter1 retryReconcile, parameter2 error
func (r *RolloutReconciler) reconcileRolloutProgressing(rollout *appsv1alpha1.Rollout) (*time.Time, error) {
	cond := util.GetRolloutCondition(rollout.Status, appsv1alpha1.RolloutConditionProgressing)
	klog.Infof("reconcile rollout(%s/%s) progressing action", rollout.Namespace, rollout.Name)

	var err error
	var recheckTime *time.Time
	newStatus := rollout.Status.DeepCopy()
	switch cond.Reason {
	case appsv1alpha1.ProgressingReasonInitializing:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		// new canaryStatus
		newStatus.CanaryStatus = &appsv1alpha1.CanaryStatus{}
		done, msg, err := r.doProgressingInitializing(rollout)
		if err != nil {
			klog.Errorf("rollout(%s/%s) doProgressingInitializing error(%s)", rollout.Namespace, rollout.Name, err.Error())
			return nil, err
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonInRolling, "rollout is InRolling")
		} else {
			// Incomplete, recheck
			expectedTime := time.Now().Add(5 * time.Second)
			recheckTime = &expectedTime
			progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonInitializing, msg)
			klog.Infof("rollout(%s/%s) doProgressingInitializing is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		}

	case appsv1alpha1.ProgressingReasonInRolling:
		// rollout canceled, indicates rollback(v1 -> v2 -> v1)
		if rollout.Status.StableRevision == rollout.Status.CanaryRevision {
			klog.Infof("rollout(%s/%s) workload has been rollback, then rollout canceled", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonCancelling, "workload has been rollback, then rollout canceled")
			// In case of continuous publishing(v1 -> v2 -> v3), then restart publishing
		} else if newStatus.CanaryStatus.CanaryRevision != "" && newStatus.CanaryRevision != newStatus.CanaryStatus.CanaryRevision {
			klog.Infof("rollout(%s/%s) workload continuous publishing canaryRevision from(%s) -> to(%s), then restart publishing",
				rollout.Namespace, rollout.Name, newStatus.CanaryStatus.CanaryRevision, newStatus.CanaryRevision)
			// delete batchRelease
			done, err := r.doProgressingReset(rollout, newStatus)
			if err != nil {
				klog.Errorf("rollout(%s/%s) doProgressingReset failed: %s", rollout.Namespace, rollout.Name, err.Error())
				return nil, err
			} else if done {
				progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonInitializing, "workload is continuous release")
				klog.Infof("rollout(%s/%s) workload is continuous publishing, reset complete", rollout.Namespace, rollout.Name)
			} else {
				// Incomplete, recheck
				expectedTime := time.Now().Add(3 * time.Second)
				recheckTime = &expectedTime
				klog.Infof("rollout(%s/%s) workload is continuous publishing, reset incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
			}

			// pause rollout
		} else if rollout.Spec.Strategy.Paused {
			klog.Infof("rollout(%s/%s) is Progressing, but paused", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonPaused, "rollout is paused")
		} else {
			klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
			//check if progressing is done
			if len(rollout.Spec.Strategy.Canary.Steps) == int(newStatus.CanaryStatus.CurrentStepIndex+1) &&
				newStatus.CanaryStatus.CurrentStepState == appsv1alpha1.CanaryStepStateCompleted {
				klog.Infof("rollout(%s/%s) progressing rolling done", rollout.Namespace, rollout.Name)
				progressingStateTransition(newStatus, corev1.ConditionTrue, appsv1alpha1.ProgressingReasonFinalising, "")
			} else { // rollout is in rolling
				recheckTime, err = r.doProgressingInRolling(rollout, newStatus)
				if err != nil {
					return nil, err
				}
			}
		}
	// after the normal completion of rollout, enter into the Finalising process
	case appsv1alpha1.ProgressingReasonFinalising:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		var done bool
		done, recheckTime, err = r.doFinalising(rollout, newStatus, true)
		if err != nil {
			return nil, err
			// finalizer is finished
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionTrue, appsv1alpha1.ProgressingReasonSucceeded, "rollout is success")
		}

	case appsv1alpha1.ProgressingReasonPaused:
		if !rollout.Spec.Strategy.Paused {
			klog.Infof("rollout(%s/%s) is Progressing, but paused", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonInRolling, "rollout is InRolling")
		}

	case appsv1alpha1.ProgressingReasonCancelling:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
		var done bool
		done, recheckTime, err = r.doFinalising(rollout, newStatus, false)
		if err != nil {
			return nil, err
			// finalizer is finished
		} else if done {
			progressingStateTransition(newStatus, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonCanceled, "workload has been rollback, then rollout canceled")
		}

	case appsv1alpha1.ProgressingReasonSucceeded, appsv1alpha1.ProgressingReasonCanceled:
		klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
	}

	err = r.updateRolloutStatus(rollout, *newStatus)
	if err != nil {
		klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	return recheckTime, nil
}

func progressingStateTransition(status *appsv1alpha1.RolloutStatus, condStatus corev1.ConditionStatus, reason, message string) {
	cond := util.NewRolloutCondition(appsv1alpha1.RolloutConditionProgressing, condStatus, reason, message)
	util.SetRolloutCondition(status, cond)
}

func (r *RolloutReconciler) doProgressingInitializing(rollout *appsv1alpha1.Rollout) (bool, string, error) {
	if rollout.Spec.Strategy.Type == "" || rollout.Spec.Strategy.Type == appsv1alpha1.RolloutStrategyCanary {
		if ok, msg, err := r.verifyCanaryStrategy(rollout); !ok {
			return ok, msg, err
		}
		klog.Infof("verify rollout(%s/%s) CanaryStrategy success", rollout.Namespace, rollout.Name)
	}

	return true, "", nil
}

func (r *RolloutReconciler) doProgressingInRolling(rollout *appsv1alpha1.Rollout, newStatus *appsv1alpha1.RolloutStatus) (*time.Time, error) {
	// fetch target workload
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) GetWorkloadForRef failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	} else if workload == nil {
		expectedTime := time.Now().Add(5 * time.Second)
		klog.Warningf("rollout(%s/%s) Fetch workload Not Found, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		return &expectedTime, nil
	}

	rolloutCon := &rolloutContext{
		Client:         r.Client,
		rollout:        rollout,
		newStatus:      newStatus,
		stableRevision: newStatus.StableRevision,
		canaryRevision: newStatus.CanaryRevision,
		workload:       workload,
		batchControl:   batchrelease.NewInnerBatchController(r.Client, rollout),
	}
	err = rolloutCon.reconcile()
	if err != nil {
		klog.Errorf("rollout(%s/%s) Progressing failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	return rolloutCon.recheckTime, nil
}

func (r *RolloutReconciler) doProgressingReset(rollout *appsv1alpha1.Rollout, newStatus *appsv1alpha1.RolloutStatus) (bool, error) {
	rolloutCon := &rolloutContext{
		Client:       r.Client,
		rollout:      rollout,
		newStatus:    newStatus,
		batchControl: batchrelease.NewInnerBatchController(r.Client, rollout),
	}

	if rolloutCon.rollout.Spec.Strategy.Canary.TrafficRouting != nil {
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

func (r *RolloutReconciler) verifyCanaryStrategy(rollout *appsv1alpha1.Rollout) (bool, string, error) {
	canary := rollout.Spec.Strategy.Canary
	// Traffic routing
	if canary.TrafficRouting != nil {
		if ok, msg, err := r.verifyTrafficRouting(rollout.Namespace, canary.TrafficRouting); !ok {
			return ok, msg, err
		}
	}

	// canary steps
	if len(canary.Steps) != 0 {
		// create batch release crd
		batchControl := batchrelease.NewInnerBatchController(r.Client, rollout)
		return batchControl.VerifyBatchInitial()
	}

	return true, "", nil
}

func (r *RolloutReconciler) verifyTrafficRouting(ns string, tr *appsv1alpha1.TrafficRouting) (bool, string, error) {
	// check service
	service := &corev1.Service{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: ns, Name: tr.Service}, service)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Sprintf("Service(%s/%s) is Not Found", ns, tr.Service), nil
		}
		return false, "", err
	}

	// check ingress
	var ingressName string
	switch tr.Type {
	case appsv1alpha1.TrafficRoutingNginx:
		nginx := tr.Nginx
		ingressName = nginx.Ingress
	}
	ingress := &netv1.Ingress{}
	err = r.Get(context.TODO(), types.NamespacedName{Namespace: ns, Name: ingressName}, ingress)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Sprintf("Ingress(%s/%s) is Not Found", ns, ingressName), nil
		}
		return false, "", err
	}
	return true, "", nil
}
