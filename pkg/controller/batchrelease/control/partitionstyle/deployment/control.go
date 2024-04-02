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

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	deploymentutil "github.com/openkruise/rollouts/pkg/controller/deployment/util"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/patch"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	object *apps.Deployment
}

func NewController(cli client.Client, key types.NamespacedName, _ schema.GroupVersionKind) partitionstyle.Interface {
	return &realController{
		key:    key,
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

func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if deploymentutil.IsUnderRolloutControl(rc.object) {
		return nil // No need initialize again.
	}

	// Set strategy to deployment annotations
	strategy := util.GetDeploymentStrategy(rc.object)
	rollingUpdate := strategy.RollingUpdate
	if rc.object.Spec.Strategy.RollingUpdate != nil {
		rollingUpdate = rc.object.Spec.Strategy.RollingUpdate
	}
	strategy = v1alpha1.DeploymentStrategy{
		Paused:        false,
		Partition:     intstr.FromInt(0),
		RollingStyle:  v1alpha1.PartitionRollingStyle,
		RollingUpdate: rollingUpdate,
	}
	v1alpha1.SetDefaultDeploymentStrategy(&strategy)

	d := rc.object.DeepCopy()
	patchData := patch.NewDeploymentPatch()
	patchData.InsertLabel(v1alpha1.AdvancedDeploymentControlLabel, "true")
	patchData.InsertAnnotation(v1alpha1.DeploymentStrategyAnnotation, util.DumpJSON(&strategy))
	patchData.InsertAnnotation(util.BatchReleaseControlAnnotation, util.DumpJSON(metav1.NewControllerRef(
		release, release.GetObjectKind().GroupVersionKind())))

	// Disable the native deployment controller
	patchData.UpdatePaused(true)
	patchData.UpdateStrategy(apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType})
	return rc.client.Patch(context.TODO(), d, patchData)
}

func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	if !deploymentutil.IsUnderRolloutControl(rc.object) {
		klog.Warningf("Cannot upgrade batch, because "+
			"deployment %v has ridden out of our control", klog.KObj(rc.object))
		return nil
	}

	strategy := util.GetDeploymentStrategy(rc.object)
	if control.IsCurrentMoreThanOrEqualToDesired(strategy.Partition, ctx.DesiredPartition) {
		return nil // Satisfied, no need patch again.
	}

	d := rc.object.DeepCopy()
	strategy.Partition = ctx.DesiredPartition
	patchData := patch.NewDeploymentPatch()
	patchData.InsertAnnotation(v1alpha1.DeploymentStrategyAnnotation, util.DumpJSON(&strategy))
	return rc.client.Patch(context.TODO(), d, patchData)
}

func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil // No need to finalize again.
	}
	isUnderRolloutControl := rc.object.Annotations[util.BatchReleaseControlAnnotation] != "" && rc.object.Spec.Paused
	if !isUnderRolloutControl {
		return nil // No need to finalize again.
	}

	patchData := patch.NewDeploymentPatch()
	if release.Spec.ReleasePlan.BatchPartition == nil {
		strategy := util.GetDeploymentStrategy(rc.object)
		patchData.UpdatePaused(false)
		if rc.object.Spec.Strategy.Type == apps.RecreateDeploymentStrategyType {
			patchData.UpdateStrategy(apps.DeploymentStrategy{Type: apps.RollingUpdateDeploymentStrategyType, RollingUpdate: strategy.RollingUpdate})
		}
		patchData.DeleteAnnotation(v1alpha1.DeploymentStrategyAnnotation)
		patchData.DeleteAnnotation(v1alpha1.DeploymentExtraStatusAnnotation)
		patchData.DeleteLabel(v1alpha1.DeploymentStableRevisionLabel)
		patchData.DeleteLabel(v1alpha1.AdvancedDeploymentControlLabel)
	}
	d := rc.object.DeepCopy()
	patchData.DeleteAnnotation(util.BatchReleaseControlAnnotation)
	return rc.client.Patch(context.TODO(), d, patchData)
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

	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredPartition := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	PlannedUpdatedReplicas := deploymentutil.NewRSReplicasLimit(desiredPartition, rc.object)

	return &batchcontext.BatchContext{
		Pods:             rc.pods,
		RolloutID:        rolloutID,
		CurrentBatch:     currentBatch,
		UpdateRevision:   release.Status.UpdateRevision,
		DesiredPartition: desiredPartition,
		FailureThreshold: release.Spec.ReleasePlan.FailureThreshold,

		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		PlannedUpdatedReplicas: PlannedUpdatedReplicas,
		DesiredUpdatedReplicas: PlannedUpdatedReplicas,
	}, nil
}

func (rc *realController) getWorkloadInfo(d *apps.Deployment) *util.WorkloadInfo {
	workloadInfo := util.ParseWorkload(d)
	extraStatus := util.GetDeploymentExtraStatus(d)
	workloadInfo.Status.UpdatedReadyReplicas = extraStatus.UpdatedReadyReplicas
	workloadInfo.Status.StableRevision = d.Labels[v1alpha1.DeploymentStableRevisionLabel]
	return workloadInfo
}
