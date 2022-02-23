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

package workloads

import (
	"context"
	"fmt"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CloneSetRolloutController is responsible for handling rollout CloneSet type of workloads
type CloneSetRolloutController struct {
	cloneSetController
	clone *kruiseappsv1alpha1.CloneSet
}

//TODO: scale during releasing: workload replicas changed -> Finalising CloneSet with Paused=true

// NewCloneSetRolloutController creates a new CloneSet rollout controller
func NewCloneSetRolloutController(cli client.Client, recorder record.EventRecorder, release *v1alpha1.BatchRelease, plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus, targetNamespacedName types.NamespacedName) *CloneSetRolloutController {
	return &CloneSetRolloutController{
		cloneSetController: cloneSetController{
			workloadController: workloadController{
				client:           cli,
				recorder:         recorder,
				parentController: release,
				releasePlan:      plan,
				releaseStatus:    status,
			},
			releasePlanKey:       client.ObjectKeyFromObject(release),
			targetNamespacedName: targetNamespacedName,
		},
	}
}

// IfNeedToProgress verifies that the workload is ready to execute release plan
func (c *CloneSetRolloutController) IfNeedToProgress() (bool, error) {
	var verifyErr error
	defer func() {
		if verifyErr != nil {
			klog.Warningf(verifyErr.Error())
			c.recorder.Event(c.parentController, v1.EventTypeWarning, "VerifyFailed", verifyErr.Error())
		}
	}()

	if err := c.fetchCloneSet(); err != nil {
		//c.releaseStatus.RolloutRetry(err.Error())
		return false, nil
	}

	// if the workload status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		klog.Warningf("CloneSet(%v) is still reconciling, wait for it to be done", c.targetNamespacedName)
		return false, nil
	}

	// if the cloneSet has been promoted, no need to go on
	if c.clone.Status.UpdatedReplicas == *c.clone.Spec.Replicas {
		verifyErr = fmt.Errorf("CloneSet(%v) update revision has been promoted, no need to reconcile", c.targetNamespacedName)
		return false, verifyErr
	}

	// if the cloneSet is not paused and is not under our control
	if !c.clone.Spec.UpdateStrategy.Paused && !util.IsControlledBy(c.clone, c.parentController) {
		verifyErr = fmt.Errorf("cloneSet(%v) should be paused before execute the release plan", c.targetNamespacedName)
		return false, verifyErr
	}

	klog.V(3).Infof("Verified CloneSet Successfully, Status %+v", c.targetNamespacedName, c.releaseStatus)
	c.recorder.Event(c.parentController, v1.EventTypeNormal, "VerifiedSuccessfully", "ReleasePlan and the CloneSet resource are verified")
	return true, nil
}

// PrepareBeforeProgress makes sure that the source and target CloneSet is under our control
func (c *CloneSetRolloutController) PrepareBeforeProgress() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		//c.releaseStatus.RolloutRetry(err.Error())
		return false, nil
	}

	// claim the cloneSet is under our control
	if _, err := c.claimCloneSet(c.clone); err != nil {
		return false, nil
	}

	// record revisions and replicas info to BatchRelease.Status
	c.recordCloneSetRevisionAndReplicas()

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, nil
}

// ProgressOneBatchReplicas calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *CloneSetRolloutController) ProgressOneBatchReplicas() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, nil
	}

	// if the workload status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		return false, nil
	}

	// the number of canary pods should have in current batch
	canaryGoal := c.calculateCurrentCanary(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of stable pods should have in current batch
	stableGoal := c.calculateCurrentStable(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of canary pods now we have in current state
	currentCanaryReplicas := c.clone.Status.UpdatedReplicas
	// workload partition calculated
	workloadPartition, _ := intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.Partition,
		int(c.releaseStatus.ObservedWorkloadReplicas), true)

	// in case of no need to upgrade pods
	if currentCanaryReplicas >= canaryGoal && int32(workloadPartition) <= stableGoal {
		klog.V(3).InfoS("upgraded one batch, but no need to update partition of cloneset",
			"BatchRelease", c.releasePlanKey, "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch,
			"canary-goal", canaryGoal, "stable-goal", stableGoal, "canary-replicas", currentCanaryReplicas, "partition", workloadPartition)
		return true, nil
	}

	// upgrade pods
	if err := c.patchCloneSetPartition(c.clone, stableGoal); err != nil {
		return false, nil
	}

	klog.V(3).InfoS("upgraded one batch", "BatchRelease", c.releasePlanKey,
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "updateRevision size", canaryGoal)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "SetBatchDone",
		"Finished submitting all upgrade quests for batch %d", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchReplicas checks to see if the pods are all available according to the rollout plan
func (c *CloneSetRolloutController) CheckOneBatchReplicas() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, nil
	}

	// if the workload status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		return false, nil
	}

	// the number of canary pods now we have in current state
	canaryReplicas := c.clone.Status.UpdatedReplicas
	// the number of stable pods now we have in current state
	stableReplicas := c.clone.Status.Replicas - canaryReplicas
	// the number of canary pods that have been ready in current state
	canaryReadyReplicas := c.clone.Status.UpdatedReplicas
	// the number of canary pods should have in current batch
	canaryGoal := c.calculateCurrentCanary(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of stable pods should have in current batch
	stableGoal := c.calculateCurrentStable(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of max unavailable canary pods allowed by this workload
	maxUnavailable := 0
	if c.clone.Spec.UpdateStrategy.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.MaxUnavailable, int(c.releaseStatus.ObservedWorkloadReplicas), true)
	}

	klog.InfoS("checking the batch releasing progress", "BatchRelease", c.releasePlanKey,
		"current-batch", c.releaseStatus.CanaryStatus.CurrentBatch, "canary-ready-replicas", canaryReadyReplicas,
		"stable-replicas", stableReplicas, "max-unavailable", maxUnavailable, "canary-goal", canaryGoal, "stable-goal", stableGoal)

	if c.clone.Status.Replicas != c.releaseStatus.ObservedWorkloadReplicas {
		err := fmt.Errorf("CloneSet(%v) replicas don't match ObservedWorkloadReplicas, workload status replicas: %v, observed workload replicas: %v",
			c.targetNamespacedName, c.clone.Status.Replicas, c.releaseStatus.ObservedWorkloadReplicas)
		klog.ErrorS(err, "the batch is not valid", "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	if canaryGoal > canaryReplicas || stableGoal < stableReplicas || canaryReadyReplicas+int32(maxUnavailable) < canaryGoal || (canaryGoal > 0 && canaryReadyReplicas == 0) {
		klog.InfoS("the batch is not ready yet", "CloneSet", c.targetNamespacedName,
			"ReleasePlan", c.releasePlanKey, "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	klog.Infof("All pods of CloneSet(%v) in current batch are ready, BatchRelease(%v), current-batch=%v",
		c.targetNamespacedName, c.releasePlanKey, c.releaseStatus.CanaryStatus.CurrentBatch)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "BatchAvailable", "Batch %d is available", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// FinalizeOneBatch isn't needed in this mode.
func (c *CloneSetRolloutController) FinalizeOneBatch() (bool, error) {
	return true, nil
}

// FinalizeProgress makes sure the CloneSet is all upgraded
func (c *CloneSetRolloutController) FinalizeProgress(pause *bool, cleanup bool) bool {
	if err := c.fetchCloneSet(); client.IgnoreNotFound(err) != nil {
		return false
	}

	//
	if _, err := c.releaseCloneSet(c.clone, pause, cleanup); err != nil {
		return false
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized, "+
		"pause=%v, cleanup=%v", pause, cleanup)
	return true
}

// SyncWorkloadInfo return change type if workload was changed during release
func (c *CloneSetRolloutController) SyncWorkloadInfo() (WorkloadChangeEventType, *WorkloadInfo, error) {
	if c.parentController.Spec.Cancelled ||
		c.parentController.DeletionTimestamp != nil ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseAbort ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseFinalizing ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseTerminating {
		return IgnoreWorkloadEvent, nil, nil
	}

	workloadInfo := &WorkloadInfo{}
	err := c.fetchCloneSet()
	if client.IgnoreNotFound(err) != nil {
		return "", nil, err
	} else if apierrors.IsNotFound(err) {
		workloadInfo.Status = &WorkloadStatus{}
		return "", workloadInfo, err
	}

	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		klog.Warningf("CloneSet(%v) is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			c.targetNamespacedName, c.clone.Generation, c.clone.Status.ObservedGeneration)
		return WorkloadStillReconciling, nil, nil
	}

	workloadInfo.Status = &WorkloadStatus{
		UpdatedReplicas:      c.clone.Status.UpdatedReplicas,
		UpdatedReadyReplicas: c.clone.Status.UpdatedReadyReplicas,
	}

	if !c.clone.Spec.UpdateStrategy.Paused && c.clone.Status.UpdatedReplicas == c.clone.Status.Replicas {
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	switch c.releaseStatus.Phase {
	default:
		if c.clone.Status.CurrentRevision == c.clone.Status.UpdateRevision &&
			c.parentController.Status.UpdateRevision != c.clone.Status.UpdateRevision {
			workloadInfo.UpdateRevision = &c.clone.Status.UpdateRevision
			klog.Warningf("CloneSet(%v) is stable or is rolling back", c.targetNamespacedName)
			return WorkloadRollback, workloadInfo, nil
		}
		if *c.clone.Spec.Replicas != c.releaseStatus.ObservedWorkloadReplicas {
			workloadInfo.Replicas = c.clone.Spec.Replicas
			klog.Warningf("CloneSet(%v) replicas changed during releasing, should pause and wait for it to complete, replicas from: %v -> %v",
				c.targetNamespacedName, c.releaseStatus.ObservedWorkloadReplicas, *c.clone.Spec.Replicas)
			return WorkloadReplicasChanged, workloadInfo, nil
		}
		fallthrough

	case v1alpha1.RolloutPhaseCompleted, v1alpha1.RolloutPhaseCancelled:
		if c.clone.Status.UpdateRevision != c.releaseStatus.UpdateRevision {
			workloadInfo.UpdateRevision = &c.clone.Status.UpdateRevision
			klog.Warningf("CloneSet(%v) updateRevision changed during releasing, should try to restart the release plan, updateRevision from: %v -> %v",
				c.targetNamespacedName, c.releaseStatus.UpdateRevision, c.clone.Status.UpdateRevision)
			return WorkloadPodTemplateChanged, workloadInfo, nil
		}

	case v1alpha1.RolloutPhaseHealthy, v1alpha1.RolloutPhaseInitial, v1alpha1.RolloutPhasePreparing:
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

/* ----------------------------------
The functions below are helper functions
------------------------------------- */

// fetch cloneSet to c.clone
func (c *CloneSetRolloutController) fetchCloneSet() error {
	clone := &kruiseappsv1alpha1.CloneSet{}
	if err := c.client.Get(context.TODO(), c.targetNamespacedName, clone); err != nil {
		if !apierrors.IsNotFound(err) {
			c.recorder.Event(c.parentController, v1.EventTypeWarning, "GetCloneSetFailed", err.Error())
		}
		return err
	}
	c.clone = clone
	return nil
}

// the canary workload size for the current batch
func (c *CloneSetRolloutController) calculateCurrentCanary(totalSize int32) int32 {
	targetSize := int32(util.CalculateNewBatchTarget(c.releasePlan, int(totalSize), int(c.releaseStatus.CanaryStatus.CurrentBatch)))
	klog.InfoS("Calculated the number of pods in the target CloneSet after current batch",
		"CloneSet", c.targetNamespacedName, "BatchRelease", c.releasePlanKey,
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload updateRevision size", targetSize)
	return targetSize
}

// the source workload size for the current batch
func (c *CloneSetRolloutController) calculateCurrentStable(totalSize int32) int32 {
	sourceSize := totalSize - c.calculateCurrentCanary(totalSize)
	klog.InfoS("Calculated the number of pods in the source CloneSet after current batch",
		"CloneSet", c.targetNamespacedName, "BatchRelease", c.releasePlanKey,
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload stableRevision size", sourceSize)
	return sourceSize
}

func (c *CloneSetRolloutController) recordCloneSetRevisionAndReplicas() {
	c.releaseStatus.ObservedWorkloadReplicas = *c.clone.Spec.Replicas
	c.releaseStatus.StableRevision = c.clone.Status.CurrentRevision
	c.releaseStatus.UpdateRevision = c.clone.Status.UpdateRevision
}
