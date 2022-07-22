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

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RolloutHistorySpec defines the desired state of RolloutHistory
type RolloutHistorySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// RolloutWrapper indicates information of the rollout related with rollouthistory
	RolloutWrapper RolloutWrapper `json:"rolloutWrapper,omitempty"`

	// Workload indicates information of the workload, such as cloneset, deployment, advanced statefulset
	Workload Workload `json:"workload,omitempty"`

	// ServiceWrapper indicates information of the service related with workload
	ServiceWrapper ServiceWrapper `json:"serviceWrapper,omitempty"`

	// TrafficRoutingWrapper indicates information of traffic route related with workload
	TrafficRoutingWrapper TrafficRoutingWrapper `json:"trafficRoutingWrapper,omitempty"`
}

// RolloutWrapper indicates information of the rollout related
type RolloutWrapper struct {
	// Name indicates the rollout name
	Name string `json:"name,omitempty"`
	// Rollout indecates the related rollout
	Rollout Rollout `json:"rollout,omitempty"`
}

// ServiceWrapper indicates information of the service related
type ServiceWrapper struct {
	// Name indicates the service name
	Name string `json:"name,omitempty"`
	// Service indicates the service
	Service *v1.Service `json:"service,omitempty"`
}

// TrafficRoutingWrapper indicates information of Gateway API or Ingress
type TrafficRoutingWrapper struct {
	// Ingress indicates information of ingress
	// +optional
	IngressWrapper *IngressWrapper `json:"ingressWrapper,omitempty"`
	// HTTPRouteWrapper indacates information of Gateway API
	// +optional
	HTTPRouteWrapper *HTTPRouteWrapper `json:"httpRouteWrapper,omitempty"`
}

// IngressWrapper indicates information of the ingress related
type IngressWrapper struct {
	// Name indicates the ingress name
	Name string `json:"name,omitempty"`
	// Ingress indicates the ingress
	Ingress *networkingv1.Ingress `json:"ingress,omitempty"`
}

// HTTPRouteWrapper indicates information of gateway API
type HTTPRouteWrapper struct {
	//Name indicates the httproute name
	Name string `json:"name,omitempty"`
	// HTTPRoute indicates the HTTPRoute
	HTTPRoute *v1alpha2.HTTPRoute `json:"httpRoute,omitempty"`
}

// Workload indicates information of the workload, such as cloneset, deployment, advanced statefulset
type Workload struct {
	metav1.TypeMeta `json:",inline"`
	// Name indicates the workload name
	Name string `json:"name,omitempty"`
	// Label selector for pods.
	// It must match the pod template's labels.
	Selector *metav1.LabelSelector `json:"selector" protobuf:"bytes,2,opt,name=selector"`
	// Number of desired pods. This is a pointer to distinguish between explicit
	// zero and not specified. Defaults to 1.
	// +optional
	Replicas *int32 `json:"replicas,omitempty" protobuf:"varint,1,opt,name=replicas"`
	// Type of deployment or CloneSetUpdateStrategy. Can be "Recreate" or "RollingUpdate".
	// +optional
	Type string `json:"type,omitempty" protobuf:"bytes,1,opt,name=type,casttype=DeploymentStrategyType"`
	// Template describes the pods that will be created.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Template v1.PodTemplateSpec `json:"template"`
}

// RolloutHistoryStatus defines the observed state of RolloutHistory
type RolloutHistoryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Phase indicates phase of RolloutHistory, such as "pending", "progressing", "completed"
	Phase string `json:"phase,omitempty"`
	// CanaryStepIndex indicates the current step
	CanaryStepIndex *int32 `json:"canaryStepIndex,omitempty"`
	// CanaryStepState indicates state of this rollout revision, such as "init", "pending", "update", "terminated", "completed", "cancelled", whick is upon rollout canary_step_state
	CanaryStepState string `json:"canaryStepState,omitempty"`
	// RolloutState indicates the rollouts status
	RolloutState RolloutState `json:"rolloutState,omitempty"`
	// canaryStepPods indicates the pods released
	CanaryStepPods []CanaryStepPods `json:"canaryStepPods,omitempty"`
}

// RolloutState indicates the rollouts status
type RolloutState struct {
	// RolloutPhase is the rollout phase.
	RolloutPhase RolloutPhase `json:"rolloutPhase,omitempty"`
	// Message provides details on why the rollout is in its current phase
	Message string `json:"message,omitempty"`
}

// CanaryStepPods indicates the pods for a revision
type CanaryStepPods struct {
	// StepIndex indicates the step index
	StepIndex int32 `json:"stepIndex,omitempty"`
	// Pods indicates the pods information
	Pods []Pod `json:"pods,omitempty"`
	// PodsInTotal indicates the num of new pods released by now
	PodsInTotal int32 `json:"podsInTotal"`
	// PodsInStep indicates the num of new pods released this step
	PodsInStep int32 `json:"podsInStep"`
}

// Pod indicates the information of a pod, including name, ip, node_name.
type Pod struct {
	// Name indicates the node name
	Name string `json:"name,omitempty"`
	// IP indicates the pod ip
	IP string `json:"ip,omitempty"`
	// Node indicates the node which pod is located at
	Node string `json:"node,omitempty"`
}

// CanaryStepState indicates canary-phase of rollouthistory when user do a rollout
const (
	CanaryStateInit       string = "init"
	CanaryStatePending    string = "pending"
	CanaryStateUpdated    string = "updated"
	CanaryStateTerminated string = "terminated"
	CanaryStateCompleted  string = "completed"
)

// Phase indicates rollouthistory status/phase
const (
	PhaseInit        string = "init"
	PhaseCompleted   string = "completed"
	PhaseProgressing string = "progressing"
)

// MaxRolloutHistoryNum indicates how many rollouthistories there can be at most
const MaxRolloutHistoryNum int = 10

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase",description="Phase indicates phase of RolloutHistory, such as pending, progressing, completed"
//+kubebuilder:printcolumn:name="CURRENT_STEP",type="integer",JSONPath=".status.canaryStepIndex",description="CanaryStepIndex indicates the current step"
//+kubebuilder:printcolumn:name="CURRENT_STATE",type="string",JSONPath=".status.canaryStepState",description="CanaryStepState indicates state of this rollout revision, such as init, pending, update, terminated, completed, cancelled, whick is upon rollout canary_step_state"
//+kubebuilder:printcolumn:name="MESSAGE",type="string",JSONPath=".status.rolloutState.message",description="Message provides details on why the rollout is in its current phase"
//+kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"

// RolloutHistory is the Schema for the rollouthistories API
type RolloutHistory struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RolloutHistorySpec   `json:"spec,omitempty"`
	Status RolloutHistoryStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RolloutHistoryList contains a list of RolloutHistory
type RolloutHistoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RolloutHistory `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RolloutHistory{}, &RolloutHistoryList{})
}
