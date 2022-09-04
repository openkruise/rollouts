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

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

func (r *rolloutContext) runCanary() error {
	canaryStatus := r.newStatus.CanaryStatus
	// init canary status
	if canaryStatus.CanaryRevision == "" {
		canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
		canaryStatus.CanaryRevision = r.workload.CanaryRevision
		canaryStatus.CurrentStepIndex = 1
		canaryStatus.RolloutHash = r.rollout.Annotations[util.RolloutHashAnnotation]
	}

	// update canary status
	batch, err := r.batchControl.FetchBatchRelease()
	if err != nil {
		canaryStatus.CanaryReplicas = r.workload.CanaryReplicas
		canaryStatus.CanaryReadyReplicas = r.workload.CanaryReadyReplicas
	} else {
		canaryStatus.CanaryReplicas = batch.Status.CanaryStatus.UpdatedReplicas
		canaryStatus.CanaryReadyReplicas = batch.Status.CanaryStatus.UpdatedReadyReplicas
	}

	switch canaryStatus.CurrentStepState {
	case rolloutv1alpha1.CanaryStepStateUpgrade:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateUpgrade)
		done, err := r.doCanaryUpgrade()
		if err != nil {
			return err
		} else if done {
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateTrafficRouting
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
				canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStateUpgrade, canaryStatus.CurrentStepState)
		}

	case rolloutv1alpha1.CanaryStepStateTrafficRouting:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateTrafficRouting)
		done, err := r.doCanaryTrafficRouting()
		if err != nil {
			return err
		} else if done {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateMetricsAnalysis
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
				canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStateTrafficRouting, canaryStatus.CurrentStepState)
		}
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		r.recheckTime = &expectedTime

	case rolloutv1alpha1.CanaryStepStateMetricsAnalysis:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateMetricsAnalysis)
		done, err := r.doCanaryMetricsAnalysis()
		if err != nil {
			return err
		} else if done {
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStatePaused
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
				canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStateMetricsAnalysis, canaryStatus.CurrentStepState)
		}

	case rolloutv1alpha1.CanaryStepStatePaused:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStatePaused)
		done, err := r.doCanaryPaused()
		if err != nil {
			return err
		} else if done {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateReady
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
				canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStatePaused, canaryStatus.CurrentStepState)
		}

	case rolloutv1alpha1.CanaryStepStateReady:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateReady)
		// run next step
		if len(r.rollout.Spec.Strategy.Canary.Steps) > int(canaryStatus.CurrentStepIndex) {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepIndex++
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
			klog.Infof("rollout(%s/%s) canary step from(%d) -> to(%d)", r.rollout.Namespace, r.rollout.Name, canaryStatus.CurrentStepIndex-1, canaryStatus.CurrentStepIndex)
		} else {
			klog.Infof("rollout(%s/%s) canary run all steps, and completed", r.rollout.Namespace, r.rollout.Name)
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateCompleted
		}
		klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
			canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStateReady, canaryStatus.CurrentStepState)
		// canary completed
	case rolloutv1alpha1.CanaryStepStateCompleted:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateCompleted)
	}

	return nil
}

func (r *rolloutContext) doCanaryUpgrade() (bool, error) {
	// only traffic routing
	/*if len(r.rollout.Spec.Strategy.Canary.Steps) == 0 {
		if r.workload.CanaryReadyReplicas > 0 {
			klog.Infof("rollout(%s/%s) workload(%s) canaryAvailable(%d), and go to the next stage",
				r.rollout.Namespace, r.rollout.Name, r.workload.Name, r.workload.CanaryReadyReplicas)
			return true, nil
		}
		klog.Infof("rollout(%s/%s) workload(%s) canaryAvailable(%d), and wait a moment",
			r.rollout.Namespace, r.rollout.Name, r.workload.Name, r.workload.CanaryReadyReplicas)
		return false, nil
	}*/

	// verify the step run time (now) whether in time slices
	steps := len(r.rollout.Spec.Strategy.Canary.Steps)
	canaryStatus := r.newStatus.CanaryStatus
	cond := util.GetRolloutCondition(*r.newStatus, rolloutv1alpha1.RolloutConditionProgressing)
	expectedTime, ok := r.isAllowRun(time.Now())
	if !ok {
		localTime := expectedTime
		if &r.rollout.Spec.AllowRunTime != nil {
			localTime.In(util.TimeZone(r.rollout.Spec.AllowRunTime.TimeZone))
		}
		msg := fmt.Sprintf("Rollout (%d/%d) will be start at time %s(%s), because now is not in time slices",
			canaryStatus.CurrentStepIndex, steps,
			expectedTime.Format(util.DateTimeLayout),
			localTime.Format(util.DateTimeZoneLayout))
		klog.Info(msg)
		cond.Message = msg
		r.newStatus.Message = cond.Message
		r.recheckTime = &expectedTime
		return false, nil
	}

	// verify whether batchRelease configuration is the latest
	isLatest, err := r.batchControl.Verify(canaryStatus.CurrentStepIndex)
	if err != nil {
		return false, err
	} else if !isLatest {
		return false, nil
	}

	// fetch batchRelease
	batch, err := r.batchControl.FetchBatchRelease()
	if err != nil {
		return false, err
	} else if batch.Status.ObservedReleasePlanHash != util.HashReleasePlanBatches(&batch.Spec.ReleasePlan) ||
		batch.Generation != batch.Status.ObservedGeneration {
		klog.Infof("rollout(%s/%s) batchReleasePlan is not consistent, and wait a moment", r.rollout.Namespace, r.rollout.Name)
		return false, nil
	}
	batchData := util.DumpJSON(batch.Status)
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and upgrade workload new versions", canaryStatus.CurrentStepIndex, steps)
	r.newStatus.Message = cond.Message
	// promote workload next batch release
	if !batchrelease.IsPromoted(r.rollout, batch, r.workload.IsInRollback) {
		r.recorder.Eventf(r.rollout, corev1.EventTypeNormal, "Progressing", fmt.Sprintf("start upgrade step(%d) canary pods with new versions", canaryStatus.CurrentStepIndex))
		klog.Infof("rollout(%s/%s) will promote batch from(%d) -> to(%d)", r.rollout.Namespace, r.rollout.Name, *batch.Spec.ReleasePlan.BatchPartition+1, canaryStatus.CurrentStepIndex)
		return r.batchControl.Promote(canaryStatus.CurrentStepIndex, r.workload.IsInRollback, false)
	}

	// check whether batchRelease is ready
	if batch.Status.CanaryStatus.CurrentBatchState != rolloutv1alpha1.ReadyBatchState ||
		batch.Status.CanaryStatus.CurrentBatch+1 < canaryStatus.CurrentStepIndex {
		klog.Infof("rollout(%s/%s) batch(%s) state(%s), and wait a moment",
			r.rollout.Namespace, r.rollout.Name, batchData, batch.Status.CanaryStatus.CurrentBatchState)
		return false, nil
	}
	r.recorder.Eventf(r.rollout, corev1.EventTypeNormal, "Progressing", fmt.Sprintf("upgrade step(%d) canary pods with new versions done", canaryStatus.CurrentStepIndex))
	klog.Infof("rollout(%s/%s) batch(%s) state(%s), and success",
		r.rollout.Namespace, r.rollout.Name, batchData, batch.Status.CanaryStatus.CurrentBatchState)
	return true, nil
}

func (r *rolloutContext) doCanaryMetricsAnalysis() (bool, error) {
	// todo
	return true, nil
}

func (r *rolloutContext) doCanaryPaused() (bool, error) {
	// No step set, need manual confirmation
	if len(r.rollout.Spec.Strategy.Canary.Steps) == 0 {
		klog.Infof("rollout(%s/%s) don't contains steps, and need manual confirmation", r.rollout.Namespace, r.rollout.Name)
		return false, nil
	}
	canaryStatus := r.newStatus.CanaryStatus
	currentStep := r.rollout.Spec.Strategy.Canary.Steps[canaryStatus.CurrentStepIndex-1]
	steps := len(r.rollout.Spec.Strategy.Canary.Steps)
	cond := util.GetRolloutCondition(*r.newStatus, rolloutv1alpha1.RolloutConditionProgressing)
	// need manual confirmation
	if currentStep.Pause.Duration == nil {
		klog.Infof("rollout(%s/%s) don't set pause duration, and need manual confirmation", r.rollout.Namespace, r.rollout.Name)
		cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and you need manually confirm to enter the next step", canaryStatus.CurrentStepIndex, steps)
		r.newStatus.Message = cond.Message
		return false, nil
	}
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and wait duration(%d seconds) to enter the next step", canaryStatus.CurrentStepIndex, steps, *currentStep.Pause.Duration)
	r.newStatus.Message = cond.Message
	// wait duration time, then go to next step
	duration := time.Second * time.Duration(*currentStep.Pause.Duration)
	expectedTime := canaryStatus.LastUpdateTime.Add(duration)
	if expectedTime.Before(time.Now()) {
		klog.Infof("rollout(%s/%s) canary step(%d) paused duration(%d seconds), and go to the next step",
			r.rollout.Namespace, r.rollout.Name, canaryStatus.CurrentStepIndex, *currentStep.Pause.Duration)
		return true, nil
	}
	r.recheckTime = &expectedTime
	return false, nil
}

// cleanup after rollout is completed or finished
func (r *rolloutContext) doCanaryFinalising() (bool, error) {
	// when CanaryStatus is nil, which means canary action hasn't started yet, don't need doing cleanup
	if r.newStatus.CanaryStatus == nil {
		return true, nil
	}
	klog.Infof("rollout(%s/%s) in finalizing: remove rollout state in workload", r.rollout.Namespace, r.rollout.Name)
	// 1. rollout progressing complete, allow workload paused=false in webhook
	err := r.removeRolloutStateInWorkload()
	if err != nil {
		return false, err
	}
	klog.Infof("rollout(%s/%s) in finalizing: restore stable service", r.rollout.Namespace, r.rollout.Name)
	// 2. restore stable service, remove podRevision selector
	done, err := r.restoreStableService()
	if err != nil || !done {
		return done, err
	}
	// 3. upgrade stable deployment, set paused=false
	// isComplete indicates whether rollout progressing complete, and wait for all pods are ready
	// else indicates rollout is canceled
	klog.Infof("rollout(%s/%s) in finalizing: upgrade stable workload", r.rollout.Namespace, r.rollout.Name)
	done, err = r.batchControl.Promote(-1, false, r.isComplete)
	if err != nil || !done {
		return done, err
	}
	// 4. route all traffic to stable service
	klog.Infof("rollout(%s/%s) in finalizing: restore traffic routing", r.rollout.Namespace, r.rollout.Name)
	done, err = r.doFinalisingTrafficRouting()
	if err != nil || !done {
		return done, err
	}
	// 5. delete batchRelease crd
	klog.Infof("rollout(%s/%s) in finalizing: remove batchRelease crd", r.rollout.Namespace, r.rollout.Name)
	done, err = r.batchControl.Finalize()
	if err != nil {
		klog.Errorf("rollout(%s/%s) Finalize batchRelease failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	} else if !done {
		return false, nil
	}
	klog.Infof("rollout(%s/%s) do finalize success", r.rollout.Namespace, r.rollout.Name)
	return true, nil
}

func (r *rolloutContext) removeRolloutStateInWorkload() error {
	if r.workload == nil || r.rollout.Spec.ObjectRef.WorkloadRef == nil {
		return nil
	}
	if _, ok := r.workload.Annotations[util.InRolloutProgressingAnnotation]; !ok {
		return nil
	}
	workloadRef := r.rollout.Spec.ObjectRef.WorkloadRef
	workloadGVK := schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		obj := util.GetEmptyWorkloadObject(workloadGVK)
		if obj == nil {
			return nil
		}
		if err := r.Get(context.TODO(), types.NamespacedName{Name: r.workload.Name, Namespace: r.workload.Namespace}, obj); err != nil {
			klog.Errorf("getting updated workload(%s.%s) failed: %s", r.workload.Namespace, r.workload.Name, err.Error())
			return err
		}
		annotations := obj.GetAnnotations()
		delete(annotations, util.InRolloutProgressingAnnotation)
		obj.SetAnnotations(annotations)
		return r.Update(context.TODO(), obj)
	})
	if err != nil {
		klog.Errorf("update rollout(%s/%s) workload(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.workload.Name, err.Error())
		return err
	}
	klog.Infof("remove rollout(%s/%s) workload(%s) annotation[%s] success", r.rollout.Namespace, r.rollout.Name, r.workload.Name, util.InRolloutProgressingAnnotation)
	return nil
}
