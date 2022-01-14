/*
Copyright 2022 The Kruise & KubeVela Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ReleaseStrategyType defines strategies for pods rollout
type ReleaseStrategyType string

type ReleasingBatchStateType string

const (
	InitializeBatchState ReleasingBatchStateType = "InitializeInBatch"
	DoCanaryBatchState   ReleasingBatchStateType = "DoCanaryInBatch"
	VerifyBatchState     ReleasingBatchStateType = "VerifyInBatch"
	ReadyBatchState      ReleasingBatchStateType = "ReadyInBatch"
)

const (
	// RolloutPhaseVerify indicates the phase of verifying workload health.
	RolloutPhaseVerify RolloutPhase = "Verifying"
	// RolloutPhaseFinalizing indicates the phase, where controller will do some operation after all batch ready.
	RolloutPhaseFinalizing RolloutPhase = "Finalizing"
	// RolloutPhaseCompleted indicates the release plan was executed successfully.
	RolloutPhaseCompleted RolloutPhase = "Completed"
)

// ReleasePlan fines the details of the release plan
type ReleasePlan struct {
	// Batches describe the plan for each batch in detail, including canary replicas and paused seconds.
	Batches []ReleaseBatch `json:"batches"`
	// All pods in the batches up to the batchPartition (included) will have
	// the target updated specification while the rest still have the stable resource
	// This is designed for the operators to manually release plan
	// Default is nil, which means no partition.
	// +optional
	BatchPartition *int32 `json:"batchPartition,omitempty"`
	// Paused the release plan executor, default is false
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// ReleaseBatch is used to describe how each release batch should be
type ReleaseBatch struct {
	// Replicas is the number of expected canary pods in this batch
	// it can be an absolute number (ex: 5) or a percentage of total pods.
	// it is mutually exclusive with the PodList field
	CanaryReplicas intstr.IntOrString `json:"canaryReplicas"`
	// The wait time, in seconds, between instances upgrades, default = 0
	// +optional
	PauseSeconds int64 `json:"pauseSeconds,omitempty"`
}

// BatchReleaseStatus defines the observed state of a rollout plan
type BatchReleaseStatus struct {
	// Conditions records some important message at each Phase.
	Conditions []RolloutCondition `json:"conditions,omitempty"`
	// Canary describes the status of the canary
	CanaryStatus BatchReleaseCanaryStatus `json:"canaryStatus,omitempty"`
	// StableRevision is the pod-template-hash of stable revision pod.
	StableRevision string `json:"stableRevision,omitempty"`
	// UpdateRevision is the pod-template-hash of updated revision pod.
	UpdateRevision string `json:"updateRevision,omitempty"`
	// observedGeneration is the most recent generation observed for this SidecarSet. It corresponds to the
	// SidecarSet's generation, which is updated on mutation by the API Server.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// ObservedWorkloadReplicas is the size of the target resources. This is determined once the initial spec verification
	// and does not change until the rollout is restarted.
	ObservedWorkloadReplicas int32 `json:"observedWorkloadReplicas,omitempty"`
	// ObservedReleasePlanHash is a hash code of observed itself releasePlan.Batches.
	ObservedReleasePlanHash string `json:"observedReleasePlanHash,omitempty"`
	// Phase is the release phase.
	// Clients should only rely on the value if status.observedGeneration equals metadata.generation
	Phase RolloutPhase `json:"phase,omitempty"`
}

type BatchReleaseCanaryStatus struct {
	// State indicates the state of the current batch.
	State ReleasingBatchStateType `json:"state,omitempty"`
	// The current batch the rollout is working on/blocked, it starts from 0
	CurrentBatch int32 `json:"currentBatch"`
	// LastBatchFinalizedTime is the timestamp of
	LastBatchReadyTime metav1.Time `json:"lastBatchReadyTime,omitempty"`
	// UpgradedReplicas is the number of Pods upgraded by the rollout controller
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`
	// UpgradedReadyReplicas is the number of Pods upgraded by the rollout controller that have a Ready Condition.
	UpdatedReadyReplicas int32 `json:"updatedReadyReplicas,omitempty"`
}
