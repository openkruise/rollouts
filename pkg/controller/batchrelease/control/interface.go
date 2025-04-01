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

package control

import (
	"github.com/openkruise/rollouts/pkg/util"
)

type WorkloadEventType string

const (
	// WorkloadNormalState means workload is normal and event should be ignored.
	WorkloadNormalState WorkloadEventType = "workload-is-at-normal-state"
	// WorkloadUnknownState means workload state is unknown and should retry.
	WorkloadUnknownState WorkloadEventType = "workload-is-at-unknown-state"
	// WorkloadPodTemplateChanged means workload revision changed, should be stopped to execute batch release plan.
	WorkloadPodTemplateChanged WorkloadEventType = "workload-pod-template-changed"
	// WorkloadReplicasChanged means workload is scaling during rollout, should recalculate upgraded pods in current batch.
	WorkloadReplicasChanged WorkloadEventType = "workload-replicas-changed"
	// WorkloadStillReconciling means workload status is untrusted Untrustworthy, we should wait workload controller to reconcile.
	WorkloadStillReconciling WorkloadEventType = "workload-is-reconciling"
	// WorkloadHasGone means workload is deleted during rollout, we should do something finalizing works if this event occurs.
	WorkloadHasGone WorkloadEventType = "workload-has-gone"
	// WorkloadRollbackInBatch means workload is rollback according to BatchRelease batch plan.
	WorkloadRollbackInBatch WorkloadEventType = "workload-rollback-in-batch"
)

// Interface is the interface that all type of control plane implements for rollout.
type Interface interface {
	// Initialize make sure that the resource is ready to be progressed.
	// this function is tasked to do any initialization work on the resources.
	// it returns nil if the preparation is succeeded, else the preparation should retry.
	Initialize() error

	// UpgradeBatch tries to upgrade old replicas according to the release plan.
	// it will upgrade the old replicas as the release plan allows in the current batch.
	// this function is tasked to do any initialization work on the resources.
	// it returns nil if the preparation is succeeded, else the preparation should retry.
	UpgradeBatch() error

	// EnsureBatchPodsReadyAndLabeled checks how many replicas are ready to serve requests in the current batch.
	// this function is tasked to do any initialization work on the resources.
	// it returns nil if the preparation is succeeded, else the preparation should retry.
	EnsureBatchPodsReadyAndLabeled() error

	// Finalize makes sure the resources are in a good final state.
	// this function is tasked to do any initialization work on the resources.
	// it returns nil if the preparation is succeeded, else the preparation should retry.
	Finalize() error

	// SyncWorkloadInformation will watch and compare the status recorded in Status of BatchRelease
	// and the real-time workload information. If workload status is inconsistent with that recorded
	// in BatchRelease, it will return the corresponding WorkloadEventType and info.
	SyncWorkloadInformation() (WorkloadEventType, *util.WorkloadInfo, error)
}
