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

package statefulset

import (
	"context"
	"fmt"
	"math"

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
	object client.Object
}

func NewController(cli client.Client, key types.NamespacedName, gvk schema.GroupVersionKind) partitionstyle.Interface {
	return &realController{
		key:    key,
		gvk:    gvk,
		client: cli,
	}
}

func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

func (rc *realController) BuildController() (partitionstyle.Interface, error) {
	if rc.object != nil {
		return rc, nil
	}
	object := util.GetEmptyWorkloadObject(rc.gvk)
	if err := rc.client.Get(context.TODO(), rc.key, object); err != nil {
		return rc, err
	}
	rc.object = object
	rc.WorkloadInfo = util.ParseWorkload(object)

	// for native StatefulSet which has no updatedReadyReplicas field, we should
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

func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.pods != nil {
		return rc.pods, nil
	}
	var err error
	rc.pods, err = util.ListOwnedPods(rc.client, rc.object)
	return rc.pods, err
}

func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	owner := control.BuildReleaseControlInfo(release)
	metaBody := fmt.Sprintf(`"metadata":{"annotations":{"%s":"%s"}}`, util.BatchReleaseControlAnnotation, owner)
	specBody := fmt.Sprintf(`"spec":{"updateStrategy":{"rollingUpdate":{"partition":%d,"paused":false}}}`, math.MaxInt16)
	body := fmt.Sprintf(`{%s,%s}`, metaBody, specBody)

	clone := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))
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

	clone := rc.object.DeepCopyObject().(client.Object)
	return rc.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))
}

func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil
	}

	var specBody string
	// If batchPartition == nil, workload should be promoted;
	if release.Spec.ReleasePlan.BatchPartition == nil {
		specBody = `,"spec":{"updateStrategy":{"rollingUpdate":{"partition":null}}}`
	}

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}%s}`, util.BatchReleaseControlAnnotation, specBody)

	clone := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))
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
	// if we should consider the no-need-update pods that were marked before rolling, the desired will change
	if noNeedUpdate != nil && *noNeedUpdate > 0 {
		// specially, we should ignore the pods that were marked as no-need-update, this logic is for Rollback scene
		desiredUpdateNew := int32(control.CalculateBatchReplicas(release, int(rc.Replicas-*noNeedUpdate), int(currentBatch)))
		desiredStable = rc.Replicas - *noNeedUpdate - desiredUpdateNew
		desiredUpdate = rc.Replicas - desiredStable
	}

	// Note that:
	// * if ordered update, partition is related with pod ordinals;
	// * if unordered update, partition just like cloneSet partition.
	unorderedUpdate := util.IsStatefulSetUnorderedUpdate(rc.object)
	if !unorderedUpdate && noNeedUpdate != nil {
		desiredStable += *noNeedUpdate
		desiredUpdate = rc.Replicas - desiredStable + *noNeedUpdate
	}

	// if canaryReplicas is percentage, we should calculate its real
	batchContext := &batchcontext.BatchContext{
		Pods:             rc.pods,
		RolloutID:        rolloutID,
		CurrentBatch:     currentBatch,
		UpdateRevision:   release.Status.UpdateRevision,
		DesiredPartition: intstr.FromInt(int(desiredStable)),
		CurrentPartition: intstr.FromInt(int(util.GetStatefulSetPartition(rc.object))),
		FailureThreshold: release.Spec.ReleasePlan.FailureThreshold,

		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		NoNeedUpdatedReplicas:  noNeedUpdate,
		PlannedUpdatedReplicas: plannedUpdate,
		DesiredUpdatedReplicas: desiredUpdate,
	}

	if noNeedUpdate != nil {
		if unorderedUpdate {
			batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
		} else {
			batchContext.FilterFunc = labelpatch.FilterPodsForOrderedUpdate
		}
	}
	return batchContext, nil
}
