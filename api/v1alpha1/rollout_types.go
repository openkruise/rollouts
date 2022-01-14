/*
Copyright 2022 Kruise Authors.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RolloutSpec defines the desired state of Rollout
type RolloutSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// TargetRef contains enough information to let you identify a workload for Rollout
	TargetRef ObjectRef `json:"targetRef"`

	// The deployment strategy to use to replace existing pods with new ones.
	Strategy RolloutStrategy `json:"strategy"`
}

// ObjectRef holds a references to the Kubernetes object
type ObjectRef struct {
	// API Version of the referent
	APIVersion string `json:"apiVersion"`
	// Kind of the referent
	Kind string `json:"kind"`
	// Name of the referent
	Name string `json:"name"`
}

// RolloutStrategy defines strategy to apply during next rollout
type RolloutStrategy struct {
	// +optional
	// BlueGreen *BlueGreenStrategy `json:"blueGreen,omitempty" protobuf:"bytes,1,opt,name=blueGreen"`
	// +optional
	Canary *CanaryStrategy `json:"canary,omitempty"`
}

// CanaryStrategy defines parameters for a Replica Based Canary
type CanaryStrategy struct {
	// CanaryService holds the name of a service which selects pods with canary version and don't select any pods with stable version.
	// +optional
	//CanaryService string `json:"canaryService,omitempty"`
	// StableService holds the name of a service which selects pods with stable version and don't select any pods with canary version.
	// +optional
	StableService string `json:"stableService,omitempty"`
	// Steps define the order of phases to execute the canary deployment
	// +optional
	Steps []CanaryStep `json:"steps,omitempty"`
	// TrafficRouting hosts all the supported service meshes supported to enable more fine-grained traffic routing
	TrafficRouting *TrafficRouting `json:"trafficRouting,omitempty"`

	// MetricsAnalysis runs a separate analysisRun while all the steps execute. This is intended to be a continuous validation of the new ReplicaSet
	// MetricsAnalysis *MetricsAnalysisBackground `json:"metricsAnalysis,omitempty"`
}

// CanaryStep defines a step of a canary workload.
type CanaryStep struct {
	// SetWeight sets what percentage of the canary pods should receive
	SetWeight *int32 `json:"setWeight,omitempty"`
	// Pause freezes the rollout by setting spec.Paused to true.
	// A Rollout will resume when spec.Paused is reset to false.
	// +optional
	Pause *RolloutPause `json:"pause,omitempty"`
	// MetricsAnalysis defines the AnalysisRun that will run for a step
	// MetricsAnalysis *RolloutAnalysis `json:"metricsAnalysis,omitempty"`
}

// RolloutPause defines a pause stage for a rollout
type RolloutPause struct {
	// Duration the amount of time to wait before moving to the next step.
	// +optional
	Duration *int32 `json:"duration,omitempty"`
}

// TrafficRouting hosts all the different configuration for supported service meshes to enable more fine-grained traffic routing
type TrafficRouting struct {
	Type TrafficRoutingType `json:"type,omitempty"`
	// Nginx holds Nginx Ingress specific configuration to route traffic
	Nginx *NginxTrafficRouting `json:"nginx,omitempty"`
	//
	Alb *AlbTrafficRouting `json:"alb,omitempty"`
}

type TrafficRoutingType string

const (
	TrafficRoutingNginx TrafficRoutingType = "nginx"
	TrafficRoutingAlb   TrafficRoutingType = "alb"
)

// NginxTrafficRouting configuration for Nginx ingress controller to control traffic routing
type NginxTrafficRouting struct {
	// Ingress refers to the name of an `Ingress` resource in the same namespace as the `Rollout`
	Ingress string `json:"ingress"`
	// A/B Testing
	Tickets *Tickets `json:"tickets,omitempty"`
}

// AlbTrafficRouting configuration for Nginx ingress controller to control traffic routing
type AlbTrafficRouting struct {
	// Ingress refers to the name of an `Ingress` resource in the same namespace as the `Rollout`
	Ingress string `json:"ingress"`
	// A/B Testing
	Tickets *Tickets `json:"tickets,omitempty"`
}

type Tickets struct {
	// +optional
	Header map[string]string `json:"header,omitempty"`
	// +optional
	Cookie map[string]string `json:"cookie,omitempty"`
}

// RolloutStatus defines the observed state of Rollout
type RolloutStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// observedGeneration is the most recent generation observed for this SidecarSet. It corresponds to the
	// SidecarSet's generation, which is updated on mutation by the API Server.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// UpdateRevision the hash of the current pod template
	// +optional
	UpdateRevision string `json:"updateRevision,omitempty"`
	// StableRevision indicates the revision pods that has successfully rolled out
	StableRevision string `json:"stableRevision,omitempty"`
	// Conditions a list of conditions a rollout can have.
	// +optional
	Conditions []RolloutCondition `json:"conditions,omitempty"`
	// Canary describes the state of the canary rollout
	// +optional
	CanaryStatus *CanaryStatus `json:"canaryStatus,omitempty"`
	// Phase is the rollout phase. Clients should only rely on the value if status.observedGeneration equals metadata.generation
	Phase RolloutPhase `json:"phase,omitempty"`
	// Message provides details on why the rollout is in its current phase
	Message string `json:"message,omitempty"`
}

// RolloutCondition describes the state of a rollout at a certain point.
type RolloutCondition struct {
	// Type of deployment condition.
	Type RolloutConditionType `json:"type"`
	// Phase of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// The last time this condition was updated.
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	// Last time the condition transitioned from one status to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// The reason for the condition's last transition.
	Reason string `json:"reason"`
	// A human readable message indicating details about the transition.
	Message string `json:"message"`
}

// RolloutConditionType defines the conditions of Rollout
type RolloutConditionType string

// These are valid conditions of a rollout.
const (
	// RolloutConditionProgressing means the rollout is progressing. Progress for a rollout is
	// considered when a new replica set is created or adopted, when pods scale
	// up or old pods scale down, or when the services are updated. Progress is not estimated
	// for paused rollouts.
	RolloutConditionProgressing RolloutConditionType = "Progressing"
	// reason
	ProgressingReasonInitializing = "Initializing"
	ProgressingReasonInRolling    = "InRolling"
	ProgressingReasonFinalising   = "Finalising"
	ProgressingReasonFailed       = "Failed"
	ProgressingReasonSucceeded    = "Succeeded"

	RolloutConditionTerminating RolloutConditionType = "Terminating"
	//reason
	TerminatingReasonInTerminating = "InTerminating"
	TerminatingReasonCompleted     = "Completed"
)

// CanaryStatus status fields that only pertain to the canary rollout
type CanaryStatus struct {
	// CanaryService holds the name of a service which selects pods with canary version and don't select any pods with stable version.
	CanaryService string `json:"canaryService"`
	// CanaryRevision the hash of the current pod template
	// +optional
	CanaryRevision string `json:"canaryRevision"`
	// CanaryReplicas the numbers of canary revision pods
	CanaryReplicas      int32 `json:"canaryReplicas"`
	CanaryReadyReplicas int32 `json:"canaryReadyReplicas"`
	// CurrentStepIndex defines the current step of the rollout is on. If the current step index is null, the
	// controller will execute the rollout.
	// +optional
	CurrentStepIndex int32           `json:"currentStepIndex"`
	CurrentStepState CanaryStepState `json:"currentStepState"`
	Message          string          `json:"message,omitempty"`
	// The last time this step pods is ready.
	LastReadyTime *metav1.Time `json:"lastReadyTime,omitempty"`
}

type CanaryStepState string

const (
	CanaryStepStateUpgrade         CanaryStepState = "StepInUpgrade"
	CanaryStepStateTrafficRouting  CanaryStepState = "StepInTrafficRouting"
	CanaryStepStateMetricsAnalysis CanaryStepState = "StepInMetricsAnalysis"
	CanaryStepStatePaused          CanaryStepState = "StepInPaused"
	CanaryStepStateCompleted       CanaryStepState = "StepInCompleted"
)

// RolloutPhase are a set of phases that this rollout
type RolloutPhase string

const (
	// RolloutPhaseInitial indicates a rollout is Initial
	RolloutPhaseInitial RolloutPhase = "Initial"
	// RolloutPhaseHealthy indicates a rollout is healthy
	RolloutPhaseHealthy RolloutPhase = "Healthy"
	// RolloutPhaseProgressing indicates a rollout is not yet healthy but still making progress towards a healthy state
	RolloutPhaseProgressing RolloutPhase = "Progressing"
	// RolloutPhasePaused indicates a rollout is not yet healthy and will not make progress until paused=false
	RolloutPhasePaused RolloutPhase = "Paused"
	// RolloutPhaseTerminating indicates a rollout is terminated
	RolloutPhaseTerminating RolloutPhase = "Terminating"
)

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Rollout is the Schema for the rollouts API
type Rollout struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RolloutSpec   `json:"spec,omitempty"`
	Status RolloutStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RolloutList contains a list of Rollout
type RolloutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Rollout `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Rollout{}, &RolloutList{})
}
