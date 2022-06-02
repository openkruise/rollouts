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
	"encoding/json"
	"fmt"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type UnifiedWorkloadController interface {
	GetWorkloadInfo() (*util.WorkloadInfo, error)
	ClaimWorkload() (bool, error)
	ReleaseWorkload(cleanup bool) (bool, error)
	UpgradeBatch(canaryReplicasGoal, stableReplicasGoal int32) (bool, error)
	IsBatchUpgraded(canaryReplicasGoal, stableReplicasGoal int32) (bool, error)
	IsBatchReady(canaryReplicasGoal, stableReplicasGoal int32) (bool, error)
}

// UnifiedWorkloadRolloutControlPlane is responsible for handling rollout StatefulSet type of workloads
type UnifiedWorkloadRolloutControlPlane struct {
	UnifiedWorkloadController
	client         client.Client
	recorder       record.EventRecorder
	planController *v1alpha1.BatchRelease
	newStatus      *v1alpha1.BatchReleaseStatus
}

type NewUnifiedControllerFunc = func(c client.Client, r record.EventRecorder, p *v1alpha1.BatchRelease, n types.NamespacedName, gvk schema.GroupVersionKind) UnifiedWorkloadController

// NewUnifiedWorkloadRolloutControlPlane creates a new workload rollout controller
func NewUnifiedWorkloadRolloutControlPlane(f NewUnifiedControllerFunc, c client.Client, r record.EventRecorder, p *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, n types.NamespacedName, gvk schema.GroupVersionKind) *UnifiedWorkloadRolloutControlPlane {
	return &UnifiedWorkloadRolloutControlPlane{
		client:                    c,
		recorder:                  r,
		planController:            p,
		newStatus:                 newStatus,
		UnifiedWorkloadController: f(c, r, p, n, gvk),
	}
}

// VerifyWorkload verifies that the workload is ready to execute release plan
func (c *UnifiedWorkloadRolloutControlPlane) VerifyWorkload() (bool, error) {
	var err error
	var message string
	defer func() {
		if err != nil {
			c.recorder.Event(c.planController, v1.EventTypeWarning, "VerifyFailed", err.Error())
		} else if message != "" {
			klog.Warningf(message)
		}
	}()

	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	// If the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Metadata.Generation {
		message = fmt.Sprintf("%v is still reconciling, wait for it to be done", workloadInfo.GVKWithName)
		return false, nil
	}

	// If the workload has been promoted, no need to go on
	if workloadInfo.Status.UpdatedReplicas == *workloadInfo.Replicas {
		message = fmt.Sprintf("%v update revision has been promoted, no need to reconcile", workloadInfo.GVKWithName)
		return false, nil
	}

	// If the workload is not paused and is not under our control
	if !workloadInfo.Paused {
		message = fmt.Sprintf("%v should be paused before execute the release plan", workloadInfo.GVKWithName)
		return false, nil
	}

	c.recorder.Event(c.planController, v1.EventTypeNormal, "VerifiedSuccessfully", "ReleasePlan and the workload resource are verified")
	return true, nil
}

// PrepareBeforeProgress makes sure that the source and target workload is under our control
func (c *UnifiedWorkloadRolloutControlPlane) PrepareBeforeProgress() (bool, error) {
	// claim the workload is under our control
	done, err := c.ClaimWorkload()
	if !done || err != nil {
		return false, err
	}

	// record revisions and replicas info to BatchRelease.Status
	err = c.RecordWorkloadRevisionAndReplicas()
	if err != nil {
		return false, err
	}

	c.recorder.Event(c.planController, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, nil
}

// UpgradeOneBatch calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *UnifiedWorkloadRolloutControlPlane) UpgradeOneBatch() (bool, error) {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	if c.planController.Status.ObservedWorkloadReplicas == 0 {
		klog.Infof("BatchRelease(%v) observed workload replicas is 0, no need to upgrade", client.ObjectKeyFromObject(c.planController))
		return true, nil
	}

	// if the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Metadata.Generation {
		return false, nil
	}

	currentBatch := c.newStatus.CanaryStatus.CurrentBatch
	// the number of canary pods should have in current batch
	canaryGoal := c.calculateCurrentCanary(c.planController.Status.ObservedWorkloadReplicas)
	// the number of stable pods should have in current batch
	stableGoal := c.calculateCurrentStable(c.planController.Status.ObservedWorkloadReplicas)
	// the number of canary pods now we have in current state
	currentCanaryReplicas := workloadInfo.Status.UpdatedReplicas

	// in case of no need to upgrade pods
	klog.V(3).InfoS("upgraded one batch, status info:",
		"BatchRelease", client.ObjectKeyFromObject(c.planController),
		"current-batch", currentBatch,
		"canary-goal", canaryGoal,
		"stable-goal", stableGoal,
		"canary-replicas", currentCanaryReplicas)

	upgradeDone, err := c.IsBatchUpgraded(canaryGoal, stableGoal)
	if err != nil {
		return false, err
	} else if !upgradeDone {
		if succeed, err := c.UpgradeBatch(canaryGoal, stableGoal); err != nil || !succeed {
			return false, nil
		}
	}

	c.recorder.Eventf(c.planController, v1.EventTypeNormal, "SetBatchDone",
		"Finished submitting all upgrade quests for batch %d", c.planController.Status.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchReady checks to see if the pods are all available according to the rollout plan
func (c *UnifiedWorkloadRolloutControlPlane) CheckOneBatchReady() (bool, error) {
	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		return false, err
	}

	if c.planController.Status.ObservedWorkloadReplicas == 0 {
		klog.Infof("BatchRelease(%v) observed workload replicas is 0, no need to check", client.ObjectKeyFromObject(c.planController))
		return true, nil
	}

	// if the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Metadata.Generation {
		return false, nil
	}

	// the number of canary pods now we have in current state
	canaryReplicas := workloadInfo.Status.UpdatedReplicas
	// the number of stable pods now we have in current state
	stableReplicas := workloadInfo.Status.Replicas - canaryReplicas
	// the number of canary pods that have been ready in current state
	canaryReadyReplicas := workloadInfo.Status.UpdatedReadyReplicas
	// the number of the real canary pods should have in current batch
	canaryGoal := c.calculateCurrentCanary(c.planController.Status.ObservedWorkloadReplicas)
	// the number of the real stable pods should have in current batch
	stableGoal := c.calculateCurrentStable(c.planController.Status.ObservedWorkloadReplicas)
	// the number of max unavailable canary pods allowed by this workload
	maxUnavailable := 0
	if workloadInfo.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetValueFromIntOrPercent(workloadInfo.MaxUnavailable, int(c.planController.Status.ObservedWorkloadReplicas), true)
	}

	klog.InfoS("checking the batch releasing progress",
		"BatchRelease", client.ObjectKeyFromObject(c.planController),
		"current-batch", c.planController.Status.CanaryStatus.CurrentBatch,
		"canary-goal", canaryGoal,
		"stable-goal", stableGoal,
		"stable-replicas", stableReplicas,
		"canary-ready-replicas", canaryReadyReplicas,
		"maxUnavailable", maxUnavailable)

	// maybe, the workload replicas was scaled, we should requeue and handle the workload scaling event
	if workloadInfo.Status.Replicas != c.planController.Status.ObservedWorkloadReplicas {
		err := fmt.Errorf("%v replicas don't match ObservedWorkloadReplicas, workload status replicas: %v, observed workload replicas: %v",
			workloadInfo.GVKWithName, workloadInfo.Status.Replicas, c.planController.Status.ObservedWorkloadReplicas)
		klog.ErrorS(err, "the batch is not valid", "current-batch", c.planController.Status.CanaryStatus.CurrentBatch)
		return false, err
	}

	if ready, err := c.IsBatchReady(canaryGoal, stableGoal); err != nil || !ready {
		klog.InfoS("the batch is not ready yet", "Workload", workloadInfo.GVKWithName,
			"ReleasePlan", client.ObjectKeyFromObject(c.planController), "current-batch", c.planController.Status.CanaryStatus.CurrentBatch)
		return false, nil
	}

	klog.Infof("All pods of %v in current batch are ready, BatchRelease(%v), current-batch=%v",
		workloadInfo.GVKWithName, client.ObjectKeyFromObject(c.planController), c.planController.Status.CanaryStatus.CurrentBatch)
	c.recorder.Eventf(c.planController, v1.EventTypeNormal, "BatchAvailable", "Batch %d is available", c.planController.Status.CanaryStatus.CurrentBatch)
	return true, nil
}

// FinalizeProgress makes sure the workload is all upgraded
func (c *UnifiedWorkloadRolloutControlPlane) FinalizeProgress(cleanup bool) (bool, error) {
	if _, err := c.ReleaseWorkload(cleanup); err != nil {
		return false, err
	}
	c.recorder.Eventf(c.planController, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized: cleanup=%v", cleanup)
	return true, nil
}

// SyncWorkloadInfo return change type if workload was changed during release
func (c *UnifiedWorkloadRolloutControlPlane) SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if c.planController.DeletionTimestamp != nil {
		return IgnoreWorkloadEvent, nil, nil
	}

	workloadInfo, err := c.GetWorkloadInfo()
	if err != nil {
		if apierrors.IsNotFound(err) {
			return WorkloadHasGone, nil, err
		}
		return "", nil, err
	}

	info, _ := json.Marshal(workloadInfo)
	klog.Infof("WorkloadInfo: %s", string(info))

	// in case that the workload status is untrustworthy
	if workloadInfo.Status.ObservedGeneration != workloadInfo.Metadata.Generation {
		klog.Warningf("%v is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			workloadInfo.GVKWithName, workloadInfo.Metadata.Generation, workloadInfo.Status.ObservedGeneration)
		return WorkloadStillReconciling, nil, nil
	}

	// in case of that the updated revision of the workload is promoted
	if workloadInfo.Status.UpdatedReplicas == workloadInfo.Status.Replicas {
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	// in case of that the workload is scaling
	if *workloadInfo.Replicas != c.planController.Status.ObservedWorkloadReplicas {
		klog.Warningf("%v replicas changed during releasing, should pause and wait for it to complete, "+
			"replicas from: %v -> %v", workloadInfo.GVKWithName, c.planController.Status.ObservedWorkloadReplicas, *workloadInfo.Replicas)
		return WorkloadReplicasChanged, workloadInfo, nil
	}

	// in case of that the workload was changed
	if workloadInfo.Status.UpdateRevision != c.planController.Status.UpdateRevision {
		klog.Warningf("%v updateRevision changed during releasing, should try to restart the release plan, "+
			"updateRevision from: %v -> %v", workloadInfo.GVKWithName, c.planController.Status.UpdateRevision, workloadInfo.Status.UpdateRevision)
		return WorkloadPodTemplateChanged, workloadInfo, nil
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

// the canary workload size for the current batch
func (c *UnifiedWorkloadRolloutControlPlane) calculateCurrentCanary(totalSize int32) int32 {
	canaryGoal := int32(util.CalculateNewBatchTarget(&c.planController.Spec.ReleasePlan, int(totalSize), int(c.planController.Status.CanaryStatus.CurrentBatch)))
	klog.InfoS("Calculated the number of pods in the target workload after current batch", "BatchRelease", client.ObjectKeyFromObject(c.planController),
		"current batch", c.planController.Status.CanaryStatus.CurrentBatch, "workload canary goal replicas goal", canaryGoal)
	return canaryGoal
}

// the source workload size for the current batch
func (c *UnifiedWorkloadRolloutControlPlane) calculateCurrentStable(totalSize int32) int32 {
	stableGoal := totalSize - c.calculateCurrentCanary(totalSize)
	klog.InfoS("Calculated the number of pods in the target workload after current batch", "BatchRelease", client.ObjectKeyFromObject(c.planController),
		"current batch", c.planController.Status.CanaryStatus.CurrentBatch, "workload stable  goal replicas goal", stableGoal)
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
