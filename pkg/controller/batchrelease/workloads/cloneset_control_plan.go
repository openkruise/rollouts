package workloads

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
)

// CloneSetRolloutController is responsible for handling rollout CloneSet type of workloads
type CloneSetRolloutController struct {
	cloneSetController
	clone *kruiseappsv1alpha1.CloneSet
}

//TODO: scale during releasing: workload replicas changed -> Finalising CloneSet with Paused=true

// NewCloneSetRolloutController creates a new CloneSet rollout controller
func NewCloneSetRolloutController(client client.Client, recorder record.EventRecorder, release *v1alpha1.BatchRelease, plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus, targetNamespacedName types.NamespacedName) *CloneSetRolloutController {
	return &CloneSetRolloutController{
		cloneSetController: cloneSetController{
			workloadController: workloadController{
				client:           client,
				recorder:         recorder,
				parentController: release,
				releasePlan:      plan,
				releaseStatus:    status,
			},
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

	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		klog.Warningf("CloneSet is still reconciling, wait for it to be done")
		return false, nil
	}

	if c.clone.Status.UpdatedReplicas == *c.clone.Spec.Replicas {
		verifyErr = fmt.Errorf("update revision has been promoted, no need to reconcile")
		return false, verifyErr
	}

	if !c.clone.Spec.UpdateStrategy.Paused && !IsControlledBy(c.clone, c.parentController) {
		verifyErr = fmt.Errorf("cloneSet should be paused before execute the release plan")
		return false, verifyErr
	}

	c.recordCloneSetRevisionAndReplicas()
	klog.V(3).Infof("Verified Successfully, Status %+v", c.releaseStatus)
	c.recorder.Event(c.parentController, v1.EventTypeNormal, "VerifiedSuccessfully", "ReleasePlan and the CloneSet resource are verified")
	return true, nil
}

// Prepare makes sure that the source and target CloneSet is under our control
func (c *CloneSetRolloutController) Prepare() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		//c.releaseStatus.RolloutRetry(err.Error())
		return false, nil
	}

	if _, err := c.claimCloneSet(c.clone); err != nil {
		return false, nil
	}

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, nil
}

// RolloutOneBatchPods calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *CloneSetRolloutController) RolloutOneBatchPods() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, nil
	}

	updateSize := c.calculateCurrentTarget(c.releaseStatus.ObservedWorkloadReplicas)
	stableSize := c.calculateCurrentSource(c.releaseStatus.ObservedWorkloadReplicas)
	workloadPartition, _ := intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.Partition,
		int(c.releaseStatus.ObservedWorkloadReplicas), true)

	if c.clone.Status.UpdatedReplicas >= updateSize && int32(workloadPartition) <= stableSize {
		klog.V(3).InfoS("upgraded one batch, but no need to update partition of cloneset", "current batch",
			c.releaseStatus.CanaryStatus.CurrentBatch, "real updateRevision replicas", c.clone.Status.UpdatedReplicas)
		return true, nil
	}

	if err := c.patchCloneSetPartition(c.clone, stableSize); err != nil {
		return false, nil
	}

	klog.V(3).InfoS("upgraded one batch", "current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "updateRevision size", updateSize)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "SetBatchDone", "Finished submitting all upgrade quests for batch %d", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchPods checks to see if the pods are all available according to the rollout plan
func (c *CloneSetRolloutController) CheckOneBatchPods() (bool, error) {
	if err := c.fetchCloneSet(); err != nil {
		return false, nil
	}

	// wait for cloneSet controller to watch update event
	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		return false, nil
	}

	updatePodCount := c.clone.Status.UpdatedReplicas
	stablePodCount := c.clone.Status.Replicas - updatePodCount
	readyUpdatePodCount := c.clone.Status.UpdatedReadyReplicas
	updateGoal := c.calculateCurrentTarget(c.releaseStatus.ObservedWorkloadReplicas)
	stableGoal := c.calculateCurrentSource(c.releaseStatus.ObservedWorkloadReplicas)

	c.releaseStatus.CanaryStatus.UpdatedReplicas = updatePodCount
	c.releaseStatus.CanaryStatus.UpdatedReadyReplicas = readyUpdatePodCount

	maxUnavailable := 0
	if c.clone.Spec.UpdateStrategy.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetValueFromIntOrPercent(c.clone.Spec.UpdateStrategy.MaxUnavailable, int(c.releaseStatus.ObservedWorkloadReplicas), true)
	}

	klog.InfoS("checking the batch releasing progress", "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch,
		"target-pod-ready-count", readyUpdatePodCount, "source-pod-count", stablePodCount,
		"max-unavailable-pod-allowed", maxUnavailable, "target-goal", updateGoal, "source-goal", stableGoal)

	if c.clone.Status.Replicas != c.releaseStatus.ObservedWorkloadReplicas {
		err := fmt.Errorf("CloneSet replicas don't match ObservedWorkloadReplicas, sourceTarget = %d, targetTarget = %d, "+
			"rolloutTargetSize = %d", stablePodCount, updatePodCount, c.releaseStatus.ObservedWorkloadReplicas)
		klog.ErrorS(err, "the batch is not valid", "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	if updateGoal > updatePodCount || stableGoal < stablePodCount || readyUpdatePodCount+int32(maxUnavailable) < updateGoal {
		klog.InfoS("the batch is not ready yet", "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	klog.InfoS("All pods in current batch are ready", "current-batch", c.releaseStatus.CanaryStatus.CurrentBatch)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "BatchAvailable", "Batch %d is available", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// FinalizeOneBatch makes sure that the rollout status are updated correctly
func (c *CloneSetRolloutController) FinalizeOneBatch() (bool, error) {
	return true, nil
}

// Finalize makes sure the CloneSet is all upgraded
func (c *CloneSetRolloutController) Finalize(pause, cleanup bool) bool {
	if err := c.fetchCloneSet(); client.IgnoreNotFound(err) != nil {
		return false
	}

	if _, err := c.releaseCloneSet(c.clone, pause, cleanup); err != nil {
		return false
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized, "+
		"pause = %v, cleanup = %v", pause, cleanup)
	return true
}

// WatchWorkload return change type if workload was changed during release
func (c *CloneSetRolloutController) WatchWorkload() (WorkloadChangeEventType, *WorkloadAccessor, error) {
	if c.parentController.Spec.Cancelled ||
		c.parentController.DeletionTimestamp != nil ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseFinalizing ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseRollback ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseTerminating {
		return IgnoreWorkloadEvent, nil, nil
	}

	workloadInfo := &WorkloadAccessor{}
	err := c.fetchCloneSet()
	if client.IgnoreNotFound(err) != nil {
		return "", nil, err
	} else if apierrors.IsNotFound(err) {
		workloadInfo.Status = &Status{}
		return "", workloadInfo, err
	}

	if c.clone.Status.ObservedGeneration != c.clone.Generation {
		klog.Warningf("CloneSet is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			c.clone.Generation, c.clone.Status.ObservedGeneration)
		return WorkloadStillReconciling, nil, nil
	}

	workloadInfo.Status = &Status{
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
			klog.Warning("CloneSet is stable or is rolling back, release plan should stop")
			return WorkloadRollback, workloadInfo, nil
		}
		if *c.clone.Spec.Replicas != c.releaseStatus.ObservedWorkloadReplicas {
			workloadInfo.Replicas = c.clone.Spec.Replicas
			klog.Warningf("CloneSet replicas changed during releasing, should pause and wait for it to complete, replicas from: %v -> %v",
				c.releaseStatus.ObservedWorkloadReplicas, *c.clone.Spec.Replicas)
			return WorkloadReplicasChanged, workloadInfo, nil
		}
		fallthrough

	case v1alpha1.RolloutPhaseCompleted, v1alpha1.RolloutPhaseCancelled:
		if c.clone.Status.UpdateRevision != c.releaseStatus.UpdateRevision {
			workloadInfo.UpdateRevision = &c.clone.Status.UpdateRevision
			klog.Warningf("CloneSet updateRevision changed during releasing, should try to restart the release plan, updateRevision from: %v -> %v",
				c.releaseStatus.UpdateRevision, c.clone.Status.UpdateRevision)
			return WorkloadPodTemplateChanged, workloadInfo, nil
		}

	case v1alpha1.RolloutPhaseHealthy, v1alpha1.RolloutPhaseInitial:
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

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

// the target workload size for the current batch
func (c *CloneSetRolloutController) calculateCurrentTarget(totalSize int32) int32 {
	targetSize := int32(calculateNewBatchTarget(c.releasePlan, int(totalSize), int(c.releaseStatus.CanaryStatus.CurrentBatch)))
	klog.InfoS("Calculated the number of pods in the target CloneSet after current batch",
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload updateRevision size", targetSize)
	return targetSize
}

// the source workload size for the current batch
func (c *CloneSetRolloutController) calculateCurrentSource(totalSize int32) int32 {
	sourceSize := totalSize - c.calculateCurrentTarget(totalSize)
	klog.InfoS("Calculated the number of pods in the source CloneSet after current batch",
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload stableRevision size", sourceSize)
	return sourceSize
}

func (c *CloneSetRolloutController) recordCloneSetRevisionAndReplicas() {
	c.releaseStatus.ObservedWorkloadReplicas = *c.clone.Spec.Replicas
	c.releaseStatus.StableRevision = c.clone.Status.CurrentRevision
	c.releaseStatus.UpdateRevision = c.clone.Status.UpdateRevision
}
