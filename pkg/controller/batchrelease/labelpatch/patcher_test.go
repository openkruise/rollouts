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

package labelpatch

import (
	"context"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	corev1.AddToScheme(scheme)
}

func TestLabelPatcher(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := map[string]struct {
		batchContext    func() *batchcontext.BatchContext
		Batches         []v1beta1.ReleaseBatch
		expectedPatched []int
	}{
		"10 pods, 0 patched, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generatePods(1, ctx.Replicas, 0, "", "", ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 2 patched, 3 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generatePods(1, ctx.Replicas, 2,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 5 patched, 0 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generatePods(1, ctx.Replicas, 5,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 7 patched, 0 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generatePods(1, ctx.Replicas, 7,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt(5),
				},
			},
			expectedPatched: []int{7},
		},
		"2 pods, 0 patched, 2 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generatePods(1, 2, 0,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt(5),
				},
			},
			expectedPatched: []int{2},
		},
		"10 pods, 3 patched with old rollout-id, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generatePods(1, ctx.Replicas, 3,
					"previous-rollout-id", strconv.Itoa(int(ctx.CurrentBatch+1)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 2 patched with batch-id:1, 3 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generatePods(1, 5, 2,
					"rollout-1", strconv.Itoa(1), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt(2)},
				{CanaryReplicas: intstr.FromInt(5)},
			},
			expectedPatched: []int{2, 3},
		},
		"10 pods, 0 patched with batch-id:1, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generatePods(1, 5, 0,
					"rollout-1", strconv.Itoa(1), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt(2)},
				{CanaryReplicas: intstr.FromInt(5)},
			},
			expectedPatched: []int{2, 3},
		},
		"10 pods, 3 patched with batch-id:1, 2 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generatePods(1, 5, 3,
					"rollout-1", strconv.Itoa(1), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt(2)},
				{CanaryReplicas: intstr.FromInt(5)},
			},
			expectedPatched: []int{3, 2},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := cs.batchContext()
			var objects []client.Object
			for _, pod := range ctx.Pods {
				objects = append(objects, pod)
			}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			patcher := NewLabelPatcher(cli, klog.ObjectRef{Name: "test"}, cs.Batches)
			patcher.patchPodBatchLabel(ctx.Pods, ctx)

			podList := &corev1.PodList{}
			err := cli.List(context.TODO(), podList)
			Expect(err).NotTo(HaveOccurred())
			patched := make([]int, ctx.CurrentBatch+1)
			for _, pod := range podList.Items {
				if pod.Labels[v1beta1.RolloutIDLabel] == ctx.RolloutID {
					if batchID, err := strconv.Atoi(pod.Labels[v1beta1.RolloutBatchIDLabel]); err == nil {
						patched[batchID-1]++
					}
				}
			}
			Expect(patched).To(Equal(cs.expectedPatched))
		})
	}
}

func generatePods(ordinalBegin, ordinalEnd, labeled int32, rolloutID, batchID, version string) []*corev1.Pod {
	podsWithLabel := generateLabeledPods(map[string]string{
		v1beta1.RolloutIDLabel:              rolloutID,
		v1beta1.RolloutBatchIDLabel:         batchID,
		apps.ControllerRevisionHashLabelKey: version,
	}, int(labeled), int(ordinalBegin))

	total := ordinalEnd - ordinalBegin + 1
	podsWithoutLabel := generateLabeledPods(map[string]string{
		apps.ControllerRevisionHashLabelKey: version,
	}, int(total-labeled), int(labeled+ordinalBegin))
	return append(podsWithoutLabel, podsWithLabel...)
}
