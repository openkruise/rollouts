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

package rollout

import (
	"testing"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCalculateRolloutHash(t *testing.T) {
	cases := []struct {
		name       string
		getRollout func() *v1alpha1.Rollout
		expectHash func() string
	}{
		{
			name: "hash, test1",
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				return obj
			},
			expectHash: func() string {
				return "626fd556c5d5v2d9b4f7c2xvbc9dxddxzd48xvb9w9wfcdvdz6v959fbzd84b57x"
			},
		},
		{
			name: "hash, test2",
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Paused = true
				obj.Spec.Strategy.Canary.FailureThreshold = &intstr.IntOrString{Type: intstr.Int}
				obj.Spec.Strategy.Canary.Steps[0].Pause = v1alpha1.RolloutPause{Duration: utilpointer.Int32(10)}
				return obj
			},
			expectHash: func() string {
				return "626fd556c5d5v2d9b4f7c2xvbc9dxddxzd48xvb9w9wfcdvdz6v959fbzd84b57x"
			},
		},
		{
			name: "hash, test3",
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
					{
						TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
							Weight: utilpointer.Int32(50),
						},
					},
					{
						TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
							Weight: utilpointer.Int32(100),
						},
					},
				}
				return obj
			},
			expectHash: func() string {
				return "8c449wxc46x8dd764x4v4wzvc7454f48478vd9db27fv8v9dw5cwbcb6b42b75dc"
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			rollout := cs.getRollout()
			fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
			r := &RolloutReconciler{
				Client:                fc,
				Scheme:                scheme,
				Recorder:              record.NewFakeRecorder(10),
				finder:                util.NewControllerFinder(fc),
				trafficRoutingManager: trafficrouting.NewTrafficRoutingManager(fc),
			}
			r.canaryManager = &canaryReleaseManager{
				Client:                fc,
				trafficRoutingManager: r.trafficRoutingManager,
				recorder:              r.Recorder,
			}
			_ = r.calculateRolloutHash(rollout)
			if rollout.Annotations[util.RolloutHashAnnotation] != cs.expectHash() {
				t.Fatalf("expect(%s), but get(%s)", cs.expectHash(), rollout.Annotations[util.RolloutHashAnnotation])
			}
		})
	}
}
