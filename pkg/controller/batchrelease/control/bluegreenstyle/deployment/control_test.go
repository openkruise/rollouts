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
	"reflect"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	control "github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/errors"
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
			UpdatedReplicas:    0,
			ReadyReplicas:      10,
			AvailableReplicas:  10,
			CollisionCount:     pointer.Int32Ptr(1),
			ObservedGeneration: 1,
		},
	}

	deploymentDemo2 = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apps.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "deployment",
			Namespace:  "default",
			UID:        types.UID("87076677"),
			Generation: 2,
			Labels: map[string]string{
				"app":                                "busybox",
				apps.DefaultDeploymentUniqueLabelKey: "update-pod-hash",
			},
		},
		Spec: apps.DeploymentSpec{
			Replicas: pointer.Int32Ptr(10),
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(0)},
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: containers("v2"),
				},
			},
		},
		Status: apps.DeploymentStatus{
			Replicas:          10,
			ReadyReplicas:     10,
			UpdatedReplicas:   0,
			AvailableReplicas: 10,
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
	cases := map[string]struct {
		workload func() []client.Object
		release  func() *v1beta1.BatchRelease
		result   *batchcontext.BatchContext
	}{
		"noraml case": {
			workload: func() []client.Object {
				deployment := getStableWithReady(deploymentDemo2, "v2").(*apps.Deployment)
				deployment.Status = apps.DeploymentStatus{
					Replicas:          15,
					UpdatedReplicas:   5,
					AvailableReplicas: 12,
					ReadyReplicas:     12,
				}
				// current partition, ie. maxSurge
				deployment.Spec.Strategy.RollingUpdate.MaxSurge = &intstr.IntOrString{Type: intstr.String, StrVal: "50%"}
				deployment.Spec.Replicas = pointer.Int32Ptr(10)
				newRss := makeCanaryReplicaSets(deployment).(*apps.ReplicaSet)
				newRss.Status.ReadyReplicas = 2
				return []client.Object{deployment, newRss, makeStableReplicaSets(deployment)}
			},

			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
								},
								{
									CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 1,
						},
						UpdateRevision: "version-2",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           1,
				UpdateRevision:         "version-2",
				DesiredSurge:           intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
				CurrentSurge:           intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
				Replicas:               10,
				UpdatedReplicas:        5,
				UpdatedReadyReplicas:   2,
				PlannedUpdatedReplicas: 10,
				DesiredUpdatedReplicas: 10,
			},
		},
		"maxSurge=99%, replicas=5": {
			workload: func() []client.Object {
				deployment := getStableWithReady(deploymentDemo2, "v2").(*apps.Deployment)
				deployment.Status = apps.DeploymentStatus{
					Replicas:          9,
					UpdatedReplicas:   4,
					AvailableReplicas: 9,
					ReadyReplicas:     9,
				}
				deployment.Spec.Replicas = pointer.Int32Ptr(5)
				// current partition, ie. maxSurge
				deployment.Spec.Strategy.RollingUpdate.MaxSurge = &intstr.IntOrString{Type: intstr.String, StrVal: "90%"}
				newRss := makeCanaryReplicaSets(deployment).(*apps.ReplicaSet)
				newRss.Status.ReadyReplicas = 4
				return []client.Object{deployment, newRss, makeStableReplicaSets(deployment)}
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "90%"},
								},
								{
									CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "99%"},
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 1,
						},
						UpdateRevision: "version-2",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           1,
				UpdateRevision:         "version-2",
				DesiredSurge:           intstr.FromString("99%"),
				CurrentSurge:           intstr.FromString("90%"),
				Replicas:               5,
				UpdatedReplicas:        4,
				UpdatedReadyReplicas:   4,
				PlannedUpdatedReplicas: 4,
				DesiredUpdatedReplicas: 4,
			},
		},

		// test case for continuous release
		// "maxSurge=100%, but it is initialized value": {
		// 	workload: func() []client.Object {
		// 		deployment := getStableWithReady(deploymentDemo2, "v2").(*apps.Deployment)
		// 		deployment.Status = apps.DeploymentStatus{
		// 			Replicas:          10,
		// 			UpdatedReplicas:   0,
		// 			AvailableReplicas: 10,
		// 			ReadyReplicas:     10,
		// 		}
		// 		// current partition, ie. maxSurge
		// 		deployment.Spec.Strategy.RollingUpdate.MaxSurge = &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}
		// 		newRss := makeCanaryReplicaSets(deployment).(*apps.ReplicaSet)
		// 		newRss.Status.ReadyReplicas = 0
		// 		return []client.Object{deployment, newRss, makeStableReplicaSets(deployment)}
		// 	},
		// 	release: func() *v1beta1.BatchRelease {
		// 		r := &v1beta1.BatchRelease{
		// 			Spec: v1beta1.BatchReleaseSpec{
		// 				ReleasePlan: v1beta1.ReleasePlan{
		// 					FailureThreshold: &percent,
		// 					FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
		// 					Batches: []v1beta1.ReleaseBatch{
		// 						{
		// 							CanaryReplicas: intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
		// 						},
		// 					},
		// 				},
		// 			},
		// 			Status: v1beta1.BatchReleaseStatus{
		// 				CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
		// 					CurrentBatch: 0,
		// 				},
		// 				UpdateRevision: "version-2",
		// 			},
		// 		}
		// 		return r
		// 	},
		// 	result: &batchcontext.BatchContext{
		// 		CurrentBatch:           0,
		// 		UpdateRevision:         "version-2",
		// 		DesiredPartition:       intstr.FromString("50%"),
		// 		FailureThreshold:       &percent,
		// 		CurrentPartition:       intstr.FromString("0%"), // mainly check this
		// 		Replicas:               10,
		// 		UpdatedReplicas:        0,
		// 		UpdatedReadyReplicas:   0,
		// 		PlannedUpdatedReplicas: 5,
		// 		DesiredUpdatedReplicas: 5,
		// 	},
		// },
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			cliBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cs.workload()...)
			cli := cliBuilder.Build()
			control := realController{
				client: cli,
				key:    deploymentKey,
			}
			_, err := control.BuildController()
			Expect(err).NotTo(HaveOccurred())
			got, err := control.CalculateBatchContext(cs.release())
			Expect(err).NotTo(HaveOccurred())
			fmt.Printf("expect %s, but got %s", cs.result.Log(), got.Log())
			Expect(got.Log()).Should(Equal(cs.result.Log()))
		})
	}
}

func TestRealController(t *testing.T) {
	RegisterFailHandler(Fail)

	release := releaseDemo.DeepCopy()
	clone := deploymentDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, clone).Build()
	// build new controller
	c := NewController(cli, deploymentKey, clone.GroupVersionKind()).(*realController)
	controller, err := c.BuildController()
	Expect(err).NotTo(HaveOccurred())
	// call Initialize
	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch := &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	// check strategy
	Expect(fetch.Spec.Paused).Should(BeTrue())
	Expect(fetch.Spec.Strategy.Type).Should(Equal(apps.RollingUpdateDeploymentStrategyType))
	Expect(reflect.DeepEqual(fetch.Spec.Strategy.RollingUpdate.MaxSurge, &intstr.IntOrString{Type: intstr.Int, IntVal: 1})).Should(BeTrue())
	Expect(reflect.DeepEqual(fetch.Spec.Strategy.RollingUpdate.MaxUnavailable, &intstr.IntOrString{Type: intstr.Int, IntVal: 0})).Should(BeTrue())
	Expect(fetch.Spec.MinReadySeconds).Should(Equal(int32(v1beta1.MaxReadySeconds)))
	Expect(*fetch.Spec.ProgressDeadlineSeconds).Should(Equal(int32(v1beta1.MaxProgressSeconds)))
	// check annotations
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))
	fmt.Println(fetch.Annotations[v1beta1.OriginalDeploymentStrategyAnnotation])
	Expect(fetch.Annotations[v1beta1.OriginalDeploymentStrategyAnnotation]).Should(Equal(util.DumpJSON(&control.OriginalDeploymentStrategy{
		MaxUnavailable:          &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		MaxSurge:                &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
		MinReadySeconds:         0,
		ProgressDeadlineSeconds: pointer.Int32(600),
	})))

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
	// currentBatch is 1, which means br is in the second batch, maxSurge is 50%
	Expect(reflect.DeepEqual(fetch.Spec.Strategy.RollingUpdate.MaxSurge, &intstr.IntOrString{Type: intstr.String, StrVal: "50%"})).Should(BeTrue())

	release.Spec.ReleasePlan.BatchPartition = nil
	err = controller.Finalize(release)
	Expect(errors.IsBenign(err)).Should(BeTrue())
	fetch = &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	// check workload strategy
	Expect(fetch.Spec.Paused).Should(BeFalse())
	Expect(fetch.Spec.Strategy.Type).Should(Equal(apps.RollingUpdateDeploymentStrategyType))
	Expect(reflect.DeepEqual(fetch.Spec.Strategy.RollingUpdate.MaxSurge, &intstr.IntOrString{Type: intstr.String, StrVal: "20%"})).Should(BeTrue())
	Expect(reflect.DeepEqual(fetch.Spec.Strategy.RollingUpdate.MaxUnavailable, &intstr.IntOrString{Type: intstr.Int, IntVal: 1})).Should(BeTrue())
	Expect(fetch.Spec.MinReadySeconds).Should(Equal(int32(0)))
	Expect(*fetch.Spec.ProgressDeadlineSeconds).Should(Equal(int32(600)))
}
func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}

func makeCanaryReplicaSets(d client.Object) client.Object {
	deploy := d.(*apps.Deployment)
	labels := deploy.Spec.Selector.DeepCopy().MatchLabels
	labels[apps.DefaultDeploymentUniqueLabelKey] = util.ComputeHash(&deploy.Spec.Template, nil)
	return &apps.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apps.SchemeGroupVersion.String(),
			Kind:       "ReplicaSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploy.Name + rand.String(5),
			Namespace: deploy.Namespace,
			UID:       uuid.NewUUID(),
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(deploy, deploy.GroupVersionKind()),
			},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: deploy.Spec.Replicas,
			Selector: deploy.Spec.Selector.DeepCopy(),
			Template: *deploy.Spec.Template.DeepCopy(),
		},
	}

}

func makeStableReplicaSets(d client.Object) client.Object {
	deploy := d.(*apps.Deployment)
	stableTemplate := deploy.Spec.Template.DeepCopy()
	stableTemplate.Spec.Containers = containers("v1")
	labels := deploy.Spec.Selector.DeepCopy().MatchLabels
	labels[apps.DefaultDeploymentUniqueLabelKey] = util.ComputeHash(stableTemplate, nil)
	return &apps.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apps.SchemeGroupVersion.String(),
			Kind:       "ReplicaSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploy.Name + rand.String(5),
			Namespace: deploy.Namespace,
			UID:       uuid.NewUUID(),
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(deploy, deploy.GroupVersionKind()),
			},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: deploy.Spec.Replicas,
			Selector: deploy.Spec.Selector.DeepCopy(),
			Template: *stableTemplate,
		},
	}
}

func containers(version string) []corev1.Container {
	return []corev1.Container{
		{
			Name:  "busybox",
			Image: fmt.Sprintf("busybox:%v", version),
		},
	}
}

func getStableWithReady(workload client.Object, version string) client.Object {
	switch workload.(type) {
	case *apps.Deployment:
		deploy := workload.(*apps.Deployment)
		d := deploy.DeepCopy()
		d.Spec.Paused = true
		d.ResourceVersion = strconv.Itoa(rand.Intn(100000000000))
		d.Spec.Template.Spec.Containers = containers(version)
		d.Status.ObservedGeneration = deploy.Generation
		return d

	case *kruiseappsv1alpha1.CloneSet:
		clone := workload.(*kruiseappsv1alpha1.CloneSet)
		c := clone.DeepCopy()
		c.ResourceVersion = strconv.Itoa(rand.Intn(100000000000))
		c.Spec.UpdateStrategy.Paused = true
		c.Spec.UpdateStrategy.Partition = &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}
		c.Spec.Template.Spec.Containers = containers(version)
		c.Status.ObservedGeneration = clone.Generation
		return c
	}
	return nil
}