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
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CloneSetRolloutController is responsible for handling rollout CloneSet type of workloads
type CloneSetRolloutController struct {
	cloneSetController
	clone *kruiseappsv1alpha1.CloneSet
}

// NewCloneSetRolloutController creates a new CloneSet rollout controller
func NewCloneSetRolloutController(cli client.Client, recorder record.EventRecorder, release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, targetNamespacedName types.NamespacedName) *CloneSetRolloutController {
	return &CloneSetRolloutController{
		cloneSetController: cloneSetController{
			workloadController: workloadController{
				client:    cli,
				recorder:  recorder,
				release:   release,
				newStatus: newStatus,
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
			c.recorder.Event(c.release, v1.EventTypeWarning, "VerifyFailed", err.Error())
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
	if !(c.clone.Spec.UpdateStrategy.Paused || c.clone.Spec.UpdateStrategy.Partition.IntVal > *c.clone.Spec.Replicas || c.clone.Spec.UpdateStrategy.Partition.StrVal == "100%") {
		message = fmt.Sprintf("CloneSet(%v) should be paused before execute the release plan", c.targetNamespacedName)
		return false, nil
	}

	c.recorder.Event(c.release, v1.EventTypeNormal, "VerifiedSuccessfully", "ReleasePlan and the CloneSet resource are verified")
	return true, nil
}

// prepareBeforeRollback makes sure that the updated pods have been patched no-need-update label.
// return values:
// - bool: whether all updated pods have been patched no-need-update label;
// - *int32: how many pods have been patched;
// - err: whether error occurs.
func (c *CloneSetRolloutController) prepareBeforeRollback() (bool, *int32, error) {
	if c.release.Annotations[util.RollbackInBatchAnnotation] != "true" {
		return true, nil, nil
	}

	noNeedRollbackReplicas := int32(0)
	rolloutID := c.release.Spec.ReleasePlan.RolloutID
	if rolloutID == "" {
		return true, &noNeedRollbackReplicas, nil
	}

	pods, err := util.ListOwnedPods(c.client, c.clone)
	if err != nil {
		klog.Errorf("Failed to list pods for CloneSet %v", c.targetNamespacedName)
		return false, nil, err
	}

	updateRevision := c.clone.Status.UpdateRevision
	var filterPods []*v1.Pod
	for i := range pods {
		if !pods[i].DeletionTimestamp.IsZero() {
			continue
		}
		if !util.IsConsistentWithRevision(pods[i], updateRevision) {
			continue
		}
		if id, ok := pods[i].Labels[util.NoNeedUpdatePodLabel]; ok && id == rolloutID {
			noNeedRollbackReplicas++
			continue
		}
		filterPods = append(filterPods, pods[i])
	}

	if len(filterPods) == 0 {
		return true, &noNeedRollbackReplicas, nil
	}

	for _, pod := range filterPods {
		podClone := pod.DeepCopy()
		body := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, util.NoNeedUpdatePodLabel, rolloutID)
		err = c.client.Patch(context.TODO(), podClone, client.RawPatch(types.StrategicMergePatchType, []byte(body)))
		if err != nil {
			klog.Errorf("Failed to patch rollback labels[%s]=%s to pod %v", util.NoNeedUpdatePodLabel, rolloutID, client.ObjectKeyFromObject(pod))
			return false, &noNeedRollbackReplicas, err
		} else {
			klog.Info("Succeeded to patch rollback labels[%s]=%s to pod %v", util.NoNeedUpdatePodLabel, rolloutID, client.ObjectKeyFromObject(pod))
		}
		noNeedRollbackReplicas++
	}
	klog.Infof("BatchRelease(%v) find %v replicas no need to rollback", c.releasePlanKey, noNeedRollbackReplicas)
	return false, &noNeedRollbackReplicas, nil
}

// PrepareBeforeProgress makes sure that the source and target CloneSet is under our control
func (c *CloneSetRolloutController) PrepareBeforeProgress() (bool, *int32, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, nil, err
	}

	done, noNeedRollbackReplicas, err := c.prepareBeforeRollback()
	if err != nil || !done {
		return false, noNeedRollbackReplicas, err
	}

	// claim the cloneSet is under our control
	if _, err := c.claimCloneSet(c.clone); err != nil {
		return false, noNeedRollbackReplicas, err
	}

	// record revisions and replicas info to BatchRelease.Status
	c.recordCloneSetRevisionAndReplicas()

	c.recorder.Event(c.release, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, noNeedRollbackReplicas, nil
}

// UpgradeOneBatch calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *CloneSetRolloutController) UpgradeOneBatch() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, err
	}

	if c.newStatus.ObservedWorkloadReplicas == 0 {
		klog.Infof("BatchRelease(%v) observed workload replicas is 0, no need to upgrade", c.releasePlanKey)
		return true, nil
	}

	// if the workload status is untrustworthy
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		return false, nil
	}

	pods, err := util.ListOwnedPods(c.client, c.clone)
	if err != nil {
		klog.Errorf("Failed to list pods for CloneSet %v", c.targetNamespacedName)
		return false, err
	}

	var noNeedRollbackReplicas int32
	if c.newStatus.CanaryStatus.NoNeedUpdateReplicas != nil {
		noNeedRollbackReplicas = countNoNeedRollbackReplicas(pods, c.newStatus.UpdateRevision, c.release.Spec.ReleasePlan.RolloutID)
		c.newStatus.CanaryStatus.NoNeedUpdateReplicas = pointer.Int32(noNeedRollbackReplicas)
	}

	updatedReplicas := c.clone.Status.UpdatedReplicas
	replicas := c.newStatus.ObservedWorkloadReplicas
	currentBatch := c.newStatus.CanaryStatus.CurrentBatch
	partitionedStableReplicas, _ := intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.Partition, int(replicas), true)

	// the number of canary pods should have in current batch in plan
	plannedBatchCanaryReplicas := c.calculateCurrentCanary(c.newStatus.ObservedWorkloadReplicas)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedBatchCanaryReplicas := c.calculateCurrentCanary(replicas - noNeedRollbackReplicas)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedBatchStableReplicas := replicas - noNeedRollbackReplicas - expectedBatchCanaryReplicas

	// if canaryReplicas is int, then we use int;
	// if canaryReplicas is percentage, then we use percentage.
	var expectedPartition intstr.IntOrString
	canaryIntOrStr := c.release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	if canaryIntOrStr.Type == intstr.Int {
		expectedPartition = intstr.FromInt(int(expectedBatchStableReplicas))
	} else if c.newStatus.ObservedWorkloadReplicas > 0 {
		expectedPartition = ParseIntegerAsPercentageIfPossible(expectedBatchStableReplicas, c.newStatus.ObservedWorkloadReplicas, &canaryIntOrStr)
	}

	klog.V(3).InfoS("upgraded one batch, current info:",
		"BatchRelease", c.releasePlanKey,
		"currentBatch", currentBatch,
		"replicas", replicas,
		"updatedReplicas", updatedReplicas,
		"noNeedRollbackReplicas", noNeedRollbackReplicas,
		"partitionedStableReplicas", partitionedStableReplicas,
		"plannedBatchCanaryReplicas", plannedBatchCanaryReplicas,
		"expectedBatchCanaryReplicas", expectedBatchCanaryReplicas,
		"expectedBatchStableReplicas", expectedBatchStableReplicas,
		"expectedPartition", expectedPartition)

	// 1. the number of upgrade pod satisfied; 2. partition has been satisfied
	IsWorkloadUpgraded := updatedReplicas >= expectedBatchCanaryReplicas && int32(partitionedStableReplicas) <= expectedBatchStableReplicas
	if !IsWorkloadUpgraded {
		return false, c.patchCloneSetPartition(c.clone, &expectedPartition)
	}

	patchDone, err := c.patchPodBatchLabel(pods, plannedBatchCanaryReplicas, expectedBatchStableReplicas)
	if !patchDone || err != nil {
		return false, err
	}

	c.recorder.Eventf(c.release, v1.EventTypeNormal, "SetBatchDone", "Finished submitting all upgrade quests for batch %d", c.newStatus.CanaryStatus.CurrentBatch)
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

	var noNeedRollbackReplicas int32
	if c.newStatus.CanaryStatus.NoNeedUpdateReplicas != nil {
		noNeedRollbackReplicas = *c.newStatus.CanaryStatus.NoNeedUpdateReplicas
	}

	replicas := *c.clone.Spec.Replicas
	// the number of updated pods
	updatedReplicas := c.clone.Status.UpdatedReplicas
	// the number of updated ready pods
	updatedReadyReplicas := c.clone.Status.UpdatedReadyReplicas

	// current batch id
	currentBatch := c.newStatus.CanaryStatus.CurrentBatch
	// the number of pods will be partitioned by cloneSet
	partitionedStableReplicas, _ := intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.Partition, int(replicas), true)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedBatchCanaryReplicas := c.calculateCurrentCanary(replicas - noNeedRollbackReplicas)
	// the number of stable pods that consider rollback context and other real-world situations
	expectedBatchStableReplicas := replicas - noNeedRollbackReplicas - expectedBatchCanaryReplicas
	// the number of canary pods that cloneSet will be upgraded
	realNeedUpgradeCanaryReplicas := CalculateRealCanaryReplicasGoal(expectedBatchStableReplicas, replicas, &c.release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas)

	var maxUnavailableReplicas int
	if c.clone.Spec.UpdateStrategy.MaxUnavailable != nil {
		maxUnavailableReplicas, _ = intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.MaxUnavailable, int(realNeedUpgradeCanaryReplicas), true)
	}

	klog.V(3).InfoS("check one batch, current info:",
		"BatchRelease", c.releasePlanKey,
		"currentBatch", currentBatch,
		"replicas", replicas,
		"updatedReplicas", updatedReplicas,
		"noNeedRollbackReplicas", noNeedRollbackReplicas,
		"maxUnavailableReplicas", maxUnavailableReplicas,
		"partitionedStableReplicas", partitionedStableReplicas,
		"expectedBatchCanaryReplicas", expectedBatchCanaryReplicas,
		"expectedBatchStableReplicas", expectedBatchStableReplicas)

	currentBatchIsReady := updatedReplicas >= realNeedUpgradeCanaryReplicas && // 1.the number of upgrade pods achieved the goal
		updatedReadyReplicas+int32(maxUnavailableReplicas) >= realNeedUpgradeCanaryReplicas && // 2.the number of upgraded available pods achieved the goal
		(realNeedUpgradeCanaryReplicas == 0 || updatedReadyReplicas >= 1) // 3.make sure that at least one upgrade pod is available

	if !currentBatchIsReady {
		klog.InfoS("the batch is not ready yet", "BatchRelease", c.releasePlanKey, "current-batch", c.newStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	c.recorder.Eventf(c.release, v1.EventTypeNormal, "BatchAvailable", "Batch %d is available", c.newStatus.CanaryStatus.CurrentBatch)
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

	c.recorder.Eventf(c.release, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized: cleanup=%v", cleanup)
	return true, nil
}

// SyncWorkloadInfo return change type if workload was changed during release
func (c *CloneSetRolloutController) SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if c.release.DeletionTimestamp != nil {
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

	// in case of that the workload is scaling
	if *c.clone.Spec.Replicas != c.newStatus.ObservedWorkloadReplicas && c.newStatus.ObservedWorkloadReplicas != -1 {
		workloadInfo.Replicas = c.clone.Spec.Replicas
		klog.Warningf("CloneSet(%v) replicas changed during releasing, should pause and wait for it to complete, "+
			"replicas from: %v -> %v", c.targetNamespacedName, c.newStatus.ObservedWorkloadReplicas, *c.clone.Spec.Replicas)
		return WorkloadReplicasChanged, workloadInfo, nil
	}

	// updateRevision == CurrentRevision means CloneSet is rolling back or newly-created.
	if c.clone.Status.UpdateRevision == c.clone.Status.CurrentRevision &&
		// stableRevision == UpdateRevision means CloneSet is rolling back instead of newly-created.
		c.newStatus.StableRevision == c.clone.Status.UpdateRevision &&
		// StableRevision != observed UpdateRevision means the rollback event have not been observed.
		c.newStatus.StableRevision != c.newStatus.UpdateRevision {
		klog.Warningf("CloneSet(%v) is rolling back in batches", c.targetNamespacedName)
		return WorkloadRollbackInBatch, workloadInfo, nil
	}

	// in case of that the workload was changed
	if c.clone.Status.UpdateRevision != c.newStatus.UpdateRevision {
		klog.Warningf("CloneSet(%v) updateRevision changed during releasing, should try to restart the release plan, "+
			"updateRevision from: %v -> %v", c.targetNamespacedName, c.newStatus.UpdateRevision, c.clone.Status.UpdateRevision)
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
			c.recorder.Event(c.release, v1.EventTypeWarning, "GetCloneSetFailed", err.Error())
		}
		return err
	}
	c.clone = clone
	return nil
}

func (c *CloneSetRolloutController) recordCloneSetRevisionAndReplicas() {
	c.newStatus.ObservedWorkloadReplicas = *c.clone.Spec.Replicas
	c.newStatus.StableRevision = c.clone.Status.CurrentRevision
	c.newStatus.UpdateRevision = c.clone.Status.UpdateRevision
}

func (c *CloneSetRolloutController) patchPodBatchLabel(pods []*v1.Pod, plannedBatchCanaryReplicas, expectedBatchStableReplicas int32) (bool, error) {
	rolloutID := c.release.Spec.ReleasePlan.RolloutID
	if rolloutID == "" {
		return true, nil
	}

	updateRevision := c.release.Status.UpdateRevision
	batchID := c.release.Status.CanaryStatus.CurrentBatch + 1
	if c.newStatus.CanaryStatus.NoNeedUpdateReplicas != nil {
		pods = filterPodsForUnorderedRollback(pods, plannedBatchCanaryReplicas, expectedBatchStableReplicas, c.release.Status.ObservedWorkloadReplicas, rolloutID, updateRevision)
	}
	return patchPodBatchLabel(c.client, pods, rolloutID, batchID, updateRevision, plannedBatchCanaryReplicas, c.releasePlanKey)
}
