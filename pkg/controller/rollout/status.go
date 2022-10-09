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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *RolloutReconciler) updateRolloutStatus(rollout *rolloutv1alpha1.Rollout) (done bool, err error) {
	newStatus := *rollout.Status.DeepCopy()
	newStatus.ObservedGeneration = rollout.GetGeneration()
	defer func() {
		err = r.updateRolloutStatusInternal(rollout, newStatus)
		if err != nil {
			klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
			return
		}
		err = r.calculateRolloutHash(rollout)
		if err != nil {
			return
		}
		rollout.Status = newStatus
	}()

	// delete rollout CRD
	if !rollout.DeletionTimestamp.IsZero() && newStatus.Phase != rolloutv1alpha1.RolloutPhaseTerminating {
		newStatus.Phase = rolloutv1alpha1.RolloutPhaseTerminating
		cond := util.NewRolloutCondition(rolloutv1alpha1.RolloutConditionTerminating, corev1.ConditionFalse, rolloutv1alpha1.TerminatingReasonInTerminating, "Rollout is in terminating")
		util.SetRolloutCondition(&newStatus, *cond)
	} else if newStatus.Phase == "" {
		newStatus.Phase = rolloutv1alpha1.RolloutPhaseInitial
	}
	// get ref workload
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get workload failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return
	} else if workload == nil {
		if rollout.DeletionTimestamp.IsZero() {
			resetStatus(&newStatus)
			klog.Infof("rollout(%s/%s) workload not found, and reset status be Initial", rollout.Namespace, rollout.Name)
		}
		done = true
		return
	}

	// workload status is not consistent
	if !workload.IsStatusConsistent {
		klog.Infof("rollout(%s/%s) workload status isn't consistent, then wait a moment", rollout.Namespace, rollout.Name)
		done = false
		return
	}

	currentRolloutID := getRolloutID(workload, rollout)
	// update CanaryStatus for newStatus if we need
	// update workload generation to canaryStatus.ObservedWorkloadGeneration
	// rollout is a target ref bypass, so there needs to be a field to identify the rollout execution process or results,
	// which version of deployment is targeted, ObservedWorkloadGeneration that is to compare with the workload generation
	if newStatus.CanaryStatus != nil && newStatus.CanaryStatus.CanaryRevision != "" {
		// update canaryStatus message if we need
		if isMeaninglessChangesOfRolloutID(&newStatus, workload, currentRolloutID) {
			newStatus.CanaryStatus.Message = rolloutv1alpha1.EncodeCanaryMessage(false, currentRolloutID)
		}
		// update observed rollout-id and workload generation if we need
		if isCanaryRevisionNotChanged(&newStatus, workload) {
			newStatus.CanaryStatus.ObservedRolloutID = currentRolloutID
			newStatus.CanaryStatus.ObservedWorkloadGeneration = workload.Generation
		} else {
			// revision changed, update the canary message
			newStatus.CanaryStatus.Message = rolloutv1alpha1.EncodeCanaryMessage(true, currentRolloutID)
			klog.Infof("checkpoint: update message meaningful %s", newStatus.CanaryStatus.Message)
		}
	}
	// fresh and update stable revision
	newStatus.StableRevision = workload.StableRevision

	switch newStatus.Phase {
	case rolloutv1alpha1.RolloutPhaseInitial:
		klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, rolloutv1alpha1.RolloutPhaseInitial, rolloutv1alpha1.RolloutPhaseHealthy)
		newStatus.Phase = rolloutv1alpha1.RolloutPhaseHealthy
		newStatus.Message = "rollout is healthy"
	case rolloutv1alpha1.RolloutPhaseHealthy:
		if workload.InRolloutProgressing {
			// from healthy to progressing
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, rolloutv1alpha1.RolloutPhaseHealthy, rolloutv1alpha1.RolloutPhaseProgressing)
			newStatus.Phase = rolloutv1alpha1.RolloutPhaseProgressing
			cond := util.NewRolloutCondition(rolloutv1alpha1.RolloutConditionProgressing, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonInitializing, "Rollout is in Progressing")
			util.SetRolloutCondition(&newStatus, *cond)
		} else if workload.IsInStable && newStatus.CanaryStatus == nil {
			// The following logic is to make PaaS be able to judge whether the rollout is ready
			// at the first deployment of the Rollout/Workload. For example: generally, a PaaS
			// platform can use the following code to judge whether the rollout progression is completed:
			// ```
			//   if getRolloutID(workload, rollout) == rollout.Status.CanaryStatus.ObservedRolloutID &&
			//	   rollout.Status.CanaryStatus.CurrentStepState == "Completed" {
			//	   // do something after rollout
			//   }
			//```
			// But at the first deployment of Rollout/Workload, CanaryStatus isn't set due to no rollout progression,
			// and PaaS platform cannot judge whether the deployment is completed base on the code above. So we have
			// to update the status just like the rollout was completed.
			newStatus.CanaryStatus = &rolloutv1alpha1.CanaryStatus{
				CanaryReplicas:             workload.CanaryReplicas,
				CanaryReadyReplicas:        workload.CanaryReadyReplicas,
				ObservedRolloutID:          currentRolloutID,
				ObservedWorkloadGeneration: workload.Generation,
				PodTemplateHash:            workload.PodTemplateHash,
				CanaryRevision:             workload.CanaryRevision,
				CurrentStepIndex:           int32(len(rollout.Spec.Strategy.Canary.Steps)),
				CurrentStepState:           rolloutv1alpha1.CanaryStepStateCompleted,
				Message:                    rolloutv1alpha1.EncodeCanaryMessage(false, currentRolloutID),
			}
			newStatus.Message = "workload deployment is completed"
		}
	case rolloutv1alpha1.RolloutPhaseProgressing:
		cond := util.GetRolloutCondition(newStatus, rolloutv1alpha1.RolloutConditionProgressing)
		if cond == nil || cond.Reason == rolloutv1alpha1.ProgressingReasonSucceeded || cond.Reason == rolloutv1alpha1.ProgressingReasonCanceled {
			newStatus.Phase = rolloutv1alpha1.RolloutPhaseHealthy
		}
	}
	done = true
	return
}

func (r *RolloutReconciler) updateRolloutStatusInternal(rollout *rolloutv1alpha1.Rollout, newStatus rolloutv1alpha1.RolloutStatus) error {
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
func resetStatus(status *rolloutv1alpha1.RolloutStatus) {
	status.StableRevision = ""
	//util.RemoveRolloutCondition(status, rolloutv1alpha1.RolloutConditionProgressing)
	status.Phase = rolloutv1alpha1.RolloutPhaseInitial
	status.Message = "workload not found"
}

func (r *RolloutReconciler) calculateRolloutHash(rollout *rolloutv1alpha1.Rollout) error {
	spec := rollout.Spec.DeepCopy()
	// ignore paused filed
	spec.Strategy.Paused = false
	data := util.DumpJSON(spec)
	hash := rand.SafeEncodeString(hash(data))
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
	rollout.Annotations[util.RolloutHashAnnotation] = hash
	klog.Infof("rollout(%s/%s) patch annotation(%s=%s) success", rollout.Namespace, rollout.Name, util.RolloutHashAnnotation, hash)
	return nil
}

// hash hashes `data` with sha256 and returns the hex string
func hash(data string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

// isMeaninglessChangesOfRolloutID return true if rollout-id changed, but rollout is not triggered
func isMeaninglessChangesOfRolloutID(newStatus *rolloutv1alpha1.RolloutStatus, workload *util.Workload, newRolloutID string) bool {
	if newRolloutID == "" {
		return false
	}
	// just return false if no changes of rollout-id
	if newRolloutID == newStatus.CanaryStatus.ObservedRolloutID {
		return false
	}

	record := rolloutv1alpha1.DecodeCanaryMessage(newStatus.CanaryStatus.Message)
	if record.RolloutID == newRolloutID {
		return false
	}
	// rollout-id changed but revision changes has been observed by Rollout
	return isCanaryRevisionNotChanged(newStatus, workload) && newStatus.StableRevision == workload.StableRevision
}

// observedCanaryRevision return true if newStatus.CanaryStatus.CanaryRevision == workload.CanaryRevision
func isCanaryRevisionNotChanged(newStatus *rolloutv1alpha1.RolloutStatus, workload *util.Workload) bool {
	return newStatus.CanaryStatus.CanaryRevision == workload.CanaryRevision
}
