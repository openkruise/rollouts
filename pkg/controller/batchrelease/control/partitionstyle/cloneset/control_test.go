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
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
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
				Partition:      &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
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
			CollisionCount:       pointer.Int32(1),
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

	percent := intstr.FromString("20%")
	cases := map[string]struct {
		workload func() *kruiseappsv1alpha1.CloneSet
		release  func() *v1beta1.BatchRelease
		result   *batchcontext.BatchContext
	}{
		"without NoNeedUpdate": {
			workload: func() *kruiseappsv1alpha1.CloneSet {
				return &kruiseappsv1alpha1.CloneSet{
					Spec: kruiseappsv1alpha1.CloneSetSpec{
						Replicas: pointer.Int32(10),
						UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
							Partition: func() *intstr.IntOrString { p := intstr.FromString("100%"); return &p }(),
						},
					},
					Status: kruiseappsv1alpha1.CloneSetStatus{
						Replicas:             10,
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
							FailureThreshold: &percent,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: percent,
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 0,
						},
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				FailureThreshold:       &percent,
				CurrentBatch:           0,
				Replicas:               10,
				UpdatedReplicas:        5,
				UpdatedReadyReplicas:   5,
				PlannedUpdatedReplicas: 2,
				DesiredUpdatedReplicas: 2,
				CurrentPartition:       intstr.FromString("100%"),
				DesiredPartition:       intstr.FromString("80%"),
			},
		},
		"with NoNeedUpdate": {
			workload: func() *kruiseappsv1alpha1.CloneSet {
				return &kruiseappsv1alpha1.CloneSet{
					Spec: kruiseappsv1alpha1.CloneSetSpec{
						Replicas: pointer.Int32(20),
						UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
							Partition: func() *intstr.IntOrString { p := intstr.FromString("100%"); return &p }(),
						},
					},
					Status: kruiseappsv1alpha1.CloneSetStatus{
						Replicas:             20,
						UpdatedReplicas:      10,
						UpdatedReadyReplicas: 10,
						AvailableReplicas:    20,
						ReadyReplicas:        20,
					},
				}
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FailureThreshold: &percent,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: percent,
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch:         0,
							NoNeedUpdateReplicas: pointer.Int32(10),
						},
						UpdateRevision: "update-version",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           0,
				UpdateRevision:         "update-version",
				Replicas:               20,
				UpdatedReplicas:        10,
				UpdatedReadyReplicas:   10,
				NoNeedUpdatedReplicas:  pointer.Int32(10),
				PlannedUpdatedReplicas: 4,
				DesiredUpdatedReplicas: 12,
				CurrentPartition:       intstr.FromString("100%"),
				DesiredPartition:       intstr.FromString("40%"),
				FailureThreshold:       &percent,
				FilterFunc:             labelpatch.FilterPodsForUnorderedUpdate,
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
			fmt.Println(got)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Log()).Should(Equal(cs.result.Log()))
		})
	}
}

func TestRealController(t *testing.T) {
	RegisterFailHandler(Fail)

	release := releaseDemo.DeepCopy()
	clone := cloneDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, clone).Build()
	c := NewController(cli, cloneKey, clone.GroupVersionKind()).(*realController)
	controller, err := c.BuildController()
	Expect(err).NotTo(HaveOccurred())

	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch := &kruiseappsv1alpha1.CloneSet{}
	Expect(cli.Get(context.TODO(), cloneKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Spec.UpdateStrategy.Paused).Should(BeFalse())
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))
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
	Expect(fetch.Spec.UpdateStrategy.Partition.StrVal).Should(Equal("90%"))

	err = controller.Finalize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch = &kruiseappsv1alpha1.CloneSet{}
	Expect(cli.Get(context.TODO(), cloneKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(""))

	stableInfo := controller.GetWorkloadInfo()
	Expect(stableInfo).ShouldNot(BeNil())
	checkWorkloadInfo(stableInfo, clone)
}

func checkWorkloadInfo(stableInfo *util.WorkloadInfo, clone *kruiseappsv1alpha1.CloneSet) {
	Expect(stableInfo.Replicas).Should(Equal(*clone.Spec.Replicas))
	Expect(stableInfo.Status.Replicas).Should(Equal(clone.Status.Replicas))
	Expect(stableInfo.Status.ReadyReplicas).Should(Equal(clone.Status.ReadyReplicas))
	Expect(stableInfo.Status.UpdatedReplicas).Should(Equal(clone.Status.UpdatedReplicas))
	Expect(stableInfo.Status.UpdatedReadyReplicas).Should(Equal(clone.Status.UpdatedReadyReplicas))
	Expect(stableInfo.Status.UpdateRevision).Should(Equal(clone.Status.UpdateRevision))
	Expect(stableInfo.Status.StableRevision).Should(Equal(clone.Status.CurrentRevision))
	Expect(stableInfo.Status.AvailableReplicas).Should(Equal(clone.Status.AvailableReplicas))
	Expect(stableInfo.Status.ObservedGeneration).Should(Equal(clone.Status.ObservedGeneration))
}

func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}
