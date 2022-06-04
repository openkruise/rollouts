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

import "context"

// Controller common function across all TrafficRouting implementation
type Controller interface {
	// Initialize will validate the traffic routing resource
	Initialize(ctx context.Context) (bool, error)
	// SetRoutes set canary desired weight. desiredWeight[0,100]
	// When desiredWeight=0, it means that rollout has been completed and the final traffic switching process is in progress,
	// and the canary weight should be set to 0 at this time.
	SetRoutes(ctx context.Context, desiredWeight int32) error
	// VerifyRoutes check if canary has been set desired weight. desiredWeight[0,100]
	VerifyRoutes(ctx context.Context, desiredWeight int32) (bool, error)
	// Finalise will do some cleanup work after the canary rollout complete, such as delete canary ingress.
	// Finalise is called with a 3-second delay after completing the canary.
	Finalise(ctx context.Context) error
}
