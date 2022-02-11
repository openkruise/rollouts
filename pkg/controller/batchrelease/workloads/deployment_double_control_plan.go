package workloads

import (
	"context"
	"fmt"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sort"

	"github.com/openkruise/rollouts/api/v1alpha1"
)

// DeploymentsRolloutController is responsible for handling rollout Deployment type of workloads
type DeploymentsRolloutController struct {
	deploymentController
	stable *apps.Deployment
	canary *apps.Deployment
}

//TODO: scale during releasing: workload replicas changed -> Finalising Deployment with Paused=true

// NewDeploymentRolloutController creates a new Deployment rollout controller
func NewDeploymentRolloutController(client client.Client, recorder record.EventRecorder, release *v1alpha1.BatchRelease, plan *v1alpha1.ReleasePlan, status *v1alpha1.BatchReleaseStatus, stableNamespacedName types.NamespacedName) *DeploymentsRolloutController {
	canaryNamespacedName := types.NamespacedName{
		Namespace: stableNamespacedName.Namespace,
		Name:      fmt.Sprintf("%v-canary", stableNamespacedName.Name),
	}
	return &DeploymentsRolloutController{
		deploymentController: deploymentController{
			workloadController: workloadController{
				client:           client,
				recorder:         recorder,
				parentController: release,
				releasePlan:      plan,
				releaseStatus:    status,
			},
			stableNamespacedName: stableNamespacedName,
			canaryNamespacedName: canaryNamespacedName,
		},
	}
}

// IfNeedToProgress verifies that the workload is ready to execute release plan
func (c *DeploymentsRolloutController) IfNeedToProgress() (bool, error) {
	var verifyErr error

	defer func() {
		if verifyErr != nil {
			klog.Error(verifyErr)
			c.recorder.Event(c.parentController, v1.EventTypeWarning, "VerifyFailed", verifyErr.Error())
		}
	}()

	if err := c.fetchStableDeployment(); err != nil {
		return false, nil
	}

	if c.stable.Status.ObservedGeneration != c.stable.Generation {
		klog.Warningf("Deployment is still reconciling, wait for it to be done")
		return false, nil
	}

	if c.stable.Status.UpdatedReplicas == *c.stable.Spec.Replicas {
		verifyErr = fmt.Errorf("update revision has been promoted, no need to reconcile")
		return false, verifyErr
	}

	if !c.stable.Spec.Paused {
		verifyErr = fmt.Errorf("deployment should be paused before execute the release plan")
		return false, verifyErr
	}

	if err := c.recordDeploymentRevisionAndReplicas(); err != nil {
		klog.Warningf("Failed to record deployment revision and replicas info, error: %v", err)
		return false, nil
	}

	klog.V(3).Infof("Verified Successfully, Status %+v", c.releaseStatus)
	c.recorder.Event(c.parentController, v1.EventTypeNormal, "RolloutVerified", "ReleasePlan and the Deployment resource are verified")
	return true, nil
}

// Prepare makes sure that the source and target Deployment is under our control
func (c *DeploymentsRolloutController) Prepare() (bool, error) {
	if err := c.fetchStableDeployment(); err != nil {
		//c.releaseStatus.RolloutRetry(err.Error())
		return false, nil
	}

	if err := c.fetchCanaryDeployment(); client.IgnoreNotFound(err) != nil {
		//c.releaseStatus.RolloutRetry(err.Error())
		return false, nil
	}

	if _, err := c.claimDeployment(c.stable, c.canary); err != nil {
		return false, nil
	}

	c.recorder.Event(c.parentController, v1.EventTypeNormal, "Rollout Initialized", "Rollout resource are initialized")
	return true, nil
}

// RolloutOneBatchPods calculates the number of pods we can upgrade once according to the rollout spec
// and then set the partition accordingly
func (c *DeploymentsRolloutController) RolloutOneBatchPods() (bool, error) {
	if err := c.fetchStableDeployment(); err != nil {
		return false, nil
	}

	if err := c.fetchCanaryDeployment(); err != nil {
		return false, nil
	}

	canaryGoal := c.calculateCurrentTarget(c.releaseStatus.ObservedWorkloadReplicas)
	canaryReplicas := *c.canary.Spec.Replicas
	if canaryReplicas >= canaryGoal {
		klog.V(3).InfoS("upgraded one batch, but no need to update replicas of canart Deployment", "current batch",
			c.releaseStatus.CanaryStatus.CurrentBatch, "goal canary replicas", canaryGoal, "real canary replicas", canaryReplicas, "real canary pod count", c.canary.Status.UpdatedReplicas)
		return true, nil
	}

	if err := c.patchCanaryReplicas(c.canary, canaryGoal); err != nil {
		return false, nil
	}

	klog.V(3).InfoS("upgraded one batch", "current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "canary goal size", canaryGoal)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "Batch Rollout", "Finished submitting all upgrade quests for batch %d", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// CheckOneBatchPods checks to see if the pods are all available according to the rollout plan
func (c *DeploymentsRolloutController) CheckOneBatchPods() (bool, error) {
	if err := c.fetchStableDeployment(); err != nil {
		//c.releaseStatus.RolloutRetry(err.Error())
		return false, nil
	}

	if err := c.fetchCanaryDeployment(); err != nil {
		return false, nil
	}

	if c.canary.Status.ObservedGeneration != c.canary.Generation {
		return false, nil
	}

	canaryPodCount := c.canary.Status.Replicas
	availableCanaryPodCount := c.canary.Status.AvailableReplicas
	canaryGoal := c.calculateCurrentTarget(c.releaseStatus.ObservedWorkloadReplicas)

	c.releaseStatus.CanaryStatus.UpdatedReplicas = canaryPodCount
	c.releaseStatus.CanaryStatus.UpdatedReadyReplicas = availableCanaryPodCount

	maxUnavailable := 0
	if c.canary.Spec.Strategy.RollingUpdate != nil &&
		c.canary.Spec.Strategy.RollingUpdate.MaxUnavailable != nil {
		maxUnavailable, _ = intstr.GetValueFromIntOrPercent(c.canary.Spec.Strategy.RollingUpdate.MaxUnavailable, int(c.releaseStatus.ObservedWorkloadReplicas), true)
	}

	klog.InfoS("checking the batch releasing progress", "current batch", c.releaseStatus.CanaryStatus.CurrentBatch,
		"canary pod available count", availableCanaryPodCount, "stable pod count", c.stable.Status.Replicas,
		"max unavailable pod allowed", maxUnavailable, "canary goal", canaryGoal)

	if canaryGoal > canaryPodCount || availableCanaryPodCount+int32(maxUnavailable) < canaryGoal {
		klog.InfoS("the batch is not ready yet", "current batch", c.releaseStatus.CanaryStatus.CurrentBatch)
		return false, nil
	}

	klog.InfoS("all pods in current batch are ready", "current batch", c.releaseStatus.CanaryStatus.CurrentBatch)
	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "Batch Available", "Batch %d is available", c.releaseStatus.CanaryStatus.CurrentBatch)
	return true, nil
}

// FinalizeOneBatch makes sure that the rollout status are updated correctly
func (c *DeploymentsRolloutController) FinalizeOneBatch() (bool, error) {
	return true, nil
}

// Finalize makes sure the Deployment is all upgraded
func (c *DeploymentsRolloutController) Finalize(pause, cleanup bool) bool {
	if err := c.fetchStableDeployment(); client.IgnoreNotFound(err) != nil {
		return false
	}

	if _, err := c.releaseDeployment(c.stable, pause, cleanup); err != nil {
		return false
	}

	c.recorder.Eventf(c.parentController, v1.EventTypeNormal, "Finalized", "Finalized: "+
		"paused=%v, cleanup=%v", pause, cleanup)
	return true
}

// WatchWorkload return change type if workload was changed during release
func (c *DeploymentsRolloutController) WatchWorkload() (WorkloadChangeEventType, *WorkloadAccessor, error) {
	if c.parentController.Spec.Cancelled ||
		c.parentController.DeletionTimestamp != nil ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseFinalizing ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseRollback ||
		c.releaseStatus.Phase == v1alpha1.RolloutPhaseTerminating {
		return IgnoreWorkloadEvent, nil, nil
	}

	var err error
	workloadInfo := &WorkloadAccessor{}
	err = c.fetchStableDeployment()
	if err != nil {
		return "", nil, err
	}

	err = c.fetchCanaryDeployment()
	switch {
	case client.IgnoreNotFound(err) != nil:
		return "", nil, err
	case apierrors.IsNotFound(err):
		workloadInfo.Status = &Status{}
	default:
		workloadInfo.Status = &Status{
			UpdatedReplicas:      c.canary.Status.Replicas,
			UpdatedReadyReplicas: c.canary.Status.AvailableReplicas,
		}
	}

	if c.canary != nil && c.canary.DeletionTimestamp != nil &&
		controllerutil.ContainsFinalizer(c.canary, CanaryDeploymentFinalizer) {
		return WorkloadUnHealthy, workloadInfo, nil
	}

	if c.stable.Status.ObservedGeneration != c.stable.Generation {
		klog.Warningf("Deployment is still reconciling, waiting for it to complete, generation: %v, observed: %v",
			c.stable.Generation, c.stable.Status.ObservedGeneration)
		return WorkloadStillReconciling, workloadInfo, nil
	}

	if !c.stable.Spec.Paused && c.stable.Status.UpdatedReplicas == c.stable.Status.Replicas {
		return IgnoreWorkloadEvent, workloadInfo, nil
	}

	var updateRevision string
	switch c.releaseStatus.Phase {
	case v1alpha1.RolloutPhaseInitial, v1alpha1.RolloutPhaseHealthy:
		return IgnoreWorkloadEvent, workloadInfo, nil

	default:
		if isRollingBack, err := c.isRollingBack(); err != nil {
			return "", workloadInfo, err
		} else if isRollingBack {
			workloadInfo.UpdateRevision = &updateRevision
			return WorkloadRollback, workloadInfo, nil
		}
		if *c.stable.Spec.Replicas != c.releaseStatus.ObservedWorkloadReplicas {
			workloadInfo.Replicas = c.stable.Spec.Replicas
			klog.Warningf("Deployment replicas changed during releasing, should pause and wait for it to complete, replicas from: %v -> %v",
				c.releaseStatus.ObservedWorkloadReplicas, *c.stable.Spec.Replicas)
			return WorkloadReplicasChanged, workloadInfo, nil
		}
		fallthrough

	case v1alpha1.RolloutPhaseCompleted, v1alpha1.RolloutPhaseCancelled:
		_, err = c.GetPodTemplateHash(c.stable, Latest)
		if (c.canary == nil || !EqualIgnoreHash(&c.stable.Spec.Template, &c.canary.Spec.Template)) && apierrors.IsNotFound(err) {
			workloadInfo.UpdateRevision = &updateRevision
			klog.Warning("Deployment updateRevision changed during releasing, should try to restart the release plan")
			return WorkloadPodTemplateChanged, workloadInfo, nil
		}
	}

	return IgnoreWorkloadEvent, workloadInfo, nil
}

func (c *DeploymentsRolloutController) isRollingBack() (bool, error) {
	rss, err := c.listReplicaSetsFor(c.stable)
	if err != nil {
		return false, err
	}
	for _, rs := range rss {
		if c.releaseStatus.StableRevision != "" && *rs.Spec.Replicas > 0 &&
			c.releaseStatus.StableRevision == rs.Labels[apps.DefaultDeploymentUniqueLabelKey] &&
			EqualIgnoreHash(&rs.Spec.Template, &c.stable.Spec.Template) {
			return true, nil
		}
	}
	return false, nil
}

func (c *DeploymentsRolloutController) fetchStableDeployment() error {
	if c.stable != nil {
		return nil
	}

	stable := &apps.Deployment{}
	if err := c.client.Get(context.TODO(), c.stableNamespacedName, stable); err != nil {
		if !apierrors.IsNotFound(err) {
			c.recorder.Event(c.parentController, v1.EventTypeWarning, "GetStableDeploymentFailed", err.Error())
		}
		return err
	}
	c.stable = stable
	return nil
}

func (c *DeploymentsRolloutController) fetchCanaryDeployment() error {
	err := c.fetchStableDeployment()
	if err != nil {
		return err
	}

	ds, err := c.listCanaryDeployment(client.InNamespace(c.stable.Namespace),
		client.MatchingLabels(map[string]string{CanaryDeploymentLabelKey: string(c.stable.UID)}))
	if err != nil {
		return err
	}

	sort.Slice(ds, func(i, j int) bool {
		return ds[j].CreationTimestamp.Before(&ds[i].CreationTimestamp)
	})

	if len(ds) == 0 || !EqualIgnoreHash(&ds[0].Spec.Template, &c.stable.Spec.Template) {
		err := apierrors.NewNotFound(schema.GroupResource{
			Group:    apps.SchemeGroupVersion.Group,
			Resource: c.stable.Kind,
		}, c.canaryNamespacedName.Name)
		c.recorder.Event(c.parentController, v1.EventTypeWarning, "GetCanaryDeploymentFailed", err.Error())
		return err
	}

	c.canary = ds[0]
	return nil
}

// the target workload size for the current batch
func (c *DeploymentsRolloutController) calculateCurrentTarget(totalSize int32) int32 {
	targetSize := int32(calculateNewBatchTarget(c.releasePlan, int(totalSize), int(c.releaseStatus.CanaryStatus.CurrentBatch)))
	klog.InfoS("Calculated the number of pods in the canary Deployment after current batch",
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload updateRevision size", targetSize)
	return targetSize
}

// the source workload size for the current batch
func (c *DeploymentsRolloutController) calculateCurrentSource(totalSize int32) int32 {
	sourceSize := totalSize - c.calculateCurrentTarget(totalSize)
	klog.InfoS("Calculated the number of pods in the stable Deployment after current batch",
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload stableRevision size", sourceSize)
	return sourceSize
}

func (c *DeploymentsRolloutController) recordDeploymentRevisionAndReplicas() error {
	err := c.fetchStableDeployment()
	if err != nil {
		return err
	}

	err = c.fetchCanaryDeployment()
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	var claimErr error
	c.canary, claimErr = c.claimDeployment(c.stable, c.canary)
	if claimErr != nil {
		return claimErr
	}

	c.releaseStatus.StableRevision, err = c.GetPodTemplateHash(c.stable, Stable)
	if err != nil {
		return err
	}
	c.releaseStatus.UpdateRevision, err = c.GetPodTemplateHash(c.canary, Latest)
	if err != nil {
		return err
	}
	c.releaseStatus.ObservedWorkloadReplicas = *c.stable.Spec.Replicas
	return nil
}

type PodTemplateHashType string

const (
	Latest PodTemplateHashType = "Latest"
	Stable PodTemplateHashType = "Stable"
)

func (c *DeploymentsRolloutController) GetPodTemplateHash(deploy *apps.Deployment, kind PodTemplateHashType) (string, error) {
	switch kind {
	case Latest, Stable:
		if deploy == nil {
			return "", fmt.Errorf("workload cannot be found, may be deleted or not be created yet")
		}
	default:
		panic("wrong kind type, must be 'stable' or 'canary'")
	}

	rss, err := c.listReplicaSetsFor(deploy)
	if err != nil {
		return "", err
	}

	sort.Slice(rss, func(i, j int) bool {
		return rss[i].CreationTimestamp.Before(&rss[j].CreationTimestamp)
	})

	for _, rs := range rss {
		switch kind {
		case Stable:
			if rs.Spec.Replicas != nil && *rs.Spec.Replicas > 0 {
				return rs.Labels[apps.DefaultDeploymentUniqueLabelKey], nil
			}
		case Latest:
			if EqualIgnoreHash(&deploy.Spec.Template, &rs.Spec.Template) {
				return rs.Labels[apps.DefaultDeploymentUniqueLabelKey], nil
			}
		}
	}

	notFoundErr := apierrors.NewNotFound(schema.GroupResource{
		Group:    apps.SchemeGroupVersion.Group,
		Resource: fmt.Sprintf("%v-ReplicaSet", kind),
	}, c.canaryNamespacedName.Name)

	return "", notFoundErr
}

func (c *DeploymentsRolloutController) listReplicaSetsFor(deploy *apps.Deployment) ([]*apps.ReplicaSet, error) {
	deploySelector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return nil, err
	}

	rsList := &apps.ReplicaSetList{}
	err = c.client.List(context.TODO(), rsList, &client.ListOptions{
		Namespace:     deploy.Namespace,
		LabelSelector: deploySelector,
	})
	if err != nil {
		return nil, err
	}

	var rss []*apps.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if rs.DeletionTimestamp != nil {
			continue
		}
		if owner := metav1.GetControllerOf(rs); owner == nil || owner.UID != deploy.UID {
			continue
		}
		rss = append(rss, rs)
	}
	return rss, nil
}
