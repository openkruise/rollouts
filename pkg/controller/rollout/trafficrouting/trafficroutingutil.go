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
	// SetRoutes set canary desired weight
	SetRoutes(ctx context.Context, desiredWeight int32) error
	// Verify check if canary has been set desired weight
	Verify(desiredWeight int32) (bool, error)
	// Finalise will do some cleanup work, such as delete canary ingress
	Finalise() error
	// TrafficRouting will check the traffic routing resource
	TrafficRouting() (bool, error)
}
