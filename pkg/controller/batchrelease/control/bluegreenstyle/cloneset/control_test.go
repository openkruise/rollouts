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

package cloneset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	control "github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()

	cloneKey = types.NamespacedName{
		Namespace: "default",
		Name:      "cloneset",
	}
	cloneDemo = &kruiseappsv1alpha1.CloneSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps.kruise.io/v1alpha1",
			Kind:       "CloneSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       cloneKey.Name,
			Namespace:  cloneKey.Namespace,
			Generation: 1,
			Labels: map[string]string{
				"app": "busybox",
			},
			Annotations: map[string]string{
				"type": "unit-test",
			},
		},
		Spec: kruiseappsv1alpha1.CloneSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Replicas: pointer.Int32(10),
			UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
				Paused:         true,
				Partition:      &intstr.IntOrString{Type: intstr.String, StrVal: "0%"},
				MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "busybox",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "busybox",
							Image: "busybox:latest",
						},
					},
				},
			},
		},
		Status: kruiseappsv1alpha1.CloneSetStatus{
			Replicas:             10,
			UpdatedReplicas:      0,
			ReadyReplicas:        10,
			AvailableReplicas:    10,
			UpdatedReadyReplicas: 0,
			UpdateRevision:       "version-2",
			CurrentRevision:      "version-1",
			ObservedGeneration:   1,
			CollisionCount:       pointer.Int32Ptr(1),
		},
	}

	releaseDemo = &v1beta1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rollouts.kruise.io/v1alpha1",
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: cloneKey.Namespace,
			UID:       uuid.NewUUID(),
		},
		Spec: v1beta1.BatchReleaseSpec{
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
					},
					{
						CanaryReplicas: intstr.FromString("50%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				},
			},
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: cloneDemo.APIVersion,
				Kind:       cloneDemo.Kind,
				Name:       cloneDemo.Name,
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch: 0,
			},
		},
	}
)

func init() {
	apps.AddToScheme(scheme)
	rolloutapi.AddToScheme(scheme)
	kruiseappsv1alpha1.AddToScheme(scheme)
}

func TestCalculateBatchContext(t *testing.T) {
	RegisterFailHandler(Fail)
	cases := map[string]struct {
		workload func() *kruiseappsv1alpha1.CloneSet
		release  func() *v1beta1.BatchRelease
		result   *batchcontext.BatchContext
	}{
		"normal case batch0": {
			workload: func() *kruiseappsv1alpha1.CloneSet {
				return &kruiseappsv1alpha1.CloneSet{
					Spec: kruiseappsv1alpha1.CloneSetSpec{
						Replicas: pointer.Int32Ptr(10),
						UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
							MaxSurge: func() *intstr.IntOrString { p := intstr.FromInt(1); return &p }(),
						},
					},
					Status: kruiseappsv1alpha1.CloneSetStatus{
						Replicas:             10,
						UpdatedReplicas:      0,
						UpdatedReadyReplicas: 0,
						AvailableReplicas:    10,
					},
				}
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							Batches: []v1beta1.ReleaseBatch{
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "50%"}},
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 0,
						},
						UpdateRevision: "update-version",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           0,
				DesiredSurge:           intstr.FromString("50%"),
				CurrentSurge:           intstr.FromInt(0),
				Replicas:               10,
				UpdatedReplicas:        0,
				UpdatedReadyReplicas:   0,
				UpdateRevision:         "update-version",
				PlannedUpdatedReplicas: 5,
				DesiredUpdatedReplicas: 5,
			},
		},

		"normal case batch1": {
			workload: func() *kruiseappsv1alpha1.CloneSet {
				return &kruiseappsv1alpha1.CloneSet{
					Spec: kruiseappsv1alpha1.CloneSetSpec{
						Replicas: pointer.Int32(10),
						UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
							MaxSurge: func() *intstr.IntOrString { p := intstr.FromString("50%"); return &p }(),
						},
					},
					Status: kruiseappsv1alpha1.CloneSetStatus{
						Replicas:             15,
						UpdatedReplicas:      5,
						UpdatedReadyReplicas: 5,
						AvailableReplicas:    10,
					},
				}
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							Batches: []v1beta1.ReleaseBatch{
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "50%"}},
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 1,
						},
						UpdateRevision: "update-version",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           1,
				DesiredSurge:           intstr.FromString("100%"),
				CurrentSurge:           intstr.FromString("50%"),
				Replicas:               10,
				UpdatedReplicas:        5,
				UpdatedReadyReplicas:   5,
				UpdateRevision:         "update-version",
				PlannedUpdatedReplicas: 10,
				DesiredUpdatedReplicas: 10,
			},
		},
		"normal case batch2": {
			workload: func() *kruiseappsv1alpha1.CloneSet {
				return &kruiseappsv1alpha1.CloneSet{
					Spec: kruiseappsv1alpha1.CloneSetSpec{
						Replicas: pointer.Int32Ptr(10),
						UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
							MaxSurge: func() *intstr.IntOrString { p := intstr.FromString("100%"); return &p }(),
						},
					},
					Status: kruiseappsv1alpha1.CloneSetStatus{
						Replicas:             20,
						UpdatedReplicas:      10,
						UpdatedReadyReplicas: 10,
						AvailableReplicas:    10,
						ReadyReplicas:        20,
					},
				}
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							Batches: []v1beta1.ReleaseBatch{
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "50%"}},
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
								{CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 2,
						},
						UpdateRevision: "update-version",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           2,
				UpdateRevision:         "update-version",
				DesiredSurge:           intstr.FromString("100%"),
				CurrentSurge:           intstr.FromString("100%"),
				Replicas:               10,
				UpdatedReplicas:        10,
				UpdatedReadyReplicas:   10,
				PlannedUpdatedReplicas: 10,
				DesiredUpdatedReplicas: 10,
			},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			control := realController{
				object:       cs.workload(),
				WorkloadInfo: util.ParseWorkload(cs.workload()),
			}
			got, err := control.CalculateBatchContext(cs.release())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Log()).Should(Equal(cs.result.Log()))
		})
	}
}

func TestRealController(t *testing.T) {
	RegisterFailHandler(Fail)

	release := releaseDemo.DeepCopy()
	clone := cloneDemo.DeepCopy()
	// for unit test we should set some default value since no webhook or controller is working
	clone.Spec.UpdateStrategy.Type = kruiseappsv1alpha1.RecreateCloneSetUpdateStrategyType
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, clone).Build()
	// build new controller
	c := NewController(cli, cloneKey, clone.GroupVersionKind()).(*realController)
	controller, err := c.BuildController()
	Expect(err).NotTo(HaveOccurred())
	// call Initialize
	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch := &kruiseappsv1alpha1.CloneSet{}
	Expect(cli.Get(context.TODO(), cloneKey, fetch)).NotTo(HaveOccurred())
	// check strategy
	Expect(fetch.Spec.UpdateStrategy.Type).Should(Equal(kruiseappsv1alpha1.RecreateCloneSetUpdateStrategyType))
	// partition is set to 100% in webhook, therefore we cannot observe it in unit test
	// Expect(reflect.DeepEqual(fetch.Spec.UpdateStrategy.Partition, &intstr.IntOrString{Type: intstr.String, StrVal: "100%"})).Should(BeTrue())
	Expect(reflect.DeepEqual(fetch.Spec.UpdateStrategy.MaxSurge, &intstr.IntOrString{Type: intstr.Int, IntVal: 1})).Should(BeTrue())
	Expect(reflect.DeepEqual(fetch.Spec.UpdateStrategy.MaxUnavailable, &intstr.IntOrString{Type: intstr.Int, IntVal: 0})).Should(BeTrue())
	Expect(fetch.Spec.MinReadySeconds).Should(Equal(int32(v1beta1.MaxReadySeconds)))
	// check annotations
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))
	Expect(fetch.Annotations[v1beta1.OriginalDeploymentStrategyAnnotation]).Should(Equal(util.DumpJSON(&control.OriginalDeploymentStrategy{
		MaxUnavailable:  &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		MaxSurge:        &intstr.IntOrString{Type: intstr.String, StrVal: "0%"},
		MinReadySeconds: 0,
	})))

	c.object = fetch // mock

	for {
		batchContext, err := controller.CalculateBatchContext(release)
		Expect(err).NotTo(HaveOccurred())
		err = controller.UpgradeBatch(batchContext)
		fetch = &kruiseappsv1alpha1.CloneSet{}
		// mock
		Expect(cli.Get(context.TODO(), cloneKey, fetch)).NotTo(HaveOccurred())
		c.object = fetch
		if err == nil {
			break
		}
	}

	fetch = &kruiseappsv1alpha1.CloneSet{}
	Expect(cli.Get(context.TODO(), cloneKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Spec.UpdateStrategy.MaxSurge.StrVal).Should(Equal("10%"))
	Expect(fetch.Spec.UpdateStrategy.MaxUnavailable.IntVal).Should(Equal(int32(0)))
	Expect(fetch.Spec.UpdateStrategy.Paused).Should(Equal(false))
	Expect(fetch.Spec.MinReadySeconds).Should(Equal(int32(v1beta1.MaxReadySeconds)))
	Expect(fetch.Annotations[v1beta1.OriginalDeploymentStrategyAnnotation]).Should(Equal(util.DumpJSON(&control.OriginalDeploymentStrategy{
		MaxUnavailable:  &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		MaxSurge:        &intstr.IntOrString{Type: intstr.String, StrVal: "0%"},
		MinReadySeconds: 0,
	})))

	controller.Finalize(release)
	fetch = &kruiseappsv1alpha1.CloneSet{}
	Expect(cli.Get(context.TODO(), cloneKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Spec.UpdateStrategy.MaxSurge.StrVal).Should(Equal("0%"))
	Expect(fetch.Spec.UpdateStrategy.MaxUnavailable.IntVal).Should(Equal(int32(1)))
	Expect(fetch.Spec.UpdateStrategy.Paused).Should(Equal(false))
	Expect(fetch.Spec.MinReadySeconds).Should(Equal(int32(0)))
}

func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}
