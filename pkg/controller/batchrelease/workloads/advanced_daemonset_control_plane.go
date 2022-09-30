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
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AdvancedDaemonSetController is responsible for handling rollout Advanced DaemonSet type of workloads
type AdvancedDaemonSetController struct {
	advancedDaemonSetController
	daemonSet *kruiseappsv1alpha1.DaemonSet
}

// NewAdvancedDaemonSetController creates a new AdvancedDaemonSet rollout controller
func NewAdvancedDaemonSetController(cli client.Client, recorder record.EventRecorder, release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus, targetNamespacedName types.NamespacedName) *AdvancedDaemonSetController {
	return &AdvancedDaemonSetController{
		advancedDaemonSetController: advancedDaemonSetController{
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
func (c *AdvancedDaemonSetController) VerifyWorkload() (bool, error) {
	var err error
	var message string
	defer func() {
		if err != nil {
			c.recorder.Event(c.release, v1.EventTypeWarning, "VerifyFailed", err.Error())
		} else if message != "" {
			klog.Warningf(message)
		}
	}()

	if err = c.fetchAdvanceDaemonSet(); err != nil {
		return false, err
	}

	// if the workload status is untrustworthy, return and retry
	if c.daemonSet.Status.ObservedGeneration != c.daemonSet.Generation {
		message = fmt.Sprintf("advancedDaemonset(%v) is still reconciling, wait for it to be done", c.targetNamespacedName)
		return false, nil
	}

	// if the workload has been promoted, return and not retry
	if c.daemonSet.Status.UpdatedNumberScheduled == *&c.daemonSet.Status.DesiredNumberScheduled {
		message = fmt.Sprintf("advancedDaemonset(%v) update revision has been promoted, no need to rollout", c.targetNamespacedName)
		return false, nil
	}

	// if the workload is not paused, no need to progress it
	if !*c.daemonSet.Spec.UpdateStrategy.RollingUpdate.Paused {
		message = fmt.Sprintf("advancedDaemonset(%v) should be paused before execute the release plan", c.targetNamespacedName)
		return false, nil
	}

	c.recorder.Event(c.release, v1.EventTypeNormal, "Verified", "ReleasePlan and the AdvancedDaemonset resource are verified")
	return true, nil
}

// PrepareBeforeProgress makes sure that the source and target AdvancedDaemonSet is under our control
func (c *AdvancedDaemonSetController) PrepareBeforeProgress() (bool, error) {
	if err := c.fetchAdvanceDaemonSet(); err != nil {
		return false, err
	}

	// claim the AdvancedDaemonSet is under our control
	if _, err := c.claimAdvancedDaemonSet(c.daemonSet); err != nil {
		return false, err
	}

	// record revision info to BatchRelease.Status
	c.recordAdvancedDaemonSetRevision()

	c.recorder.Event(c.release, v1.EventTypeNormal, "InitializedSuccessfully", "Rollout resource are initialized")
	return true, nil
}

// UpgradeOneBatch calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *AdvancedDaemonSetController) UpgradeOneBatch() (bool, error) {
	if err := c.fetchAdvanceDaemonSet(); err != nil {
		return false, err
	}
	// todo

	c.recorder.Eventf(c.release, v1.EventTypeNormal, "SetBatchDone",
		"Finished submitting all upgrade quests for batch %d", c.newStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchReady checks to see if the pods are all available according to the rollout plan
func (c *AdvancedDaemonSetController) CheckOneBatchReady() (bool, error) {

	return true, nil
}

// FinalizeProgress makes sure the AdvancedDaemonSet is all upgraded
func (c *AdvancedDaemonSetController) FinalizeProgress(cleanup bool) (bool, error) {

	c.recorder.Eventf(c.release, v1.EventTypeNormal, "FinalizedSuccessfully", "Rollout resource are finalized: cleanup=%v", cleanup)
	return true, nil
}

// SyncWorkloadInfo return change type if workload was changed during release
func (c *AdvancedDaemonSetController) SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if c.release.DeletionTimestamp != nil {
		return IgnoreWorkloadEvent, nil, nil
	}

	if err := c.fetchAdvanceDaemonSet(); err != nil {
		if apierrors.IsNotFound(err) {
			return WorkloadHasGone, nil, err
		}
		return "", nil, err
	}

	return IgnoreWorkloadEvent, nil, nil
}

// fetchAdvancedDaemonSet fetch advancedDaemonSet to c.daemonSet
func (c *AdvancedDaemonSetController) fetchAdvanceDaemonSet() error {
	daemonSet := &kruiseappsv1alpha1.DaemonSet{}
	if err := c.client.Get(context.TODO(), c.targetNamespacedName, daemonSet); err != nil {
		if !apierrors.IsNotFound(err) {
			c.recorder.Event(c.release, v1.EventTypeWarning, "GetDaemonSetFailed", err.Error())
		}
		return err
	}
	c.daemonSet = daemonSet
	return nil
}

func (c *AdvancedDaemonSetController) patchPodBatchLabel(canaryGoal int32) (bool, error) {
	rolloutID, exist := c.release.Labels[util.RolloutIDLabel]
	if !exist || rolloutID == "" {
		return true, nil
	}

	pods, err := util.ListOwnedPods(c.client, c.daemonSet)
	if err != nil {
		klog.Errorf("Failed to list pods for CloneSet %v", c.targetNamespacedName)
		return false, err
	}

	batchID := c.release.Status.CanaryStatus.CurrentBatch + 1
	updateRevision := c.release.Status.UpdateRevision
	return util.PatchPodBatchLabel(c.client, pods, rolloutID, batchID, updateRevision, canaryGoal, c.releasePlanKey)
}

func (c *AdvancedDaemonSetController) recordAdvancedDaemonSetRevision() {
	c.newStatus.UpdateRevision = c.daemonSet.Status.DaemonSetHash
}
