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

func (r *RolloutReconciler) updateRolloutStatus(rollout *rolloutv1alpha1.Rollout) (bool, error) {
	newStatus := *rollout.Status.DeepCopy()
	newStatus.ObservedGeneration = rollout.GetGeneration()
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
		return false, err
	} else if workload == nil && rollout.DeletionTimestamp.IsZero() {
		resetStatus(&newStatus)
		klog.Infof("rollout(%s/%s) workload not found, and reset status be Initial", rollout.Namespace, rollout.Name)
		// workload status is not consistent
	} else if workload != nil && !workload.IsStatusConsistent {
		klog.Infof("rollout(%s/%s) workload status isn't consistent, then wait a moment", rollout.Namespace, rollout.Name)
		return false, nil
	} else if workload != nil {
		newStatus.StableRevision = workload.StableRevision
		newStatus.CanaryRevision = workload.CanaryRevision
		// update workload generation to canaryStatus.ObservedWorkloadGeneration
		// rollout is a target ref bypass, so there needs to be a field to identify the rollout execution process or results,
		// which version of deployment is targeted, ObservedWorkloadGeneration that is to compare with the workload generation
		if newStatus.CanaryStatus != nil && newStatus.CanaryStatus.CanaryRevision != "" &&
			newStatus.CanaryStatus.CanaryRevision == workload.CanaryRevision {
			newStatus.CanaryStatus.ObservedWorkloadGeneration = workload.Generation
		}
	}

	switch newStatus.Phase {
	case rolloutv1alpha1.RolloutPhaseInitial:
		if workload != nil {
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, rolloutv1alpha1.RolloutPhaseInitial, rolloutv1alpha1.RolloutPhaseHealthy)
			newStatus.Phase = rolloutv1alpha1.RolloutPhaseHealthy
			newStatus.Message = "rollout is healthy"
		}
	case rolloutv1alpha1.RolloutPhaseHealthy:
		// from healthy to progressing
		if workload.InRolloutProgressing {
			klog.Infof("rollout(%s/%s) status phase from(%s) -> to(%s)", rollout.Namespace, rollout.Name, rolloutv1alpha1.RolloutPhaseHealthy, rolloutv1alpha1.RolloutPhaseProgressing)
			newStatus.Phase = rolloutv1alpha1.RolloutPhaseProgressing
			cond := util.NewRolloutCondition(rolloutv1alpha1.RolloutConditionProgressing, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonInitializing, "Rollout is in Progressing")
			util.SetRolloutCondition(&newStatus, *cond)
		}
	case rolloutv1alpha1.RolloutPhaseProgressing:
		cond := util.GetRolloutCondition(newStatus, rolloutv1alpha1.RolloutConditionProgressing)
		if cond == nil || cond.Reason == rolloutv1alpha1.ProgressingReasonSucceeded || cond.Reason == rolloutv1alpha1.ProgressingReasonCanceled {
			newStatus.Phase = rolloutv1alpha1.RolloutPhaseHealthy
		}
	}
	err = r.updateRolloutStatusInternal(rollout, newStatus)
	if err != nil {
		klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, err
	}
	err = r.calculateRolloutHash(rollout)
	if err != nil {
		return false, err
	}
	rollout.Status = newStatus
	return true, nil
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
	status.CanaryRevision = ""
	status.StableRevision = ""
	util.RemoveRolloutCondition(status, rolloutv1alpha1.RolloutConditionProgressing)
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
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, util.RolloutHashAnnotation, hash)
	err := r.Patch(context.TODO(), rollout, client.RawPatch(types.MergePatchType, []byte(body)))
	if err != nil {
		klog.Errorf("rollout(%s/%s) patch(%s) failed: %s", rollout.Namespace, rollout.Name, body, err.Error())
		return err
	}
	rollout.Annotations[util.RolloutHashAnnotation] = hash
	klog.Infof("rollout(%s/%s) patch annotation(%s=%s) success", rollout.Namespace, rollout.Name, util.RolloutHashAnnotation, hash)
	return nil
}

// hash hashes `data` with sha256 and returns the hex string
func hash(data string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}
