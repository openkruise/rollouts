/*
Copyright 2026 The Kruise Authors.

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
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
)

func newV1alpha1CanaryRollout(disableGenerateCanaryService bool) *Rollout {
	return &Rollout{
		Spec: RolloutSpec{
			ObjectRef: ObjectRef{
				WorkloadRef: &WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "demo",
				},
			},
			Strategy: RolloutStrategy{
				Canary: &CanaryStrategy{
					DisableGenerateCanaryService: disableGenerateCanaryService,
				},
			},
		},
	}
}

// TestConvertDisableGenerateCanaryService verifies that the
// Canary.DisableGenerateCanaryService flag survives conversion between the
// v1alpha1 spoke and the v1beta1 hub (the stored version). Dropping it would
// make the controller generate a canary Service the user asked to suppress.
func TestConvertDisableGenerateCanaryService(t *testing.T) {
	// v1alpha1 -> v1beta1 (the write path, since v1beta1 is the storage version).
	src := newV1alpha1CanaryRollout(true)
	hub := &v1beta1.Rollout{}
	if err := src.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo returned error: %v", err)
	}
	if hub.Spec.Strategy.Canary == nil {
		t.Fatalf("ConvertTo dropped the canary strategy")
	}
	if !hub.Spec.Strategy.Canary.DisableGenerateCanaryService {
		t.Fatalf("ConvertTo dropped DisableGenerateCanaryService: got false, want true")
	}

	// v1beta1 -> v1alpha1 (the read path back to the spoke).
	dst := &Rollout{}
	if err := dst.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom returned error: %v", err)
	}
	if dst.Spec.Strategy.Canary == nil {
		t.Fatalf("ConvertFrom dropped the canary strategy")
	}
	if !dst.Spec.Strategy.Canary.DisableGenerateCanaryService {
		t.Fatalf("ConvertFrom dropped DisableGenerateCanaryService: got false, want true")
	}
}

// TestConvertDisableGenerateCanaryServiceFalseStaysFalse guards against the
// flag being forced on during conversion.
func TestConvertDisableGenerateCanaryServiceFalseStaysFalse(t *testing.T) {
	src := newV1alpha1CanaryRollout(false)
	hub := &v1beta1.Rollout{}
	if err := src.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo returned error: %v", err)
	}
	if hub.Spec.Strategy.Canary.DisableGenerateCanaryService {
		t.Fatalf("ConvertTo set DisableGenerateCanaryService: got true, want false")
	}
}
