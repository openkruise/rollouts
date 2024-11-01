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

package network

import (
	"context"

	"github.com/openkruise/rollouts/api/v1beta1"
)

// NetworkProvider common function across all TrafficRouting implementation
type NetworkProvider interface {
	// Initialize only determine if the network resources(ingress & gateway api) exist.
	// If error is nil, then the network resources exist.
	Initialize(ctx context.Context) error
	// EnsureRoutes check and set routes, e.g. canary weight and match conditions.
	// 1. Canary weight specifies the relative proportion of traffic to be forwarded to the canary service within the range of [0,100]
	// 2. Match conditions indicates rules to be satisfied for A/B testing scenarios, such as header, cookie, queryParams etc.
	// Return true if and only if the route resources have been correctly updated and does not change in this round of reconciliation.
	// Otherwise, return false to wait for the eventual consistency.
	EnsureRoutes(ctx context.Context, strategy *v1beta1.TrafficRoutingStrategy) (bool, error)
	// Finalise will do some cleanup work after the canary rollout complete, such as delete canary ingress.
	// if error is nil, the return bool value means if the resources are modified
	Finalise(ctx context.Context) (bool, error)
}
