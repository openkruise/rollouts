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
	"context"
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
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
		getRollout func() *v1beta1.Rollout
		expectHash func() string
	}{
		{
			name: "hash, test1",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				return obj
			},
			expectHash: func() string {
				return "75746v7d5z9x59v5c7dff4wd9cv9cc28czf6c2z664w7zbb7vw2bzv76v99z6bd9"
			},
		},
		{
			name: "hash, test2",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Paused = true
				obj.Spec.Strategy.Canary.FailureThreshold = &intstr.IntOrString{Type: intstr.Int}
				obj.Spec.Strategy.Canary.Steps[0].Pause = v1beta1.RolloutPause{Duration: utilpointer.Int32(10)}
				return obj
			},
			expectHash: func() string {
				return "75746v7d5z9x59v5c7dff4wd9cv9cc28czf6c2z664w7zbb7vw2bzv76v99z6bd9"
			},
		},
		{
			name: "hash, test3",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
					{
						TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
							Traffic: utilpointer.String("50%"),
						},
					},
					{
						TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
							Traffic: utilpointer.String("100%"),
						},
					},
				}
				return obj
			},
			expectHash: func() string {
				return "db9c2x47d282c84z6684d598bzwf9b4x6ffb45fc456xdfv97945v2vb79w72c7z"
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

func TestCalculateRolloutStatus(t *testing.T) {
	cases := []struct {
		name        string
		getRollout  func() *v1beta1.Rollout
		expectPhase v1beta1.RolloutPhase
	}{
		{
			name: "apply an enabled rollout",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Name = "Rollout-demo1"
				obj.Status = v1beta1.RolloutStatus{}
				obj.Spec.Disabled = false
				return obj
			},
			expectPhase: v1beta1.RolloutPhaseInitial,
		},
		{
			name: "disable an working rollout",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Name = "Rollout-demo1"
				obj.Status = v1beta1.RolloutStatus{}
				obj.Spec.Disabled = true
				return obj
			},
			expectPhase: v1beta1.RolloutPhaseDisabled,
		},
		{
			name: "enable an disabled rollout",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Name = "Rollout-demo2"
				obj.Status = v1beta1.RolloutStatus{}
				obj.Spec.Disabled = false
				return obj
			},
			expectPhase: v1beta1.RolloutPhaseInitial,
		},
	}

	t.Run("RolloutStatus test", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()
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
		for _, cs := range cases {
			rollout := cs.getRollout()
			fc.Create(context.TODO(), rollout)
			_, newStatus, _ := r.calculateRolloutStatus(rollout)
			r.updateRolloutStatusInternal(rollout, *newStatus)
			if cs.expectPhase != newStatus.Phase {
				t.Fatalf("expect phase %s, get %s, for rollout %s", cs.expectPhase, newStatus.Phase, rollout.Name)
			}
		}
	})
}
