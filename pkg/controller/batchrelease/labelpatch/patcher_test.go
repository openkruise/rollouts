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
	"reflect"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
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
					CanaryReplicas: intstr.FromInt32(5),
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
					CanaryReplicas: intstr.FromInt32(5),
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
					CanaryReplicas: intstr.FromInt32(5),
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
					CanaryReplicas: intstr.FromInt32(5),
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
					CanaryReplicas: intstr.FromInt32(5),
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
					CanaryReplicas: intstr.FromInt32(5),
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
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
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
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
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
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
			},
			expectedPatched: []int{3, 2},
		},
		"batch-id out of range, equal to no pods are labelled": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generatePods(1, 5, 3,
					"rollout-1", strconv.Itoa(3), ctx.UpdateRevision)
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
			},
			expectedPatched: []int{2, 3},
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
			_ = patcher.patchPodBatchLabel(ctx.Pods, ctx)

			podList := &corev1.PodList{}
			err := cli.List(context.TODO(), podList)
			Expect(err).NotTo(HaveOccurred())
			patched := make([]int, ctx.CurrentBatch+1)
			for _, pod := range podList.Items {
				if pod.Labels[v1beta1.RolloutIDLabel] == ctx.RolloutID {
					if batchID, err := strconv.Atoi(pod.Labels[v1beta1.RolloutBatchIDLabel]); err == nil && batchID <= len(patched) {
						patched[batchID-1]++
					}
				}
			}
			Expect(patched).To(Equal(cs.expectedPatched))
		})
	}
}

func TestDeploymentPatch(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rs-1",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "apps/v1",
					Kind:               "Deployment",
					Name:               "deploy-1",
					UID:                types.UID("deploy-1"),
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:1.14.2",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
						},
					},
				},
			},
		},
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "deploy-1",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(10)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:1.14.2",
						},
					},
				},
			},
		},
	}
	revision := util.ComputeHash(&rs.Spec.Template, nil)
	// randomly inserted to test cases, pods with this revision should be skipped and
	// the result should not be influenced
	skippedRevision := "should-skip"
	cases := map[string]struct {
		batchContext    func() *batchcontext.BatchContext
		Batches         []v1beta1.ReleaseBatch
		expectedPatched []int
		modifier        func(rs *appsv1.ReplicaSet, deploy *appsv1.Deployment)
		expectErr       bool
	}{
		"10 pods, 0 patched, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, ctx.Replicas, 0, "", "")
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt32(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 2 patched, 3 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, ctx.Replicas, 2,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt32(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 5 patched, 0 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, ctx.Replicas, 5,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt32(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 7 patched, 0 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, ctx.Replicas, 7,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt32(5),
				},
			},
			expectedPatched: []int{7},
		},
		"2 pods, 0 patched, 2 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, 2, 0,
					ctx.RolloutID, strconv.Itoa(int(ctx.CurrentBatch+1)))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt32(5),
				},
			},
			expectedPatched: []int{2},
		},
		"10 pods, 3 patched with old rollout-id, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, ctx.Replicas, 3,
					"previous-rollout-id", strconv.Itoa(int(ctx.CurrentBatch+1)))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{
					CanaryReplicas: intstr.FromInt32(5),
				},
			},
			expectedPatched: []int{5},
		},
		"10 pods, 2 patched with batch-id:1, 3 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, 5, 2,
					"rollout-1", strconv.Itoa(1))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
			},
			expectedPatched: []int{2, 3},
		},
		"10 pods, 0 patched with batch-id:1, 5 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, 5, 0,
					"rollout-1", strconv.Itoa(1))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
			},
			expectedPatched: []int{2, 3},
		},
		"10 pods, 3 patched with batch-id:1, 2 new patched": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, 5, 3,
					"rollout-1", strconv.Itoa(1))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt32(2)},
				{CanaryReplicas: intstr.FromInt32(5)},
			},
			expectedPatched: []int{3, 2},
		},
		"rs with no controller": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, 5, 3,
					"rollout-1", strconv.Itoa(1))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt(2)},
				{CanaryReplicas: intstr.FromInt(5)},
			},
			expectedPatched: []int{3, 2},
			expectErr:       true,
			modifier: func(rs *appsv1.ReplicaSet, deploy *appsv1.Deployment) {
				rs.OwnerReferences = nil
			},
		},
		"failed to get deployment": {
			batchContext: func() *batchcontext.BatchContext {
				ctx := &batchcontext.BatchContext{
					RolloutID:              "rollout-1",
					UpdateRevision:         revision,
					PlannedUpdatedReplicas: 5,
					CurrentBatch:           1,
					Replicas:               10,
				}
				pods := generateDeploymentPods(1, 5, 3,
					"rollout-1", strconv.Itoa(1))
				ctx.Pods = pods
				return ctx
			},
			Batches: []v1beta1.ReleaseBatch{
				{CanaryReplicas: intstr.FromInt(2)},
				{CanaryReplicas: intstr.FromInt(5)},
			},
			expectedPatched: []int{3, 2},
			expectErr:       true,
			modifier: func(rs *appsv1.ReplicaSet, deploy *appsv1.Deployment) {
				deploy.Name = "not-exist"
			},
		},
	}
	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := cs.batchContext()
			insertedSkippedPodNum := int32(rand.Intn(3))
			if insertedSkippedPodNum > 0 {
				ctx.Pods = append(ctx.Pods, generatePods(
					100, 99+insertedSkippedPodNum, 0, "doesn't matter", "1", skippedRevision)...)
			}
			t.Logf("%d should-skip pods inserted", insertedSkippedPodNum)
			if rand.Intn(2) > 0 {
				now := metav1.Now()
				ctx.Pods = append(ctx.Pods, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: &now,
						Labels: map[string]string{
							appsv1.ControllerRevisionHashLabelKey: skippedRevision,
						},
					},
				})
				t.Logf("deleted pod inserted")
			}
			var objects []client.Object
			for _, pod := range ctx.Pods {
				objects = append(objects, pod)
			}
			rsCopy, deployCopy := rs.DeepCopy(), deploy.DeepCopy()
			if cs.modifier != nil {
				cs.modifier(rsCopy, deployCopy)
			}
			objects = append(objects, rsCopy, deployCopy)
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			patcher := NewLabelPatcher(cli, klog.ObjectRef{Name: "test"}, cs.Batches)
			if err := patcher.patchPodBatchLabel(ctx.Pods, ctx); err != nil {
				if cs.expectErr {
					return
				}
				t.Fatalf("failed to patch pods: %v", err)
			}
			if cs.expectErr {
				t.Fatalf("errors should occur")
			}

			podList := &corev1.PodList{}
			if err := cli.List(context.TODO(), podList); err != nil {
				t.Fatalf("failed to list pods: %v", err)
			}
			patched := make([]int, ctx.CurrentBatch+1)
			for _, pod := range podList.Items {
				if pod.Labels[v1beta1.RolloutIDLabel] == ctx.RolloutID {
					if batchID, err := strconv.Atoi(pod.Labels[v1beta1.RolloutBatchIDLabel]); err == nil {
						patched[batchID-1]++
					}
				}
			}
			if !reflect.DeepEqual(patched, cs.expectedPatched) {
				t.Fatalf("expected patched: %v, got: %v", cs.expectedPatched, patched)
			}
			for _, pod := range podList.Items {
				if pod.Labels[appsv1.ControllerRevisionHashLabelKey] != revision &&
					pod.Labels[appsv1.ControllerRevisionHashLabelKey] != skippedRevision {
					t.Fatalf("expected pod %s/%s to have revision %s, got %s", pod.Namespace, pod.Name, revision, pod.Labels[appsv1.ControllerRevisionHashLabelKey])
				}
			}
		})
	}
}

func generateDeploymentPods(ordinalBegin, ordinalEnd, labeled int32, rolloutID, batchID string) []*corev1.Pod {
	podsWithLabel := generateLabeledPods(map[string]string{
		v1beta1.RolloutIDLabel:      rolloutID,
		v1beta1.RolloutBatchIDLabel: batchID,
	}, int(labeled), int(ordinalBegin))

	total := ordinalEnd - ordinalBegin + 1
	podsWithoutLabel := generateLabeledPods(map[string]string{}, int(total-labeled), int(labeled+ordinalBegin))
	pods := append(podsWithoutLabel, podsWithLabel...)
	for _, pod := range pods {
		pod.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       "rs-1",
				UID:        "123",
				Controller: pointer.Bool(true),
			},
		}
	}
	return pods
}

func generatePods(ordinalBegin, ordinalEnd, labeled int32, rolloutID, batchID, version string) []*corev1.Pod {
	podsWithLabel := generateLabeledPods(map[string]string{
		v1beta1.RolloutIDLabel:                rolloutID,
		v1beta1.RolloutBatchIDLabel:           batchID,
		appsv1.ControllerRevisionHashLabelKey: version,
	}, int(labeled), int(ordinalBegin))

	total := ordinalEnd - ordinalBegin + 1
	podsWithoutLabel := generateLabeledPods(map[string]string{
		appsv1.ControllerRevisionHashLabelKey: version,
	}, int(total-labeled), int(labeled+ordinalBegin))
	return append(podsWithoutLabel, podsWithLabel...)
}
