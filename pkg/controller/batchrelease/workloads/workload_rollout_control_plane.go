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

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type UnifiedWorkloadController interface {
	GetWorkloadInfo() (*util.WorkloadInfo, error)
	ClaimWorkload() (bool, error)
	ReleaseWorkload(cleanup bool) (bool, error)
	UpgradeBatch(canaryReplicasGoal, stableReplicasGoal int32) (bool, error)
	ListOwnedPods() ([]*v1.Pod, error)
	IsOrderedUpdate() (bool, error)
}

// UnifiedWorkloadRolloutControlPlane is responsible for handling rollout StatefulSet type of workloads
type UnifiedWorkloadRolloutControlPlane struct {
	UnifiedWorkloadController
	client    client.Client
	recorder  record.EventRecorder
	release   *v1alpha1.BatchRelease
	newStatus *v1alpha1.BatchReleaseStatus
}

type NewUnifiedControllerFunc = func(c client.Client, r record.EventRecorder, p *v1alpha1.BatchRelease, n types.NamespacedName, gvk schema.GroupVersionKind) UnifiedWorkloadController

// NewUnifiedWorkloadRolloutControlPlane creates a new workload rollout controller
func NewUnifiedWorkloadRolloutControlPlane(f NewUnifiedControllerFunc, c client.Client, r record.EventRecorder, p *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, n types.NamespacedName, gvk schema.GroupVersionKind) *UnifiedWorkloadRolloutControlPlane {
	return &UnifiedWorkloadRolloutControlPlane{
		client:                    c,
		recorder:                  r,
		release:                   p,
		newStatus:                 newStatus,
		UnifiedWorkloadController: f(c, r, p, n, gvk),
	}
}

// VerifyWorkload verifies that the workload is ready to execute release plan
func (c *UnifiedWorkloadRolloutControlPlane) VerifyWorkload() (bool, error) {
	return true, nil
}

// prepareBeforeRollback makes sure that the updated pods have been patched no-need-update label.
// return values:
// - bool: whether all updated pods have been patched no-need-update label;
// - *int32: how many pods have been patched;
// - err: whether error occurs.
func (c *UnifiedWorkloadRolloutControlPlane) prepareBeforeRollback() (bool, *int32, error) {
	if c.release.Annotations[util.RollbackInBatchAnnotation] == "" {
		return true, nil, nil
	}

	noNeedRollbackReplicas := int32(0)
	rolloutID := c.release.Spec.ReleasePlan.RolloutID
	if rolloutID == "" {
		return true, &noNeedRollbackReplicas, nil
	}

	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, &noNeedRollbackReplicas, nil
	}

	pods, err := c.ListOwnedPods()
	if err != nil {
		klog.Errorf("Failed to list pods for %v", workloadInfo.GVKWithName)
		return false, &noNeedRollbackReplicas, err
	}

	updateRevision := workloadInfo.Status.UpdateRevision
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
	klog.Infof("BatchRelease(%v) find %v replicas no need to rollback", client.ObjectKeyFromObject(c.release), noNeedRollbackReplicas)
	return false, &noNeedRollbackReplicas, nil
}

// PrepareBeforeProgress makes sure that the source and target workload is under our control
func (c *UnifiedWorkloadRolloutControlPlane) PrepareBeforeProgress() (bool, *int32, error) {
	done, noNeedRollbackReplicas, err := c.prepareBeforeRollback()
	if err != nil || !done {
		return false, nil, err
	}

	// claim the workload is under our control
	done, err = c.ClaimWorkload()
	if !done || err != nil {
		return false, noNeedRollbackReplicas, err
	}

	// record revisions and replicas info to BatchRelease.Status
	err = c.RecordWorkloadRevisionAndReplicas()
	if err != nil {
		return false, noNeedRollbackReplicas, err
	}

	c.recorder.Event(c.release, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, noNeedRollbackReplicas, nil
}

// UpgradeOneBatch calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
// TODO: support advanced statefulSet reserveOrdinal feature0
func (c *UnifiedWorkloadRolloutControlPlane) UpgradeOneBatch() (bool, error) {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	if c.release.Status.ObservedWorkloadReplicas == 0 {
		klog.Infof("BatchRelease(%v) observed workload replicas is 0, no need to upgrade", client.ObjectKeyFromObject(c.release))
		return true, nil
	}

	// if the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Generation {
		return false, nil
	}

	pods, err := c.ListOwnedPods()
	if err != nil {
		return false, err
	}

	var noNeedRollbackReplicas int32
	if c.newStatus.CanaryStatus.NoNeedUpdateReplicas != nil {
		rolloutID := c.release.Spec.ReleasePlan.RolloutID
		noNeedRollbackReplicas = countNoNeedRollbackReplicas(pods, c.newStatus.UpdateRevision, rolloutID)
		c.newStatus.CanaryStatus.NoNeedUpdateReplicas = pointer.Int32(noNeedRollbackReplicas)
	}
	replicas := c.newStatus.ObservedWorkloadReplicas
	currentBatch := c.newStatus.CanaryStatus.CurrentBatch

	// the number of canary pods should have in current batch in plan
	plannedBatchCanaryReplicas := c.calculateCurrentCanary(c.newStatus.ObservedWorkloadReplicas)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedBatchCanaryReplicas := c.calculateCurrentCanary(replicas - noNeedRollbackReplicas)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedBatchStableReplicas := replicas - expectedBatchCanaryReplicas

	// if ordered update, partition is related with pod ordinals
	// if unordered update, partition just like cloneSet partition
	orderedUpdate, _ := c.IsOrderedUpdate()
	if !orderedUpdate {
		expectedBatchStableReplicas -= noNeedRollbackReplicas
	}

	klog.V(3).InfoS("upgrade one batch, current info:",
		"BatchRelease", client.ObjectKeyFromObject(c.release),
		"currentBatch", currentBatch,
		"replicas", replicas,
		"noNeedRollbackReplicas", noNeedRollbackReplicas,
		"plannedBatchCanaryReplicas", plannedBatchCanaryReplicas,
		"expectedBatchCanaryReplicas", expectedBatchCanaryReplicas,
		"expectedBatchStableReplicas", expectedBatchStableReplicas)

	isUpgradedDone, err := c.UpgradeBatch(expectedBatchCanaryReplicas, expectedBatchStableReplicas)
	if err != nil || !isUpgradedDone {
		return false, nil
	}

	isPatchedDone, err := c.patchPodBatchLabel(pods, plannedBatchCanaryReplicas, expectedBatchStableReplicas)
	if err != nil || !isPatchedDone {
		return false, err
	}

	c.recorder.Eventf(c.release, v1.EventTypeNormal, "SetBatchDone",
		"Finished submitting all upgrade quests for batch %d", c.release.Status.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchReady checks to see if the pods are all available according to the rollout plan
func (c *UnifiedWorkloadRolloutControlPlane) CheckOneBatchReady() (bool, error) {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	if c.release.Status.ObservedWorkloadReplicas == 0 {
		klog.Infof("BatchRelease(%v) observed workload replicas is 0, no need to check", client.ObjectKeyFromObject(c.release))
		return true, nil
	}

	// if the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Generation {
		return false, nil
	}

	var noNeedRollbackReplicas int32
	if c.newStatus.CanaryStatus.NoNeedUpdateReplicas != nil {
		noNeedRollbackReplicas = *c.newStatus.CanaryStatus.NoNeedUpdateReplicas
	}

	replicas := c.newStatus.ObservedWorkloadReplicas
	updatedReplicas := workloadInfo.Status.UpdatedReplicas
	updatedReadyReplicas := workloadInfo.Status.UpdatedReadyReplicas

	currentBatch := c.newStatus.CanaryStatus.CurrentBatch
	// the number of canary pods should have in current batch in plan
	plannedUpdatedReplicas := c.calculateCurrentCanary(c.newStatus.ObservedWorkloadReplicas)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedUpdatedReplicas := c.calculateCurrentCanary(replicas - noNeedRollbackReplicas)
	// the number of canary pods that consider rollback context and other real-world situations
	expectedStableReplicas := replicas - expectedUpdatedReplicas
	// the number of pods that should be upgraded in this batch
	updatedReplicasInBatch := plannedUpdatedReplicas
	if currentBatch > 0 {
		updatedReplicasInBatch -= int32(calculateNewBatchTarget(&c.release.Spec.ReleasePlan, int(replicas), int(currentBatch-1)))
	}

	// if ordered update, partition is related with pod ordinals
	// if unordered update, partition just like cloneSet partition
	orderedUpdate, _ := c.IsOrderedUpdate()
	if !orderedUpdate {
		expectedStableReplicas -= noNeedRollbackReplicas
	}

	klog.V(3).InfoS("check one batch, current info:",
		"BatchRelease", client.ObjectKeyFromObject(c.release),
		"currentBatch", currentBatch,
		"replicas", replicas,
		"noNeedRollbackReplicas", noNeedRollbackReplicas,
		"updatedReplicasInBatch", updatedReplicasInBatch,
		"plannedUpdatedReplicas", plannedUpdatedReplicas,
		"expectedUpdatedReplicas", expectedUpdatedReplicas,
		"expectedStableReplicas", expectedStableReplicas)

	pods, err := c.ListOwnedPods()
	if err != nil {
		return false, err
	}

	if !isBatchReady(c.release, pods, workloadInfo.MaxUnavailable,
		plannedUpdatedReplicas, expectedUpdatedReplicas, updatedReplicas, updatedReadyReplicas) {
		klog.Infof("BatchRelease(%v) batch is not ready yet, current batch=%d", klog.KObj(c.release), currentBatch)
		return false, nil
	}

	klog.Infof("BatchRelease(%v) %d batch is ready", klog.KObj(c.release), currentBatch)
	return true, nil
}

// FinalizeProgress makes sure the workload is all upgraded
func (c *UnifiedWorkloadRolloutControlPlane) FinalizeProgress(cleanup bool) (bool, error) {
	if _, err := c.ReleaseWorkload(cleanup); err != nil {
		return false, err
	}
	c.recorder.Eventf(c.release, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized: cleanup=%v", cleanup)
	return true, nil
}

// SyncWorkloadInfo return change type if workload was changed during release
func (c *UnifiedWorkloadRolloutControlPlane) SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if c.release.DeletionTimestamp != nil {
		return IgnoreWorkloadEvent, nil, nil
	}

	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		if apierrors.IsNotFound(err) {
			return WorkloadHasGone, nil, err
		}
		return "", nil, err
	}

	// in case that the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Generation {
		klog.Warningf("%v is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			workloadInfo.GVKWithName, workloadInfo.Generation, workloadInfo.Status.ObservedGeneration)
		return WorkloadStillReconciling, nil, nil
	}

	// in case of that the updated revision of the workload is promoted
	if workloadInfo.Status.UpdatedReplicas == workloadInfo.Status.Replicas {
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	// in case of that the workload is scaling
	if *workloadInfo.Replicas != c.release.Status.ObservedWorkloadReplicas {
		klog.Warningf("%v replicas changed during releasing, should pause and wait for it to complete, "+
			"replicas from: %v -> %v", workloadInfo.GVKWithName, c.release.Status.ObservedWorkloadReplicas, *workloadInfo.Replicas)
		return WorkloadReplicasChanged, workloadInfo, nil
	}

	// updateRevision == CurrentRevision means CloneSet is rolling back or newly-created.
	if workloadInfo.Status.UpdateRevision == workloadInfo.Status.StableRevision &&
		// stableRevision == UpdateRevision means CloneSet is rolling back instead of newly-created.
		c.newStatus.StableRevision == workloadInfo.Status.UpdateRevision &&
		// StableRevision != observed UpdateRevision means the rollback event have not been observed.
		c.newStatus.StableRevision != c.newStatus.UpdateRevision {
		klog.Warningf("Workload(%v) is rolling back in batches", workloadInfo.GVKWithName)
		return WorkloadRollbackInBatch, workloadInfo, nil
	}

	// in case of that the workload was changed
	if workloadInfo.Status.UpdateRevision != c.release.Status.UpdateRevision {
		klog.Warningf("%v updateRevision changed during releasing, should try to restart the release plan, "+
			"updateRevision from: %v -> %v", workloadInfo.GVKWithName, c.release.Status.UpdateRevision, workloadInfo.Status.UpdateRevision)
		return WorkloadPodTemplateChanged, workloadInfo, nil
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

// the canary workload size for the current batch
func (c *UnifiedWorkloadRolloutControlPlane) calculateCurrentCanary(totalSize int32) int32 {
	canaryGoal := int32(calculateNewBatchTarget(&c.release.Spec.ReleasePlan, int(totalSize), int(c.release.Status.CanaryStatus.CurrentBatch)))
	klog.InfoS("Calculated the number of pods in the target workload after current batch", "BatchRelease", client.ObjectKeyFromObject(c.release),
		"current batch", c.release.Status.CanaryStatus.CurrentBatch, "workload canary goal replicas goal", canaryGoal)
	return canaryGoal
}

// the source workload size for the current batch
func (c *UnifiedWorkloadRolloutControlPlane) calculateCurrentStable(totalSize int32) int32 {
	stableGoal := totalSize - c.calculateCurrentCanary(totalSize)
	klog.InfoS("Calculated the number of pods in the target workload after current batch", "BatchRelease", client.ObjectKeyFromObject(c.release),
		"current batch", c.release.Status.CanaryStatus.CurrentBatch, "workload stable  goal replicas goal", stableGoal)
	return stableGoal
}

func (c *UnifiedWorkloadRolloutControlPlane) RecordWorkloadRevisionAndReplicas() error {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return err
	}

	c.newStatus.ObservedWorkloadReplicas = *workloadInfo.Replicas
	c.newStatus.StableRevision = workloadInfo.Status.StableRevision
	c.newStatus.UpdateRevision = workloadInfo.Status.UpdateRevision
	return nil
}

func (c *UnifiedWorkloadRolloutControlPlane) patchPodBatchLabel(pods []*v1.Pod, plannedBatchCanaryReplicas, expectedBatchStableReplicas int32) (bool, error) {
	rolloutID := c.release.Spec.ReleasePlan.RolloutID
	if rolloutID == "" {
		return true, nil
	}

	updateRevision := c.release.Status.UpdateRevision
	batchID := c.release.Status.CanaryStatus.CurrentBatch + 1
	if c.newStatus.CanaryStatus.NoNeedUpdateReplicas != nil {
		orderedUpdate, _ := c.IsOrderedUpdate()
		if orderedUpdate {
			pods = filterPodsForOrderedRollback(pods, plannedBatchCanaryReplicas, expectedBatchStableReplicas, c.release.Status.ObservedWorkloadReplicas, rolloutID, updateRevision)
		} else {
			pods = filterPodsForUnorderedRollback(pods, plannedBatchCanaryReplicas, expectedBatchStableReplicas, c.release.Status.ObservedWorkloadReplicas, rolloutID, updateRevision)
		}
	}
	return patchPodBatchLabel(c.client, pods, rolloutID, batchID, updateRevision, plannedBatchCanaryReplicas, client.ObjectKeyFromObject(c.release))
}
