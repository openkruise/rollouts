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

// VerifyWorkload verifies that the workload is ready to execute release plan
func (c *CloneSetRolloutController) VerifyWorkload() (bool, error) {
	var err error
	var message string
	defer func() {
		if err != nil {
			c.recorder.Event(c.parentController, v1.EventTypeWarning, "VerifyFailed", err.Error())
		} else if message != "" {
			klog.Warningf(message)
		}
	}()

	if err = c.fetchCloneSet(); err != nil {
		message = err.Error()
		return false, err
	}

	// if the workload status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		message = fmt.Sprintf("CloneSet(%v) is still reconciling, wait for it to be done", c.targetNamespacedName)
		return false, nil
	}

	// if the cloneSet has been promoted, no need to go on
	if c.clone.Status.UpdatedReplicas == *c.clone.Spec.Replicas {
		message = fmt.Sprintf("CloneSet(%v) update revision has been promoted, no need to reconcile", c.targetNamespacedName)
		return false, nil
	}

	// if the cloneSet is not paused and is not under our control
	if !c.clone.Spec.UpdateStrategy.Paused {
		message = fmt.Sprintf("CloneSet(%v) should be paused before execute the release plan", c.targetNamespacedName)
		return false, nil
	}

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "VerifiedSuccessfully", "ReleasePlan and the CloneSet resource are verified")
	return true, nil
}

// PrepareBeforeProgress makes sure that the source and target CloneSet is under our control
func (c *CloneSetRolloutController) PrepareBeforeProgress() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, err
	}

	// claim the cloneSet is under our control
	if _, err := c.claimCloneSet(c.clone); err != nil {
		return false, err
	}

	// record revisions and replicas info to BatchRelease.Status
	c.recordCloneSetRevisionAndReplicas()

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, nil
}

// UpgradeOneBatch calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *CloneSetRolloutController) UpgradeOneBatch() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, err
	}

	if c.releaseStatus.ObservedWorkloadReplicas == 0 {
		klog.Infof("BatchRelease(%v) observed workload replicas is 0, no need to upgrade", c.releasePlanKey)
		return true, nil
	}

	// if the workload status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		return false, nil
	}

	currentBatch := c.parentController.Status.CanaryStatus.CurrentBatch
	// the number of canary pods should have in current batch
	canaryGoal := c.calculateCurrentCanary(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of stable pods should have in current batch
	stableGoal := c.calculateCurrentStable(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of canary pods now we have in current state
	currentCanaryReplicas := c.clone.Status.UpdatedReplicas
	// workload partition calculated
	workloadPartition, _ := intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.Partition,
		int(c.releaseStatus.ObservedWorkloadReplicas), true)

	// if canaryReplicas is int, then we use int;
	// if canaryReplicas is percentage, then we use percentage.
	var partitionGoal intstr.IntOrString
	canaryIntOrStr := c.releasePlan.Batches[c.releaseStatus.CanaryStatus.CurrentBatch].CanaryReplicas
	if canaryIntOrStr.Type == intstr.Int {
		partitionGoal = intstr.FromInt(int(stableGoal))
	} else if c.releaseStatus.ObservedWorkloadReplicas > 0 {
		partitionGoal = ParseIntegerAsPercentageIfPossible(stableGoal, c.releaseStatus.ObservedWorkloadReplicas, &canaryIntOrStr)
	}

	klog.V(3).InfoS("upgraded one batch, current info:",
		"BatchRelease", c.releasePlanKey,
		"current-batch", currentBatch,
		"canary-goal", canaryGoal,
		"stable-goal", stableGoal,
		"partition-goal", partitionGoal,
		"partition-current", workloadPartition,
		"canary-replicas", currentCanaryReplicas)

	// in case of no need to upgrade pods
	IsUpgradedDone := func() bool {
		return currentCanaryReplicas >= canaryGoal && int32(workloadPartition) <= stableGoal
	}

	if !IsUpgradedDone() {
		if err := c.patchCloneSetPartition(c.clone, &partitionGoal); err != nil {
			return false, err
		}
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "SetBatchDone",
		"Finished submitting all upgrade quests for batch %d", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchReady checks to see if the pods are all available according to the rollout plan
func (c *CloneSetRolloutController) CheckOneBatchReady() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, err
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
	canaryReadyReplicas := c.clone.Status.UpdatedReadyReplicas
	// the number of expected stable pods should have in current batch, but this number may
	// be inconsistent with the real canary goal due to the accuracy of percent-type partition
	expectedStableGoal := c.calculateCurrentStable(c.releaseStatus.ObservedWorkloadReplicas)
	// the number of the real canary pods should have in current batch
	originalGoal := &c.releasePlan.Batches[c.releaseStatus.CanaryStatus.CurrentBatch].CanaryReplicas
	canaryGoal := CalculateRealCanaryReplicasGoal(expectedStableGoal, c.releaseStatus.ObservedWorkloadReplicas, originalGoal)
	// the number of the real stable pods should have in current batch
	stableGoal := c.releaseStatus.ObservedWorkloadReplicas - canaryGoal
	// the number of max unavailable canary pods allowed by this workload
	maxUnavailable := 0
	if c.clone.Spec.UpdateStrategy.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.MaxUnavailable, int(canaryGoal), true)
	}

	klog.InfoS("checking the batch releasing progress",
		"BatchRelease", c.releasePlanKey,
		"current-batch", c.releaseStatus.CanaryStatus.CurrentBatch,
		"canary-goal", canaryGoal,
		"stable-goal", stableGoal,
		"stable-replicas", stableReplicas,
		"max-unavailable", maxUnavailable,
		"canary-ready-replicas", canaryReadyReplicas)

	// maybe, the workload replicas was scaled, we should requeue and handle the workload scaling event
	if c.clone.Status.Replicas != c.releaseStatus.ObservedWorkloadReplicas {
		err := fmt.Errorf("CloneSet(%v) replicas don't match ObservedWorkloadReplicas, workload status replicas: %v, observed workload replicas: %v",
			c.targetNamespacedName, c.clone.Status.Replicas, c.releaseStatus.ObservedWorkloadReplicas)
		klog.ErrorS(err, "the batch is not valid", "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	currentBatchIsNotReadyYet := func() bool {
		// the number of upgrade pods does not achieve the goal
		return canaryGoal > canaryReplicas ||
			// the number of upgraded available pods does not achieve the goal
			canaryReadyReplicas+int32(maxUnavailable) < canaryGoal ||
			// make sure that at least one upgrade pod is available
			(canaryGoal > 0 && canaryReadyReplicas == 0)
	}

	if currentBatchIsNotReadyYet() {
		klog.InfoS("the batch is not ready yet", "BatchRelease",
			c.releasePlanKey, "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	klog.Infof("All pods of CloneSet(%v) in current batch are ready, BatchRelease(%v), current-batch=%v",
		c.targetNamespacedName, c.releasePlanKey, c.releaseStatus.CanaryStatus.CurrentBatch)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "BatchAvailable", "Batch %d is available", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// FinalizeProgress makes sure the CloneSet is all upgraded
func (c *CloneSetRolloutController) FinalizeProgress(cleanup bool) (bool, error) {
	if err := c.fetchCloneSet(); client.IgnoreNotFound(err) != nil {
		return false, err
	}

	if _, err := c.releaseCloneSet(c.clone, cleanup); err != nil {
		return false, err
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized: cleanup=%v", cleanup)
	return true, nil
}

// SyncWorkloadInfo return change type if workload was changed during release
func (c *CloneSetRolloutController) SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if c.parentController.DeletionTimestamp != nil {
		return IgnoreWorkloadEvent, nil, nil
	}

	if err := c.fetchCloneSet(); err != nil {
		if apierrors.IsNotFound(err) {
			return WorkloadHasGone, nil, err
		}
		return "", nil, err
	}

	// in case that the cloneSet status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		klog.Warningf("CloneSet(%v) is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			c.targetNamespacedName, c.clone.Generation, c.clone.Status.ObservedGeneration)
		return WorkloadStillReconciling, nil, nil
	}

	workloadInfo := &util.WorkloadInfo{
		Status: &util.WorkloadStatus{
			UpdatedReplicas:      c.clone.Status.UpdatedReplicas,
			UpdatedReadyReplicas: c.clone.Status.UpdatedReadyReplicas,
			UpdateRevision:       c.clone.Status.UpdateRevision,
			StableRevision:       c.clone.Status.CurrentRevision,
		},
	}

	// in case of that the updated revision of the workload is promoted
	if c.clone.Status.UpdatedReplicas == c.clone.Status.Replicas {
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	// updateRevision == CurrentRevision means CloneSet is rolling back or newly-created.
	if c.clone.Status.UpdateRevision == c.clone.Status.CurrentRevision &&
		// stableRevision == UpdateRevision means CloneSet is not newly-created.
		c.releaseStatus.StableRevision == c.clone.Status.UpdateRevision &&
		c.releaseStatus.StableRevision != c.releaseStatus.UpdateRevision &&
		// Rollout is on disable quick rollback policy, and will roll back workload in batch
		util.IsRollbackInBatchPolicy(c.parentController.Spec.TargetRef.WorkloadRef, c.parentController.Annotations) {
		klog.Warningf("CloneSet(%v) is rolling back in batch", c.targetNamespacedName)
		return WorkloadRollbackInBatch, workloadInfo, nil
	}

	// in case of that the workload is scaling
	if *c.clone.Spec.Replicas != c.releaseStatus.ObservedWorkloadReplicas && c.releaseStatus.ObservedWorkloadReplicas != -1 {
		workloadInfo.Replicas = c.clone.Spec.Replicas
		klog.Warningf("CloneSet(%v) replicas changed during releasing, should pause and wait for it to complete, "+
			"replicas from: %v -> %v", c.targetNamespacedName, c.releaseStatus.ObservedWorkloadReplicas, *c.clone.Spec.Replicas)
		return WorkloadReplicasChanged, workloadInfo, nil
	}

	// in case of that the workload was changed
	if c.clone.Status.UpdateRevision != c.releaseStatus.UpdateRevision {
		klog.Warningf("CloneSet(%v) updateRevision changed during releasing, should try to restart the release plan, "+
			"updateRevision from: %v -> %v", c.targetNamespacedName, c.releaseStatus.UpdateRevision, c.clone.Status.UpdateRevision)
		return WorkloadPodTemplateChanged, workloadInfo, nil
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

/* ----------------------------------
The functions below are helper functions
------------------------------------- */
// fetchCloneSet fetch cloneSet to c.clone
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

func (c *CloneSetRolloutController) recordCloneSetRevisionAndReplicas() {
	c.releaseStatus.ObservedWorkloadReplicas = *c.clone.Spec.Replicas
	c.releaseStatus.StableRevision = c.clone.Status.CurrentRevision
	c.releaseStatus.UpdateRevision = c.clone.Status.UpdateRevision
}
