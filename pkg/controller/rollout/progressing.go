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

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		newStatus.CanaryStatus = &rolloutv1alpha1.CanaryStatus{}
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
		// rollout canceled, indicates rollback(v1 -> v2 -> v1)
		if workload.IsInRollback && workload.CanaryRevision != rollout.Status.CanaryStatus.CanaryRevision {
			// cloneset may disable quick rollback and expect to rollback in batch style
			if util.IsRollbackInBatchPolicy(rollout.Spec.ObjectRef.WorkloadRef, rollout.Annotations) {
				batchControl := batchrelease.NewInnerBatchController(r.Client, rollout)
				newStepIndex, err := r.reCalculateCanaryStepIndex(rollout, batchControl, true)
				if err != nil {
					klog.Errorf("rollout(%s/%s) reCalculate Canary StepIndex failed: %s", rollout.Namespace, rollout.Name, err.Error())
					return nil, err
				}
				// canary step configuration change causes current step index change
				newStatus.CanaryStatus.CurrentStepIndex = newStepIndex
				newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
				newStatus.CanaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
				newStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
				newStatus.CanaryStatus.RolloutHash = rollout.Annotations[util.RolloutHashAnnotation]
				r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload has been rollback(disable quickly rollback policy), then recalculate index as %v", newStepIndex)
				klog.Infof("rollout(%s/%s) workload has been rollback(disable quickly rollback policy), then recalculate batch index as %d", rollout.Namespace, rollout.Name, newStepIndex)
			} else {
				newStatus.CanaryStatus.CanaryRevision = workload.CanaryRevision
				r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload has been rollback, then rollout is canceled")
				klog.Infof("rollout(%s/%s) workload has been rollback(quickly rollback policy), then rollout canceled", rollout.Namespace, rollout.Name)
				progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonCancelling, "The workload has been rolled back and the rollout process will be cancelled")
			}
			// paused rollout progress
		} else if rollout.Spec.Strategy.Paused {
			klog.Infof("rollout(%s/%s) is Progressing, but paused", rollout.Namespace, rollout.Name)
			progressingStateTransition(newStatus, corev1.ConditionFalse, rolloutv1alpha1.ProgressingReasonPaused, "Rollout has been paused, you can resume it by kube-cli")
			// In case of continuous publishing(v1 -> v2 -> v3), then restart publishing
		} else if newStatus.CanaryStatus.CanaryRevision != "" && workload.CanaryRevision != newStatus.CanaryStatus.CanaryRevision {
			r.Recorder.Eventf(rollout, corev1.EventTypeNormal, "Progressing", "workload continuous publishing canaryRevision, then restart publishing")
			klog.Infof("rollout(%s/%s) workload continuous publishing canaryRevision from(%s) -> to(%s), then restart publishing",
				rollout.Namespace, rollout.Name, newStatus.CanaryStatus.CanaryRevision, workload.CanaryRevision)
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
			// rollout canary steps configuration change
		} else if newStatus.CanaryStatus.RolloutHash != "" && newStatus.CanaryStatus.RolloutHash != rollout.Annotations[util.RolloutHashAnnotation] {
			batchControl := batchrelease.NewInnerBatchController(r.Client, rollout)
			newStepIndex, err := r.reCalculateCanaryStepIndex(rollout, batchControl, false)
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
		} else {
			klog.Infof("rollout(%s/%s) is Progressing, and in reason(%s)", rollout.Namespace, rollout.Name, cond.Reason)
			//check if canary is done
			if newStatus.CanaryStatus.CurrentStepState == rolloutv1alpha1.CanaryStepStateCompleted {
				klog.Infof("rollout(%s/%s) progressing rolling done", rollout.Namespace, rollout.Name)
				progressingStateTransition(newStatus, corev1.ConditionTrue, rolloutv1alpha1.ProgressingReasonFinalising, "Rollout has been completed and some closing work is being done")
			} else { // rollout is in rolling
				newStatus.CanaryStatus.PodTemplateHash = workload.PodTemplateHash
				recheckTime, err = r.doProgressingInRolling(rollout, newStatus)
				if err != nil {
					return nil, err
				}
			}
		}
	// after the normal completion of rollout, enter into the Finalising process
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
		// rollout canceled, indicates rollback(v1 -> v2 -> v1)
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
	return r.verifyCanaryStrategy(rollout, newStatus)
}

func (r *RolloutReconciler) doProgressingInRolling(rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus) (*time.Time, error) {
	// fetch target workload
	workload, err := r.Finder.GetWorkloadForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		klog.Errorf("rollout(%s/%s) GetWorkloadForRef failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	} else if workload == nil {
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		klog.Warningf("rollout(%s/%s) Fetch workload Not Found, and recheck(%s)", rollout.Namespace, rollout.Name, expectedTime.String())
		return &expectedTime, nil
	}

	rolloutCon := &rolloutContext{
		Client:       r.Client,
		rollout:      rollout,
		newStatus:    newStatus,
		workload:     workload,
		batchControl: batchrelease.NewInnerBatchController(r.Client, rollout),
		recorder:     r.Recorder,
	}
	err = rolloutCon.reconcile()
	if err != nil {
		klog.Errorf("rollout(%s/%s) Progressing failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return nil, err
	}
	return rolloutCon.recheckTime, nil
}

func (r *RolloutReconciler) doProgressingReset(rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus) (bool, error) {
	rolloutCon := &rolloutContext{
		Client:       r.Client,
		rollout:      rollout,
		newStatus:    newStatus,
		batchControl: batchrelease.NewInnerBatchController(r.Client, rollout),
		recorder:     r.Recorder,
	}

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
	if canary.TrafficRoutings != nil {
		if ok, msg, err := r.verifyTrafficRouting(rollout.Namespace, canary.TrafficRoutings[0]); !ok {
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

func (r *RolloutReconciler) verifyTrafficRouting(ns string, tr *rolloutv1alpha1.TrafficRouting) (bool, string, error) {
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
	case "nginx":
		ingressName = tr.Ingress.Name
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

func (r *RolloutReconciler) reCalculateCanaryStepIndex(rollout *rolloutv1alpha1.Rollout, batchControl batchrelease.BatchRelease, isRollback bool) (int32, error) {
	batch, err := batchControl.FetchBatchRelease()
	if errors.IsNotFound(err) {
		return 1, nil
	} else if err != nil {
		return 0, err
	}

	workloadObj, err := r.Finder.GetObjectForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
	if err != nil {
		return 0, err
	}

	workloadReplicas := util.ParseReplicas(workloadObj)

	var currentReplicas int
	if isRollback {
		/* currently, only CloneSet will enter this rollback logic */

		// If trafficRouting is nil, we will restart the rollout from beginning.
		// This is for patching correct batch label to pod in rollback scene.
		if rollout.Spec.Strategy.Canary.TrafficRoutings == nil {
			return 1, nil
		}

		// If trafficRouting has been set, we have to recalculate the canaryStepIndex
		// based on current updatedReplicas and expectedUpdatedReplicas, otherwise,
		// improper traffic weight will be set for versioned services.
		switch object := workloadObj.(type) {
		case *appsv1alpha1.CloneSet:
			// the number of expected updated pods, which is calculated via partition and replicas.
			expect := util.GetCloneSetExpectedUpdatedReplicas(object)
			// the number of updated pods.
			actual := object.Status.UpdatedReplicas
			klog.Infof("CloneSet(%v) expectedUpdatedReplicas: %v, updatedReplicas: %v", client.ObjectKeyFromObject(workloadObj), expect, actual)
			// we should select the MAX one between expected and actual updated replicas.
			currentReplicas = integer.IntMax(int(expect), int(actual))
		}
	} else {
		currentReplicas, _ = intstr.GetScaledValueFromIntOrPercent(&batch.Spec.ReleasePlan.Batches[*batch.Spec.ReleasePlan.BatchPartition].CanaryReplicas, int(workloadReplicas), true)
		return util.ReCalculateCanaryStepIndex(rollout, int(workloadReplicas), currentReplicas), nil
	}

	return util.ReCalculateCanaryStepIndex(rollout, int(workloadReplicas), currentReplicas), nil
}
