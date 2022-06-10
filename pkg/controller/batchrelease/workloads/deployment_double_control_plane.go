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
	"sort"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// DeploymentsRolloutController is responsible for handling Deployment type of workloads
type DeploymentsRolloutController struct {
	deploymentController
	stable *apps.Deployment
	canary *apps.Deployment
}

// NewDeploymentRolloutController creates a new Deployment rollout controller
func NewDeploymentRolloutController(cli client.Client, recorder record.EventRecorder, release *v1alpha1.BatchRelease, plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus, stableNamespacedName types.NamespacedName) *DeploymentsRolloutController {
	return &DeploymentsRolloutController{
		deploymentController: deploymentController{
			workloadController: workloadController{
				client:           cli,
				recorder:         recorder,
				parentController: release,
				releasePlan:      plan,
				releaseStatus:    status,
			},
			stableNamespacedName: stableNamespacedName,
			canaryNamespacedName: stableNamespacedName,
			releaseKey:           client.ObjectKeyFromObject(release),
		},
	}
}

// VerifyWorkload verifies that the workload is ready to execute release plan
func (c *DeploymentsRolloutController) VerifyWorkload() (bool, error) {
	var err error
	var message string
	defer func() {
		if err != nil {
			c.recorder.Event(c.parentController, v1.EventTypeWarning, "VerifyFailed", err.Error())
		} else if message != "" {
			klog.Warningf(message)
		}
	}()

	if err = c.fetchStableDeployment(); err != nil {
		return false, err
	}

	if err = c.fetchCanaryDeployment(); client.IgnoreNotFound(err) != nil {
		return false, err
	}

	// if the workload status is untrustworthy, return and retry
	if c.stable.Status.ObservedGeneration != c.stable.Generation {
		message = fmt.Sprintf("deployment(%v) is still reconciling, wait for it to be done", c.stableNamespacedName)
		return false, nil
	}

	// if the workload has been promoted, return and not retry
	if c.stable.Status.UpdatedReplicas == *c.stable.Spec.Replicas {
		message = fmt.Sprintf("deployment(%v) update revision has been promoted, no need to rollout", c.stableNamespacedName)
		return false, nil
	}

	// if the workload is not paused, no need to progress it
	if !c.stable.Spec.Paused {
		message = fmt.Sprintf("deployment(%v) should be paused before execute the release plan", c.stableNamespacedName)
		return false, nil
	}

	// claim the deployment is under our control, and create canary deployment if it needs.
	// Do not move this function to Preparing phase, otherwise multi canary deployments
	// will be repeatedly created due to informer cache latency.
	if _, err = c.claimDeployment(c.stable, c.canary); err != nil {
		return false, err
	}

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "Verified", "ReleasePlan and the Deployment resource are verified")
	return true, nil
}

// PrepareBeforeProgress makes sure that the Deployment is under our control
func (c *DeploymentsRolloutController) PrepareBeforeProgress() (bool, error) {
	// the workload is verified, and we should record revision and replicas info before progressing
	if err := c.recordDeploymentRevisionAndReplicas(); err != nil {
		klog.Errorf("Failed to record deployment(%v) revision and replicas info, error: %v", c.stableNamespacedName, err)
		return false, err
	}

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "Initialized", "Rollout resource are initialized")
	return true, nil
}

// UpgradeOneBatch calculates the number of pods we can upgrade once
// according to the release plan and then set the canary deployment replicas
func (c *DeploymentsRolloutController) UpgradeOneBatch() (bool, error) {
	if err := c.fetchStableDeployment(); err != nil {
		return false, err
	}
	if err := c.fetchCanaryDeployment(); err != nil {
		return false, err
	}

	// canary replicas now we have at current state
	currentCanaryReplicas := *c.canary.Spec.Replicas

	// canary goal we should achieve
	canaryGoal := c.calculateCurrentCanary(c.releaseStatus.ObservedWorkloadReplicas)

	klog.V(3).InfoS("upgraded one batch, but no need to update replicas of canary Deployment",
		"Deployment", client.ObjectKeyFromObject(c.canary),
		"BatchRelease", c.releaseKey,
		"current-batch", c.releaseStatus.CanaryStatus.CurrentBatch,
		"canary-goal", canaryGoal,
		"current-canary-replicas", currentCanaryReplicas,
		"current-canary-status-replicas", c.canary.Status.UpdatedReplicas)

	// upgrade pods if it needs
	if currentCanaryReplicas < canaryGoal {
		if err := c.patchDeploymentReplicas(c.canary, canaryGoal); err != nil {
			return false, err
		}
	}

	// patch current batch label to pods
	patchDone, err := c.patchPodBatchLabel(canaryGoal)
	if !patchDone || err != nil {
		return false, err
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "Batch Rollout", "Finished submitting all upgrade quests for batch %d", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchReady checks to see if the pods are all available according to the rollout plan
func (c *DeploymentsRolloutController) CheckOneBatchReady() (bool, error) {
	if err := c.fetchStableDeployment(); err != nil {
		return false, err
	}
	if err := c.fetchCanaryDeployment(); err != nil {
		return false, err
	}

	// in case of workload status is Untrustworthy
	if c.canary.Status.ObservedGeneration != c.canary.Generation {
		return false, nil
	}

	// canary pods that have been created
	canaryPodCount := c.canary.Status.Replicas
	// canary pods that have been available
	availableCanaryPodCount := c.canary.Status.AvailableReplicas
	// canary goal that should have in current batch
	canaryGoal := c.calculateCurrentCanary(c.releaseStatus.ObservedWorkloadReplicas)
	// max unavailable allowed replicas
	maxUnavailable := 0
	if c.canary.Spec.Strategy.RollingUpdate != nil &&
		c.canary.Spec.Strategy.RollingUpdate.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetScaledValueFromIntOrPercent(c.canary.Spec.Strategy.RollingUpdate.MaxUnavailable, int(*c.canary.Spec.Replicas), true)
	}

	klog.InfoS("checking the batch releasing progress",
		"BatchRelease", c.releaseKey,
		"current-batch", c.releaseStatus.CanaryStatus.CurrentBatch,
		"canary-goal", canaryGoal,
		"canary-available-pod-count", availableCanaryPodCount,
		"stable-pod-status-replicas", c.stable.Status.Replicas,
		"maxUnavailable", maxUnavailable)

	currentBatchIsNotReadyYet := func() bool {
		// the number of upgrade pods does not achieve the goal
		return canaryPodCount < canaryGoal ||
			// the number of upgraded available pods does not achieve the goal
			availableCanaryPodCount+int32(maxUnavailable) < canaryGoal ||
			// make sure that at least one upgrade pod is available
			(canaryGoal > 0 && availableCanaryPodCount == 0)
	}

	// make sure there is at least one pod is available
	if currentBatchIsNotReadyYet() {
		klog.Infof("BatchRelease(%v) batch is not ready yet, current batch=%v", c.releaseKey, c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "BatchReady", "Batch %d is available", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// FinalizeProgress makes sure restore deployments and clean up some canary settings
func (c *DeploymentsRolloutController) FinalizeProgress(cleanup bool) (bool, error) {
	if err := c.fetchStableDeployment(); client.IgnoreNotFound(err) != nil {
		return false, err
	}

	// make the deployment ride out of our control, and clean up canary resources
	succeed, err := c.releaseDeployment(c.stable, cleanup)
	if !succeed || err != nil {
		klog.Errorf("Failed to finalize deployment(%v), error: %v", c.stableNamespacedName, err)
		return false, err
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "Finalized", "Finalized: cleanup=%v", cleanup)
	return true, nil
}

// SyncWorkloadInfo return workloadInfo if workload info is changed during rollout
// TODO: abstract a WorkloadEventTypeJudge interface for these following `if` clauses
func (c *DeploymentsRolloutController) SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if c.parentController.DeletionTimestamp != nil {
		return IgnoreWorkloadEvent, nil, nil
	}

	var err error
	err = c.fetchStableDeployment()
	if err != nil {
		if apierrors.IsNotFound(err) {
			return WorkloadHasGone, nil, err
		}
		return "", nil, err
	}

	err = c.fetchCanaryDeployment()
	if client.IgnoreNotFound(err) != nil {
		return "", nil, err
	}

	workloadInfo := util.NewWorkloadInfo()
	if c.canary != nil {
		workloadInfo.Status = &util.WorkloadStatus{
			UpdatedReplicas:      c.canary.Status.Replicas,
			UpdatedReadyReplicas: c.canary.Status.AvailableReplicas,
		}
	}

	// in case of that the canary deployment is being deleted but still have the finalizer, it is out of our expectation
	if c.canary != nil && c.canary.DeletionTimestamp != nil && controllerutil.ContainsFinalizer(c.canary, util.CanaryDeploymentFinalizer) {
		return WorkloadUnHealthy, workloadInfo, nil
	}

	// in case of that the workload status is trustworthy
	if c.stable.Status.ObservedGeneration != c.stable.Generation {
		klog.Warningf("Deployment(%v) is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			c.stableNamespacedName, c.stable.Generation, c.stable.Status.ObservedGeneration)
		return WorkloadStillReconciling, workloadInfo, nil
	}

	// in case of that the workload has been promoted
	if !c.stable.Spec.Paused && c.stable.Status.UpdatedReplicas == c.stable.Status.Replicas {
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	// in case of that the workload is scaling up/down
	if *c.stable.Spec.Replicas != c.releaseStatus.ObservedWorkloadReplicas && c.releaseStatus.ObservedWorkloadReplicas != -1 {
		workloadInfo.Replicas = c.stable.Spec.Replicas
		klog.Warningf("Deployment(%v) replicas changed during releasing, should pause and wait for it to complete, replicas from: %v -> %v",
			c.stableNamespacedName, c.releaseStatus.ObservedWorkloadReplicas, *c.stable.Spec.Replicas)
		return WorkloadReplicasChanged, workloadInfo, nil
	}

	// in case of that the workload revision was changed
	if hashRevision := util.ComputeHash(&c.stable.Spec.Template, nil); hashRevision != c.releaseStatus.UpdateRevision {
		workloadInfo.Status.UpdateRevision = hashRevision
		klog.Warningf("Deployment(%v) updateRevision changed during releasing", c.stableNamespacedName)
		return WorkloadPodTemplateChanged, workloadInfo, nil
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

/* ----------------------------------
The functions below are helper functions
------------------------------------- */
// fetchStableDeployment fetch stable deployment to c.stable
func (c *DeploymentsRolloutController) fetchStableDeployment() error {
	if c.stable != nil {
		return nil
	}

	stable := &apps.Deployment{}
	if err := c.client.Get(context.TODO(), c.stableNamespacedName, stable); err != nil {
		klog.Errorf("BatchRelease(%v) get stable deployment error: %v", c.releaseKey, err)
		return err
	}
	c.stable = stable
	return nil
}

// fetchCanaryDeployment fetch canary deployment to c.canary
func (c *DeploymentsRolloutController) fetchCanaryDeployment() error {
	var err error
	defer func() {
		if err != nil {
			klog.Errorf("BatchRelease(%v) get canary deployment error: %v", c.releaseKey, err)
		}
	}()

	err = c.fetchStableDeployment()
	if err != nil {
		return err
	}

	ds, err := c.listCanaryDeployment(client.InNamespace(c.stable.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return err
	}

	ds = util.FilterActiveDeployment(ds)
	sort.Slice(ds, func(i, j int) bool {
		return ds[i].CreationTimestamp.After(ds[j].CreationTimestamp.Time)
	})

	if len(ds) == 0 || !util.EqualIgnoreHash(&ds[0].Spec.Template, &c.stable.Spec.Template) {
		err = apierrors.NewNotFound(schema.GroupResource{
			Group:    apps.SchemeGroupVersion.Group,
			Resource: c.stable.Kind,
		}, fmt.Sprintf("%v-canary", c.canaryNamespacedName.Name))
		return err
	}

	c.canary = ds[0].DeepCopy()
	return nil
}

// recordDeploymentRevisionAndReplicas records stableRevision, canaryRevision, workloadReplicas to BatchRelease.Status
func (c *DeploymentsRolloutController) recordDeploymentRevisionAndReplicas() error {
	err := c.fetchStableDeployment()
	if err != nil {
		return err
	}

	updateRevision := util.ComputeHash(&c.stable.Spec.Template, nil)
	stableRevision, err := c.GetStablePodTemplateHash(c.stable)
	if err != nil {
		return err
	}
	c.releaseStatus.StableRevision = stableRevision
	c.releaseStatus.UpdateRevision = updateRevision
	c.releaseStatus.ObservedWorkloadReplicas = *c.stable.Spec.Replicas
	return nil
}

func (c *DeploymentsRolloutController) patchPodBatchLabel(canaryGoal int32) (bool, error) {
	rolloutID, exist := c.parentController.Labels[util.RolloutIDLabel]
	if !exist || rolloutID == "" || c.canary == nil {
		return true, nil
	}

	pods, err := util.ListOwnedPods(c.client, c.canary)
	if err != nil {
		klog.Errorf("Failed to list pods for Deployment %v", c.stableNamespacedName)
		return false, err
	}

	batchID := c.parentController.Status.CanaryStatus.CurrentBatch + 1
	updateRevision := c.parentController.Status.UpdateRevision
	return util.PatchPodBatchLabel(c.client, pods, rolloutID, batchID, updateRevision, canaryGoal, c.releaseKey)
}
