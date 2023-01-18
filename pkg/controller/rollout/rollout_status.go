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
	"reflect"
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *RolloutReconciler) calculateRolloutStatus(rollout *v1alpha1.Rollout) (retry bool, newStatus *v1alpha1.RolloutStatus, err error) {
	// hash rollout
	if err = r.calculateRolloutHash(rollout); err != nil {
		return false, nil, err
	}
	newStatus = rollout.Status.DeepCopy()
	newStatus.ObservedGeneration = rollout.GetGeneration()
	// delete rollout CRD
	if !rollout.DeletionTimestamp.IsZero() {
		if newStatus.Phase != v1alpha1.RolloutPhaseTerminating {
			newStatus.Phase = v1alpha1.RolloutPhaseTerminating
			cond := util.NewRolloutCondition(v1alpha1.RolloutConditionTerminating, corev1.ConditionTrue, v1alpha1.TerminatingReasonInTerminating, "Rollout is in terminating")
			util.SetRolloutCondition(newStatus, *cond)
		}
		return false, newStatus, nil
	}
	if newStatus.Phase == "" {
		newStatus.Phase = v1alpha1.RolloutPhaseInitial
	}
	// get ref workload
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	} else if workload == nil {
		newStatus = &v1alpha1.RolloutStatus{
			ObservedGeneration: rollout.Generation,
			Phase:              v1alpha1.RolloutPhaseInitial,
			Message:            "Workload Not Found",
		}
		klog.Infof("rollout(%s/%s) workload not found, and reset status be Initial", rollout.Namespace, rollout.Name)
		return false, newStatus, nil
	}
	// workload status generation is not equal to workload.generation
	if !workload.IsStatusConsistent {
		klog.Infof("rollout(%s/%s) workload status is inconsistent, then wait a moment", rollout.Namespace, rollout.Name)
		return true, nil, nil
	}

	// update workload generation to canaryStatus.ObservedWorkloadGeneration
	// rollout is a target ref bypass, so there needs to be a field to identify the rollout execution process or results,
	// which version of deployment is targeted, ObservedWorkloadGeneration that is to compare with the workload generation
	if newStatus.CanaryStatus != nil && newStatus.CanaryStatus.CanaryRevision != "" &&
		newStatus.CanaryStatus.CanaryRevision == workload.CanaryRevision {
		newStatus.CanaryStatus.ObservedRolloutID = getRolloutID(workload)
		newStatus.CanaryStatus.ObservedWorkloadGeneration = workload.Generation
	}

	switch newStatus.Phase {
	case v1alpha1.RolloutPhaseInitial:
		klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, v1alpha1.RolloutPhaseInitial, v1alpha1.RolloutPhaseHealthy)
		newStatus.Phase = v1alpha1.RolloutPhaseHealthy
		newStatus.Message = "rollout is healthy"
	case v1alpha1.RolloutPhaseHealthy:
		// workload released, entering the rollout progressing phase
		if workload.InRolloutProgressing {
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, v1alpha1.RolloutPhaseHealthy, v1alpha1.RolloutPhaseProgressing)
			newStatus.Phase = v1alpha1.RolloutPhaseProgressing
			cond := util.NewRolloutCondition(v1alpha1.RolloutConditionProgressing, corev1.ConditionTrue, v1alpha1.ProgressingReasonInitializing, "Rollout is in Progressing")
			util.SetRolloutCondition(newStatus, *cond)
			util.RemoveRolloutCondition(newStatus, v1alpha1.RolloutConditionSucceeded)
		} else if newStatus.CanaryStatus == nil {
			// The following logic is to make PaaS be able to judge whether the rollout is ready
			// at the first deployment of the Rollout/Workload. For example: generally, a PaaS
			// platform can use the following code to judge whether the rollout progression is completed:
			// ```
			//   if getRolloutID(workload, rollout) == newStatus.CanaryStatus.ObservedRolloutID &&
			//	   newStatus.CanaryStatus.CurrentStepState == "Completed" {
			//	   // do something after rollout
			//   }
			//```
			// But at the first deployment of Rollout/Workload, CanaryStatus isn't set due to no rollout progression,
			// and PaaS platform cannot judge whether the deployment is completed base on the code above. So we have
			// to update the status just like the rollout was completed.

			newStatus.CanaryStatus = &v1alpha1.CanaryStatus{
				ObservedRolloutID:          getRolloutID(workload),
				ObservedWorkloadGeneration: workload.Generation,
				PodTemplateHash:            workload.PodTemplateHash,
				CanaryRevision:             workload.CanaryRevision,
				StableRevision:             workload.StableRevision,
				CurrentStepIndex:           int32(len(rollout.Spec.Strategy.Canary.Steps)),
				CurrentStepState:           v1alpha1.CanaryStepStateCompleted,
				RolloutHash:                rollout.Annotations[util.RolloutHashAnnotation],
			}
			newStatus.Message = "workload deployment is completed"
		}
	}
	return false, newStatus, nil
}

// rolloutHash mainly records the step batch information, when the user step changes,
// the current batch can be recalculated
func (r *RolloutReconciler) calculateRolloutHash(rollout *v1alpha1.Rollout) error {
	canary := rollout.Spec.Strategy.Canary.DeepCopy()
	canary.FailureThreshold = nil
	canary.Steps = nil
	for i := range rollout.Spec.Strategy.Canary.Steps {
		step := rollout.Spec.Strategy.Canary.Steps[i].DeepCopy()
		step.Pause = v1alpha1.RolloutPause{}
		canary.Steps = append(canary.Steps, *step)
	}
	data := util.DumpJSON(canary)
	hash := rand.SafeEncodeString(util.EncodeHash(data))
	if rollout.Annotations[util.RolloutHashAnnotation] == hash {
		return nil
	}
	// update rollout hash in annotation
	cloneObj := rollout.DeepCopy()
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, util.RolloutHashAnnotation, hash)
	err := r.Patch(context.TODO(), cloneObj, client.RawPatch(types.MergePatchType, []byte(body)))
	if err != nil {
		klog.Errorf("rollout(%s/%s) patch(%s) failed: %s", rollout.Namespace, rollout.Name, body, err.Error())
		return err
	}
	if rollout.Annotations == nil {
		rollout.Annotations = map[string]string{}
	}
	klog.Infof("rollout(%s/%s) patch hash from(%s) -> to(%s)", rollout.Namespace, rollout.Name, rollout.Annotations[util.RolloutHashAnnotation], hash)
	rollout.Annotations[util.RolloutHashAnnotation] = hash
	return nil
}

func (r *RolloutReconciler) updateRolloutStatusInternal(rollout *v1alpha1.Rollout, newStatus v1alpha1.RolloutStatus) error {
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
		return r.Client.Status().Update(context.TODO(), rolloutClone)
	}); err != nil {
		klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return err
	}
	rollout.Status = newStatus
	klog.Infof("rollout(%s/%s) status from(%s) -> to(%s) success", rollout.Namespace, rollout.Name, util.DumpJSON(rollout.Status), util.DumpJSON(newStatus))
	return nil
}

func (r *RolloutReconciler) reconcileRolloutTerminating(rollout *v1alpha1.Rollout, newStatus *v1alpha1.RolloutStatus) (*time.Time, error) {
	cond := util.GetRolloutCondition(rollout.Status, v1alpha1.RolloutConditionTerminating)
	if cond.Reason == v1alpha1.TerminatingReasonCompleted {
		return nil, nil
	}
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	c := &util.RolloutContext{Rollout: rollout, NewStatus: newStatus, Workload: workload}
	done, err := r.doFinalising(c)
	if err != nil {
		return nil, err
	} else if done {
		klog.Infof("rollout(%s/%s) is terminating, and state from(%s) -> to(%s)", rollout.Namespace, rollout.Name, cond.Reason, v1alpha1.TerminatingReasonCompleted)
		cond.Reason = v1alpha1.TerminatingReasonCompleted
		cond.Status = corev1.ConditionFalse
		util.SetRolloutCondition(newStatus, *cond)
	} else {
		// Incomplete, recheck
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		c.RecheckTime = &expectedTime
		klog.Infof("rollout(%s/%s) terminating is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
	}
	return c.RecheckTime, nil
}

// handle adding and handle finalizer logic, it turns if we should continue to reconcile
func (r *RolloutReconciler) handleFinalizer(rollout *v1alpha1.Rollout) error {
	// delete rollout crd, remove finalizer
	if !rollout.DeletionTimestamp.IsZero() {
		cond := util.GetRolloutCondition(rollout.Status, v1alpha1.RolloutConditionTerminating)
		if cond != nil && cond.Reason == v1alpha1.TerminatingReasonCompleted {
			// Completed
			if controllerutil.ContainsFinalizer(rollout, util.KruiseRolloutFinalizer) {
				err := util.UpdateFinalizer(r.Client, rollout, util.RemoveFinalizerOpType, util.KruiseRolloutFinalizer)
				if err != nil {
					klog.Errorf("remove rollout(%s/%s) finalizer failed: %s", rollout.Namespace, rollout.Name, err.Error())
					return err
				}
				klog.Infof("remove rollout(%s/%s) finalizer success", rollout.Namespace, rollout.Name)
			}
			return nil
		}
		return nil
	}

	// create rollout crd, add finalizer
	if !controllerutil.ContainsFinalizer(rollout, util.KruiseRolloutFinalizer) {
		err := util.UpdateFinalizer(r.Client, rollout, util.AddFinalizerOpType, util.KruiseRolloutFinalizer)
		if err != nil {
			klog.Errorf("register rollout(%s/%s) finalizer failed: %s", rollout.Namespace, rollout.Name, err.Error())
			return err
		}
		klog.Infof("register rollout(%s/%s) finalizer success", rollout.Namespace, rollout.Name)
	}
	return nil
}

func getRolloutID(workload *util.Workload) string {
	if workload != nil {
		return workload.Labels[v1alpha1.RolloutIDLabel]
	}
	return ""
}
