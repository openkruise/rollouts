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
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
		expectedPatched int
	}{
		"10 pods, 0 patched, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           0,
					Replicas:               10,
				}
				pods := generatePods(1, ctx.Replicas, 0, "", "", ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			expectedPatched: 5,
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
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			expectedPatched: 5,
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
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			expectedPatched: 5,
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
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			expectedPatched: 7,
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
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			expectedPatched: 2,
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
					"previous-rollout-id", strconv.Itoa(int(ctx.CurrentBatch)), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			expectedPatched: 5,
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
			patcher := NewLabelPatcher(cli, klog.ObjectRef{Name: "test"})
			patchErr := patcher.patchPodBatchLabel(ctx.Pods, ctx)

			podList := &corev1.PodList{}
			err := cli.List(context.TODO(), podList)
			Expect(err).NotTo(HaveOccurred())
			patched := 0
			for _, pod := range podList.Items {
				if pod.Labels[util.RolloutIDLabel] == ctx.RolloutID {
					patched++
				}
			}
			Expect(patched).Should(BeNumerically("==", cs.expectedPatched))
			if patched < int(ctx.PlannedUpdatedReplicas) {
				Expect(patchErr).To(HaveOccurred())
			}
		})
	}
}

func generatePods(ordinalBegin, ordinalEnd, labeled int32, rolloutID, batchID, version string) []*corev1.Pod {
	podsWithLabel := generateLabeledPods(map[string]string{
		util.RolloutIDLabel:                 rolloutID,
		util.RolloutBatchIDLabel:            batchID,
		apps.ControllerRevisionHashLabelKey: version,
	}, int(labeled), int(ordinalBegin))

	total := ordinalEnd - ordinalBegin + 1
	podsWithoutLabel := generateLabeledPods(map[string]string{
		apps.ControllerRevisionHashLabelKey: version,
	}, int(total-labeled), int(labeled+ordinalBegin))
	return append(podsWithoutLabel, podsWithLabel...)
}
