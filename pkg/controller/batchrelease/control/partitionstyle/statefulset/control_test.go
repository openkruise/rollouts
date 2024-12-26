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

package statefulset

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1pub "github.com/openkruise/kruise-api/apps/pub"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruiseappsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
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
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()

	stsKey = types.NamespacedName{
		Namespace: "default",
		Name:      "statefulset",
	}
	stsDemo = &kruiseappsv1beta1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps.kruise.io/v1alpha1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       stsKey.Name,
			Namespace:  stsKey.Namespace,
			Generation: 1,
			Labels: map[string]string{
				"app": "busybox",
			},
			Annotations: map[string]string{
				"type": "unit-test",
			},
		},
		Spec: kruiseappsv1beta1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Replicas: pointer.Int32(10),
			UpdateStrategy: kruiseappsv1beta1.StatefulSetUpdateStrategy{
				Type: apps.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &kruiseappsv1beta1.RollingUpdateStatefulSetStrategy{
					Paused:         true,
					Partition:      pointer.Int32(10),
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
				},
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
		Status: kruiseappsv1beta1.StatefulSetStatus{
			Replicas:             10,
			UpdatedReplicas:      0,
			ReadyReplicas:        10,
			AvailableReplicas:    10,
			UpdateRevision:       "version-2",
			CurrentRevision:      "version-1",
			ObservedGeneration:   1,
			UpdatedReadyReplicas: 0,
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
			Namespace: stsKey.Namespace,
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
				APIVersion: stsDemo.APIVersion,
				Kind:       stsDemo.Kind,
				Name:       stsDemo.Name,
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
	rand.Seed(87076677)
	apps.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	rolloutapi.AddToScheme(scheme)
	kruiseappsv1alpha1.AddToScheme(scheme)
	kruiseappsv1beta1.AddToScheme(scheme)
}

func TestCalculateBatchContextForNativeStatefulSet(t *testing.T) {
	RegisterFailHandler(Fail)

	percent := intstr.FromString("20%")
	cases := map[string]struct {
		workload func() *apps.StatefulSet
		release  func() *v1beta1.BatchRelease
		pods     func() []*corev1.Pod
		result   *batchcontext.BatchContext
	}{
		"without NoNeedUpdate": {
			workload: func() *apps.StatefulSet {
				return &apps.StatefulSet{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "apps/v1",
						Kind:       "StatefulSet",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts",
						Namespace: "test",
						UID:       "test",
					},
					Spec: apps.StatefulSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}},
						Replicas: pointer.Int32(10),
						UpdateStrategy: apps.StatefulSetUpdateStrategy{
							Type: apps.RollingUpdateStatefulSetStrategyType,
							RollingUpdate: &apps.RollingUpdateStatefulSetStrategy{
								Partition: pointer.Int32(100),
							},
						},
					},
					Status: apps.StatefulSetStatus{
						Replicas:          10,
						UpdatedReplicas:   5,
						AvailableReplicas: 10,
						CurrentRevision:   "stable-version",
						UpdateRevision:    "update-version",
					},
				}
			},
			pods: func() []*corev1.Pod {
				stablePods := generatePods(5, "stable-version", "True")
				updatedReadyPods := generatePods(5, "update-version", "True")
				return append(stablePods, updatedReadyPods...)
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-br",
						Namespace: "test",
					},
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
						UpdateRevision: "update-version",
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
				UpdateRevision:         "update-version",
				CurrentPartition:       intstr.FromInt(100),
				DesiredPartition:       intstr.FromInt(8),
				Pods:                   generatePods(10, "", ""),
			},
		},
		"with NoNeedUpdate": {
			workload: func() *apps.StatefulSet {
				return &apps.StatefulSet{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "apps/v1",
						Kind:       "StatefulSet",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts",
						Namespace: "test",
						UID:       "test",
					},
					Spec: apps.StatefulSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}},
						Replicas: pointer.Int32(20),
						UpdateStrategy: apps.StatefulSetUpdateStrategy{
							Type: apps.RollingUpdateStatefulSetStrategyType,
							RollingUpdate: &apps.RollingUpdateStatefulSetStrategy{
								Partition: pointer.Int32(100),
							},
						},
					},
					Status: apps.StatefulSetStatus{
						Replicas:          20,
						UpdatedReplicas:   10,
						AvailableReplicas: 20,
						CurrentRevision:   "stable-version",
						UpdateRevision:    "update-version",
					},
				}
			},
			pods: func() []*corev1.Pod {
				stablePods := generatePods(10, "stable-version", "True")
				updatedReadyPods := generatePods(10, "update-version", "True")
				return append(stablePods, updatedReadyPods...)
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-br",
						Namespace: "test",
					},
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
				CurrentPartition:       intstr.FromInt(100),
				DesiredPartition:       intstr.FromInt(18),
				FailureThreshold:       &percent,
				FilterFunc:             labelpatch.FilterPodsForUnorderedUpdate,
				Pods:                   generatePods(20, "", ""),
			},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			pods := func() []client.Object {
				var objects []client.Object
				pods := cs.pods()
				for _, pod := range pods {
					objects = append(objects, pod)
				}
				return objects
			}()
			br := cs.release()
			sts := cs.workload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(br, sts).WithObjects(pods...).Build()
			control := realController{
				client: cli,
				gvk:    sts.GetObjectKind().GroupVersionKind(),
				key:    types.NamespacedName{Namespace: "test", Name: "test-sts"},
			}
			c, err := control.BuildController()
			Expect(err).NotTo(HaveOccurred())
			got, err := c.CalculateBatchContext(cs.release())
			fmt.Println(got.Log())
			fmt.Println(cs.result.Log())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Log()).Should(Equal(cs.result.Log()))
		})
	}
}

func TestCalculateBatchContextForAdvancedStatefulSet(t *testing.T) {
	RegisterFailHandler(Fail)

	percent := intstr.FromString("20%")
	cases := map[string]struct {
		workload func() *kruiseappsv1beta1.StatefulSet
		release  func() *v1beta1.BatchRelease
		pods     func() []*corev1.Pod
		result   *batchcontext.BatchContext
	}{
		"without NoNeedUpdate": {
			workload: func() *kruiseappsv1beta1.StatefulSet {
				return &kruiseappsv1beta1.StatefulSet{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "apps.kruise.io/v1beta1",
						Kind:       "StatefulSet",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts",
						Namespace: "test",
						UID:       "test",
					},
					Spec: kruiseappsv1beta1.StatefulSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}},
						Replicas: pointer.Int32(10),
						UpdateStrategy: kruiseappsv1beta1.StatefulSetUpdateStrategy{
							Type: apps.RollingUpdateStatefulSetStrategyType,
							RollingUpdate: &kruiseappsv1beta1.RollingUpdateStatefulSetStrategy{
								Partition: pointer.Int32(100),
								UnorderedUpdate: &kruiseappsv1beta1.UnorderedUpdateStrategy{
									PriorityStrategy: &appsv1pub.UpdatePriorityStrategy{
										OrderPriority: []appsv1pub.UpdatePriorityOrderTerm{
											{
												OrderedKey: "test",
											},
										},
									},
								},
							},
						},
					},
					Status: kruiseappsv1beta1.StatefulSetStatus{
						Replicas:          10,
						UpdatedReplicas:   5,
						AvailableReplicas: 10,
						CurrentRevision:   "stable-version",
						UpdateRevision:    "update-version",
					},
				}
			},
			pods: func() []*corev1.Pod {
				stablePods := generatePods(5, "stable-version", "True")
				updatedReadyPods := generatePods(5, "update-version", "True")
				return append(stablePods, updatedReadyPods...)
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-br",
						Namespace: "test",
					},
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
						UpdateRevision: "update-version",
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
				UpdateRevision:         "update-version",
				CurrentPartition:       intstr.FromInt(100),
				DesiredPartition:       intstr.FromInt(8),
				Pods:                   generatePods(10, "", ""),
			},
		},
		"with NoNeedUpdate": {
			workload: func() *kruiseappsv1beta1.StatefulSet {
				return &kruiseappsv1beta1.StatefulSet{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "apps.kruise.io/v1beta1",
						Kind:       "StatefulSet",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts",
						Namespace: "test",
						UID:       "test",
					},
					Spec: kruiseappsv1beta1.StatefulSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}},
						Replicas: pointer.Int32(20),
						UpdateStrategy: kruiseappsv1beta1.StatefulSetUpdateStrategy{
							Type: apps.RollingUpdateStatefulSetStrategyType,
							RollingUpdate: &kruiseappsv1beta1.RollingUpdateStatefulSetStrategy{
								Partition: pointer.Int32(100),
								UnorderedUpdate: &kruiseappsv1beta1.UnorderedUpdateStrategy{
									PriorityStrategy: &appsv1pub.UpdatePriorityStrategy{
										OrderPriority: []appsv1pub.UpdatePriorityOrderTerm{
											{
												OrderedKey: "test",
											},
										},
									},
								},
							},
						},
					},
					Status: kruiseappsv1beta1.StatefulSetStatus{
						Replicas:          20,
						UpdatedReplicas:   10,
						AvailableReplicas: 20,
						CurrentRevision:   "stable-version",
						UpdateRevision:    "update-version",
					},
				}
			},
			pods: func() []*corev1.Pod {
				stablePods := generatePods(10, "stable-version", "True")
				updatedReadyPods := generatePods(10, "update-version", "True")
				return append(stablePods, updatedReadyPods...)
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-br",
						Namespace: "test",
					},
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
				CurrentPartition:       intstr.FromInt(100),
				DesiredPartition:       intstr.FromInt(8),
				FailureThreshold:       &percent,
				FilterFunc:             labelpatch.FilterPodsForUnorderedUpdate,
				Pods:                   generatePods(20, "", ""),
			},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			pods := func() []client.Object {
				var objects []client.Object
				pods := cs.pods()
				for _, pod := range pods {
					objects = append(objects, pod)
				}
				return objects
			}()
			br := cs.release()
			sts := cs.workload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(br, sts).WithObjects(pods...).Build()
			control := realController{
				client: cli,
				gvk:    sts.GetObjectKind().GroupVersionKind(),
				key:    types.NamespacedName{Namespace: "test", Name: "test-sts"},
			}
			c, err := control.BuildController()
			Expect(err).NotTo(HaveOccurred())
			got, err := c.CalculateBatchContext(cs.release())
			fmt.Println(got.Log())
			fmt.Println(cs.result.Log())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Log()).Should(Equal(cs.result.Log()))
		})
	}
}

func TestRealController(t *testing.T) {
	RegisterFailHandler(Fail)

	release := releaseDemo.DeepCopy()
	release.Spec.ReleasePlan.RolloutID = "1"
	sts := stsDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, sts).Build()
	c := NewController(cli, stsKey, sts.GroupVersionKind()).(*realController)
	controller, err := c.BuildController()
	Expect(err).NotTo(HaveOccurred())

	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch := &kruiseappsv1beta1.StatefulSet{}
	Expect(cli.Get(context.TODO(), stsKey, fetch)).NotTo(HaveOccurred())
	Expect(*fetch.Spec.UpdateStrategy.RollingUpdate.Partition).Should(BeNumerically(">=", 1000))
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))
	c.object = fetch // mock

	for {
		batchContext, err := controller.CalculateBatchContext(release)
		Expect(err).NotTo(HaveOccurred())
		err = controller.UpgradeBatch(batchContext)
		// mock
		fetch = &kruiseappsv1beta1.StatefulSet{}
		Expect(cli.Get(context.TODO(), stsKey, fetch)).NotTo(HaveOccurred())
		c.object = fetch
		if err == nil {
			break
		}
	}
	fetch = &kruiseappsv1beta1.StatefulSet{}
	Expect(cli.Get(context.TODO(), stsKey, fetch)).NotTo(HaveOccurred())
	Expect(*fetch.Spec.UpdateStrategy.RollingUpdate.Partition).Should(BeNumerically("==", 9))

	// mock
	_ = controller.Finalize(release)
	fetch = &kruiseappsv1beta1.StatefulSet{}
	Expect(cli.Get(context.TODO(), stsKey, fetch)).NotTo(HaveOccurred())
	c.object = fetch
	err = controller.Finalize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch = &kruiseappsv1beta1.StatefulSet{}
	Expect(cli.Get(context.TODO(), stsKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(""))

	stableInfo := controller.GetWorkloadInfo()
	Expect(stableInfo).ShouldNot(BeNil())
	checkWorkloadInfo(stableInfo, sts)
}

func checkWorkloadInfo(stableInfo *util.WorkloadInfo, sts *kruiseappsv1beta1.StatefulSet) {
	Expect(stableInfo.Replicas).Should(Equal(*sts.Spec.Replicas))
	Expect(stableInfo.Status.Replicas).Should(Equal(sts.Status.Replicas))
	Expect(stableInfo.Status.ReadyReplicas).Should(Equal(sts.Status.ReadyReplicas))
	Expect(stableInfo.Status.UpdatedReplicas).Should(Equal(sts.Status.UpdatedReplicas))
	Expect(stableInfo.Status.UpdateRevision).Should(Equal(sts.Status.UpdateRevision))
	Expect(stableInfo.Status.UpdatedReadyReplicas).Should(Equal(sts.Status.UpdatedReadyReplicas))
	Expect(stableInfo.Status.StableRevision).Should(Equal(sts.Status.CurrentRevision))
	Expect(stableInfo.Status.AvailableReplicas).Should(Equal(sts.Status.AvailableReplicas))
	Expect(stableInfo.Status.ObservedGeneration).Should(Equal(sts.Status.ObservedGeneration))
}

func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}

func generatePods(replicas int, version, readyStatus string) []*corev1.Pod {
	var pods []*corev1.Pod
	for replicas > 0 {
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      fmt.Sprintf("pod-%s", rand.String(10)),
				Labels: map[string]string{
					"app":                               "foo",
					apps.ControllerRevisionHashLabelKey: version,
				},
				OwnerReferences: []metav1.OwnerReference{{
					UID:        "test",
					Controller: pointer.Bool(true),
				}},
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: corev1.ConditionStatus(readyStatus),
				}},
			},
		})
		replicas--
	}
	return pods
}
