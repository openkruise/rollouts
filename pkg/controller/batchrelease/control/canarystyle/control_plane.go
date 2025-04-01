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

package canarystyle

import (
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CloneSetRolloutController is responsible for handling rollout CloneSet type of workloads
type realCanaryController struct {
	Interface
	client.Client
	record.EventRecorder
	patcher   labelpatch.LabelPatcher
	release   *v1beta1.BatchRelease
	newStatus *v1beta1.BatchReleaseStatus
}

type NewInterfaceFunc func(cli client.Client, key types.NamespacedName) Interface

// NewControlPlane creates a new release controller to drive batch release state machine
func NewControlPlane(f NewInterfaceFunc, cli client.Client, recorder record.EventRecorder, release *v1beta1.BatchRelease, newStatus *v1beta1.BatchReleaseStatus, key types.NamespacedName) *realCanaryController {
	return &realCanaryController{
		Client:        cli,
		EventRecorder: recorder,
		newStatus:     newStatus,
		Interface:     f(cli, key),
		release:       release.DeepCopy(),
		patcher:       labelpatch.NewLabelPatcher(cli, klog.KObj(release), release.Spec.ReleasePlan.Batches),
	}
}

func (rc *realCanaryController) Initialize() error {
	stable, err := rc.BuildStableController()
	if err != nil {
		return err
	}

	err = stable.Initialize(rc.release)
	if err != nil {
		return err
	}

	canary, err := rc.BuildCanaryController(rc.release)
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	err = canary.Create(rc.release)
	if err != nil {
		return err
	}

	// record revision and replicas
	stableInfo := stable.GetStableInfo()
	canaryInfo := canary.GetCanaryInfo()
	rc.newStatus.ObservedWorkloadReplicas = stableInfo.Replicas
	rc.newStatus.StableRevision = stableInfo.Status.StableRevision
	rc.newStatus.UpdateRevision = canaryInfo.Status.UpdateRevision
	return nil
}

func (rc *realCanaryController) UpgradeBatch() error {
	stable, err := rc.BuildStableController()
	if err != nil {
		return err
	}

	if stable.GetStableInfo().Replicas == 0 {
		return nil
	}

	canary, err := rc.BuildCanaryController(rc.release)
	if err != nil {
		return err
	}

	if !canary.GetCanaryInfo().IsStable() {
		return fmt.Errorf("wait canary workload %v reconcile", canary.GetCanaryInfo().LogKey)
	}

	batchContext, err := rc.CalculateBatchContext(rc.release)
	if err != nil {
		return err
	}
	klog.Infof("BatchRelease %v calculated context when upgrade batch: %s",
		klog.KObj(rc.release), batchContext.Log())

	err = canary.UpgradeBatch(batchContext)
	if err != nil {
		return err
	}

	return nil
}

func (rc *realCanaryController) EnsureBatchPodsReadyAndLabeled() error {
	stable, err := rc.BuildStableController()
	if err != nil {
		return err
	}

	if stable.GetStableInfo().Replicas == 0 {
		return nil
	}

	canary, err := rc.BuildCanaryController(rc.release)
	if err != nil {
		return err
	}

	if !canary.GetCanaryInfo().IsStable() {
		return fmt.Errorf("wait canary workload %v reconcile", canary.GetCanaryInfo().LogKey)
	}

	batchContext, err := rc.CalculateBatchContext(rc.release)
	if err != nil {
		return err
	}
	klog.Infof("BatchRelease %v calculated context when check batch ready: %s",
		klog.KObj(rc.release), batchContext.Log())
	if err = rc.patcher.PatchPodBatchLabel(batchContext); err != nil {
		klog.ErrorS(err, "failed to patch pod labels", "release", klog.KObj(rc.release))
	}
	return batchContext.IsBatchReady()
}

func (rc *realCanaryController) Finalize() error {
	stable, err := rc.BuildStableController()
	if client.IgnoreNotFound(err) != nil {
		klog.Errorf("BatchRelease %v build stable controller err: %v", klog.KObj(rc.release), err)
		return err
	}

	err = stable.Finalize(rc.release)
	if err != nil {
		klog.Errorf("BatchRelease %v finalize stable err: %v", klog.KObj(rc.release), err)
		return err
	}

	canary, err := rc.BuildCanaryController(rc.release)
	if client.IgnoreNotFound(err) != nil {
		klog.Errorf("BatchRelease %v build canary controller err: %v", klog.KObj(rc.release), err)
		return err
	}
	err = canary.Delete(rc.release)
	if err != nil {
		klog.Errorf("BatchRelease %v delete canary workload err: %v", klog.KObj(rc.release), err)
	}
	return err
}

func (rc *realCanaryController) SyncWorkloadInformation() (control.WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if rc.release.DeletionTimestamp != nil {
		return control.WorkloadNormalState, nil, nil
	}

	stable, err := rc.BuildStableController()
	if err != nil {
		if errors.IsNotFound(err) {
			return control.WorkloadHasGone, nil, err
		}
		return control.WorkloadUnknownState, nil, err
	}

	canary, err := rc.BuildCanaryController(rc.release)
	if client.IgnoreNotFound(err) != nil {
		return control.WorkloadUnknownState, nil, err
	}

	syncInfo := &util.WorkloadInfo{}
	stableInfo, canaryInfo := stable.GetStableInfo(), canary.GetCanaryInfo()
	if canaryInfo != nil {
		syncInfo.Status.UpdatedReplicas = canaryInfo.Status.Replicas
		syncInfo.Status.UpdatedReadyReplicas = canaryInfo.Status.AvailableReplicas
	}

	if !stableInfo.IsStable() {
		klog.Warningf("Workload(%v) is still reconciling, generation: %v, observed: %v",
			stableInfo.LogKey, stableInfo.Generation, stableInfo.Status.ObservedGeneration)
		return control.WorkloadStillReconciling, syncInfo, nil
	}

	// in case of that the workload has been promoted
	if stableInfo.IsPromoted() {
		return control.WorkloadNormalState, syncInfo, nil
	}

	if stableInfo.IsScaling(rc.newStatus.ObservedWorkloadReplicas) {
		syncInfo.Replicas = stableInfo.Replicas
		klog.Warningf("Workload(%v) replicas is modified, replicas from: %v to -> %v",
			stableInfo.LogKey, rc.newStatus.ObservedWorkloadReplicas, stableInfo.Replicas)
		return control.WorkloadReplicasChanged, syncInfo, nil
	}

	if stableInfo.IsRevisionNotEqual(rc.newStatus.UpdateRevision) {
		syncInfo.Status.UpdateRevision = stableInfo.Status.UpdateRevision
		klog.Warningf("Workload(%v) updateRevision is modified, updateRevision from: %v to -> %v",
			stableInfo.LogKey, rc.newStatus.UpdateRevision, stableInfo.Status.UpdateRevision)
		return control.WorkloadPodTemplateChanged, syncInfo, nil
	}
	return control.WorkloadUnknownState, syncInfo, nil
}
