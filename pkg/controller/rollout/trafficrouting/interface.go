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

package trafficrouting

import (
	"context"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
)

// Controller common function across all TrafficRouting implementation
type Controller interface {
	// Initialize will validate the traffic routing resource
	// 1. Ingress type, verify the existence of the ingress resource and generate the canary ingress[weight=0%]
	// 2. Gateway type, verify the existence of the gateway resource
	Initialize(ctx context.Context) error
	// EnsureRoutes check and set canary weight and matches.
	// weight indicates percentage of traffic to canary service, and range of values[0,100]
	// matches indicates A/B Testing release for headers, cookies
	// 1. check if canary has been set desired weight.
	// 2. If not, set canary desired weight
	// When the first set weight is returned false, mainly to give the provider some time to process, only when again ensure, will return true
	EnsureRoutes(ctx context.Context, weight *int32, matches []rolloutv1alpha1.HttpRouteMatch) (bool, error)
	// Finalise will do some cleanup work after the canary rollout complete, such as delete canary ingress.
	// Finalise is called with a 3-second delay after completing the canary.
	// bool indicates whether function Finalise is complete,
	// for example, when ingress type, only canary ingress Not Found is considered function finalise complete
	Finalise(ctx context.Context) (bool, error)
}
