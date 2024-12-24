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

package deployment

import (
	"context"
	"fmt"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/bluegreenstyle"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/bluegreenstyle/hpa"
	deploymentutil "github.com/openkruise/rollouts/pkg/controller/deployment/util"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/errors"
	"github.com/openkruise/rollouts/pkg/util/patch"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	object *apps.Deployment
	finder *util.ControllerFinder
}

func NewController(cli client.Client, key types.NamespacedName, _ schema.GroupVersionKind) bluegreenstyle.Interface {
	return &realController{
		key:    key,
		client: cli,
		finder: util.NewControllerFinder(cli),
	}
}

func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

func (rc *realController) BuildController() (bluegreenstyle.Interface, error) {
	if rc.object != nil {
		return rc, nil
	}
	object := &apps.Deployment{}
	if err := rc.client.Get(context.TODO(), rc.key, object); err != nil {
		return rc, err
	}
	rc.object = object
	rc.WorkloadInfo = rc.getWorkloadInfo(object)
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

// Initialize prepares the Deployment for the BatchRelease process
func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if rc.object == nil || control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}
	// Disable the HPA
	if err := hpa.DisableHPA(rc.client, rc.object); err != nil {
		return err
	}
	klog.InfoS("Initialize: disabled HPA for deployment successfully", "deployment", klog.KObj(rc.object))

	// Patch minReadySeconds for stable ReplicaSet
	if err := rc.patchStableRSMinReadySeconds(v1beta1.MaxReadySeconds); err != nil {
		return err
	}
	klog.InfoS("Initialize: patched minReadySeconds for stable replicaset successfully", "deployment", klog.KObj(rc.object))

	// Patch Deplopyment
	if err := rc.patchDeployment(release); err != nil {
		return err
	}
	klog.InfoS("Initialize: patched deployment successfully", "deployment", klog.KObj(rc.object))
	return nil
}

func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	if err := control.ValidateReadyForBlueGreenRelease(rc.object); err != nil {
		return errors.NewBadRequestError(fmt.Errorf("cannot upgrade batch, because deployment %v doesn't satisfy conditions: %s", klog.KObj(rc.object), err.Error()))
	}
	desired, _ := intstr.GetScaledValueFromIntOrPercent(&ctx.DesiredSurge, int(ctx.Replicas), true)
	current, _ := intstr.GetScaledValueFromIntOrPercent(&ctx.CurrentSurge, int(ctx.Replicas), true)

	if current >= desired {
		klog.Infof("No need to upgrade batch for deployment %v: because current %d >= desired %d", klog.KObj(rc.object), current, desired)
		return nil
	}
	klog.Infof("Ready to upgrade batch for deployment %v: current %d < desired %d", klog.KObj(rc.object), current, desired)
	patchData := patch.NewDeploymentPatch()
	// different with canary release, bluegreen don't need to set pause in the process of rollout
	// because our webhook may pause the Deployment in some situations, we ensure that the Deployment is not paused
	patchData.UpdatePaused(false)
	patchData.UpdateStrategy(apps.DeploymentStrategy{
		Type: apps.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &apps.RollingUpdateDeployment{
			MaxSurge:       &ctx.DesiredSurge,
			MaxUnavailable: &intstr.IntOrString{},
		},
	})
	return rc.client.Patch(context.TODO(), util.GetEmptyObjectWithKey(rc.object), patchData)
}

// set pause to false, restore the original setting, delete annotation
func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if release.Spec.ReleasePlan.BatchPartition != nil {
		// continuous release (not supported yet)
		/*
			patchData := patch.NewDeploymentPatch()
			patchData.DeleteAnnotation(util.BatchReleaseControlAnnotation)
			if err := rc.client.Patch(context.TODO(), d, patchData); err != nil {
				return err
			}
		*/
		klog.Warningf("continuous release is not supported yet for bluegreen style release")
		return nil
	}

	// restore the original setting and remove annotation
	d := util.GetEmptyObjectWithKey(rc.object)
	if !rc.restored() {
		setting, err := control.GetOriginalSetting(rc.object)
		if err != nil {
			return err
		}
		patchData := patch.NewDeploymentPatch()
		// restore the original setting
		patchData.UpdatePaused(false)
		patchData.UpdateMinReadySeconds(setting.MinReadySeconds)
		patchData.UpdateProgressDeadlineSeconds(setting.ProgressDeadlineSeconds)
		patchData.UpdateMaxSurge(setting.MaxSurge)
		patchData.UpdateMaxUnavailable(setting.MaxUnavailable)
		// restore label and annotation
		patchData.DeleteAnnotation(v1beta1.OriginalDeploymentStrategyAnnotation)
		patchData.DeleteLabel(v1alpha1.DeploymentStableRevisionLabel)
		patchData.DeleteAnnotation(util.BatchReleaseControlAnnotation)
		if err := rc.client.Patch(context.TODO(), d, patchData); err != nil {
			return err
		}
		klog.InfoS("Finalize: deployment bluegreen release: wait all pods updated and ready", "Deployment", klog.KObj(rc.object))
	}

	// wait all pods updated and ready
	if err := waitAllUpdatedAndReady(d.(*apps.Deployment)); err != nil {
		return errors.NewRetryError(err)
	}
	klog.InfoS("Finalize: All pods updated and ready, then restore hpa", "Deployment", klog.KObj(rc.object))

	// restore hpa
	return hpa.RestoreHPA(rc.client, rc.object)
}

func (rc *realController) restored() bool {
	if rc.object == nil || rc.object.DeletionTimestamp != nil {
		return true
	}
	if rc.object.Annotations == nil || len(rc.object.Annotations[v1beta1.OriginalDeploymentStrategyAnnotation]) == 0 {
		return true
	}
	return false
}

func (rc *realController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredSurge := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	PlannedUpdatedReplicas := deploymentutil.NewRSReplicasLimit(desiredSurge, rc.object)
	currentSurge := intstr.FromInt(0)
	if rc.object.Spec.Strategy.RollingUpdate != nil && rc.object.Spec.Strategy.RollingUpdate.MaxSurge != nil {
		currentSurge = *rc.object.Spec.Strategy.RollingUpdate.MaxSurge
		if currentSurge == intstr.FromInt(1) {
			// currentSurge == intstr.FromInt(1) means that currentSurge is the initial value
			// if the value is indeed set by user, setting it to 0 still does no harm
			currentSurge = intstr.FromInt(0)
		}
	}
	return &batchcontext.BatchContext{
		Pods:           rc.pods,
		RolloutID:      release.Spec.ReleasePlan.RolloutID,
		CurrentBatch:   currentBatch,
		CurrentSurge:   currentSurge,
		DesiredSurge:   desiredSurge,
		UpdateRevision: release.Status.UpdateRevision,

		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		PlannedUpdatedReplicas: PlannedUpdatedReplicas,
		DesiredUpdatedReplicas: PlannedUpdatedReplicas,
	}, nil
}

func (rc *realController) getWorkloadInfo(d *apps.Deployment) *util.WorkloadInfo {
	workloadInfo := util.ParseWorkload(d)
	workloadInfo.Status.UpdatedReadyReplicas = 0
	if res, err := rc.getUpdatedReadyReplicas(d); err == nil {
		workloadInfo.Status.UpdatedReadyReplicas = res
	}
	workloadInfo.Status.StableRevision = d.Labels[v1alpha1.DeploymentStableRevisionLabel]
	return workloadInfo
}

func (rc *realController) getUpdatedReadyReplicas(d *apps.Deployment) (int32, error) {
	rss := &apps.ReplicaSetList{}
	listOpts := []client.ListOption{
		client.InNamespace(d.Namespace),
		client.MatchingLabels(d.Spec.Selector.MatchLabels),
		client.UnsafeDisableDeepCopy,
	}
	if err := rc.client.List(context.TODO(), rss, listOpts...); err != nil {
		klog.Warningf("getWorkloadInfo failed, because"+"%s", err.Error())
		return -1, err
	}
	allRSs := rss.Items
	// select rs owner by current deployment
	ownedRSs := make([]*apps.ReplicaSet, 0)
	for i := range allRSs {
		rs := &allRSs[i]
		if !rs.DeletionTimestamp.IsZero() {
			continue
		}

		if metav1.IsControlledBy(rs, d) {
			ownedRSs = append(ownedRSs, rs)
		}
	}
	newRS := deploymentutil.FindNewReplicaSet(d, ownedRSs)
	updatedReadyReplicas := int32(0)
	// if newRS is nil, it means the replicaset hasn't been created (because the deployment is paused)
	// therefore we can return 0 directly
	if newRS != nil {
		updatedReadyReplicas = newRS.Status.ReadyReplicas
	}
	return updatedReadyReplicas, nil
}

func waitAllUpdatedAndReady(deployment *apps.Deployment) error {
	if deployment.Spec.Paused {
		return fmt.Errorf("deployment should not be paused")
	}

	// ALL pods updated AND ready
	if deployment.Status.ReadyReplicas != deployment.Status.UpdatedReplicas {
		return fmt.Errorf("all ready replicas should be updated, and all updated replicas should be ready")
	}

	availableReplicas := deployment.Status.AvailableReplicas
	allowedUnavailable := util.DeploymentMaxUnavailable(deployment)
	if allowedUnavailable+availableReplicas < deployment.Status.Replicas {
		return fmt.Errorf("ready replicas should satisfy maxUnavailable")
	}
	return nil
}

// Patch minReadySeconds for stable ReplicaSet
/*
	Here is why:
	For rollback scenario, we should set the stable rs minReadySeconds to infinity to make pods of the stable rs unavailable,
	otherwise Pods in new version would be terminated immediately when rollback happens.
	we want to keep them until traffic is switched to the stable version
*/
func (rc *realController) patchStableRSMinReadySeconds(seconds int32) error {
	if stableRS, err := rc.finder.GetDeploymentStableRs(rc.object); err != nil {
		return fmt.Errorf("failed to get stable ReplicaSet: %v", err)
	} else if stableRS == nil {
		klog.Warningf("No stable ReplicaSet found for deployment %s/%s", rc.object.Namespace, rc.object.Name)
	} else {
		body := fmt.Sprintf(`{"spec":{"minReadySeconds":%v}}`, seconds)
		if err = rc.client.Patch(context.TODO(), stableRS, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
			return fmt.Errorf("failed to patch ReplicaSet %s/%s minReadySeconds to %v: %v", stableRS.Namespace, stableRS.Name, v1beta1.MaxReadySeconds, err)
		}
	}
	return nil
}

// Update deployment strategy: MinReadySeconds, ProgressDeadlineSeconds, MaxSurge, MaxUnavailable
func (rc *realController) patchDeployment(release *v1beta1.BatchRelease) error {
	setting, err := control.GetOriginalSetting(rc.object)
	if err != nil {
		return errors.NewBadRequestError(fmt.Errorf("cannot get original setting for deployment %v: %s", klog.KObj(rc.object), err.Error()))
	}
	control.InitOriginalSetting(&setting, rc.object)
	patchData := patch.NewDeploymentPatch()
	patchData.InsertAnnotation(v1beta1.OriginalDeploymentStrategyAnnotation, util.DumpJSON(&setting))
	patchData.InsertAnnotation(util.BatchReleaseControlAnnotation, util.DumpJSON(metav1.NewControllerRef(
		release, release.GetObjectKind().GroupVersionKind())))

	patchData.UpdateStrategy(apps.DeploymentStrategy{
		Type: apps.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &apps.RollingUpdateDeployment{
			MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
		},
	})
	patchData.UpdateMinReadySeconds(v1beta1.MaxReadySeconds)
	patchData.UpdateProgressDeadlineSeconds(utilpointer.Int32(v1beta1.MaxProgressSeconds))

	// Apply the patch to the Deployment
	if err := rc.client.Patch(context.TODO(), util.GetEmptyObjectWithKey(rc.object), patchData); err != nil {
		return fmt.Errorf("failed to patch deployment %v: %v", klog.KObj(rc.object), err)
	}
	return nil
}
