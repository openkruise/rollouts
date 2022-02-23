package validating

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

var (
	scheme = runtime.NewScheme()

	rollout = appsv1alpha1.Rollout{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1alpha1.SchemeGroupVersion.String(),
			Kind:       "Rollout",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout-demo",
			Namespace: "namespace-unit-test",
		},
		Spec: appsv1alpha1.RolloutSpec{
			ObjectRef: appsv1alpha1.ObjectRef{
				Type: appsv1alpha1.WorkloadRefType,
				WorkloadRef: &appsv1alpha1.WorkloadRef{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       "deployment-demo",
				},
			},
			Strategy: appsv1alpha1.RolloutStrategy{
				Type: appsv1alpha1.RolloutStrategyCanary,
				Canary: &appsv1alpha1.CanaryStrategy{
					Steps: []appsv1alpha1.CanaryStep{
						{
							Weight:   10,
							Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
							Pause:    appsv1alpha1.RolloutPause{},
						},
						{
							Weight:   10,
							Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(3)},
							Pause:    appsv1alpha1.RolloutPause{Duration: pointer.Int32(1 * 24 * 60 * 60)},
						},
						{
							Weight: 30,
							Pause:  appsv1alpha1.RolloutPause{Duration: pointer.Int32(7 * 24 * 60 * 60)},
						},
						{
							Weight: 100,
						},
					},
					TrafficRouting: &appsv1alpha1.TrafficRouting{
						Type:    appsv1alpha1.TrafficRoutingNginx,
						Service: "service-demo",
						Nginx: &appsv1alpha1.NginxTrafficRouting{
							Ingress: "ingress-nginx-demo",
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
	_ = appsv1alpha1.AddToScheme(scheme)
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
				return []client.Object{rollout.DeepCopy()}
			},
		},
		/***********************************************************
			The following cases may lead to controller panic
		 **********************************************************/
		{
			Name:    "WorkloadRef is nil",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.ObjectRef.WorkloadRef = nil
				return []client.Object{object}
			},
		},
		{
			Name:    "Canary is nil",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary = nil
				return []client.Object{object}
			},
		},
		{
			Name:    "Traffic is nil",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.TrafficRouting = nil
				return []client.Object{object}
			},
		},
		{
			Name:    "Nginx is nil",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.TrafficRouting.Nginx = nil
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
				object.Spec.Strategy.Canary.TrafficRouting.Service = ""
				return []client.Object{object}
			},
		},
		{
			Name:    "Nginx ingress name is empty",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.TrafficRouting.Nginx.Ingress = ""
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps is empty",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps = []appsv1alpha1.CanaryStep{}
				return []client.Object{object}
			},
		},
		{
			Name:    "WorkloadRef is not Deployment kind",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.ObjectRef.WorkloadRef = &appsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       "whatever",
				}
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Weight is a decreasing sequence",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[2].Weight = 5
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Replicas is a decreasing sequence",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Replicas = &intstr.IntOrString{Type: intstr.String, StrVal: "50%"}
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
			Name:    "Steps.Weight is illegal value, 0",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Weight = 0
				return []client.Object{object}
			},
		},
		{
			Name:    "Steps.Weight is illegal value, 101",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.Steps[1].Weight = 101
				return []client.Object{object}
			},
		},
		{
			Name:    "The last Steps.Weight is not 100",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				n := len(object.Spec.Strategy.Canary.Steps)
				object.Spec.Strategy.Canary.Steps[n-1].Weight = 80
				return []client.Object{object}
			},
		},
		{
			Name:    "Wrong objectRef type",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.ObjectRef.Type = "Whatever"
				return []client.Object{object}
			},
		},
		{
			Name:    "Wrong strategy type",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Type = "Whatever"
				return []client.Object{object}
			},
		},
		{
			Name:    "Wrong Traffic type",
			Succeed: false,
			GetObject: func() []client.Object {
				object := rollout.DeepCopy()
				object.Spec.Strategy.Canary.TrafficRouting.Type = "Whatever"
				return []client.Object{object}
			},
		},
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
				object1.Spec.ObjectRef.WorkloadRef.Name = "another"

				object2 := rollout.DeepCopy()
				object2.Name = "object-2"
				object2.Spec.ObjectRef.WorkloadRef.APIVersion = "another"

				object3 := rollout.DeepCopy()
				object3.Name = "object-3"
				object3.Spec.ObjectRef.WorkloadRef.Kind = "another"
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
				object1.Spec.ObjectRef.WorkloadRef.Name = "another"

				object2 := rollout.DeepCopy()
				object2.Name = "object-2"
				object2.Spec.ObjectRef.WorkloadRef.APIVersion = "another"

				object3 := rollout.DeepCopy()
				object3.Name = "object-3"
				object3.Spec.ObjectRef.WorkloadRef.Kind = "another"

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
			errList := handler.validateRollout(objects[0].(*appsv1alpha1.Rollout))
			t.Log(errList)
			Expect(len(errList) == 0).Should(Equal(cs.Succeed))
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
				object.Spec.Strategy.Canary.Steps[0].Weight = 5
				return object
			},
		},
		{
			Name:    "Rollout is progressing, but spec not changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				object.Status.CanaryStatus.CurrentStepIndex = 1
				return object
			},
		},
		{
			Name:    "Rollout is progressing, and spec changed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				object.Spec.Strategy.Canary.Steps[0].Weight = 5
				return object
			},
		},
		{
			Name:    "Rollout is terminating, and spec changed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseTerminating
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseTerminating
				object.Spec.Strategy.Canary.Steps[0].Weight = 5
				return object
			},
		},
		{
			Name:    "Rollout is initial, and spec changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseInitial
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseInitial
				object.Spec.Strategy.Canary.Steps[0].Weight = 5
				return object
			},
		},
		{
			Name:    "Rollout is healthy, and spec changed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseHealthy
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.Phase = appsv1alpha1.RolloutPhaseHealthy
				object.Spec.Strategy.Canary.Steps[0].Weight = 5
				return object
			},
		},
		{
			Name:    "Rollout canary state: paused -> completed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStatePaused
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: completed -> completed",
			Succeed: true,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: upgrade -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateUpgrade
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: routing -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateTrafficRouting
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
				return object
			},
		},
		{
			Name:    "Rollout canary state: analysis -> completed",
			Succeed: false,
			GetOldObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateMetricsAnalysis
				return object
			},
			GetNewObject: func() client.Object {
				object := rollout.DeepCopy()
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
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
				object.Status.CanaryStatus.CurrentStepState = appsv1alpha1.CanaryStepStateCompleted
				return object
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			oldObject := cs.GetOldObject()
			newObject := cs.GetNewObject()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oldObject).Build()
			handler := RolloutCreateUpdateHandler{
				Client: cli,
			}
			errList := handler.validateRolloutUpdate(oldObject.(*appsv1alpha1.Rollout), newObject.(*appsv1alpha1.Rollout))
			t.Log(errList)
			Expect(len(errList) == 0).Should(Equal(cs.Succeed))
		})
	}
}
