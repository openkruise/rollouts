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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
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
	if br, err := fetchBatchRelease(m.Client, c.Rollout.Namespace, c.Rollout.Name); err != nil && !errors.IsNotFound(err) {
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
		return nil
	}
	// When the first batch is trafficRouting rolling and the next steps are rolling release,
	// We need to clean up the canary-related resources first and then rollout the rest of the batch.
	currentStep := c.Rollout.Spec.Strategy.Canary.Steps[canaryStatus.CurrentStepIndex-1]
	if currentStep.Traffic == nil && len(currentStep.Matches) == 0 {
		tr := newTrafficRoutingContext(c)
		done, err := m.trafficRoutingManager.FinalisingTrafficRouting(tr)
		c.NewStatus.CanaryStatus.LastUpdateTime = tr.LastUpdateTime
		if err != nil {
			return err
		} else if !done {
			klog.Infof("rollout(%s/%s) cleaning up canary-related resources", c.Rollout.Namespace, c.Rollout.Name)
			expectedTime := time.Now().Add(tr.RecheckDuration)
			c.RecheckTime = &expectedTime
			return nil
		}
	}
	switch canaryStatus.CurrentStepState {
	// before CanaryStepStateUpgrade, handle some special cases, to prevent traffic loss
	case v1beta1.CanaryStepStateInit:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateInit)
		tr := newTrafficRoutingContext(c)
		if currentStep.Traffic == nil && len(currentStep.Matches) == 0 {
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
			klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
				canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStateInit, canaryStatus.CurrentStepState)
			return nil
		}

		/*
			The following check serves to bypass the bug in ingress-nginx controller https://github.com/kubernetes/ingress-nginx/issues/9635
			For partition-style: if the expected replicas of the current rollout step is not less than workload.spec.replicas,
			it indicates that this step will release all stable pods to new version, ie. there will be no stable pods, which will
			trigger the bug.
			To avoid this issue, we restore stable Service before scaling the stable pods down to zero.
			This ensures that the backends behind the stable ingress remain active, preventing the bug from being triggered.
		*/
		expectedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(currentStep.Replicas, int(c.Workload.Replicas), true)
		if expectedReplicas >= int(c.Workload.Replicas) && v1beta1.IsRealPartition(c.Rollout) {
			klog.Infof("Bypass the ingress-nginx bug for partition-style, rollout(%s/%s) restore stable Service", c.Rollout.Namespace, c.Rollout.Name)
			retry, err := m.trafficRoutingManager.RestoreStableService(tr)
			if err != nil {
				return err
			} else if retry {
				expectedTime := time.Now().Add(tr.RecheckDuration)
				c.RecheckTime = &expectedTime
				return nil
			}
		}

		/*
			The following check is used to solve scenario like this:
			steps:
			- replicas: 1 # first batch
				matches:
				- headers:
				- name: user-agent
					type: Exact
					value: pc
			in the first batch, pods with new version will be created in step CanaryStepStateUpgrade, once ready,
			they will serve as active backends behind the stable service, because the stable service hasn't been
			modified by rollout (ie. it selects pods of all versions).
			Thus, requests with or without the header (user-agent: pc) will be routed to pods of all versions evenly, before
			we arrive the CanaryStepStateTrafficRouting step.
			To avoid this issue, we
			- patch selector to stable Service before CanaryStepStateUpgrade step.
		*/
		if canaryStatus.CurrentStepIndex == 1 {
			if !tr.DisableGenerateCanaryService {
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

		canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		canaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
		klog.Infof("rollout(%s/%s) step(%d) state from(%s) -> to(%s)", c.Rollout.Namespace, c.Rollout.Name,
			canaryStatus.CurrentStepIndex, v1beta1.CanaryStepStateInit, canaryStatus.CurrentStepState)
		fallthrough

	case v1beta1.CanaryStepStateUpgrade:
		klog.Infof("rollout(%s/%s) run canary strategy, and state(%s)", c.Rollout.Namespace, c.Rollout.Name, v1beta1.CanaryStepStateUpgrade)
		done, err := m.doCanaryUpgrade(c)
		if err != nil {
			return err
		} else if done {
			canaryStatus.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
			// To correspond with the above explanation wrt. the mentioned bug https://github.com/kubernetes/ingress-nginx/issues/9635
			// we likewise do this check again to skip the CanaryStepStateTrafficRouting step, since
			// it has been done in the CanaryStepInit step
			expectedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(currentStep.Replicas, int(c.Workload.Replicas), true)
			if expectedReplicas >= int(c.Workload.Replicas) && v1beta1.IsRealPartition(c.Rollout) {
				canaryStatus.CurrentStepState = v1beta1.CanaryStepStateMetricsAnalysis
			}
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
	done, br, err := runBatchRelease(m, c.Rollout, getRolloutID(c.Workload), canaryStatus.CurrentStepIndex, c.Workload.IsInRollback)
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
	// If it is the last step, and 100% of pods, then return true
	if int32(steps) == canaryStatus.CurrentStepIndex {
		if currentStep.Replicas != nil && currentStep.Replicas.StrVal == "100%" {
			return true, nil
		}
	}
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
	if c.Workload == nil {
		return false
	}
	return doStepJump(c.Rollout, c.NewStatus, c.Rollout.Spec.Strategy.Canary.Steps, int(c.Workload.Replicas))
}

// cleanup after rollout is completed or finished
func (m *canaryReleaseManager) doCanaryFinalising(c *RolloutContext) (bool, error) {
	canaryStatus := c.NewStatus.CanaryStatus
	// when CanaryStatus is nil, which means canary action hasn't started yet, don't need doing cleanup
	if canaryStatus == nil {
		return true, nil
	}
	// rollout progressing complete, remove rollout progressing annotation in workload
	err := removeRolloutProgressingAnnotation(m.Client, c)
	if err != nil {
		return false, err
	}
	tr := newTrafficRoutingContext(c)
	// execute steps based on the predefined order for each reason
	nextStep := nextCanaryTask(c.FinalizeReason, canaryStatus.FinalisingStep)
	// if current step is empty, set it with the first step
	// if current step is end, we just return
	if len(canaryStatus.FinalisingStep) == 0 {
		canaryStatus.FinalisingStep = nextStep
		canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	} else if canaryStatus.FinalisingStep == v1beta1.FinalisingStepTypeEnd {
		klog.Infof("rollout(%s/%s) finalising process is already completed", c.Rollout.Namespace, c.Rollout.Name)
		return true, nil
	}
	klog.Infof("rollout(%s/%s) Finalising Step is %s", c.Rollout.Namespace, c.Rollout.Name, canaryStatus.FinalisingStep)

	var retry bool
	// the order of steps is maitained by calculating the nextStep
	switch canaryStatus.FinalisingStep {
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

	default:
		nextStep = nextCanaryTask(c.FinalizeReason, "")
		klog.Warningf("unexpected finalising step, current step(%s),  start from the first step(%s)", canaryStatus.FinalisingStep, nextStep)
		canaryStatus.FinalisingStep = nextStep
		return false, nil
	}
	if err != nil || retry {
		return false, err
	}
	// current step is done, run the next step
	canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
	canaryStatus.FinalisingStep = nextStep
	if canaryStatus.FinalisingStep == v1beta1.FinalisingStepTypeEnd {
		return true, nil
	}
	return false, nil
}

func (m *canaryReleaseManager) fetchClient() client.Client {
	return m.Client
}

func (m *canaryReleaseManager) createBatchRelease(rollout *v1beta1.Rollout, rolloutID string, batch int32, isRollback bool) *v1beta1.BatchRelease {
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
				BatchPartition:               utilpointer.Int32(batch),
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
	// br.Status.Message records messages that help users to understand what is going wrong
	if len(br.Status.Message) > 0 {
		canaryStatus.Message += fmt.Sprintf(", %s", br.Status.Message)
	}

	// sync rolloutId from canaryStatus to BatchRelease
	if canaryStatus.ObservedRolloutID != br.Spec.ReleasePlan.RolloutID {
		body := fmt.Sprintf(`{"spec":{"releasePlan":{"rolloutID":"%s"}}}`, canaryStatus.ObservedRolloutID)
		return m.Patch(context.TODO(), br, client.RawPatch(types.MergePatchType, []byte(body)))
	}
	return nil
}

// calculate next task
func nextCanaryTask(reason string, currentTask v1beta1.FinalisingStepType) v1beta1.FinalisingStepType {
	var taskSequence []v1beta1.FinalisingStepType
	//REVIEW - should we consider more complex scenarios?
	// like, user rollbacks the workload and disables the Rollout at the same time?
	switch reason {
	// in rollback, we cannot remove selector of the stable service in the first step,
	// which may cause some traffic to problematic new version, so we should route all traffic to stable service
	// in the first step
	case v1beta1.FinaliseReasonRollback: // rollback
		taskSequence = []v1beta1.FinalisingStepType{
			v1beta1.FinalisingStepRouteTrafficToStable, // route all traffic to stable version
			v1beta1.FinalisingStepResumeWorkload,       // scale up old, scale down new
			v1beta1.FinalisingStepReleaseWorkloadControl,
			v1beta1.FinalisingStepRestoreStableService,
			v1beta1.FinalisingStepRemoveCanaryService,
		}
	default: // others: success/disabled/deleting rollout
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
