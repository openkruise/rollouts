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
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *RolloutReconciler) calculateRolloutStatus(rollout *v1beta1.Rollout) (retry bool, newStatus *v1beta1.RolloutStatus, err error) {
	// hash rollout
	if err = r.calculateRolloutHash(rollout); err != nil {
		return false, nil, err
	}
	newStatus = rollout.Status.DeepCopy()
	newStatus.ObservedGeneration = rollout.GetGeneration()
	// delete rollout CRD
	if !rollout.DeletionTimestamp.IsZero() {
		if newStatus.Phase != v1beta1.RolloutPhaseTerminating {
			newStatus.Phase = v1beta1.RolloutPhaseTerminating
			cond := util.NewRolloutCondition(v1beta1.RolloutConditionTerminating, corev1.ConditionTrue, v1alpha1.TerminatingReasonInTerminating, "Rollout is in terminating")
			util.SetRolloutCondition(newStatus, *cond)
		}
		return false, newStatus, nil
	}

	if rollout.Spec.Disabled && newStatus.Phase != v1beta1.RolloutPhaseDisabled && newStatus.Phase != v1beta1.RolloutPhaseDisabling {
		// if rollout in progressing, indicates a working rollout is disabled, then the rollout should be finalized
		if newStatus.Phase == v1beta1.RolloutPhaseProgressing {
			newStatus.Phase = v1beta1.RolloutPhaseDisabling
			newStatus.Message = "Disabling rollout, release resources"
		} else {
			newStatus.Phase = v1beta1.RolloutPhaseDisabled
			newStatus.Message = "Rollout is disabled"
		}
	}

	if newStatus.Phase == "" {
		newStatus.Phase = v1beta1.RolloutPhaseInitial
	}
	// get ref workload
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	} else if workload == nil {
		if !rollout.Spec.Disabled {
			newStatus = &v1beta1.RolloutStatus{
				ObservedGeneration: rollout.Generation,
				Phase:              v1beta1.RolloutPhaseInitial,
				Message:            "Workload Not Found",
			}
			klog.Infof("rollout(%s/%s) workload not found, and reset status be Initial", rollout.Namespace, rollout.Name)
		}
		return false, newStatus, nil
	}
	klog.V(5).Infof("rollout(%s/%s) fetch workload(%s)", rollout.Namespace, rollout.Name, util.DumpJSON(workload))
	// workload status generation is not equal to workload.generation
	if !workload.IsStatusConsistent {
		klog.Infof("rollout(%s/%s) workload status is inconsistent, then wait a moment", rollout.Namespace, rollout.Name)
		return true, nil, nil
	}
	// update workload generation to canaryStatus.ObservedWorkloadGeneration
	// rollout is a target ref bypass, so there needs to be a field to identify the rollout execution process or results,
	// which version of deployment is targeted, ObservedWorkloadGeneration that is to compare with the workload generation
	if !newStatus.IsSubStatusEmpty() && newStatus.GetCanaryRevision() != "" &&
		newStatus.GetCanaryRevision() == workload.CanaryRevision {
		newStatus.GetSubStatus().ObservedRolloutID = getRolloutID(workload)
		newStatus.GetSubStatus().ObservedWorkloadGeneration = workload.Generation
	}

	switch newStatus.Phase {
	case v1beta1.RolloutPhaseInitial:
		klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, v1beta1.RolloutPhaseInitial, v1beta1.RolloutPhaseHealthy)
		newStatus.Phase = v1beta1.RolloutPhaseHealthy
		newStatus.Message = "rollout is healthy"
	case v1beta1.RolloutPhaseHealthy:
		// workload released, entering the rollout progressing phase
		if workload.InRolloutProgressing {
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, v1beta1.RolloutPhaseHealthy, v1beta1.RolloutPhaseProgressing)
			newStatus.Phase = v1beta1.RolloutPhaseProgressing
			cond := util.NewRolloutCondition(v1beta1.RolloutConditionProgressing, corev1.ConditionTrue, v1alpha1.ProgressingReasonInitializing, "Rollout is in Progressing")
			util.SetRolloutCondition(newStatus, *cond)
			util.RemoveRolloutCondition(newStatus, v1beta1.RolloutConditionSucceeded)
		} else if newStatus.IsSubStatusEmpty() {
			// The following logic is to make PaaS be able to judge whether the rollout is ready
			// at the first deployment of the Rollout/Workload. For example: generally, a PaaS
			// platform can use the following code to judge whether the rollout progression is completed:
			// ```
			//   if getRolloutID(workload, rollout) == newStatus.CanaryStatus.ObservedRolloutID &&
			//	   newStatus.CanaryStatus.CurrentStepState == "Completed" {
			//	   // do something after rollout
			//   }
			// ```
			// But at the first deployment of Rollout/Workload, CanaryStatus isn't set due to no rollout progression,
			// and PaaS platform cannot judge whether the deployment is completed base on the code above. So we have
			// to update the status just like the rollout was completed.
			commonStatus := v1beta1.CommonStatus{
				ObservedRolloutID:          getRolloutID(workload),
				ObservedWorkloadGeneration: workload.Generation,
				PodTemplateHash:            workload.PodTemplateHash,
				StableRevision:             workload.StableRevision,
				CurrentStepIndex:           int32(len(rollout.Spec.Strategy.GetSteps())),
				NextStepIndex:              util.NextBatchIndex(rollout, int32(len(rollout.Spec.Strategy.GetSteps()))),
				CurrentStepState:           v1beta1.CanaryStepStateCompleted,
				RolloutHash:                rollout.Annotations[util.RolloutHashAnnotation],
			}
			if rollout.Spec.Strategy.IsBlueGreenRelease() {
				newStatus.BlueGreenStatus = &v1beta1.BlueGreenStatus{
					CommonStatus:    commonStatus,
					UpdatedRevision: workload.CanaryRevision,
				}
			} else {
				newStatus.CanaryStatus = &v1beta1.CanaryStatus{
					CommonStatus:   commonStatus,
					CanaryRevision: workload.CanaryRevision,
				}
			}

			newStatus.Message = "workload deployment is completed"
		}
	case v1beta1.RolloutPhaseDisabled:
		if !rollout.Spec.Disabled {
			newStatus.Phase = v1beta1.RolloutPhaseHealthy
			newStatus.Message = "rollout is healthy"
		}
	}
	return false, newStatus, nil
}

// rolloutHash mainly records the step batch information, when the user step changes,
// the current batch can be recalculated
func (r *RolloutReconciler) calculateRolloutHash(rollout *v1beta1.Rollout) error {
	var data string
	if rollout.Spec.Strategy.IsCanaryStragegy() {
		canary := rollout.Spec.Strategy.Canary.DeepCopy()
		canary.FailureThreshold = nil
		canary.Steps = nil
		for i := range rollout.Spec.Strategy.Canary.Steps {
			step := rollout.Spec.Strategy.Canary.Steps[i].DeepCopy()
			step.Pause = v1beta1.RolloutPause{}
			canary.Steps = append(canary.Steps, *step)
		}
		data = util.DumpJSON(canary)
	} else if rollout.Spec.Strategy.IsBlueGreenRelease() {
		blueGreen := rollout.Spec.Strategy.BlueGreen.DeepCopy()
		blueGreen.FailureThreshold = nil
		blueGreen.Steps = nil
		for i := range rollout.Spec.Strategy.BlueGreen.Steps {
			step := rollout.Spec.Strategy.BlueGreen.Steps[i].DeepCopy()
			step.Pause = v1beta1.RolloutPause{}
			blueGreen.Steps = append(blueGreen.Steps, *step)
		}
		data = util.DumpJSON(blueGreen)
	} else {
		return fmt.Errorf("unknown rolling style: %s", rollout.Spec.Strategy.GetRollingStyle())
	}
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

func (r *RolloutReconciler) updateRolloutStatusInternal(rollout *v1beta1.Rollout, newStatus v1beta1.RolloutStatus) error {
	if newStatus.GetSubStatus() != nil {
		newStatus.CurrentStepIndex = newStatus.GetSubStatus().CurrentStepIndex
		newStatus.CurrentStepState = newStatus.GetSubStatus().CurrentStepState
	}
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
	klog.Infof("rollout(%s/%s) status from(%s) -> to(%s) success", rollout.Namespace, rollout.Name, util.DumpJSON(rollout.Status), util.DumpJSON(newStatus))
	rollout.Status = newStatus
	return nil
}

func (r *RolloutReconciler) reconcileRolloutTerminating(rollout *v1beta1.Rollout, newStatus *v1beta1.RolloutStatus) (*time.Time, error) {
	cond := util.GetRolloutCondition(rollout.Status, v1beta1.RolloutConditionTerminating)
	if cond.Reason == v1alpha1.TerminatingReasonCompleted {
		return nil, nil
	}
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	c := &RolloutContext{Rollout: rollout, NewStatus: newStatus, Workload: workload, FinalizeReason: v1beta1.FinaliseReasonDelete}
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

func (r *RolloutReconciler) reconcileRolloutDisabling(rollout *v1beta1.Rollout, newStatus *v1beta1.RolloutStatus) (*time.Time, error) {
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	c := &RolloutContext{Rollout: rollout, NewStatus: newStatus, Workload: workload, FinalizeReason: v1beta1.FinaliseReasonDisalbed}
	done, err := r.doFinalising(c)
	if err != nil {
		return nil, err
	} else if done {
		klog.Infof("rollout(%s/%s) is disabled", rollout.Namespace, rollout.Name)
		newStatus.Phase = v1beta1.RolloutPhaseDisabled
		newStatus.Message = "Rollout is disabled"
	} else {
		// Incomplete, recheck
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		c.RecheckTime = &expectedTime
		klog.Infof("rollout(%s/%s) disabling is incomplete, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
	}
	return c.RecheckTime, nil
}

func (r *RolloutReconciler) patchWorkloadRolloutWebhookLabel(rollout *v1beta1.Rollout) error {
	// get ref workload
	workload, err := r.finder.GetWorkloadForRef(rollout)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return err
	} else if workload == nil {
		return nil
	}

	var workloadType util.WorkloadType
	switch workload.Kind {
	case util.ControllerKruiseKindCS.Kind:
		workloadType = util.CloneSetType
	case util.ControllerKindDep.Kind:
		workloadType = util.DeploymentType
	case util.ControllerKindSts.Kind:
		workloadType = util.StatefulSetType
	case util.ControllerKruiseKindDS.Kind:
		workloadType = util.DaemonSetType
	}
	if workload.Annotations[util.WorkloadTypeLabel] == "" && workloadType != "" {
		workloadGVK := schema.FromAPIVersionAndKind(workload.APIVersion, workload.Kind)
		obj := util.GetEmptyWorkloadObject(workloadGVK)
		obj.SetNamespace(workload.Namespace)
		obj.SetName(workload.Name)
		body := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, util.WorkloadTypeLabel, workloadType)
		if err := r.Patch(context.TODO(), obj, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch workload(%s) failed: %s", rollout.Namespace, rollout.Name, workload.Name, err.Error())
			return err
		}
		klog.Infof("rollout(%s/%s) patch workload(%s) labels[%s] success", rollout.Namespace, rollout.Name, workload.Name, util.WorkloadTypeLabel)
	}
	return nil
}

// handle adding and handle finalizer logic, it turns if we should continue to reconcile
func (r *RolloutReconciler) handleFinalizer(rollout *v1beta1.Rollout) error {
	// delete rollout crd, remove finalizer
	if !rollout.DeletionTimestamp.IsZero() {
		cond := util.GetRolloutCondition(rollout.Status, v1beta1.RolloutConditionTerminating)
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
		rolloutID := workload.Labels[v1beta1.RolloutIDLabel]
		if rolloutID == "" {
			rolloutID = workload.CanaryRevision
			if workload.IsInRollback {
				rolloutID = fmt.Sprintf("rollback-%s", rolloutID)
			}
		}
		return rolloutID
	}
	return ""
}

// UnifiedStatus is a data structure used to unify BlueGreenStatus and CanaryStatus, allowing the controller to operate
// the current release status in a unified manner without considering the release form.
type UnifiedStatus struct {
	*v1beta1.CommonStatus
	// UpdatedRevision is the pointer of BlueGreenStatus.UpdatedRevision or CanaryStatus.CanaryRevision
	UpdatedRevision *string `json:"updatedRevision"`
	// UpdatedReplicas is the pointer of BlueGreenStatus.UpdatedReplicas or CanaryStatus.CanaryReplicas
	UpdatedReplicas *int32 `json:"updatedReplicas"`
	// UpdatedReadyReplicas is the pointer of BlueGreenStatus.UpdatedReadyReplicas or CanaryStatus.CanaryReadyReplicas
	UpdatedReadyReplicas *int32 `json:"updatedReadyReplicas"`
}

func (u UnifiedStatus) IsNil() bool {
	return u.CommonStatus == nil || u.UpdatedReplicas == nil || u.UpdatedReadyReplicas == nil || u.UpdatedRevision == nil
}

func GetUnifiedStatus(r *v1beta1.RolloutStatus) UnifiedStatus {
	status := UnifiedStatus{}
	if r.CanaryStatus != nil {
		status.CommonStatus = &r.CanaryStatus.CommonStatus
		status.UpdatedReadyReplicas = &r.CanaryStatus.CanaryReadyReplicas
		status.UpdatedReplicas = &r.CanaryStatus.CanaryReplicas
		status.UpdatedRevision = &r.CanaryStatus.CanaryRevision
	} else if r.BlueGreenStatus != nil {
		status.CommonStatus = &r.BlueGreenStatus.CommonStatus
		status.UpdatedReadyReplicas = &r.BlueGreenStatus.UpdatedReadyReplicas
		status.UpdatedReplicas = &r.BlueGreenStatus.UpdatedReplicas
		status.UpdatedRevision = &r.BlueGreenStatus.UpdatedRevision
	}
	return status
}

// doStepJump implements the common logic for both canary and bluegreen rollout strategies
// to handle step jumping based on NextStepIndex.
func doStepJump(rollout *v1beta1.Rollout, newStatus *v1beta1.RolloutStatus, steps []v1beta1.CanaryStep, workloadReplicas int) (jumped bool) {
	status := GetUnifiedStatus(newStatus)
	if status.IsNil() {
		klog.InfoS("doStepJump skipped: unified status is nil", "rollout", klog.KObj(rollout))
		return false
	}
	klog.InfoS("will do step jump", "steps", len(steps), "updatedReplicas", *status.UpdatedReplicas,
		"nextStepIndex", status.NextStepIndex, "rollout", klog.KObj(rollout))
	if nextIndex := status.NextStepIndex; nextIndex != util.NextBatchIndex(rollout, status.CurrentStepIndex) &&
		nextIndex > 0 && nextIndex <= int32(len(steps)) {
		currentIndexBackup := status.CurrentStepIndex
		currentStepStateBackup := status.CurrentStepState
		// update the current and next stepIndex
		status.CurrentStepIndex = nextIndex
		status.NextStepIndex = util.NextBatchIndex(rollout, nextIndex)
		nextStep := steps[nextIndex-1]
		// compare next step and current step to decide the state we should go
		nextStepReplicas, _ := intstr.GetScaledValueFromIntOrPercent(nextStep.Replicas, workloadReplicas, true)
		if int32(nextStepReplicas) == *status.UpdatedReplicas {
			status.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
		} else {
			status.CurrentStepState = v1beta1.CanaryStepStateInit
		}
		status.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.InfoS("step jumped", "rollout", klog.KObj(rollout),
			"oldCurrentIndex", currentIndexBackup, "newCurrentIndex", status.CurrentStepIndex,
			"oldCurrentStepState", currentStepStateBackup, "newCurrentStepState", status.CurrentStepState)
		return true
	}
	klog.InfoS("step not jumped", "rollout", klog.KObj(rollout))
	return false
}
