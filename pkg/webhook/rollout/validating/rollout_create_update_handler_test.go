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

package validating

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	rolloutapi "github.com/openkruise/rollouts/api"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()

	rollout = appsv1beta1.Rollout{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1beta1.SchemeGroupVersion.String(),
			Kind:       "Rollout",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rollout-demo",
			Namespace:   "namespace-unit-test",
			Annotations: map[string]string{},
		},
		Spec: appsv1beta1.RolloutSpec{
			WorkloadRef: appsv1beta1.ObjectRef{
				APIVersion: apps.SchemeGroupVersion.String(),
				Kind:       "Deployment",
				Name:       "deployment-demo",
			},
			Strategy: appsv1beta1.RolloutStrategy{
				Canary: &appsv1beta1.CanaryStrategy{
					Steps: []appsv1beta1.CanaryStep{
						{
							TrafficRoutingStrategy: appsv1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("10%"),
							},
							Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
							Pause:    appsv1beta1.RolloutPause{},
						},
						{
							TrafficRoutingStrategy: appsv1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("10%"),
							},
							Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(3)},
							Pause:    appsv1beta1.RolloutPause{Duration: utilpointer.Int32(1 * 24 * 60 * 60)},
						},
						{
							TrafficRoutingStrategy: appsv1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("30%"),
							},
							Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(10)},
							Pause:    appsv1beta1.RolloutPause{Duration: utilpointer.Int32(7 * 24 * 60 * 60)},
						},
						{
							TrafficRoutingStrategy: appsv1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("100%"),
							},
							Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(20)},
						},
					},
					TrafficRoutings: []appsv1beta1.TrafficRoutingRef{
						{
							Service: "service-demo",
							Ingress: &appsv1beta1.IngressTrafficRouting{
								ClassType: "nginx",
								Name:      "ingress-nginx-demo",
							},
						},
					},
				},
			},
		},
		Status: appsv1beta1.RolloutStatus{
			CanaryStatus: &appsv1beta1.CanaryStatus{
				CommonStatus: appsv1beta1.CommonStatus{
					CurrentStepState: appsv1beta1.CanaryStepStateCompleted,
				},
			},
		},
	}
	rolloutV1alpha1 = appsv1alpha1.Rollout{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1alpha1.SchemeGroupVersion.String(),
			Kind:       "Rollout",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rollout-demo",
			Namespace:   "namespace-unit-test",
			Annotations: map[string]string{},
		},
		Spec: appsv1alpha1.RolloutSpec{
			ObjectRef: appsv1alpha1.ObjectRef{
				WorkloadRef: &appsv1alpha1.WorkloadRef{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       "deployment-demo",
				},
			},
			Strategy: appsv1alpha1.RolloutStrategy{
				Canary: &appsv1alpha1.CanaryStrategy{
					Steps: []appsv1alpha1.CanaryStep{
						{
							TrafficRoutingStrategy: appsv1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(10),
							},
							Pause: appsv1alpha1.RolloutPause{},
						},
						{
							TrafficRoutingStrategy: appsv1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(30),
							},
							Pause: appsv1alpha1.RolloutPause{Duration: utilpointer.Int32(1 * 24 * 60 * 60)},
						},
						{
							TrafficRoutingStrategy: appsv1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(100),
							},
						},
					},
					TrafficRoutings: []appsv1alpha1.TrafficRoutingRef{
						{
							Service: "service-demo",
							Ingress: &appsv1alpha1.IngressTrafficRouting{
								ClassType: "nginx",
								Name:      "ingress-nginx-demo",
							},
						},
					},
				},
			},
		},
		Status: appsv1alpha1.RolloutStatus{
			CanaryStatus: &appsv1alpha1.CanaryStatus{
				CurrentStepState: appsv1alpha1.CanaryStepStateCompleted,
			},
		},
	}
)

func init() {
	_ = rolloutapi.AddToScheme(scheme)
}

func TestRolloutValidateCreate(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name      string
		Succeed   bool
		GetObject func() []client.Object
	}{
		{
			Name:    "Normal case",
			Succeed: true,
			GetObject: func() []client.Object {
				obj := rollout.DeepCopy()
				return []client.Object{obj}
			},
		},
		/***********************************************************
			The following cases may lead to controller panic
		 **********************************************************/
		{
			Name:    "Canary is nil",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary = nil
				return []client.Object{object}
			},
		},
		/****************************************************************
			The following cases may lead to that controller cannot work
		 ***************************************************************/
		{
			Name:    "Service name is empty",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.TrafficRoutings[0].Service = ""
				return []client.Object{object}
			},
		},
		{
			Name:    "Nginx ingress name is empty",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.Name = ""
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps is empty",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps = []appsv1beta1.CanaryStep{}
				return []client.Object{object}
			},
		},
		{
			Name:    "WorkloadRef is not Deployment kind",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.WorkloadRef = appsv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       "whatever",
				}

				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Traffic is a decreasing sequence",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[2].Traffic = utilpointer.String("%5")
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Replicas is illegal value, '50'",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "50"}
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Replicas is illegal value, '101%'",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "101%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Replicas is illegal value, '0%'",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "0%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Traffic is illegal value, 0",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Traffic = utilpointer.String("0%")
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Traffic is illegal value, 101",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Traffic = utilpointer.String("101%")
				return []client.Object{object}
			},
		},
		{
			Name:    "Canary rolling style",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
				return []client.Object{object}
			},
		},
		{
			Name:    "Partition rolling style",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				return []client.Object{object}
			},
		},
		{
			Name:    "matched rolling style",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
				object.Spec.WorkloadRef.APIVersion = "apps.kruise.io/v1alpha1"
				object.Spec.WorkloadRef.Kind = "CloneSet"
				return []client.Object{object}
			},
		},
		{
			Name:    "test with replicasLimitWithTraffic - 1",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "test with replicasLimitWithTraffic - 2",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "31%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "test with replicasLimitWithTraffic - 2 - canary style",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "31%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "test with replicasLimitWithTraffic - 2 - cloneset",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.WorkloadRef = appsv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "whatever",
				}
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "31%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "test with replicasLimitWithTraffic - 3",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				PartitionReplicasLimitWithTraffic = 100
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}
				return []client.Object{object}
			},
		},
		{
			Name:    "test with replicasLimitWithTraffic - 4",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				PartitionReplicasLimitWithTraffic = 50
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "51%"}
				return []client.Object{object}
			},
		},
		//{
		//	Name:    "The last Steps.Traffic is not 100",
		//	Succeed: false,
		//	GetObject: func() []client.Object {
		//		object := rollout.DeepCopy()
		//		n := len(object.Spec.Strategy.Canary.Steps)
		//		object.Spec.Strategy.Canary.Steps[n-1].Traffic = 80
		//		return []client.Object{object}
		//	},
		//},
		/****************************************************************
			The following cases are conflict cases
		 ***************************************************************/
		{
			Name:    "Without conflict",
			Succeed: true,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()

				object1 := rollout.DeepCopy()
				object1.Name = "object-1"
				object1.Spec.WorkloadRef.Name = "another"

				object2 := rollout.DeepCopy()
				object2.Name = "object-2"
				object2.Spec.WorkloadRef.APIVersion = "another"

				object3 := rollout.DeepCopy()
				object3.Name = "object-3"
				object3.Spec.WorkloadRef.Kind = "another"

				return []client.Object{
					object, object1, object2, object3,
				}
			},
		},
		{
			Name:    "With conflict",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()

				object1 := rollout.DeepCopy()
				object1.Name = "object-1"
				object1.Spec.WorkloadRef.Name = "another"

				object2 := rollout.DeepCopy()
				object2.Name = "object-2"
				object2.Spec.WorkloadRef.APIVersion = "another"

				object3 := rollout.DeepCopy()
				object3.Name = "object-3"
				object3.Spec.WorkloadRef.Kind = "another"

				object4 := rollout.DeepCopy()
				object4.Name = "object-4"
				return []client.Object{
					object, object1, object2, object3, object4,
				}
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			objects := cs.GetObject()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			handler := RolloutCreateUpdateHandler{
				Client: cli,
			}
			errList := handler.validateRollout(objects[0].(*appsv1beta1.Rollout))
			t.Log(errList)
			Expect(len(errList) == 0).Should(Equal(cs.Succeed))
			// restore PartitionReplicasLimitWithTraffic after each case
			PartitionReplicasLimitWithTraffic = 30
		})
	}
}

func TestRolloutV1alpha1ValidateCreate(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name      string
		Succeed   bool
		GetObject func() []client.Object
	}{
		{
			Name:    "Canary style",
			Succeed: true,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style without replicas - 1",
			Succeed: false,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style without replicas - 2",
			Succeed: true,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				PartitionReplicasLimitWithTraffic = 100
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style without replicas - 3",
			Succeed: false,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Weight = utilpointer.Int32(32)
				PartitionReplicasLimitWithTraffic = 31
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style without replicas- 3 - canary style",
			Succeed: true,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Weight = utilpointer.Int32(32)
				PartitionReplicasLimitWithTraffic = 31
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style without replicas - 3 - cloneset",
			Succeed: false,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Spec.ObjectRef.WorkloadRef = &appsv1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "whatever",
				}
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Weight = utilpointer.Int32(32)
				PartitionReplicasLimitWithTraffic = 31
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style with replicas - 1",
			Succeed: false,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Weight = utilpointer.Int32(50)
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "32%"}
				PartitionReplicasLimitWithTraffic = 31
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style with replicas - 2",
			Succeed: true,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Weight = utilpointer.Int32(50)
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "31%"}
				PartitionReplicasLimitWithTraffic = 31
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
		{
			Name:    "Partition style with replicas - 3",
			Succeed: true,
			GetObject: func() []client.Object {
				obj := rolloutV1alpha1.DeepCopy()
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Weight = nil
				obj.Spec.Strategy.Canary.Steps[len(obj.Spec.Strategy.Canary.Steps)-1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "50%"}
				obj.Annotations[appsv1alpha1.RolloutStyleAnnotation] = string(appsv1alpha1.PartitionRollingStyle)
				return []client.Object{obj}
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			objects := cs.GetObject()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			handler := RolloutCreateUpdateHandler{
				Client: cli,
			}
			errList := handler.validateV1alpha1Rollout(objects[0].(*appsv1alpha1.Rollout))
			t.Log(errList)
			Expect(len(errList) == 0).Should(Equal(cs.Succeed))
			// restore PartitionReplicasLimitWithTraffic after each case
			PartitionReplicasLimitWithTraffic = 30
		})
	}
}

func TestRolloutValidateUpdate(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name         string
		Succeed      bool
		GetOldObject func() client.Object
		GetNewObject func() client.Object
	}{
		{
			Name:    "Normal case",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[0].Traffic = utilpointer.String("5%")
				return object
			},
		},
		{
			Name:    "Rollout is progressing, but spec not changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseProgressing
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseProgressing
				object.Status.CanaryStatus.CurrentStepIndex = 1
				return object
			},
		},
		{
			Name:    "Rollout is progressing, and spec changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseProgressing
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseProgressing
				object.Spec.Strategy.Canary.Steps[0].Traffic = utilpointer.String("5%")
				return object
			},
		},
		{
			Name:    "Rollout is progressing, and rolling style changed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseProgressing
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseProgressing
				object.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
				return object
			},
		},
		{
			Name:    "Rollout is terminating, and spec changed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseTerminating
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseTerminating
				object.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.ClassType = "alb"
				return object
			},
		},
		{
			Name:    "Rollout is initial, and spec changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseInitial
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseInitial
				object.Spec.Strategy.Canary.Steps[0].Traffic = utilpointer.String("5%")
				return object
			},
		},
		{
			Name:    "Rollout is healthy, and spec changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseHealthy
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1beta1.RolloutPhaseHealthy
				object.Spec.Strategy.Canary.Steps[0].Traffic = utilpointer.String("5%")
				return object
			},
		},
		{
			Name:    "Rollout canary state: paused -> completed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStatePaused
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: completed -> completed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
		},
		/*{
			Name:    "Rollout canary state: upgrade -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateUpgrade
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: routing -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateTrafficRouting
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: analysis -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateMetricsAnalysis
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: others -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = "Whatever"
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1beta1.CanaryStepStateCompleted
				return object
			},
		},*/
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			oldObject := cs.GetOldObject()
			newObject := cs.GetNewObject()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oldObject).Build()
			handler := RolloutCreateUpdateHandler{
				Client: cli,
			}
			errList := handler.validateRolloutUpdate(oldObject.(*appsv1beta1.Rollout), newObject.(*appsv1beta1.Rollout))
			t.Log(errList)
			Expect(len(errList) == 0).Should(Equal(cs.Succeed))
		})
	}
}
