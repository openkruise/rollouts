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

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type blueGreenReleaseManager struct {
	client.Client
	trafficRoutingManager *trafficrouting.Manager
	recorder              record.EventRecorder
}

func (m *blueGreenReleaseManager) runCanary(c *RolloutContext) error {
	blueGreenStatus := c.NewStatus.BlueGreenStatus
	if br, err := fetchBatchRelease(m.Client, c.Rollout.Namespace, c.Rollout.Name); err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) fetch batchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return err
	} else if err == nil {
		// This line will do something important:
		// - sync status from br to Rollout: to better observability;
		// - sync rollout-id from Rollout to br: to make BatchRelease
		//   relabels pods in the scene where only rollout-id is changed.
		if err = m.syncBatchRelease(br, blueGreenStatus); err != nil {
			klog.Errorf("rollout(%s/%s) sync batchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
			return err
		}
	}
	if blueGreenStatus.PodTemplateHash == "" {
		blueGreenStatus.PodTemplateHash = c.Workload.PodTemplateHash
	}

	if m.doCanaryJump(c) {
		return nil
	}
	// When the first batch is trafficRouting rolling and the next steps are rolling release,
	// We need to clean up the canary-related resources first and then rollout the rest of the batch.
	currentStep := c.Rollout.Spec.Strategy.BlueGreen.Steps[blueGreenStatus.CurrentStepIndex-1]
	if currentStep.Traffic == nil && len(currentStep.Matches) == 0 {
		tr := newTrafficRoutingContext(c)
		done, err := m.trafficRoutingManager.FinalisingTrafficRouting(tr)
		blueGreenStatus.LastUpdateTime = tr.LastUpdateTime
		if err != nil {
			return err
		} else if !done {
			klog.Infof("rollout(%s/%s) cleaning up canary-related resources", c.Rollout.Namespace, c.Rollout.Name)
			expectedTime := time.Now().Add(tr.RecheckDuration)
			c.RecheckTime = &expectedTime
			return nil
		}
	}

	switch blueGreenStatus.CurrentStepState {
	// before CanaryStepStateUpgrade, handle some special cases, to prevent traffic loss
	case v1beta1.CanaryStepStateInit:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateInit)
		tr := newTrafficRoutingContext(c)
		if currentStep.Traffic != nil || len(currentStep.Matches) > 0 {
			//TODO - consider istio subsets
			if blueGreenStatus.CurrentStepIndex == 1 {
				klog.Infof("Before the first batch, rollout(%s/%s) patch stable Service", c.Rollout.Namespace, c.Rollout.Name)
				retry, err := m.trafficRoutingManager.PatchStableService(tr)
				if err != nil {
					return err
				} else if retry {
					expectedTime := time.Now().Add(tr.RecheckDuration)
					c.RecheckTime = &expectedTime
					return nil
				}
			}
		}

		blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
		klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
			blueGreenStatus.CurrentStepIndex, v1beta1.CanaryStepStateInit, blueGreenStatus.CurrentStepState)
		fallthrough

	case v1beta1.CanaryStepStateUpgrade:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateUpgrade)
		done, err := m.doCanaryUpgrade(c)
		if err != nil {
			return err
		} else if done {
			blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
			blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				blueGreenStatus.CurrentStepIndex, v1beta1.CanaryStepStateUpgrade, blueGreenStatus.CurrentStepState)
		}

	case v1beta1.CanaryStepStateTrafficRouting:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateTrafficRouting)
		tr := newTrafficRoutingContext(c)
		done, err := m.trafficRoutingManager.DoTrafficRouting(tr)
		blueGreenStatus.LastUpdateTime = tr.LastUpdateTime
		if err != nil {
			return err
		} else if done {
			blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateMetricsAnalysis
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				blueGreenStatus.CurrentStepIndex, v1beta1.CanaryStepStateTrafficRouting, blueGreenStatus.CurrentStepState)
		}
		expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		c.RecheckTime = &expectedTime

	case v1beta1.CanaryStepStateMetricsAnalysis:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateMetricsAnalysis)
		done, err := m.doCanaryMetricsAnalysis(c)
		if err != nil {
			return err
		} else if done {
			blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStatePaused
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				blueGreenStatus.CurrentStepIndex, v1beta1.CanaryStepStateMetricsAnalysis, blueGreenStatus.CurrentStepState)
		}

	case v1beta1.CanaryStepStatePaused:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStatePaused)
		done, err := m.doCanaryPaused(c)
		if err != nil {
			return err
		} else if done {
			blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
			blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateReady
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				blueGreenStatus.CurrentStepIndex, v1beta1.CanaryStepStatePaused, blueGreenStatus.CurrentStepState)
		}

	case v1beta1.CanaryStepStateReady:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateReady)
		blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		// run next step
		if len(c.Rollout.Spec.Strategy.BlueGreen.Steps) > int(blueGreenStatus.CurrentStepIndex) {
			blueGreenStatus.CurrentStepIndex++
			blueGreenStatus.NextStepIndex = util.NextBatchIndex(c.Rollout, blueGreenStatus.CurrentStepIndex)
			blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateInit
			klog.Infof("rollout(%s/%s) bluegreen step from(%d) -> to(%d)", c.Rollout.Namespace, c.Rollout.Name, blueGreenStatus.CurrentStepIndex-1, blueGreenStatus.CurrentStepIndex)
			return nil
		}
		// completed
		blueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
		klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s), run all steps", c.Rollout.Namespace, c.Rollout.Name,
			blueGreenStatus.CurrentStepIndex, v1beta1.CanaryStepStateReady, blueGreenStatus.CurrentStepState)
		fallthrough
	// canary completed
	case v1beta1.CanaryStepStateCompleted:
		klog.Infof("rollout(%s/%s) run bluegreen strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateCompleted)
	}

	return nil
}

func (m *blueGreenReleaseManager) doCanaryUpgrade(c *RolloutContext) (bool, error) {
	// verify whether batchRelease configuration is the latest
	steps := len(c.Rollout.Spec.Strategy.BlueGreen.Steps)
	blueGreenStatus := c.NewStatus.BlueGreenStatus
	cond := util.GetRolloutCondition(*c.NewStatus, v1beta1.RolloutConditionProgressing)
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and upgrade workload to new version", blueGreenStatus.CurrentStepIndex, steps)
	c.NewStatus.Message = cond.Message
	// run batch release to upgrade the workloads
	done, br, err := runBatchRelease(m, c.Rollout, getRolloutID(c.Workload), blueGreenStatus.CurrentStepIndex, c.Workload.IsInRollback)
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
		br.Status.CanaryStatus.CurrentBatch+1 < blueGreenStatus.CurrentStepIndex {
		klog.Infof("rollout(%s/%s) batchRelease status(%s) is not ready, and wait a moment", c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(br.Status))
		return false, nil
	}
	m.recorder.Eventf(c.Rollout, corev1.EventTypeNormal, "Progressing", fmt.Sprintf("upgrade step(%d) canary pods with new versions done", blueGreenStatus.CurrentStepIndex))
	klog.Infof("rollout(%s/%s) batch(%s) state(%s), and success",
		c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(br.Status), br.Status.CanaryStatus.CurrentBatchState)
	// set the latest PodTemplateHash to selector the latest pods.
	blueGreenStatus.PodTemplateHash = c.Workload.PodTemplateHash
	return true, nil
}

func (m *blueGreenReleaseManager) doCanaryMetricsAnalysis(c *RolloutContext) (bool, error) {
	// todo
	return true, nil
}

func (m *blueGreenReleaseManager) doCanaryPaused(c *RolloutContext) (bool, error) {
	blueGreenStatus := c.NewStatus.BlueGreenStatus
	currentStep := c.Rollout.Spec.Strategy.BlueGreen.Steps[blueGreenStatus.CurrentStepIndex-1]
	steps := len(c.Rollout.Spec.Strategy.BlueGreen.Steps)
	cond := util.GetRolloutCondition(*c.NewStatus, v1beta1.RolloutConditionProgressing)
	// need manual confirmation
	if currentStep.Pause.Duration == nil {
		klog.Infof("rollout(%s/%s) don't set pause duration, and need manual confirmation", c.Rollout.Namespace, c.Rollout.Name)
		cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and you need manually confirm to enter the next step", blueGreenStatus.CurrentStepIndex, steps)
		c.NewStatus.Message = cond.Message
		return false, nil
	}
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and wait duration(%d seconds) to enter the next step", blueGreenStatus.CurrentStepIndex, steps, *currentStep.Pause.Duration)
	c.NewStatus.Message = cond.Message
	// wait duration time, then go to next step
	duration := time.Second * time.Duration(*currentStep.Pause.Duration)
	expectedTime := blueGreenStatus.LastUpdateTime.Add(duration)
	if expectedTime.Before(time.Now()) {
		klog.Infof("rollout(%s/%s) canary step(%d) paused duration(%d seconds), and go to the next step",
			c.Rollout.Namespace, c.Rollout.Name, blueGreenStatus.CurrentStepIndex, *currentStep.Pause.Duration)
		return true, nil
	}
	c.RecheckTime = &expectedTime
	return false, nil
}

func (m *blueGreenReleaseManager) doCanaryJump(c *RolloutContext) (jumped bool) {
	if c.Workload == nil {
		return false
	}
	return doStepJump(c.Rollout, c.NewStatus, c.Rollout.Spec.Strategy.BlueGreen.Steps, int(c.Workload.Replicas))
}

// cleanup after rollout is completed or finished
func (m *blueGreenReleaseManager) doCanaryFinalising(c *RolloutContext) (bool, error) {
	blueGreenStatus := c.NewStatus.BlueGreenStatus
	if blueGreenStatus == nil {
		return true, nil
	}
	// rollout progressing complete, remove rollout progressing annotation in workload
	err := removeRolloutProgressingAnnotation(m.Client, c)
	if err != nil {
		return false, err
	}

	tr := newTrafficRoutingContext(c)
	// execute steps based on the predefined order for each reason
	nextStep := nextBlueGreenTask(c.FinalizeReason, blueGreenStatus.FinalisingStep)
	// if current step is empty, set it with the first step
	// if current step is end, we just return
	if len(blueGreenStatus.FinalisingStep) == 0 {
		blueGreenStatus.FinalisingStep = nextStep
		blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	} else if blueGreenStatus.FinalisingStep == v1beta1.FinalisingStepTypeEnd {
		klog.Infof("rollout(%s/%s) finalising process is already completed", c.Rollout.Namespace, c.Rollout.Name)
		return true, nil
	}
	klog.Infof("rollout(%s/%s) Finalising Step is %s", c.Rollout.Namespace, c.Rollout.Name, blueGreenStatus.FinalisingStep)

	var retry bool
	// the order of steps is maitained by calculating thenextStep
	switch blueGreenStatus.FinalisingStep {
	// set workload.pause=false; set workload.partition=0
	case v1beta1.FinalisingStepResumeWorkload:
		retry, err = finalizingBatchRelease(m.Client, c)
	// delete batchRelease
	case v1beta1.FinalisingStepReleaseWorkloadControl:
		retry, err = removeBatchRelease(m.Client, c)
	// restore the gateway resources (ingress/gatewayAPI/Istio), that means
	// only stable Service will accept the traffic
	case v1beta1.FinalisingStepRouteTrafficToStable:
		retry, err = m.trafficRoutingManager.RestoreGateway(tr)
	// restore the stable service
	case v1beta1.FinalisingStepRestoreStableService:
		retry, err = m.trafficRoutingManager.RestoreStableService(tr)
	// remove canary service
	case v1beta1.FinalisingStepRemoveCanaryService:
		retry, err = m.trafficRoutingManager.RemoveCanaryService(tr)
	// route all traffic to new version
	case v1beta1.FinalisingStepRouteTrafficToNew:
		retry, err = m.trafficRoutingManager.RouteAllTrafficToNewVersion(tr)
	// dangerous, wait endlessly, only for debugging use
	case v1beta1.FinalisingStepWaitEndless:
		retry, err = true, fmt.Errorf("only for debugging, just wait endlessly")
	default:
		nextStep = nextBlueGreenTask(c.FinalizeReason, "")
		klog.Warningf("unexpected finalising step, current step(%s),  start from the first step(%s)", blueGreenStatus.FinalisingStep, nextStep)
		blueGreenStatus.FinalisingStep = nextStep
		return false, nil
	}
	if err != nil || retry {
		return false, err
	}
	// current step is done, run the next step
	blueGreenStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	blueGreenStatus.FinalisingStep = nextStep
	if blueGreenStatus.FinalisingStep == v1beta1.FinalisingStepTypeEnd {
		return true, nil
	}

	return false, nil
}

func (m *blueGreenReleaseManager) fetchClient() client.Client {
	return m.Client
}

func (m *blueGreenReleaseManager) createBatchRelease(rollout *v1beta1.Rollout, rolloutID string, batch int32, isRollback bool) *v1beta1.BatchRelease {
	var batches []v1beta1.ReleaseBatch
	for _, step := range rollout.Spec.Strategy.BlueGreen.Steps {
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
				Batches:          batches,
				RolloutID:        rolloutID,
				BatchPartition:   utilpointer.Int32(batch),
				FailureThreshold: rollout.Spec.Strategy.BlueGreen.FailureThreshold,
				RollingStyle:     v1beta1.BlueGreenRollingStyle,
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

// syncBatchRelease sync status of br to blueGreenStatus, and sync rollout-id of blueGreenStatus to br.
func (m *blueGreenReleaseManager) syncBatchRelease(br *v1beta1.BatchRelease, blueGreenStatus *v1beta1.BlueGreenStatus) error {
	// sync from BatchRelease status to Rollout blueGreenStatus
	blueGreenStatus.UpdatedReplicas = br.Status.CanaryStatus.UpdatedReplicas
	blueGreenStatus.UpdatedReadyReplicas = br.Status.CanaryStatus.UpdatedReadyReplicas
	// Do not remove this line currently, otherwise, users will be not able to judge whether the BatchRelease works
	// in the scene where only rollout-id changed.
	// TODO: optimize the logic to better understand
	blueGreenStatus.Message = fmt.Sprintf("BatchRelease is at state %s, rollout-id %s, step %d",
		br.Status.CanaryStatus.CurrentBatchState, br.Status.ObservedRolloutID, br.Status.CanaryStatus.CurrentBatch+1)
	// br.Status.Message records messages that help users to understand what is going wrong
	if len(br.Status.Message) > 0 {
		blueGreenStatus.Message += fmt.Sprintf(", %s", br.Status.Message)
	}
	// sync rolloutId from blueGreenStatus to BatchRelease
	if blueGreenStatus.ObservedRolloutID != br.Spec.ReleasePlan.RolloutID {
		body := fmt.Sprintf(`{"spec":{"releasePlan":{"rolloutID":"%s"}}}`, blueGreenStatus.ObservedRolloutID)
		return m.Patch(context.TODO(), br, client.RawPatch(types.MergePatchType, []byte(body)))
	}
	return nil
}

/*
- Rollback Scenario:
why the first step is to restore the gateway? (aka. route all traffic to stable version)
we cannot remove selector of the stable service firstly as canary does, because users are allowed to configure "0%" traffic
in bluegreen strategy. Consider the following example:
  - replicas: 50% // step 1
    traffic: 0%

if user is at step 1, and then attempts to rollback directly, Rollout should route all traffic to stable service
(keep unchanged actually). However, if we remove the selector of the stable service instead, we would inadvertently
route some traffic to the new version for a period, which is undesirable.

- Rollout Deletion and Disabling Scenario:
If Rollout is being deleted or disabled, it suggests users want to release the new version using workload built-in strategy,
such as rollingUpdate for Deployment, instead of blue-green or canary. And thus, we can simply remove
the label selector of the stable service, routing traffic to reach both stable and updated pods.

- Rollout success Scenario:
This indicates the rollout has completed its final batch and the user has confirmed to
transition fully to the new version. We can simply route all traffic to new version. Additionally, given that all
traffic is routed to the canary Service, it is safe to remove selector of stable Service, which additionally works
as a workaround for a bug caused by ingress-nginx controller (see https://github.com/kubernetes/ingress-nginx/issues/9635)
*/
func nextBlueGreenTask(reason string, currentTask v1beta1.FinalisingStepType) v1beta1.FinalisingStepType {
	var taskSequence []v1beta1.FinalisingStepType
	switch reason {
	case v1beta1.FinaliseReasonSuccess: // success
		taskSequence = []v1beta1.FinalisingStepType{
			v1beta1.FinalisingStepRouteTrafficToNew,
			v1beta1.FinalisingStepRestoreStableService,
			v1beta1.FinalisingStepResumeWorkload,
			v1beta1.FinalisingStepRouteTrafficToStable,

			v1beta1.FinalisingStepRemoveCanaryService,
			v1beta1.FinalisingStepReleaseWorkloadControl,
		}

	case v1beta1.FinaliseReasonRollback: // rollback
		taskSequence = []v1beta1.FinalisingStepType{
			v1beta1.FinalisingStepRouteTrafficToStable, // route all traffic to stable version
			v1beta1.FinalisingStepResumeWorkload,
			v1beta1.FinalisingStepRestoreStableService,

			v1beta1.FinalisingStepRemoveCanaryService,
			v1beta1.FinalisingStepReleaseWorkloadControl,
		}
	default: // others: disabled/deleting rollout
		taskSequence = []v1beta1.FinalisingStepType{
			v1beta1.FinalisingStepRestoreStableService,
			v1beta1.FinalisingStepRouteTrafficToStable,
			v1beta1.FinalisingStepRemoveCanaryService,
			v1beta1.FinalisingStepResumeWorkload, // scale up new, scale down old
			v1beta1.FinalisingStepReleaseWorkloadControl,
		}
	}
	// if currentTask is empty, return first task
	if len(currentTask) == 0 {
		return taskSequence[0]
	}
	// find next task
	for i := range taskSequence {
		if currentTask == taskSequence[i] && i < len(taskSequence)-1 {
			return taskSequence[i+1]
		}
	}
	return v1beta1.FinalisingStepTypeEnd
}
