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

package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
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

	deploymentKey = types.NamespacedName{
		Name:      "deployment",
		Namespace: "default",
	}

	deploymentDemo = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       deploymentKey.Name,
			Namespace:  deploymentKey.Namespace,
			Generation: 1,
			Labels: map[string]string{
				"app": "busybox",
			},
			Annotations: map[string]string{
				"type": "unit-test",
			},
		},
		Spec: apps.DeploymentSpec{
			Paused: true,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Replicas: pointer.Int32(10),
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
					MaxSurge:       &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
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
		Status: apps.DeploymentStatus{
			Replicas:           10,
			UpdatedReplicas:    10,
			ReadyReplicas:      10,
			AvailableReplicas:  10,
			CollisionCount:     pointer.Int32(1),
			ObservedGeneration: 1,
		},
	}

	releaseDemo = &v1beta1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rollouts.kruise.io/v1alpha1",
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: deploymentKey.Namespace,
			UID:       uuid.NewUUID(),
		},
		Spec: v1beta1.BatchReleaseSpec{
			ReleasePlan: v1beta1.ReleasePlan{
				FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
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
				APIVersion: deploymentDemo.APIVersion,
				Kind:       deploymentDemo.Kind,
				Name:       deploymentDemo.Name,
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch: 1,
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
		workload func() *apps.Deployment
		release  func() *v1beta1.BatchRelease
		result   *batchcontext.BatchContext
	}{
		"noraml case": {
			workload: func() *apps.Deployment {
				deployment := &apps.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1alpha1.DeploymentStrategyAnnotation: util.DumpJSON(&v1alpha1.DeploymentStrategy{
								RollingStyle:  v1alpha1.PartitionRollingStyle,
								RollingUpdate: &apps.RollingUpdateDeployment{MaxUnavailable: &percent, MaxSurge: &percent},
								Partition:     percent,
								Paused:        false,
							}),
							v1alpha1.DeploymentExtraStatusAnnotation: util.DumpJSON(&v1alpha1.DeploymentExtraStatus{
								UpdatedReadyReplicas:    1,
								ExpectedUpdatedReplicas: 2,
							}),
						},
					},
					Spec: apps.DeploymentSpec{
						Replicas: pointer.Int32(10),
					},
					Status: apps.DeploymentStatus{
						Replicas:          10,
						UpdatedReplicas:   2,
						AvailableReplicas: 9,
						ReadyReplicas:     9,
					},
				}
				return deployment
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FailureThreshold: &percent,
							FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
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
						UpdateRevision: "version-2",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:     0,
				UpdateRevision:   "version-2",
				DesiredPartition: percent,
				FailureThreshold: &percent,

				Replicas:               10,
				UpdatedReplicas:        2,
				UpdatedReadyReplicas:   1,
				PlannedUpdatedReplicas: 2,
				DesiredUpdatedReplicas: 2,
			},
		},
		"partition=90%, replicas=5": {
			workload: func() *apps.Deployment {
				deployment := &apps.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1alpha1.DeploymentStrategyAnnotation: util.DumpJSON(&v1alpha1.DeploymentStrategy{
								RollingStyle:  v1alpha1.PartitionRollingStyle,
								RollingUpdate: &apps.RollingUpdateDeployment{MaxUnavailable: &percent, MaxSurge: &percent},
								Partition:     intstr.FromString("20%"),
								Paused:        false,
							}),
							v1alpha1.DeploymentExtraStatusAnnotation: util.DumpJSON(&v1alpha1.DeploymentExtraStatus{
								UpdatedReadyReplicas:    4,
								ExpectedUpdatedReplicas: 4,
							}),
						},
					},
					Spec: apps.DeploymentSpec{
						Replicas: pointer.Int32(5),
					},
					Status: apps.DeploymentStatus{
						Replicas:          5,
						UpdatedReplicas:   4,
						AvailableReplicas: 5,
						ReadyReplicas:     5,
					},
				}
				return deployment
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FailureThreshold: &percent,
							FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: intstr.FromString("90%"),
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 0,
						},
						UpdateRevision: "version-2",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:     0,
				UpdateRevision:   "version-2",
				DesiredPartition: intstr.FromString("90%"),
				FailureThreshold: &percent,

				Replicas:               5,
				UpdatedReplicas:        4,
				UpdatedReadyReplicas:   4,
				PlannedUpdatedReplicas: 4,
				DesiredUpdatedReplicas: 4,
			},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cs.workload()).Build()
			control := realController{
				client: cli,
			}
			_, err := control.BuildController()
			Expect(err).NotTo(HaveOccurred())
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
	clone := deploymentDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, clone).Build()
	c := NewController(cli, deploymentKey, clone.GroupVersionKind()).(*realController)
	controller, err := c.BuildController()
	Expect(err).NotTo(HaveOccurred())

	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch := &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Spec.Paused).Should(BeTrue())
	Expect(fetch.Spec.Strategy.Type).Should(Equal(apps.RecreateDeploymentStrategyType))
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))
	strategy := util.GetDeploymentStrategy(fetch)
	Expect(strategy.Paused).Should(BeFalse())
	c.object = fetch // mock

	for {
		batchContext, err := controller.CalculateBatchContext(release)
		Expect(err).NotTo(HaveOccurred())
		err = controller.UpgradeBatch(batchContext)
		fetch := &apps.Deployment{}
		// mock
		Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
		c.object = fetch
		if err == nil {
			break
		}
	}
	fetch = &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	strategy = util.GetDeploymentStrategy(fetch)
	Expect(strategy.Partition.StrVal).Should(Equal("50%"))

	release.Spec.ReleasePlan.BatchPartition = nil
	err = controller.Finalize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch = &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(""))
	Expect(fetch.Annotations[v1alpha1.DeploymentStrategyAnnotation]).Should(Equal(""))
	Expect(fetch.Annotations[v1alpha1.DeploymentExtraStatusAnnotation]).Should(Equal(""))
	Expect(fetch.Spec.Paused).Should(BeFalse())
	Expect(fetch.Spec.Strategy.Type).Should(Equal(apps.RollingUpdateDeploymentStrategyType))

	workloadInfo := controller.GetWorkloadInfo()
	Expect(workloadInfo).ShouldNot(BeNil())
	checkWorkloadInfo(workloadInfo, clone)
}

func checkWorkloadInfo(stableInfo *util.WorkloadInfo, clone *apps.Deployment) {
	Expect(stableInfo.Replicas).Should(Equal(*clone.Spec.Replicas))
	Expect(stableInfo.Status.Replicas).Should(Equal(clone.Status.Replicas))
	Expect(stableInfo.Status.ReadyReplicas).Should(Equal(clone.Status.ReadyReplicas))
	Expect(stableInfo.Status.UpdatedReplicas).Should(Equal(clone.Status.UpdatedReplicas))
	Expect(stableInfo.Status.AvailableReplicas).Should(Equal(clone.Status.AvailableReplicas))
	Expect(stableInfo.Status.ObservedGeneration).Should(Equal(clone.Status.ObservedGeneration))
}

func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}
