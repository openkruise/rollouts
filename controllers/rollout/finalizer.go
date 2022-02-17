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
	"time"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/controllers/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *RolloutReconciler) reconcileRolloutTerminating(rollout *appsv1alpha1.Rollout) (*time.Time, error) {
	cond := util.GetRolloutCondition(rollout.Status, appsv1alpha1.RolloutConditionTerminating)
	klog.Infof("reconcile rollout(%s/%s) Terminating action", rollout.Namespace, rollout.Name)
	if cond.Reason == appsv1alpha1.TerminatingReasonCompleted {
		return nil, nil
	}

	newStatus := rollout.Status.DeepCopy()
	done, recheckTime, err := r.doFinalising(rollout, newStatus, false)
	if err != nil {
		return nil, err
	} else if done {
		klog.Infof("rollout(%s/%s) is terminating, and state from(%s) -> to(%s)", rollout.Namespace, rollout.Name, cond.Reason, appsv1alpha1.TerminatingReasonCompleted)
		cond.Reason = appsv1alpha1.TerminatingReasonCompleted
		cond.Status = corev1.ConditionTrue
		util.SetRolloutCondition(newStatus, *cond)
	}
	err = r.updateRolloutStatus(rollout, *newStatus)
	if err != nil {
		klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	return recheckTime, nil
}

func (r *RolloutReconciler) doFinalising(rollout *appsv1alpha1.Rollout, newStatus *appsv1alpha1.RolloutStatus, isComplete bool) (bool, *time.Time, error) {
	// fetch target workload
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) GetWorkloadForRef failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	}

	rolloutCon := &rolloutContext{
		Client:         r.Client,
		rollout:        rollout,
		newStatus:      newStatus,
		stableRevision: newStatus.StableRevision,
		canaryRevision: newStatus.CanaryRevision,
		batchControl:   batchrelease.NewInnerBatchController(r.Client, rollout),
		workload:       workload,
		isComplete:     isComplete,
	}
	done, err := rolloutCon.finalising()
	if err != nil {
		klog.Errorf("rollout(%s/%s) Progressing failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	} else if !done {
		klog.Infof("rollout(%s/%s) finalizer is not finished, and time(%s) retry reconcile", rollout.Namespace, rollout.Name, rolloutCon.recheckTime.String())
		return false, rolloutCon.recheckTime, nil
	}
	//newStatus.CanaryStatus = nil
	klog.Infof("run rollout(%s/%s) Progressing Finalising done", rollout.Namespace, rollout.Name)
	return true, nil, nil
}

// handle adding and handle finalizer logic, it turns if we should continue to reconcile
func (r *RolloutReconciler) handleFinalizer(rollout *appsv1alpha1.Rollout) (bool, error) {
	if !rollout.DeletionTimestamp.IsZero() {
		cond := util.GetRolloutCondition(rollout.Status, appsv1alpha1.RolloutConditionTerminating)
		if cond != nil && cond.Reason == appsv1alpha1.TerminatingReasonCompleted {
			// Completed
			if controllerutil.ContainsFinalizer(rollout, util.KruiseRolloutFinalizer) {
				controllerutil.RemoveFinalizer(rollout, util.KruiseRolloutFinalizer)
				err := r.Update(context.TODO(), rollout)
				if err != nil {
					klog.Errorf("remove rollout(%s/%s) finalizer failed: %s", rollout.Namespace, rollout.Name, err.Error())
					return false, err
				}
				klog.Infof("remove rollout(%s/%s) finalizer success", rollout.Namespace, rollout.Name)
			}
			return true, nil
		}
		return false, nil
	}

	if !controllerutil.ContainsFinalizer(rollout, util.KruiseRolloutFinalizer) {
		controllerutil.AddFinalizer(rollout, util.KruiseRolloutFinalizer)
		err := r.Update(context.TODO(), rollout)
		if err != nil {
			klog.Errorf("register rollout(%s/%s) finalizer failed: %s", rollout.Namespace, rollout.Name, err.Error())
			return false, err
		}
		klog.Infof("register rollout(%s/%s) finalizer success", rollout.Namespace, rollout.Name)
	}
	return false, nil
}
