/*
Copyright 2021.

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
	"encoding/json"
	"github.com/openkruise/rollouts/pkg/util"
	"reflect"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

func (r *RolloutReconciler) checkRolloutStatus(rollout *appsv1alpha1.Rollout) error {
	newStatus := *rollout.Status.DeepCopy()
	newStatus.ObservedGeneration = rollout.GetGeneration()
	// delete rollout CRD
	if !rollout.DeletionTimestamp.IsZero() && newStatus.Phase != appsv1alpha1.RolloutPhaseTerminating {
		newStatus.Phase = appsv1alpha1.RolloutPhaseTerminating
		cond := util.NewRolloutCondition(appsv1alpha1.RolloutConditionTerminating, corev1.ConditionFalse, appsv1alpha1.TerminatingReasonInTerminating, "rollout is in terminating")
		util.SetRolloutCondition(&newStatus, cond)
	} else if newStatus.Phase == "" {
		newStatus.Phase = appsv1alpha1.RolloutPhaseInitial
	}
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return err
	} else if workload == nil && rollout.DeletionTimestamp.IsZero() {
		resetStatus(&newStatus)
		klog.Infof("rollout(%s/%s) workload not found, and reset status be Initial", rollout.Namespace, rollout.Name)
	} else if workload != nil {
		newStatus.StableRevision = workload.StableRevision
		newStatus.CanaryRevision = workload.CanaryRevision
	}
	klog.Infof("rollout(%s/%s) workload(%s) StableRevision(%s) UpdateRevision(%s)",
		rollout.Namespace, rollout.Name, rollout.Spec.ObjectRef.WorkloadRef.Name, newStatus.StableRevision, newStatus.CanaryRevision)

	switch newStatus.Phase {
	case appsv1alpha1.RolloutPhaseInitial:
		if workload != nil {
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, appsv1alpha1.RolloutPhaseInitial, appsv1alpha1.RolloutPhaseHealthy)
			newStatus.Phase = appsv1alpha1.RolloutPhaseHealthy
			newStatus.Message = "rollout is healthy"
		}
	case appsv1alpha1.RolloutPhaseHealthy:
		// from healthy to rollout
		if workload.InRolloutProgressing {
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, appsv1alpha1.RolloutPhaseHealthy, appsv1alpha1.RolloutPhaseProgressing)
			newStatus.Phase = appsv1alpha1.RolloutPhaseProgressing
			// new canaryStatus
			newStatus.CanaryStatus = &appsv1alpha1.CanaryStatus{}
			cond := util.NewRolloutCondition(appsv1alpha1.RolloutConditionProgressing, corev1.ConditionFalse, appsv1alpha1.ProgressingReasonInitializing, "initiate rollout progressing action")
			util.SetRolloutCondition(&newStatus, cond)
		}
	case appsv1alpha1.RolloutPhaseProgressing:
		cond := util.GetRolloutCondition(newStatus, appsv1alpha1.RolloutConditionProgressing)
		if cond == nil || cond.Reason == appsv1alpha1.ProgressingReasonSucceeded {
			newStatus.Phase = appsv1alpha1.RolloutPhaseHealthy
			// whether workload is in rollback phase
		} else if workload.StableRevision == workload.CanaryRevision {
			newStatus.Phase = appsv1alpha1.RolloutPhaseRollback
			cond.Reason = appsv1alpha1.ProgressingReasonCanceled
			cond.Message = "workload has been rollback"
			util.SetRolloutCondition(&newStatus, *cond)
			condR := util.NewRolloutCondition(appsv1alpha1.RolloutConditionRollback, corev1.ConditionFalse, appsv1alpha1.RollbackReasonInRollback, "")
			util.SetRolloutCondition(&newStatus, condR)
		}
	case appsv1alpha1.RolloutPhaseRollback:
		cond := util.GetRolloutCondition(newStatus, appsv1alpha1.RolloutConditionRollback)
		if cond == nil || cond.Reason == appsv1alpha1.RollbackReasonCompleted {
			newStatus.Phase = appsv1alpha1.RolloutPhaseHealthy
		}
	}
	err = r.updateRolloutStatus(rollout, newStatus)
	if err != nil {
		klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return err
	}
	rollout.Status = newStatus
	return nil
}

func (r *RolloutReconciler) updateRolloutStatus(rollout *appsv1alpha1.Rollout, newStatus appsv1alpha1.RolloutStatus) error {
	if reflect.DeepEqual(rollout.Status, newStatus) {
		return nil
	}
	rolloutClone := rollout.DeepCopy()
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Name}, rolloutClone); err != nil {
			klog.Errorf("error getting updated rollout(%s/%s) from client", rollout.Namespace, rollout.Name)
			return err
		}
		rolloutClone.Status = newStatus
		rolloutClone.Status.ObservedGeneration = rolloutClone.Generation
		if err := r.Client.Status().Update(context.TODO(), rolloutClone); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	oldBy, _ := json.Marshal(rollout.Status)
	newBy, _ := json.Marshal(newStatus)
	klog.Infof("rollout(%s/%s) status from(%s) -> to(%s)", rollout.Namespace, rollout.Name, string(oldBy), string(newBy))
	return nil
}

// ResetStatus resets the status of the rollout to start from beginning
func resetStatus(status *appsv1alpha1.RolloutStatus) {
	status.CanaryRevision = ""
	status.StableRevision = ""
	util.RemoveRolloutCondition(status, appsv1alpha1.RolloutConditionProgressing)
	status.Phase = appsv1alpha1.RolloutPhaseInitial
	status.Message = "workload not found"
	//status.CanaryStatus = nil
}
