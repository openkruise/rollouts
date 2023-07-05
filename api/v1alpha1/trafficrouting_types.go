/*
Copyright 2023 The Kruise Authors.

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
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

const (
	ProgressingRolloutFinalizerPrefix = "progressing.rollouts.kruise.io"
)

// TrafficRoutingRef hosts all the different configuration for supported service meshes to enable more fine-grained traffic routing
type TrafficRoutingRef struct {
	// Service holds the name of a service which selects pods with stable version and don't select any pods with canary version.
	Service string `json:"service"`
	// Optional duration in seconds the traffic provider(e.g. nginx ingress controller) consumes the service, ingress configuration changes gracefully.
	GracePeriodSeconds int32 `json:"gracePeriodSeconds,omitempty"`
	// Ingress holds Ingress specific configuration to route traffic, e.g. Nginx, Alb.
	Ingress *IngressTrafficRouting `json:"ingress,omitempty"`
	// Gateway holds Gateway specific configuration to route traffic
	// Gateway configuration only supports >= v0.4.0 (v1alpha2).
	Gateway *GatewayTrafficRouting `json:"gateway,omitempty"`
}

// IngressTrafficRouting configuration for ingress controller to control traffic routing
type IngressTrafficRouting struct {
	// ClassType refers to the type of `Ingress`.
	// current support nginx, aliyun-alb. default is nginx.
	// +optional
	ClassType string `json:"classType,omitempty"`
	// Name refers to the name of an `Ingress` resource in the same namespace as the `Rollout`
	Name string `json:"name"`
}

// GatewayTrafficRouting configuration for gateway api
type GatewayTrafficRouting struct {
	// HTTPRouteName refers to the name of an `HTTPRoute` resource in the same namespace as the `Rollout`
	HTTPRouteName *string `json:"httpRouteName,omitempty"`
	// TCPRouteName *string `json:"tcpRouteName,omitempty"`
	// UDPRouteName *string `json:"udpRouteName,omitempty"`
}

type TrafficRoutingSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ObjectRef indicates trafficRouting ref
	ObjectRef []TrafficRoutingRef `json:"objectRef"`
	// trafficrouting strategy
	Strategy TrafficRoutingStrategy `json:"strategy"`
}

type TrafficRoutingStrategy struct {
	// Weight indicate how many percentage of traffic the canary pods should receive
	// +optional
	Weight *int32 `json:"weight,omitempty"`
	// Set overwrites the request with the given header (name, value)
	// before the action.
	//
	// Input:
	//   GET /foo HTTP/1.1
	//   my-header: foo
	//
	// requestHeaderModifier:
	//   set:
	//   - name: "my-header"
	//     value: "bar"
	//
	// Output:
	//   GET /foo HTTP/1.1
	//   my-header: bar
	//
	// +optional
	RequestHeaderModifier *gatewayv1alpha2.HTTPRequestHeaderFilter `json:"requestHeaderModifier,omitempty"`
	// Matches define conditions used for matching the incoming HTTP requests to canary service.
	// Each match is independent, i.e. this rule will be matched if **any** one of the matches is satisfied.
	// If Gateway API, current only support one match.
	// And cannot support both weight and matches, if both are configured, then matches takes precedence.
	Matches []HttpRouteMatch `json:"matches,omitempty"`
}

type TrafficRoutingStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// observedGeneration is the most recent generation observed for this Rollout.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Phase is the trafficRouting phase.
	Phase TrafficRoutingPhase `json:"phase,omitempty"`
	// Message provides details on why the rollout is in its current phase
	Message string `json:"message,omitempty"`
}

// TrafficRoutingPhase are a set of phases that this rollout
type TrafficRoutingPhase string

const (
	// TrafficRoutingPhaseInitial indicates a traffic routing is Initial
	TrafficRoutingPhaseInitial TrafficRoutingPhase = "Initial"
	// TrafficRoutingPhaseHealthy indicates a traffic routing is healthy.
	// This means that Ingress and Service Resources exist.
	TrafficRoutingPhaseHealthy TrafficRoutingPhase = "Healthy"
	// TrafficRoutingPhaseProgressing indicates a traffic routing is not yet healthy but still making progress towards a healthy state
	TrafficRoutingPhaseProgressing TrafficRoutingPhase = "Progressing"
	// TrafficRoutingPhaseFinalizing indicates the trafficRouting progress is complete, and is running recycle operations.
	TrafficRoutingPhaseFinalizing TrafficRoutingPhase = "Finalizing"
	// TrafficRoutingPhaseTerminating indicates a traffic routing is terminated
	TrafficRoutingPhaseTerminating TrafficRoutingPhase = "Terminating"
)

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="STATUS",type="string",JSONPath=".status.phase",description="The TrafficRouting status phase"
// +kubebuilder:printcolumn:name="MESSAGE",type="string",JSONPath=".status.message",description="The TrafficRouting canary status message"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"

// TrafficRouting is the Schema for the TrafficRoutings API
type TrafficRouting struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TrafficRoutingSpec   `json:"spec,omitempty"`
	Status TrafficRoutingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TrafficRoutingList contains a list of TrafficRouting
type TrafficRoutingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TrafficRouting `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TrafficRouting{}, &TrafficRoutingList{})
}
