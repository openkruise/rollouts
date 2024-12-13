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

package rollout

import (
	"fmt"

	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	utilpointer "k8s.io/utils/pointer"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

var (
	scheme *runtime.Scheme

	rolloutDemo = &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
			Annotations: map[string]string{
				util.RolloutHashAnnotation: "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd",
			},
		},
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "echoserver",
			},
			Strategy: v1beta1.RolloutStrategy{
				Canary: &v1beta1.CanaryStrategy{
					EnableExtraWorkloadForCanary: true,
					Steps: []v1beta1.CanaryStep{
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("5%"),
							},
							Replicas: &intstr.IntOrString{IntVal: 1},
						},
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("20%"),
							},
							Replicas: &intstr.IntOrString{IntVal: 2},
						},
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("60%"),
							},
							Replicas: &intstr.IntOrString{IntVal: 6},
						},
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("100%"),
							},
							Replicas: &intstr.IntOrString{IntVal: 10},
						},
					},
					TrafficRoutings: []v1beta1.TrafficRoutingRef{
						{
							Service: "echoserver",
							Ingress: &v1beta1.IngressTrafficRouting{
								Name: "echoserver",
							},
							GracePeriodSeconds: 0, // To facilitate testing, don't wait after traffic routing operation
						},
					},
				},
			},
		},
		Status: v1beta1.RolloutStatus{
			Phase:        v1beta1.RolloutPhaseProgressing,
			CanaryStatus: &v1beta1.CanaryStatus{},
			Conditions: []v1beta1.RolloutCondition{
				{
					Type:   v1beta1.RolloutConditionProgressing,
					Reason: v1alpha1.ProgressingReasonInitializing,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	rolloutDemoBlueGreen = &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
			Annotations: map[string]string{
				util.RolloutHashAnnotation: "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd",
			},
		},
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "echoserver",
			},
			Strategy: v1beta1.RolloutStrategy{
				BlueGreen: &v1beta1.BlueGreenStrategy{
					Steps: []v1beta1.CanaryStep{
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("0%"),
							},
							Replicas: &intstr.IntOrString{StrVal: "50%", Type: intstr.String},
						},
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("0%"),
							},
							Replicas: &intstr.IntOrString{StrVal: "100%", Type: intstr.String},
						},
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("50%"),
							},
							Replicas: &intstr.IntOrString{StrVal: "100%", Type: intstr.String},
						},
						{
							TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
								Traffic: utilpointer.String("100%"),
							},
							Replicas: &intstr.IntOrString{StrVal: "100%", Type: intstr.String},
						},
					},
					TrafficRoutings: []v1beta1.TrafficRoutingRef{
						{
							Service: "echoserver",
							Ingress: &v1beta1.IngressTrafficRouting{
								Name: "echoserver",
							},
							GracePeriodSeconds: 0, // To facilitate testing, don't wait after traffic routing operation
						},
					},
				},
			},
		},
		Status: v1beta1.RolloutStatus{
			Phase:           v1beta1.RolloutPhaseProgressing,
			BlueGreenStatus: &v1beta1.BlueGreenStatus{},
			Conditions: []v1beta1.RolloutCondition{
				{
					Type:   v1beta1.RolloutConditionProgressing,
					Reason: v1alpha1.ProgressingReasonInitializing,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	maxUnavailable = intstr.FromString("20%")
	deploymentDemo = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "echoserver",
			Labels: map[string]string{},
			Annotations: map[string]string{
				util.InRolloutProgressingAnnotation: "rollout-demo",
			},
			Generation: 2,
			UID:        types.UID("606132e0-85ef-460a-8cf5-cd8f915a8cc3"),
		},
		Spec: apps.DeploymentSpec{
			Replicas: utilpointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "echoserver",
				},
			},
			Strategy: apps.DeploymentStrategy{
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "echoserver",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "echoserver:v2",
						},
					},
				},
			},
		},
		Status: apps.DeploymentStatus{
			ObservedGeneration: 2,
		},
	}

	rsDemo = &apps.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver-1",
			Labels: map[string]string{
				"app":               "echoserver",
				"pod-template-hash": "pod-template-hash-v1",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "echoserver",
					UID:        types.UID("606132e0-85ef-460a-8cf5-cd8f915a8cc3"),
					Controller: utilpointer.Bool(true),
				},
			},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: utilpointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "echoserver",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "echoserver",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "echoserver:v1",
						},
					},
				},
			},
		},
	}

	batchDemo = &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "rollout-demo",
			Labels:     map[string]string{},
			Generation: 1,
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "echoserver",
			},
		},
		Status: v1beta1.BatchReleaseStatus{},
	}

	demoService = corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       80,
					TargetPort: intstr.FromInt(8080),
				},
			},
			Selector: map[string]string{
				"app": "echoserver",
			},
		},
	}

	demoIngress = netv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver",
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "nginx",
			},
		},
		Spec: netv1.IngressSpec{
			Rules: []netv1.IngressRule{
				{
					Host: "echoserver.example.com",
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path: "/apis/echo",
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: "echoserver",
											Port: netv1.ServiceBackendPort{
												Name: "http",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	demoConf = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configuration.RolloutConfigurationName,
			Namespace: util.GetRolloutNamespace(),
		},
		Data: map[string]string{
			fmt.Sprintf("%s.nginx", configuration.LuaTrafficRoutingIngressTypePrefix): `
				annotations = obj.annotations
				annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-by-header"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-weight"] = nil
				if ( obj.weight ~= "-1" )
				then
					annotations["nginx.ingress.kubernetes.io/canary-weight"] = obj.weight
				end
				if ( not obj.matches )
				then
					return annotations
				end
				for _,match in ipairs(obj.matches) do
					header = match.headers[1]
					if ( header.name == "canary-by-cookie" )
					then
						annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = header.value
					else
						annotations["nginx.ingress.kubernetes.io/canary-by-header"] = header.name
						if ( header.type == "RegularExpression" )
						then
							annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = header.value
						else
							annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = header.value
						end
					end
				end
				return annotations
 			`,
		},
	}

	demoTR = &v1alpha1.TrafficRouting{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "tr-demo",
			Labels: map[string]string{},
		},
		Spec: v1alpha1.TrafficRoutingSpec{
			ObjectRef: []v1alpha1.TrafficRoutingRef{
				{
					Service: "echoserver",
					Ingress: &v1alpha1.IngressTrafficRouting{
						Name: "echoserver",
					},
				},
			},
			Strategy: v1alpha1.TrafficRoutingStrategy{
				Matches: []v1alpha1.HttpRouteMatch{
					// header
					{
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{
								Name:  "user_id",
								Value: "123456",
							},
						},
					},
				},
			},
		},
	}
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rolloutapi.AddToScheme(scheme)
}
