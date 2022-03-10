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
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *rolloutContext) runCanary() error {
	canaryStatus := r.newStatus.CanaryStatus
	// In case of continuous publishing(v1 -> v2 -> v3), then restart publishing
	if canaryStatus.CanaryRevision == "" {
		canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
		canaryStatus.CanaryRevision = r.workload.CanaryRevision
	}
	if canaryStatus.CurrentStepIndex == 0 {
		canaryStatus.CurrentStepIndex = 1
	}
	if r.rollout.Spec.Strategy.Canary.TrafficRouting.GracePeriodSeconds <= 0 {
		r.rollout.Spec.Strategy.Canary.TrafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}
	// update canary status
	canaryStatus.CanaryReplicas = r.workload.CanaryReplicas
	canaryStatus.CanaryReadyReplicas = r.workload.CanaryReadyReplicas
	switch canaryStatus.CurrentStepState {
	case rolloutv1alpha1.CanaryStepStateUpgrade:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateUpgrade)
		done, err := r.doCanaryUpgrade()
		if err != nil {
			return err
		} else if done {
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateTrafficRouting
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			klog.Infof("rollout(%s/%s) step(index:%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
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
			klog.Infof("rollout(%s/%s) step(index:%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
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
			klog.Infof("rollout(%s/%s) step(index:%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
				canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStateMetricsAnalysis, canaryStatus.CurrentStepState)
		}

	case rolloutv1alpha1.CanaryStepStatePaused:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStatePaused)
		done, err := r.doCanaryPaused()
		if err != nil {
			return err
		} else if done {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateCompleted
			klog.Infof("rollout(%s/%s) step(index:%d) state from(%s) -> to(%s)", r.rollout.Namespace, r.rollout.Name,
				canaryStatus.CurrentStepIndex, rolloutv1alpha1.CanaryStepStatePaused, canaryStatus.CurrentStepState)
		}

	case rolloutv1alpha1.CanaryStepStateCompleted:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", r.rollout.Namespace, r.rollout.Name, rolloutv1alpha1.CanaryStepStateCompleted)
		if len(r.rollout.Spec.Strategy.Canary.Steps) > int(canaryStatus.CurrentStepIndex) {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepIndex++
			canaryStatus.CurrentStepState = rolloutv1alpha1.CanaryStepStateUpgrade
			klog.Infof("rollout(%s/%s) canary step from(%d) -> to(%d)", r.rollout.Namespace, r.rollout.Name, canaryStatus.CurrentStepIndex-1, canaryStatus.CurrentStepIndex)
		} else {
			klog.Infof("rollout(%s/%s) canary run all steps, and completed", r.rollout.Namespace, r.rollout.Name, canaryStatus.CurrentStepIndex-1, canaryStatus.CurrentStepIndex)
		}
	}

	return nil
}

func (r *rolloutContext) doCanaryUpgrade() (bool, error) {
	steps := len(r.rollout.Spec.Strategy.Canary.Steps)
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
	// canary release
	batchState, err := r.batchControl.BatchReleaseState()
	if err != nil {
		return false, err
	}
	canaryStatus := r.newStatus.CanaryStatus
	cond := util.GetRolloutCondition(*r.newStatus, rolloutv1alpha1.RolloutConditionProgressing)
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and upgrade workload new versions", canaryStatus.CurrentStepIndex, steps)
	r.newStatus.Message = cond.Message
	// promote workload next batch release
	if batchState.CurrentBatch < canaryStatus.CurrentStepIndex {
		r.recorder.Eventf(r.rollout, corev1.EventTypeNormal, "Progressing", fmt.Sprintf("start upgrade step(%d) canary pods with new versions", canaryStatus.CurrentStepIndex))
		klog.Infof("rollout(%s/%s) will promote batch from(%d) -> to(%d)", r.rollout.Namespace, r.rollout.Name, batchState.CurrentBatch, canaryStatus.CurrentStepIndex)
		return r.batchControl.Promote(canaryStatus.CurrentStepIndex, false)
	}

	// check whether batchRelease is ready
	if batchState.State == batchrelease.BatchInRollingState {
		klog.Infof("rollout(%s/%s) workload(%s) batch(%d) availableReplicas(%d) state(%s), and wait a moment",
			r.rollout.Namespace, r.rollout.Name, r.workload.Name, canaryStatus.CurrentStepIndex, batchState.UpdatedReadyReplicas, batchState.State)
		return false, nil
	}
	r.recorder.Eventf(r.rollout, corev1.EventTypeNormal, "Progressing", fmt.Sprintf("upgrade step(%d) canary pods with new versions done", canaryStatus.CurrentStepIndex))
	klog.Infof("rollout(%s/%s) workload(%s) batch(%d) availableReplicas(%d) state(%s), and continue",
		r.rollout.Namespace, r.rollout.Name, r.workload.Name, canaryStatus.CurrentStepIndex, batchState.UpdatedReadyReplicas, batchState.State)
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
		cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and you need manually confirm(kube-cli approve) to enter the next step", canaryStatus.CurrentStepIndex, steps)
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
	if r.rollout.Spec.Strategy.Canary.TrafficRouting.GracePeriodSeconds <= 0 {
		r.rollout.Spec.Strategy.Canary.TrafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}
	// 1. rollout progressing complete, allow workload paused=false in webhook
	err := r.removeRolloutStateInWorkload()
	if err != nil {
		return false, err
	}
	// 2. restore stable service, remove podRevision selector
	done, err := r.restoreStableService()
	if err != nil || !done {
		return done, err
	}
	// 3. upgrade stable deployment, set paused=false
	// isComplete indicates whether rollout progressing complete, and wait for all pods are ready
	// else indicates rollout is canceled
	done, err = r.batchControl.Promote(-1, r.isComplete)
	if err != nil || !done {
		return done, err
	}
	// 4. route all traffic to stable service
	done, err = r.doFinalisingTrafficRouting()
	if err != nil || !done {
		return done, err
	}
	// 5. delete batchRelease crd
	done, err = r.batchControl.Finalize()
	if err != nil {
		klog.Errorf("rollout(%s/%s) DoFinalising batchRelease failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	} else if !done {
		return false, nil
	}
	klog.Infof("rollout(%s/%s) batchRelease Finalize success", r.rollout.Namespace, r.rollout.Name)
	return true, nil
}

func (r *rolloutContext) podRevisionLabelKey() string {
	if r.rollout.Spec.ObjectRef.WorkloadRef.Kind == util.ControllerKruiseKindCS.Kind {
		return util.CloneSetPodRevisionLabelKey
	}
	return util.RsPodRevisionLabelKey
}

func (r *rolloutContext) removeRolloutStateInWorkload() error {
	if r.workload == nil {
		return nil
	}
	if _, ok := r.workload.Annotations[util.InRolloutProgressingAnnotation]; !ok {
		return nil
	}
	var obj client.Object
	// cloneSet
	if r.workload.Kind == util.ControllerKruiseKindCS.Kind {
		obj = &appsv1alpha1.CloneSet{}
		// deployment
	} else {
		obj = &apps.Deployment{}
	}
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
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
