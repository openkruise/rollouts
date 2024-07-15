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
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type canaryReleaseManager struct {
	client.Client
	trafficRoutingManager *trafficrouting.Manager
	recorder              record.EventRecorder
}

func (m *canaryReleaseManager) runCanary(c *RolloutContext) error {
	canaryStatus := c.NewStatus.CanaryStatus
	if br, err := m.fetchBatchRelease(c.Rollout.Namespace, c.Rollout.Name); err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) fetch batchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return err
	} else if err == nil {
		// This line will do something important:
		// - sync status from br to Rollout: to better observability;
		// - sync rollout-id from Rollout to br: to make BatchRelease
		//   relabels pods in the scene where only rollout-id is changed.
		if err = m.syncBatchRelease(br, canaryStatus); err != nil {
			klog.Errorf("rollout(%s/%s) sync batchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
			return err
		}
	}
	// update podTemplateHash, Why is this position assigned?
	// Because If workload is deployment, only after canary pod already was created,
	// we can get the podTemplateHash from pod.annotations[pod-template-hash]
	// PodTemplateHash is used to select a new version of the Pod.

	// Note:
	// In a scenario of successive releases v1->v2->v3, It is possible that this is the PodTemplateHash value of v2,
	// so it needs to be set later in the stepUpgrade
	if canaryStatus.PodTemplateHash == "" {
		canaryStatus.PodTemplateHash = c.Workload.PodTemplateHash
	}

	if m.doCanaryJump(c) {
		klog.Infof("rollout(%s/%s) canary step jumped", c.Rollout.Namespace, c.Rollout.Name)
		return nil
	}
	// When the first batch is trafficRouting rolling and the next steps are rolling release,
	// We need to clean up the canary-related resources first and then rollout the rest of the batch.
	currentStep := c.Rollout.Spec.Strategy.Canary.Steps[canaryStatus.CurrentStepIndex-1]
	if currentStep.Traffic == nil && len(currentStep.Matches) == 0 {
		tr := newTrafficRoutingContext(c)
		done, err := m.trafficRoutingManager.FinalisingTrafficRouting(tr, false)
		c.NewStatus.CanaryStatus.LastUpdateTime = tr.LastUpdateTime
		if err != nil {
			return err
		} else if !done {
			klog.Infof("rollout(%s/%s) cleaning up canary-related resources", c.Rollout.Namespace, c.Rollout.Name)
			expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
			c.RecheckTime = &expectedTime
			return nil
		}
	}
	switch canaryStatus.CurrentStepState {
	// before CanaryStepStateUpgrade, handle some special cases, to prevent traffic loss
	case v1beta1.CanaryStepStateInit:
		// placeholder for the later traffic modification Pull Request
		canaryStatus.NextStepIndex = util.NextBatchIndex(c.Rollout, canaryStatus.CurrentStepIndex)
		canaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
		fallthrough

	case v1beta1.CanaryStepStateUpgrade:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateUpgrade)
		done, err := m.doCanaryUpgrade(c)
		if err != nil {
			return err
		} else if done {
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStateUpgrade, canaryStatus.CurrentStepState)
		}

	case v1beta1.CanaryStepStateTrafficRouting:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateTrafficRouting)
		tr := newTrafficRoutingContext(c)
		done, err := m.trafficRoutingManager.DoTrafficRouting(tr)
		c.NewStatus.CanaryStatus.LastUpdateTime = tr.LastUpdateTime
		if err != nil {
			return err
		} else if done {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateMetricsAnalysis
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStateTrafficRouting, canaryStatus.CurrentStepState)
		}
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		c.RecheckTime = &expectedTime

	case v1beta1.CanaryStepStateMetricsAnalysis:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateMetricsAnalysis)
		done, err := m.doCanaryMetricsAnalysis(c)
		if err != nil {
			return err
		} else if done {
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStatePaused
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStateMetricsAnalysis, canaryStatus.CurrentStepState)
		}

	case v1beta1.CanaryStepStatePaused:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStatePaused)
		done, err := m.doCanaryPaused(c)
		if err != nil {
			return err
		} else if done {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateReady
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStatePaused, canaryStatus.CurrentStepState)
		}

	case v1beta1.CanaryStepStateReady:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateReady)
		// run next step
		if len(c.Rollout.Spec.Strategy.Canary.Steps) > int(canaryStatus.CurrentStepIndex) {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepIndex++
			canaryStatus.NextStepIndex = util.NextBatchIndex(c.Rollout, canaryStatus.CurrentStepIndex)
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateInit
			klog.Infof("rollout(%s/%s) canary step from(%d) -> to(%d)", c.Rollout.Namespace, c.Rollout.Name, canaryStatus.CurrentStepIndex-1, canaryStatus.CurrentStepIndex)
		} else {
			klog.Infof("rollout(%s/%s) canary run all steps, and completed", c.Rollout.Namespace, c.Rollout.Name)
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
			return nil
		}
		klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
			canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStateReady, canaryStatus.CurrentStepState)
		// canary completed
	case v1beta1.CanaryStepStateCompleted:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateCompleted)
	}

	return nil
}

func (m *canaryReleaseManager) doCanaryUpgrade(c *RolloutContext) (bool, error) {
	// verify whether batchRelease configuration is the latest
	steps := len(c.Rollout.Spec.Strategy.Canary.Steps)
	canaryStatus := c.NewStatus.CanaryStatus
	cond := util.GetRolloutCondition(*c.NewStatus, v1beta1.RolloutConditionProgressing)
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and upgrade workload to new version", canaryStatus.CurrentStepIndex, steps)
	c.NewStatus.Message = cond.Message
	// run batch release to upgrade the workloads
	done, br, err := m.runBatchRelease(c.Rollout, getRolloutID(c.Workload), canaryStatus.CurrentStepIndex, c.Workload.IsInRollback)
	if err != nil {
		return false, err
	} else if !done {
		return false, nil
	}
	if br.Status.ObservedReleasePlanHash != util.HashReleasePlanBatches(&br.Spec.ReleasePlan) ||
		br.Generation != br.Status.ObservedGeneration {
		klog.Infof("rollout(%s/%s) batchRelease status is inconsistent, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
		return false, nil
	}
	// check whether batchRelease is ready(whether new pods is ready.)
	if br.Status.CanaryStatus.CurrentBatchState != v1beta1.ReadyBatchState ||
		br.Status.CanaryStatus.CurrentBatch+1 < canaryStatus.CurrentStepIndex {
		klog.Infof("rollout(%s/%s) batchRelease status(%s) is not ready, and wait a moment", c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(br.Status))
		return false, nil
	}
	m.recorder.Eventf(c.Rollout, corev1.EventTypeNormal, "Progressing", fmt.Sprintf("upgrade step(%d) canary pods with new versions done", canaryStatus.CurrentStepIndex))
	klog.Infof("rollout(%s/%s) batch(%s) state(%s), and success",
		c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(br.Status), br.Status.CanaryStatus.CurrentBatchState)
	// set the latest PodTemplateHash to selector the latest pods.
	canaryStatus.PodTemplateHash = c.Workload.PodTemplateHash
	return true, nil
}

func (m *canaryReleaseManager) doCanaryMetricsAnalysis(c *RolloutContext) (bool, error) {
	// todo
	return true, nil
}

func (m *canaryReleaseManager) doCanaryPaused(c *RolloutContext) (bool, error) {
	canaryStatus := c.NewStatus.CanaryStatus
	currentStep := c.Rollout.Spec.Strategy.Canary.Steps[canaryStatus.CurrentStepIndex-1]
	steps := len(c.Rollout.Spec.Strategy.Canary.Steps)
	cond := util.GetRolloutCondition(*c.NewStatus, v1beta1.RolloutConditionProgressing)
	// need manual confirmation
	if currentStep.Pause.Duration == nil {
		klog.Infof("rollout(%s/%s) don't set pause duration, and need manual confirmation", c.Rollout.Namespace, c.Rollout.Name)
		cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and you need manually confirm to enter the next step", canaryStatus.CurrentStepIndex, steps)
		c.NewStatus.Message = cond.Message
		return false, nil
	}
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and wait duration(%d seconds) to enter the next step", canaryStatus.CurrentStepIndex, steps, *currentStep.Pause.Duration)
	c.NewStatus.Message = cond.Message
	// wait duration time, then go to next step
	duration := time.Second * time.Duration(*currentStep.Pause.Duration)
	expectedTime := canaryStatus.LastUpdateTime.Add(duration)
	if expectedTime.Before(time.Now()) {
		klog.Infof("rollout(%s/%s) canary step(%d) paused duration(%d seconds), and go to the next step",
			c.Rollout.Namespace, c.Rollout.Name, canaryStatus.CurrentStepIndex, *currentStep.Pause.Duration)
		return true, nil
	}
	c.RecheckTime = &expectedTime
	return false, nil
}

func (m *canaryReleaseManager) doCanaryJump(c *RolloutContext) (jumped bool) {
	canaryStatus := c.NewStatus.CanaryStatus
	nextIndex := canaryStatus.NextStepIndex
	/*
		we set the CurrentStepIndex same as NextStepIndex to prevent currentStepIndex from out of range
		for example, if we had a rollout with 4 steps and CurrentStepIndex was 2
		then, the user removed 3 steps from the plan, we can calculate NextStepIndex is 1 correctly,
		but CurrentStepIndex remains 2, which could cause out of range.
	*/
	resetCurrentIndex := false
	if int(canaryStatus.CurrentStepIndex) > len(c.Rollout.Spec.Strategy.Canary.Steps) {
		canaryStatus.CurrentStepIndex = nextIndex
		resetCurrentIndex = true
	}
	currentStep := c.Rollout.Spec.Strategy.Canary.Steps[canaryStatus.CurrentStepIndex-1]
	if resetCurrentIndex || nextIndex != util.NextBatchIndex(c.Rollout, canaryStatus.CurrentStepIndex) && nextIndex > 0 {
		currentIndexBackup := canaryStatus.CurrentStepIndex
		currentStepStateBackup := canaryStatus.CurrentStepState
		canaryStatus.CurrentStepIndex = nextIndex
		canaryStatus.NextStepIndex = util.NextBatchIndex(c.Rollout, nextIndex)
		nextStep := c.Rollout.Spec.Strategy.Canary.Steps[nextIndex-1]
		// if the Replicas between currentStep and nextStep is same, we can jump to
		// the TrafficRouting step; otherwise, we should start from the Init step
		if reflect.DeepEqual(nextStep.Replicas, currentStep.Replicas) && !resetCurrentIndex {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, currentStepStateBackup, canaryStatus.CurrentStepState)
		} else {
			canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateInit
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, currentStepStateBackup, v1beta1.CanaryStepStateInit)
		}
		klog.Infof("rollout(%s/%s) canary step from(%d) -> to(%d)", c.Rollout.Namespace, c.Rollout.Name, currentIndexBackup, canaryStatus.CurrentStepIndex)
		return true
	}
	return false
}

// cleanup after rollout is completed or finished
func (m *canaryReleaseManager) doCanaryFinalising(c *RolloutContext) (bool, error) {
	// when CanaryStatus is nil, which means canary action hasn't started yet, don't need doing cleanup
	if c.NewStatus.CanaryStatus == nil {
		return true, nil
	}
	// 1. rollout progressing complete, remove rollout progressing annotation in workload
	err := m.removeRolloutProgressingAnnotation(c)
	if err != nil {
		return false, err
	}
	tr := newTrafficRoutingContext(c)
	// 2. remove stable service the pod revision selector, so stable service will be selector all version pods.
	done, err := m.trafficRoutingManager.FinalisingTrafficRouting(tr, true)
	c.NewStatus.CanaryStatus.LastUpdateTime = tr.LastUpdateTime
	if err != nil || !done {
		return done, err
	}
	// 3. modify network api(ingress or gateway api) configuration, and route 100% traffic to stable pods.
	done, err = m.trafficRoutingManager.FinalisingTrafficRouting(tr, false)
	c.NewStatus.CanaryStatus.LastUpdateTime = tr.LastUpdateTime
	if err != nil || !done {
		return done, err
	}
	// 4. set workload.pause=false; set workload.partition=0
	done, err = m.finalizingBatchRelease(c)
	if err != nil || !done {
		return done, err
	}
	// 5. delete batchRelease crd
	done, err = m.removeBatchRelease(c)
	if err != nil {
		klog.Errorf("rollout(%s/%s) Finalize batchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return false, err
	} else if !done {
		return false, nil
	}
	klog.Infof("rollout(%s/%s) doCanaryFinalising success", c.Rollout.Namespace, c.Rollout.Name)
	return true, nil
}

func (m *canaryReleaseManager) removeRolloutProgressingAnnotation(c *RolloutContext) error {
	if c.Workload == nil {
		return nil
	}
	if _, ok := c.Workload.Annotations[util.InRolloutProgressingAnnotation]; !ok {
		return nil
	}
	workloadRef := c.Rollout.Spec.WorkloadRef
	workloadGVK := schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
	obj := util.GetEmptyWorkloadObject(workloadGVK)
	obj.SetNamespace(c.Workload.Namespace)
	obj.SetName(c.Workload.Name)
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, util.InRolloutProgressingAnnotation)
	if err := m.Patch(context.TODO(), obj, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
		klog.Errorf("rollout(%s/%s) patch workload(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, c.Workload.Name, err.Error())
		return err
	}
	klog.Infof("remove rollout(%s/%s) workload(%s) annotation[%s] success", c.Rollout.Namespace, c.Rollout.Name, c.Workload.Name, util.InRolloutProgressingAnnotation)
	return nil
}

func (m *canaryReleaseManager) runBatchRelease(rollout *v1beta1.Rollout, rolloutId string, batch int32, isRollback bool) (bool, *v1beta1.BatchRelease, error) {
	batch = batch - 1
	br, err := m.fetchBatchRelease(rollout.Namespace, rollout.Name)
	if errors.IsNotFound(err) {
		// create new BatchRelease Crd
		br = createBatchRelease(rollout, rolloutId, batch, isRollback)
		if err = m.Create(context.TODO(), br); err != nil && !errors.IsAlreadyExists(err) {
			klog.Errorf("rollout(%s/%s) create BatchRelease failed: %s", rollout.Namespace, rollout.Name, err.Error())
			return false, nil, err
		}
		klog.Infof("rollout(%s/%s) create BatchRelease(%s) success", rollout.Namespace, rollout.Name, util.DumpJSON(br))
		return false, br, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	}

	// check whether batchRelease configuration is the latest
	newBr := createBatchRelease(rollout, rolloutId, batch, isRollback)
	if reflect.DeepEqual(br.Spec, newBr.Spec) && reflect.DeepEqual(br.Annotations, newBr.Annotations) {
		klog.Infof("rollout(%s/%s) do batchRelease batch(%d) success", rollout.Namespace, rollout.Name, batch+1)
		return true, br, nil
	}
	// update batchRelease to the latest version
	if err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err = m.Get(context.TODO(), client.ObjectKey{Namespace: newBr.Namespace, Name: newBr.Name}, br); err != nil {
			klog.Errorf("error getting BatchRelease(%s/%s) from client", newBr.Namespace, newBr.Name)
			return err
		}
		br.Spec = newBr.Spec
		br.Annotations = newBr.Annotations
		return m.Client.Update(context.TODO(), br)
	}); err != nil {
		klog.Errorf("rollout(%s/%s) update batchRelease failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	}
	klog.Infof("rollout(%s/%s) update batchRelease(%s) configuration to latest", rollout.Namespace, rollout.Name, util.DumpJSON(br))
	return false, br, nil
}

func (m *canaryReleaseManager) fetchBatchRelease(ns, name string) (*v1beta1.BatchRelease, error) {
	br := &v1beta1.BatchRelease{}
	// batchRelease.name is equal related rollout.name
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: name}, br)
	return br, err
}

func createBatchRelease(rollout *v1beta1.Rollout, rolloutID string, batch int32, isRollback bool) *v1beta1.BatchRelease {
	var batches []v1beta1.ReleaseBatch
	for _, step := range rollout.Spec.Strategy.Canary.Steps {
		batches = append(batches, v1beta1.ReleaseBatch{CanaryReplicas: *step.Replicas})
	}
	br := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       rollout.Namespace,
			Name:            rollout.Name,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rollout, rolloutControllerKind)},
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: rollout.Spec.WorkloadRef.APIVersion,
				Kind:       rollout.Spec.WorkloadRef.Kind,
				Name:       rollout.Spec.WorkloadRef.Name,
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches:                      batches,
				RolloutID:                    rolloutID,
				BatchPartition:               utilpointer.Int32Ptr(batch),
				FailureThreshold:             rollout.Spec.Strategy.Canary.FailureThreshold,
				PatchPodTemplateMetadata:     rollout.Spec.Strategy.Canary.PatchPodTemplateMetadata,
				RollingStyle:                 rollout.Spec.Strategy.GetRollingStyle(),
				EnableExtraWorkloadForCanary: rollout.Spec.Strategy.Canary.EnableExtraWorkloadForCanary,
			},
		},
	}
	annotations := map[string]string{}
	if isRollback {
		annotations[v1alpha1.RollbackInBatchAnnotation] = rollout.Annotations[v1alpha1.RollbackInBatchAnnotation]
	}
	if len(annotations) > 0 {
		br.Annotations = annotations
	}
	return br
}

func (m *canaryReleaseManager) removeBatchRelease(c *RolloutContext) (bool, error) {
	batch := &v1beta1.BatchRelease{}
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Rollout.Namespace, Name: c.Rollout.Name}, batch)
	if err != nil && errors.IsNotFound(err) {
		return true, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name)
		return false, err
	}
	if !batch.DeletionTimestamp.IsZero() {
		klog.Infof("rollout(%s/%s) BatchRelease is terminating, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
		return false, nil
	}

	//delete batchRelease
	err = m.Delete(context.TODO(), batch)
	if err != nil {
		klog.Errorf("rollout(%s/%s) delete BatchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) deleting BatchRelease, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
	return false, nil
}

func (m *canaryReleaseManager) finalizingBatchRelease(c *RolloutContext) (bool, error) {
	br, err := m.fetchBatchRelease(c.Rollout.Namespace, c.Rollout.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	waitReady := c.WaitReady
	// The Completed phase means batchRelease controller has processed all it
	// should process. If BatchRelease phase is completed, we can do nothing.
	if br.Spec.ReleasePlan.BatchPartition == nil &&
		br.Status.Phase == v1beta1.RolloutPhaseCompleted {
		klog.Infof("rollout(%s/%s) finalizing batchRelease(%s) done", c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(br.Status))
		return true, nil
	}

	// If BatchPartition is nil, BatchRelease will directly resume workload via:
	// - * set workload Paused = false if it needs;
	// - * set workload Partition = null if it needs.
	if br.Spec.ReleasePlan.BatchPartition == nil {
		// - If checkReady is true, finalizing policy must be "WaitResume";
		// - If checkReady is false, finalizing policy must be NOT "WaitResume";
		// Otherwise, we should correct it.
		switch br.Spec.ReleasePlan.FinalizingPolicy {
		case v1beta1.WaitResumeFinalizingPolicyType:
			if waitReady { // no need to patch again
				return false, nil
			}
		default:
			if !waitReady { // no need to patch again
				return false, nil
			}
		}
	}

	// Correct finalizing policy.
	policy := v1beta1.ImmediateFinalizingPolicyType
	if waitReady {
		policy = v1beta1.WaitResumeFinalizingPolicyType
	}

	// Patch BatchPartition and FinalizingPolicy, BatchPartition always patch null here.
	body := fmt.Sprintf(`{"spec":{"releasePlan":{"batchPartition":null,"finalizingPolicy":"%s"}}}`, policy)
	if err = m.Patch(context.TODO(), br, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
		return false, err
	}
	klog.Infof("rollout(%s/%s) patch batchRelease(%s) success", c.Rollout.Namespace, c.Rollout.Name, body)
	return false, nil
}

// syncBatchRelease sync status of br to canaryStatus, and sync rollout-id of canaryStatus to br.
func (m *canaryReleaseManager) syncBatchRelease(br *v1beta1.BatchRelease, canaryStatus *v1beta1.CanaryStatus) error {
	// sync from BatchRelease status to Rollout canaryStatus
	canaryStatus.CanaryReplicas = br.Status.CanaryStatus.UpdatedReplicas
	canaryStatus.CanaryReadyReplicas = br.Status.CanaryStatus.UpdatedReadyReplicas
	// Do not remove this line currently, otherwise, users will be not able to judge whether the BatchRelease works
	// in the scene where only rollout-id changed.
	// TODO: optimize the logic to better understand
	canaryStatus.Message = fmt.Sprintf("BatchRelease is at state %s, rollout-id %s, step %d",
		br.Status.CanaryStatus.CurrentBatchState, br.Status.ObservedRolloutID, br.Status.CanaryStatus.CurrentBatch+1)

	// sync rolloutId from canaryStatus to BatchRelease
	if canaryStatus.ObservedRolloutID != br.Spec.ReleasePlan.RolloutID {
		body := fmt.Sprintf(`{"spec":{"releasePlan":{"rolloutID":"%s"}}}`, canaryStatus.ObservedRolloutID)
		return m.Patch(context.TODO(), br, client.RawPatch(types.MergePatchType, []byte(body)))
	}
	return nil
}
