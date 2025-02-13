package daemonset

import (
	"context"
	"fmt"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	gvk    schema.GroupVersionKind
	object *kruiseappsv1alpha1.DaemonSet
	//
}

func NewController(cli client.Client, key types.NamespacedName, gvk schema.GroupVersionKind) partitionstyle.Interface {
	return &realController{
		key:    key,
		client: cli,
		gvk:    gvk,
	}
}

// GetWorkloadInfo return workload information.
func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

// BuildController will get workload object and parse workload info,
// and return a initialized controller for workload.
func (rc *realController) BuildController() (partitionstyle.Interface, error) {
	if rc.object != nil {
		return rc, nil
	}
	object := &kruiseappsv1alpha1.DaemonSet{}
	if err := rc.client.Get(context.TODO(), rc.key, object); err != nil {
		return rc, err
	}
	rc.object = object

	//update this function
	rc.WorkloadInfo = util.ParseWorkload(object)

	// for Advanced DaemonSet which has no updatedReadyReplicas field, we should
	// list and count its owned Pods one by one.
	if rc.WorkloadInfo != nil && rc.WorkloadInfo.Status.UpdatedReadyReplicas <= 0 {
		pods, err := rc.ListOwnedPods()
		if err != nil {
			return nil, err
		}
		updatedReadyReplicas := util.WrappedPodCount(pods, func(pod *corev1.Pod) bool {
			if !pod.DeletionTimestamp.IsZero() {
				return false
			}
			if !util.IsConsistentWithRevision(pod.GetLabels(), rc.WorkloadInfo.Status.UpdateRevision) {
				return false
			}
			return util.IsPodReady(pod)
		})
		rc.WorkloadInfo.Status.UpdatedReadyReplicas = int32(updatedReadyReplicas)
	}
	return rc, nil
}

// ListOwnedPods fetch the pods owned by the workload.
// Note that we should list pod only if we really need it.
func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.pods != nil {
		return rc.pods, nil
	}
	var err error
	// update this
	rc.pods, err = util.ListOwnedPods(rc.client, rc.object)
	return rc.pods, err
}

func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	daemon := util.GetEmptyObjectWithKey(rc.object)
	owner := control.BuildReleaseControlInfo(release)
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}},"spec":{"updateStrategy":{"rollingUpdate":{"paused":%v,"partition":%d}}}}`,
		util.BatchReleaseControlAnnotation, owner, false, rc.Replicas)

	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body)))
}

func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {

	desired := ctx.DesiredPartition.IntVal
	current := ctx.CurrentPartition.IntVal
	// current less than desired, which means current revision replicas will be less than desired,
	// in other word, update revision replicas will be more than desired, no need to update again.
	if current <= desired {
		return nil
	}

	body := fmt.Sprintf(`{"spec":{"updateStrategy":{"rollingUpdate":{"partition":%d}}}}`, desired)

	daemon := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body)))
}

func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil
	}

	var specBody string
	// if batchPartition == nil, workload should be promoted.
	if release.Spec.ReleasePlan.BatchPartition == nil {
		specBody = `,"spec":{"updateStrategy":{"rollingUpdate": {"partition":null,"paused":false}}}`
	}

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}%s}`, util.BatchReleaseControlAnnotation, specBody)

	daemon := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body)))
}

func (rc *realController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	if rolloutID != "" {
		// if rollout-id is set, the pod will be patched batch label,
		// so we have to list pod here.
		if _, err := rc.ListOwnedPods(); err != nil {
			return nil, err
		}
	}

	// current batch index
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	// the number of no need update pods that marked before rollout
	noNeedUpdate := release.Status.CanaryStatus.NoNeedUpdateReplicas
	// the number of upgraded pods according to release plan in current batch.
	plannedUpdate := int32(control.CalculateBatchReplicas(release, int(rc.Replicas), int(currentBatch)))
	// the number of pods that should be upgraded in real
	desiredUpdate := plannedUpdate
	// the number of pods that should not be upgraded in real
	desiredStable := rc.Replicas - desiredUpdate
	// if we should consider the no-need-update pods that were marked before progressing
	if noNeedUpdate != nil && *noNeedUpdate > 0 {
		// specially, we should ignore the pods that were marked as no-need-update, this logic is for Rollback scene
		desiredUpdateNew := int32(control.CalculateBatchReplicas(release, int(rc.Replicas-*noNeedUpdate), int(currentBatch)))
		desiredStable = rc.Replicas - *noNeedUpdate - desiredUpdateNew
		desiredUpdate = rc.Replicas - desiredStable
	}

	// make sure at least one pod is upgrade is canaryReplicas is not "0%"
	desiredPartition := intstr.FromInt(int(desiredStable))
	if desiredStable <= 0 {
		desiredPartition = intstr.FromInt(0)
	}

	currentPartition := intstr.FromInt(0)
	if rc.object.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
		intPartition := *rc.object.Spec.UpdateStrategy.RollingUpdate.Partition
		currentPartition = intstr.FromInt(int(intPartition))
	}

	batchContext := &batchcontext.BatchContext{
		Pods:                   rc.pods,
		RolloutID:              rolloutID,
		CurrentBatch:           currentBatch,
		UpdateRevision:         release.Status.UpdateRevision,
		DesiredPartition:       desiredPartition,
		CurrentPartition:       currentPartition,
		FailureThreshold:       release.Spec.ReleasePlan.FailureThreshold,
		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		NoNeedUpdatedReplicas:  noNeedUpdate,
		PlannedUpdatedReplicas: plannedUpdate,
		DesiredUpdatedReplicas: desiredUpdate,
	}

	if noNeedUpdate != nil {
		batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
	}
	return batchContext, nil
}
