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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openkruise/rollouts/api/v1beta1"
)

func TestRoundTripDeploymentStrategyFromV1alpha1(t *testing.T) {
	source := &Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo",
			Annotations: map[string]string{
				RolloutStyleAnnotation: string(PartitionRollingStyle),
			},
		},
		Spec: RolloutSpec{
			ObjectRef: ObjectRef{WorkloadRef: &WorkloadRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "demo",
			}},
			Strategy: RolloutStrategy{Canary: &CanaryStrategy{
				DeploymentStrategy: DeploymentStrategyMinReadySeconds,
			}},
		},
	}

	hub := &v1beta1.Rollout{}
	if err := source.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo failed: %v", err)
	}
	if got := hub.Spec.Strategy.Canary.DeploymentStrategy; got != v1beta1.DeploymentStrategyMinReadySeconds {
		t.Fatalf("ConvertTo DeploymentStrategy = %q, want %q", got, v1beta1.DeploymentStrategyMinReadySeconds)
	}

	roundTripped := &Rollout{}
	if err := roundTripped.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom failed: %v", err)
	}
	if got := roundTripped.Spec.Strategy.Canary.DeploymentStrategy; got != DeploymentStrategyMinReadySeconds {
		t.Fatalf("round-trip DeploymentStrategy = %q, want %q", got, DeploymentStrategyMinReadySeconds)
	}
}

func TestRoundTripDeploymentStrategyFromV1beta1(t *testing.T) {
	source := &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "demo",
			},
			Strategy: v1beta1.RolloutStrategy{Canary: &v1beta1.CanaryStrategy{
				DeploymentStrategy: v1beta1.DeploymentStrategyMinReadySeconds,
			}},
		},
	}

	spoke := &Rollout{}
	if err := spoke.ConvertFrom(source); err != nil {
		t.Fatalf("ConvertFrom failed: %v", err)
	}
	if got := spoke.Spec.Strategy.Canary.DeploymentStrategy; got != DeploymentStrategyMinReadySeconds {
		t.Fatalf("ConvertFrom DeploymentStrategy = %q, want %q", got, DeploymentStrategyMinReadySeconds)
	}

	roundTripped := &v1beta1.Rollout{}
	if err := spoke.ConvertTo(roundTripped); err != nil {
		t.Fatalf("ConvertTo failed: %v", err)
	}
	if got := roundTripped.Spec.Strategy.Canary.DeploymentStrategy; got != v1beta1.DeploymentStrategyMinReadySeconds {
		t.Fatalf("round-trip DeploymentStrategy = %q, want %q", got, v1beta1.DeploymentStrategyMinReadySeconds)
	}
}
