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

package v1beta1

// TrafficRoutingRef hosts all the different configuration for supported service meshes to enable more fine-grained traffic routing
type TrafficRoutingRef struct {
	// Service holds the name of a service which selects pods with stable version and don't select any pods with canary version.
	Service string `json:"service"`
	// Optional duration in seconds the traffic provider(e.g. nginx ingress controller) consumes the service, ingress configuration changes gracefully.
	// +kubebuilder:default=3
	GracePeriodSeconds int32 `json:"gracePeriodSeconds,omitempty"`
	// Ingress holds Ingress specific configuration to route traffic, e.g. Nginx, Alb.
	Ingress *IngressTrafficRouting `json:"ingress,omitempty"`
	// Gateway holds Gateway specific configuration to route traffic
	// Gateway configuration only supports >= v0.4.0 (v1alpha2).
	Gateway *GatewayTrafficRouting `json:"gateway,omitempty"`
	// CustomNetworkRefs hold a list of custom providers to route traffic
	CustomNetworkRefs []ObjectRef `json:"customNetworkRefs,omitempty"`
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
