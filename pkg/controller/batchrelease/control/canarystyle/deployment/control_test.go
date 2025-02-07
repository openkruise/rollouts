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
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	expectations "github.com/openkruise/rollouts/pkg/util/expectation"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
}

func TestCalculateBatchContext(t *testing.T) {
	RegisterFailHandler(Fail)

	percent := intstr.FromString("20%")
	cases := map[string]struct {
		workload func() (*apps.Deployment, *apps.Deployment)
		release  func() *v1beta1.BatchRelease
		result   *batchcontext.BatchContext
	}{
		"normal case": {
			workload: func() (*apps.Deployment, *apps.Deployment) {
				stable := &apps.Deployment{
					Spec: apps.DeploymentSpec{
						Replicas: pointer.Int32(10),
					},
					Status: apps.DeploymentStatus{
						Replicas:          10,
						UpdatedReplicas:   0,
						AvailableReplicas: 10,
					},
				}
				canary := &apps.Deployment{
					Spec: apps.DeploymentSpec{
						Replicas: pointer.Int32(5),
					},
					Status: apps.DeploymentStatus{
						Replicas:          5,
						UpdatedReplicas:   5,
						AvailableReplicas: 5,
					},
				}
				return stable, canary
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
				DesiredUpdatedReplicas: 2,
			},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			stable, canary := cs.workload()
			control := realController{
				realStableController: realStableController{
					stableObject: stable,
				},
				realCanaryController: realCanaryController{
					canaryObject: canary,
				},
			}
			got, err := control.CalculateBatchContext(cs.release())
			got.FilterFunc = nil
			Expect(err).NotTo(HaveOccurred())
			Expect(reflect.DeepEqual(got, cs.result)).Should(BeTrue())
		})
	}
}

func TestRealStableController(t *testing.T) {
	RegisterFailHandler(Fail)

	release := releaseDemo.DeepCopy()
	deployment := deploymentDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, deployment).Build()
	c := NewController(cli, deploymentKey).(*realController)
	controller, err := c.BuildStableController()
	Expect(err).NotTo(HaveOccurred())

	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch := &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))
	c.stableObject = fetch // mock

	err = controller.Finalize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch = &apps.Deployment{}
	Expect(cli.Get(context.TODO(), deploymentKey, fetch)).NotTo(HaveOccurred())
	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(""))

	stableInfo := controller.GetStableInfo()
	Expect(stableInfo).ShouldNot(BeNil())
	checkWorkloadInfo(stableInfo, deployment)
}

func TestRealCanaryController(t *testing.T) {
	RegisterFailHandler(Fail)
	release := releaseDemo.DeepCopy()
	deployment := deploymentDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, deployment).Build()
	c := NewController(cli, deploymentKey).(*realController)
	controller, err := c.BuildCanaryController(release)
	Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

	// check creation
	for {
		err = controller.Create(release)
		if err == nil {
			break
		}
		// mock create event handler
		controller, err = c.BuildCanaryController(release)
		Expect(err).NotTo(HaveOccurred())
		if c.canaryObject != nil {
			controllerKey := client.ObjectKeyFromObject(release).String()
			resourceKey := client.ObjectKeyFromObject(c.canaryObject).String()
			expectations.ResourceExpectations.Observe(controllerKey, expectations.Create, resourceKey)
		}
	}
	Expect(metav1.IsControlledBy(c.canaryObject, release)).Should(BeTrue())
	Expect(controllerutil.ContainsFinalizer(c.canaryObject, util.CanaryDeploymentFinalizer)).Should(BeTrue())
	Expect(*c.canaryObject.Spec.Replicas).Should(BeNumerically("==", 0))
	Expect(util.EqualIgnoreHash(&c.canaryObject.Spec.Template, &deployment.Spec.Template)).Should(BeTrue())

	// check rolling
	batchContext, err := c.CalculateBatchContext(release)
	Expect(err).NotTo(HaveOccurred())
	err = controller.UpgradeBatch(batchContext)
	Expect(err).NotTo(HaveOccurred())
	canary := getCanaryDeployment(release, deployment, c)
	Expect(canary).ShouldNot(BeNil())
	Expect(*canary.Spec.Replicas).Should(BeNumerically("==", batchContext.DesiredUpdatedReplicas))

	// check deletion
	for {
		err = controller.Delete(release)
		if err == nil {
			break
		}
	}
	d := getCanaryDeployment(release, deployment, c)
	Expect(d).NotTo(BeNil())
	Expect(len(d.Finalizers)).Should(Equal(0))
}

func getCanaryDeployment(release *v1beta1.BatchRelease, stable *apps.Deployment, c *realController) *apps.Deployment {
	ds, err := c.listDeployment(release)
	Expect(err).NotTo(HaveOccurred())
	if len(ds) == 0 {
		return nil
	}
	return filterCanaryDeployment(release, ds, &stable.Spec.Template)
}

func checkWorkloadInfo(stableInfo *util.WorkloadInfo, deployment *apps.Deployment) {
	Expect(stableInfo.Replicas).Should(Equal(*deployment.Spec.Replicas))
	Expect(stableInfo.Status.Replicas).Should(Equal(deployment.Status.Replicas))
	Expect(stableInfo.Status.ReadyReplicas).Should(Equal(deployment.Status.ReadyReplicas))
	Expect(stableInfo.Status.UpdatedReplicas).Should(Equal(deployment.Status.UpdatedReplicas))
	Expect(stableInfo.Status.AvailableReplicas).Should(Equal(deployment.Status.AvailableReplicas))
	Expect(stableInfo.Status.ObservedGeneration).Should(Equal(deployment.Status.ObservedGeneration))
}

func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}
