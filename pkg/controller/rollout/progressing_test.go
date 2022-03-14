/*
Copyright 2021.

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

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReCalculateCanaryStepIndex(t *testing.T) {
	cases := []struct {
		name            string
		getObj          func() *apps.Deployment
		getRollout      func() *rolloutv1alpha1.Rollout
		getBatchRelease func() *rolloutv1alpha1.BatchRelease
		expectStepIndex int32
	}{
		{
			name: "steps changed v1",
			getObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				return obj
			},
			getRollout: func() *rolloutv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []rolloutv1alpha1.CanaryStep{
					{
						Weight: 20,
					},
					{
						Weight: 50,
					},
					{
						Weight: 100,
					},
				}
				return obj
			},
			getBatchRelease: func() *rolloutv1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []rolloutv1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("60%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 2,
		},
		{
			name: "steps changed v2",
			getObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				return obj
			},
			getRollout: func() *rolloutv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []rolloutv1alpha1.CanaryStep{
					{
						Weight: 20,
					},
					{
						Weight: 40,
					},
					{
						Weight: 100,
					},
				}
				return obj
			},
			getBatchRelease: func() *rolloutv1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []rolloutv1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("60%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 2,
		},
		{
			name: "steps changed v3",
			getObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				return obj
			},
			getRollout: func() *rolloutv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []rolloutv1alpha1.CanaryStep{
					{
						Weight: 40,
					},
					{
						Weight: 60,
					},
					{
						Weight: 100,
					},
				}
				return obj
			},
			getBatchRelease: func() *rolloutv1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []rolloutv1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("20%"),
					},
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(1)
				return obj
			},
			expectStepIndex: 1,
		},
		{
			name: "steps changed v4",
			getObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				return obj
			},
			getRollout: func() *rolloutv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []rolloutv1alpha1.CanaryStep{
					{
						Weight: 10,
					},
					{
						Weight: 30,
					},
					{
						Weight: 100,
					},
				}
				return obj
			},
			getBatchRelease: func() *rolloutv1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []rolloutv1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("20%"),
					},
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 2,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			client.Create(context.TODO(), cs.getBatchRelease())
			client.Create(context.TODO(), cs.getObj())
			client.Create(context.TODO(), cs.getRollout())

			reconciler := &RolloutReconciler{
				Client: client,
				Scheme: scheme,
				Finder: util.NewControllerFinder(client),
			}
			batchControl := batchrelease.NewInnerBatchController(client, cs.getRollout())
			newStepIndex, err := reconciler.reCalculateCanaryStepIndex(cs.getRollout(), batchControl)
			if err != nil {
				t.Fatalf(err.Error())
			}
			if cs.expectStepIndex != newStepIndex {
				t.Fatalf("expect %d, but %d", cs.expectStepIndex, newStepIndex)
			}
		})
	}
}
