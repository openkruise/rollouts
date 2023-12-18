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

package rollouthistory

import (
	"context"
	"reflect"
	"testing"

	"github.com/openkruise/kruise-api/apps/pub"
	kruisev1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutapi "github.com/openkruise/rollouts/api"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	utilpointer "k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = kruisev1alpha1.AddToScheme(scheme)
	_ = rolloutapi.AddToScheme(scheme)
	_ = v1alpha2.AddToScheme(scheme)
}

var (
	scheme *runtime.Scheme

	rollouthistoryDemo = rolloutv1alpha1.RolloutHistory{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RolloutHistory",
			APIVersion: "rollouts.kruise.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollouthistory-demo",
			Namespace: "default",
			Labels: map[string]string{
				rolloutIDLabel:   "1",
				rolloutNameLabel: "rollout-demo",
			},
		},
		Spec: rolloutv1alpha1.RolloutHistorySpec{
			Rollout: rolloutv1alpha1.RolloutInfo{
				RolloutID: "1",
				NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
					Name: "rollout-demo",
				},
			},
			Service: rolloutv1alpha1.ServiceInfo{
				NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
					Name: "service-demo",
				},
			},
			TrafficRouting: rolloutv1alpha1.TrafficRoutingInfo{
				Ingress: &rolloutv1alpha1.IngressInfo{
					NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
						Name: "ingress-demo",
					},
				},
				HTTPRoute: &rolloutv1alpha1.HTTPRouteInfo{
					NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
						Name: "HTTPRoute-demo",
					},
				},
			},
			Workload: rolloutv1alpha1.WorkloadInfo{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
				},
				NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
					Name: "workload-demo",
				},
			},
		},
		Status: rolloutv1alpha1.RolloutHistoryStatus{
			Phase: "",
			CanarySteps: []rolloutv1alpha1.CanaryStepInfo{
				{
					CanaryStepIndex: 1,
					Pods: []rolloutv1alpha1.Pod{
						{
							Name:     "pod-1",
							IP:       "1.2.3.4",
							NodeName: "local",
						},
					},
				},
			},
		},
	}

	rolloutDemo1 = rolloutv1alpha1.Rollout{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Rollout",
			APIVersion: "rollouts.kruise.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout-demo",
			Namespace: "default",
			Labels:    map[string]string{},
		},
		Spec: rolloutv1alpha1.RolloutSpec{
			ObjectRef: rolloutv1alpha1.ObjectRef{
				WorkloadRef: &rolloutv1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "workload-demo",
				},
			},
			DeprecatedRolloutID: "1",
			Strategy: rolloutv1alpha1.RolloutStrategy{
				Canary: &rolloutv1alpha1.CanaryStrategy{
					Steps: []rolloutv1alpha1.CanaryStep{
						{
							TrafficRoutingStrategy: rolloutv1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(5),
							},
							Pause: rolloutv1alpha1.RolloutPause{
								Duration: utilpointer.Int32(0),
							},
						},
						{
							TrafficRoutingStrategy: rolloutv1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(40),
							},
							Pause: rolloutv1alpha1.RolloutPause{},
						},
						{
							TrafficRoutingStrategy: rolloutv1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(100),
							},
							Pause: rolloutv1alpha1.RolloutPause{
								Duration: utilpointer.Int32(0),
							},
						},
					},
					TrafficRoutings: []rolloutv1alpha1.TrafficRoutingRef{
						{
							Service: "service-demo",
							Ingress: &rolloutv1alpha1.IngressTrafficRouting{
								ClassType: "nginx",
								Name:      "ingress-demo",
							},
							Gateway: &rolloutv1alpha1.GatewayTrafficRouting{
								HTTPRouteName: utilpointer.String("HTTPRoute-demo"),
							},
						},
					},
				},
			},
		},
	}

	cloneSetDemo = kruisev1alpha1.CloneSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps.kruise.io/v1alpha1",
			Kind:       "CloneSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workload-demo",
			Namespace: "default",
			Labels: map[string]string{
				"app": "echoserver",
			},
		},
		Spec: kruisev1alpha1.CloneSetSpec{
			UpdateStrategy: kruisev1alpha1.CloneSetUpdateStrategy{
				Type: "InPlaceIfPossible",
				InPlaceUpdateStrategy: &pub.InPlaceUpdateStrategy{
					GracePeriodSeconds: 1,
				},
			},
			Replicas: utilpointer.Int32(5),
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
							Name:            "echoserver",
							Image:           "cilium/echoserver:1.10.1",
							ImagePullPolicy: "IfNotPresent",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "PORT",
									Value: "8080",
								},
							},
						},
					},
				},
			},
		},
	}

	serviceDemo = corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-demo",
			Namespace: "default",
			Labels: map[string]string{
				"app": "echoserver",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       int32(80),
					TargetPort: intstr.IntOrString{IntVal: int32(8080)},
					Name:       "http",
					Protocol:   "TCP",
				},
			},
			Selector: map[string]string{
				"app": "echoserver",
			},
		},
	}

	ingressDemo = networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-demo",
			Namespace: "default",
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "nginx",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "echoserver.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/apis/echo",
									PathType: (*networkingv1.PathType)(utilpointer.String("Exact")),
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "service-demo",
											Port: networkingv1.ServiceBackendPort{
												Number: int32(80),
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

	httpRouteDemo = v1alpha2.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1alpha2",
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "HTTPRoute-demo",
			Namespace: "default",
		},
		Spec: v1alpha2.HTTPRouteSpec{
			CommonRouteSpec: v1alpha2.CommonRouteSpec{
				ParentRefs: []v1alpha2.ParentReference{
					{
						Name: "demo-lb",
					},
				},
			},
		},
	}

	podDemo = corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-demo",
			Namespace: "default",
			Labels: map[string]string{
				rolloutv1alpha1.RolloutBatchIDLabel: "1",
				rolloutv1alpha1.RolloutIDLabel:      "1",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "local",
		},
	}
)

func TestReconcile(t *testing.T) {
	cases := []struct {
		name                 string
		req                  ctrl.Request
		getPods              func() []*corev1.Pod
		getService           func() []*corev1.Service
		getWorkload          func() []*kruisev1alpha1.CloneSet
		getIngress           func() []*networkingv1.Ingress
		getHTTPRoute         func() []*v1alpha2.HTTPRoute
		getRollout           func() []*rolloutv1alpha1.Rollout
		getRolloutHistory    func() []*rolloutv1alpha1.RolloutHistory
		expectRolloutHistory func() []*rolloutv1alpha1.RolloutHistory
	}{
		{
			name: "test1, create a new rolloutHistory for rollout",
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "rollout-demo",
				},
			},
			getPods: func() []*corev1.Pod {
				pods := []*corev1.Pod{}
				return pods
			},
			getService: func() []*corev1.Service {
				services := []*corev1.Service{}
				return services
			},
			getWorkload: func() []*kruisev1alpha1.CloneSet {
				workloads := []*kruisev1alpha1.CloneSet{}
				return workloads
			},
			getIngress: func() []*networkingv1.Ingress {
				ingresses := []*networkingv1.Ingress{}
				return ingresses
			},
			getHTTPRoute: func() []*v1alpha2.HTTPRoute {
				httpRoutes := []*v1alpha2.HTTPRoute{}
				return httpRoutes
			},
			getRollout: func() []*rolloutv1alpha1.Rollout {
				rollout := rolloutDemo1.DeepCopy()
				rollout.Status = rolloutv1alpha1.RolloutStatus{
					CanaryStatus: &rolloutv1alpha1.CanaryStatus{
						ObservedRolloutID: "1",
					},
					Phase: rolloutv1alpha1.RolloutPhaseProgressing,
				}
				return []*rolloutv1alpha1.Rollout{rollout}
			},
			getRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistories := []*rolloutv1alpha1.RolloutHistory{}
				return rollouthistories
			},
			expectRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistory := rollouthistoryDemo.DeepCopy()
				rollouthistory.Spec = rolloutv1alpha1.RolloutHistorySpec{}
				rollouthistory.Status = rolloutv1alpha1.RolloutHistoryStatus{}
				return []*rolloutv1alpha1.RolloutHistory{rollouthistory}
			},
		},
		{
			name: "test2, completed a rolloutHistory for rollout",
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "rollout-demo",
				},
			},
			getPods: func() []*corev1.Pod {
				pod1 := podDemo.DeepCopy()
				pod1.Name = "pod1"
				pod1.Spec.NodeName = "local"
				pod1.Status = corev1.PodStatus{
					PodIP: "1.2.3.1",
				}
				pod1.Labels = map[string]string{
					rolloutv1alpha1.RolloutBatchIDLabel: "1",
					rolloutv1alpha1.RolloutIDLabel:      "2",
					"app":                               "echoserver",
				}

				pod2 := podDemo.DeepCopy()
				pod2.Name = "pod2"
				pod2.Spec.NodeName = "local"
				pod2.Status = corev1.PodStatus{
					PodIP: "1.2.3.2",
				}
				pod2.Labels = map[string]string{
					rolloutv1alpha1.RolloutBatchIDLabel: "2",
					rolloutv1alpha1.RolloutIDLabel:      "2",
					"app":                               "echoserver",
				}

				pod3 := podDemo.DeepCopy()
				pod3.Name = "pod3"
				pod3.Spec.NodeName = "local"
				pod3.Status = corev1.PodStatus{
					PodIP: "1.2.3.3",
				}
				pod3.Labels = map[string]string{
					rolloutv1alpha1.RolloutBatchIDLabel: "3",
					rolloutv1alpha1.RolloutIDLabel:      "2",
					"app":                               "echoserver",
				}

				pod4 := podDemo.DeepCopy()
				pod4.Name = "pod4"
				pod4.Spec.NodeName = "local"
				pod4.Status = corev1.PodStatus{
					PodIP: "1.2.3.4",
				}
				pod4.Labels = map[string]string{
					rolloutv1alpha1.RolloutBatchIDLabel: "3",
					rolloutv1alpha1.RolloutIDLabel:      "2",
					"app":                               "echoserver",
				}

				pod5 := podDemo.DeepCopy()
				pod5.Name = "pod5"
				pod5.Spec.NodeName = "local"
				pod5.Status = corev1.PodStatus{
					PodIP: "1.2.3.5",
				}
				pod5.Labels = map[string]string{
					rolloutv1alpha1.RolloutBatchIDLabel: "3",
					rolloutv1alpha1.RolloutIDLabel:      "2",
					"app":                               "echoserver",
				}

				return []*corev1.Pod{pod1, pod2, pod3, pod4, pod5}
			},
			getService: func() []*corev1.Service {
				services := []*corev1.Service{serviceDemo.DeepCopy()}
				return services
			},
			getWorkload: func() []*kruisev1alpha1.CloneSet {
				workloads := []*kruisev1alpha1.CloneSet{cloneSetDemo.DeepCopy()}
				return workloads
			},
			getIngress: func() []*networkingv1.Ingress {
				ingresses := []*networkingv1.Ingress{ingressDemo.DeepCopy()}
				return ingresses
			},
			getHTTPRoute: func() []*v1alpha2.HTTPRoute {
				httpRoutes := []*v1alpha2.HTTPRoute{httpRouteDemo.DeepCopy()}
				return httpRoutes
			},
			getRollout: func() []*rolloutv1alpha1.Rollout {
				rollout := rolloutDemo1.DeepCopy()
				rollout.Spec.DeprecatedRolloutID = "2"
				rollout.Status = rolloutv1alpha1.RolloutStatus{
					CanaryStatus: &rolloutv1alpha1.CanaryStatus{
						ObservedRolloutID: "2",
					},
					Phase: rolloutv1alpha1.RolloutPhaseHealthy,
				}
				return []*rolloutv1alpha1.Rollout{rollout}
			},
			getRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistory := rollouthistoryDemo.DeepCopy()
				rollouthistory.Labels = map[string]string{
					rolloutIDLabel:   "2",
					rolloutNameLabel: "rollout-demo",
				}
				rollouthistory.Spec = rolloutv1alpha1.RolloutHistorySpec{}
				rollouthistory.Status = rolloutv1alpha1.RolloutHistoryStatus{}
				return []*rolloutv1alpha1.RolloutHistory{rollouthistory}
			},
			expectRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistory := rollouthistoryDemo.DeepCopy()
				rollouthistory.Labels = map[string]string{
					rolloutIDLabel:   "2",
					rolloutNameLabel: "rollout-demo",
				}
				rollouthistory.Spec = rolloutv1alpha1.RolloutHistorySpec{
					Rollout: rolloutv1alpha1.RolloutInfo{
						RolloutID: "2",
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "rollout-demo",
						},
					},
					Workload: rolloutv1alpha1.WorkloadInfo{
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "workload-demo",
						},
					},
					Service: rolloutv1alpha1.ServiceInfo{
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "service-demo",
						},
					},
					TrafficRouting: rolloutv1alpha1.TrafficRoutingInfo{
						Ingress: &rolloutv1alpha1.IngressInfo{
							NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
								Name: "ingress-demo",
							},
						},
						HTTPRoute: &rolloutv1alpha1.HTTPRouteInfo{
							NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
								Name: "HTTPRoute-demo",
							},
						},
					},
				}
				rollouthistory.Status = rolloutv1alpha1.RolloutHistoryStatus{
					Phase: rolloutv1alpha1.PhaseCompleted,
					CanarySteps: []rolloutv1alpha1.CanaryStepInfo{
						{
							CanaryStepIndex: 1,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod1",
									IP:       "1.2.3.1",
									NodeName: "local",
								},
							},
						},
						{
							CanaryStepIndex: 2,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod2",
									IP:       "1.2.3.2",
									NodeName: "local",
								},
							},
						},
						{
							CanaryStepIndex: 3,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod3",
									IP:       "1.2.3.3",
									NodeName: "local",
								},
								{
									Name:     "pod4",
									IP:       "1.2.3.4",
									NodeName: "local",
								},
								{
									Name:     "pod5",
									IP:       "1.2.3.5",
									NodeName: "local",
								},
							},
						},
					},
				}
				return []*rolloutv1alpha1.RolloutHistory{rollouthistory}
			},
		},
		{
			name: "test3, don't create a new rolloutHistory for rollout without rolloutID or ObservedRolloutID",
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "rollout-demo",
				},
			},
			getPods: func() []*corev1.Pod {
				pods := []*corev1.Pod{}
				return pods
			},
			getService: func() []*corev1.Service {
				services := []*corev1.Service{}
				return services
			},
			getWorkload: func() []*kruisev1alpha1.CloneSet {
				workloads := []*kruisev1alpha1.CloneSet{}
				return workloads
			},
			getIngress: func() []*networkingv1.Ingress {
				ingresses := []*networkingv1.Ingress{}
				return ingresses
			},
			getHTTPRoute: func() []*v1alpha2.HTTPRoute {
				httpRoutes := []*v1alpha2.HTTPRoute{}
				return httpRoutes
			},
			getRollout: func() []*rolloutv1alpha1.Rollout {
				rollout := rolloutDemo1.DeepCopy()
				rollout.Spec.DeprecatedRolloutID = ""
				rollout.Status = rolloutv1alpha1.RolloutStatus{
					CanaryStatus: &rolloutv1alpha1.CanaryStatus{
						ObservedRolloutID: "",
					},
					Phase: rolloutv1alpha1.RolloutPhaseProgressing,
				}
				return []*rolloutv1alpha1.Rollout{rollout}
			},
			getRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistories := []*rolloutv1alpha1.RolloutHistory{}
				return rollouthistories
			},
			expectRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistories := []*rolloutv1alpha1.RolloutHistory{}
				return rollouthistories
			},
		},
		{
			name: "test4, don't create a new rolloutHistory for rollout which doesn't change its rolloutID",
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "rollout-demo",
				},
			},
			getPods: func() []*corev1.Pod {
				pods := []*corev1.Pod{}
				return pods
			},
			getService: func() []*corev1.Service {
				services := []*corev1.Service{}
				return services
			},
			getWorkload: func() []*kruisev1alpha1.CloneSet {
				workloads := []*kruisev1alpha1.CloneSet{}
				return workloads
			},
			getIngress: func() []*networkingv1.Ingress {
				ingresses := []*networkingv1.Ingress{}
				return ingresses
			},
			getHTTPRoute: func() []*v1alpha2.HTTPRoute {
				httpRoutes := []*v1alpha2.HTTPRoute{}
				return httpRoutes
			},
			getRollout: func() []*rolloutv1alpha1.Rollout {
				rollout := rolloutDemo1.DeepCopy()
				rollout.Spec.DeprecatedRolloutID = "4"
				rollout.Status = rolloutv1alpha1.RolloutStatus{
					CanaryStatus: &rolloutv1alpha1.CanaryStatus{
						ObservedRolloutID: "4",
					},
					Phase: rolloutv1alpha1.RolloutPhaseProgressing,
				}
				return []*rolloutv1alpha1.Rollout{rollout}
			},
			getRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistory := rollouthistoryDemo.DeepCopy()
				rollouthistory.Labels = map[string]string{
					rolloutIDLabel:   "4",
					rolloutNameLabel: "rollout-demo",
				}
				rollouthistory.Spec = rolloutv1alpha1.RolloutHistorySpec{
					Rollout: rolloutv1alpha1.RolloutInfo{
						RolloutID: "4",
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "rollout-demo",
						},
					},
					Workload: rolloutv1alpha1.WorkloadInfo{
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "workload-demo",
						},
					},
					Service: rolloutv1alpha1.ServiceInfo{
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "service-demo",
						},
					},
					TrafficRouting: rolloutv1alpha1.TrafficRoutingInfo{
						Ingress: &rolloutv1alpha1.IngressInfo{
							NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
								Name: "ingress-demo",
							},
						},
						HTTPRoute: &rolloutv1alpha1.HTTPRouteInfo{
							NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
								Name: "HTTPRoute-demo",
							},
						},
					},
				}
				rollouthistory.Status = rolloutv1alpha1.RolloutHistoryStatus{
					Phase: rolloutv1alpha1.PhaseCompleted,
					CanarySteps: []rolloutv1alpha1.CanaryStepInfo{
						{
							CanaryStepIndex: 1,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod1",
									IP:       "1.2.3.1",
									NodeName: "local",
								},
							},
						},
						{
							CanaryStepIndex: 2,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod2",
									IP:       "1.2.3.2",
									NodeName: "local",
								},
							},
						},
						{
							CanaryStepIndex: 3,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod3",
									IP:       "1.2.3.3",
									NodeName: "local",
								},
								{
									Name:     "pod4",
									IP:       "1.2.3.4",
									NodeName: "local",
								},
								{
									Name:     "pod5",
									IP:       "1.2.3.5",
									NodeName: "local",
								},
							},
						},
					},
				}
				return []*rolloutv1alpha1.RolloutHistory{rollouthistory}
			},
			expectRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistory := rollouthistoryDemo.DeepCopy()
				rollouthistory.Labels = map[string]string{
					rolloutIDLabel:   "4",
					rolloutNameLabel: "rollout-demo",
				}
				rollouthistory.Spec = rolloutv1alpha1.RolloutHistorySpec{
					Rollout: rolloutv1alpha1.RolloutInfo{
						RolloutID: "4",
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "rollout-demo",
						},
					},
					Workload: rolloutv1alpha1.WorkloadInfo{
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "workload-demo",
						},
					},
					Service: rolloutv1alpha1.ServiceInfo{
						NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
							Name: "service-demo",
						},
					},
					TrafficRouting: rolloutv1alpha1.TrafficRoutingInfo{
						Ingress: &rolloutv1alpha1.IngressInfo{
							NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
								Name: "ingress-demo",
							},
						},
						HTTPRoute: &rolloutv1alpha1.HTTPRouteInfo{
							NameAndSpecData: rolloutv1alpha1.NameAndSpecData{
								Name: "HTTPRoute-demo",
							},
						},
					},
				}
				rollouthistory.Status = rolloutv1alpha1.RolloutHistoryStatus{
					Phase: rolloutv1alpha1.PhaseCompleted,
					CanarySteps: []rolloutv1alpha1.CanaryStepInfo{
						{
							CanaryStepIndex: 1,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod1",
									IP:       "1.2.3.1",
									NodeName: "local",
								},
							},
						},
						{
							CanaryStepIndex: 2,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod2",
									IP:       "1.2.3.2",
									NodeName: "local",
								},
							},
						},
						{
							CanaryStepIndex: 3,
							Pods: []rolloutv1alpha1.Pod{
								{
									Name:     "pod3",
									IP:       "1.2.3.3",
									NodeName: "local",
								},
								{
									Name:     "pod4",
									IP:       "1.2.3.4",
									NodeName: "local",
								},
								{
									Name:     "pod5",
									IP:       "1.2.3.5",
									NodeName: "local",
								},
							},
						},
					},
				}
				return []*rolloutv1alpha1.RolloutHistory{rollouthistory}
			},
		},
		{
			name: "test5, don't create a new rolloutHistory for rollout if its phase isn't RolloutPhaseProgressing",
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "rollout-demo",
				},
			},
			getPods: func() []*corev1.Pod {
				pods := []*corev1.Pod{}
				return pods
			},
			getService: func() []*corev1.Service {
				services := []*corev1.Service{}
				return services
			},
			getWorkload: func() []*kruisev1alpha1.CloneSet {
				workloads := []*kruisev1alpha1.CloneSet{}
				return workloads
			},
			getIngress: func() []*networkingv1.Ingress {
				ingresses := []*networkingv1.Ingress{}
				return ingresses
			},
			getHTTPRoute: func() []*v1alpha2.HTTPRoute {
				httpRoutes := []*v1alpha2.HTTPRoute{}
				return httpRoutes
			},
			getRollout: func() []*rolloutv1alpha1.Rollout {
				rollout := rolloutDemo1.DeepCopy()
				rollout.Spec.DeprecatedRolloutID = "5"
				rollout.Status = rolloutv1alpha1.RolloutStatus{
					CanaryStatus: &rolloutv1alpha1.CanaryStatus{
						ObservedRolloutID: "5",
					},
					Phase: rolloutv1alpha1.RolloutPhaseHealthy,
				}
				return []*rolloutv1alpha1.Rollout{rollout}
			},
			getRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistories := []*rolloutv1alpha1.RolloutHistory{}
				return rollouthistories
			},
			expectRolloutHistory: func() []*rolloutv1alpha1.RolloutHistory {
				rollouthistories := []*rolloutv1alpha1.RolloutHistory{}
				return rollouthistories
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			for _, obj := range cs.getPods() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create Pod failed: %s", err.Error())
				}
			}
			for _, obj := range cs.getRollout() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create Rollout failed: %s", err.Error())
				}
			}
			for _, obj := range cs.getWorkload() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create Workload failed: %s", err.Error())
				}
			}
			for _, obj := range cs.getService() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create Service failed: %s", err.Error())
				}
			}
			for _, obj := range cs.getIngress() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create Ingress failed: %s", err.Error())
				}
			}
			for _, obj := range cs.getHTTPRoute() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create HTTPRoute failed: %s", err.Error())
				}
			}
			for _, obj := range cs.getRolloutHistory() {
				err := fakeClient.Create(context.TODO(), obj.DeepCopy(), &client.CreateOptions{})
				if err != nil {
					t.Fatalf("create RolloutHistory failed: %s", err.Error())
				}
			}

			recon := RolloutHistoryReconciler{
				Client: fakeClient,
				Scheme: scheme,
				Finder: newControllerFinder2(fakeClient),
			}

			// Firstly, update spec, and requeue namespacedName
			_, err := recon.Reconcile(context.TODO(), cs.req)
			if err != nil {
				t.Fatalf("Reconcile failed: %s", err.Error())
			}

			// Secondly, update spec, and requeue namespacedName
			_, err = recon.Reconcile(context.TODO(), cs.req)
			if err != nil {
				t.Fatalf("Reconcile failed: %s", err.Error())
			}

			if !checkRolloutHistoryInfoEqual(fakeClient, t, cs.expectRolloutHistory()) {
				t.Fatalf("Reconcile failed")
			}

			if !checkRolloutHistoryNum(fakeClient, t, cs.expectRolloutHistory()) {
				t.Fatalf("RolloutHistory generated invalid: %s", err.Error())
			}
		})
	}
}

func checkRolloutHistoryNum(c client.WithWatch, t *testing.T, expect []*rolloutv1alpha1.RolloutHistory) bool {
	rollouthistories := &rolloutv1alpha1.RolloutHistoryList{}
	err := c.List(context.TODO(), rollouthistories, &client.ListOptions{}, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("get rollouthistories failed: %s", err.Error())
	}
	if len(rollouthistories.Items) != len(expect) {
		return false
	}
	return true
}

func checkRolloutHistoryInfoEqual(c client.WithWatch, t *testing.T, expect []*rolloutv1alpha1.RolloutHistory) bool {
	for i := range expect {
		obj := expect[i]
		rollouthistories := &rolloutv1alpha1.RolloutHistoryList{}
		err := c.List(context.TODO(), rollouthistories, &client.ListOptions{}, client.InNamespace(obj.Namespace))
		if err != nil {
			t.Fatalf("get rollouthistories failed: %s", err.Error())
		}
		// in cases, there will be just one rollouthistory
		if len(rollouthistories.Items) != 1 {
			t.Fatalf("create rollouthistory failed: %s", err.Error())
		}
		rollouthistory := rollouthistories.Items[0]
		// compare Label
		if !reflect.DeepEqual(obj.ObjectMeta.Labels, rollouthistory.ObjectMeta.Labels) {
			t.Fatalf("diff rollouthistory label failed: %s", err.Error())
			return false
		}
		// compare Spec
		if !checkRolloutHistorySpec(&obj.Spec, &rollouthistory.Spec) {
			t.Fatalf("diff rollouthistory spec failed: %s", err.Error())
			return false
		}
		// compare status
		// in the first reconcile, there is only spec updated
		if !checkRolloutHistoryStatus(&obj.Status, &rollouthistory.Status) {
			t.Fatalf("diff rollouthistory status failed: %s", err.Error())
			return false
		}
	}

	return true
}

func checkRolloutHistorySpec(spec1 *rolloutv1alpha1.RolloutHistorySpec, spec2 *rolloutv1alpha1.RolloutHistorySpec) bool {
	// spec1 and spec2 may be empty when rollouthistory is not completed
	if reflect.DeepEqual(spec1, spec2) {
		return true
	}
	// just compare those fields
	if spec1.Rollout.Name != spec2.Rollout.Name ||
		spec1.Service.Name != spec2.Service.Name ||
		spec1.Workload.Name != spec2.Workload.Name ||
		spec1.Rollout.RolloutID != spec2.Rollout.RolloutID ||
		spec1.TrafficRouting.Ingress.Name != spec2.TrafficRouting.Ingress.Name ||
		spec1.TrafficRouting.HTTPRoute.Name != spec2.TrafficRouting.HTTPRoute.Name {
		return false
	}

	return true
}

func checkRolloutHistoryStatus(status1 *rolloutv1alpha1.RolloutHistoryStatus, status2 *rolloutv1alpha1.RolloutHistoryStatus) bool {
	// in the first reconcile, there is only spec updated
	// status1 and status2 may be empty when rollouthistory is not completed
	if reflect.DeepEqual(status1, status2) {
		return true
	}
	// just compare those fields
	if status1.Phase != status2.Phase ||
		len(status1.CanarySteps) != len(status2.CanarySteps) {
		return false
	}
	// compare canarySteps, including CanaryStepIndex and pods for each canaryStep
	for i := 0; i < len(status1.CanarySteps); i++ {
		step1 := status1.CanarySteps[i]
		step2 := status2.CanarySteps[i]
		if step1.CanaryStepIndex != step2.CanaryStepIndex {
			return false
		}
		if len(step1.Pods) != len(step2.Pods) {
			return false
		}
		for j := 0; j < len(step1.Pods); j++ {
			if step1.Pods[j].IP != step2.Pods[j].IP ||
				step1.Pods[j].Name != step2.Pods[j].Name ||
				step1.Pods[j].NodeName != step2.Pods[j].NodeName {
				return false
			}
		}
	}

	return true
}
