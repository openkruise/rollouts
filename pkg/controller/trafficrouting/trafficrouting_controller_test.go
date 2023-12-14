/*
Copyright 2023 The Kruise Authors.

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

package trafficrouting

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

var (
	scheme                              *runtime.Scheme
	nginxIngressAnnotationDefaultPrefix = "nginx.ingress.kubernetes.io"

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

	demoTR = &v1alpha1.TrafficRouting{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rollouts.kruise.io/v1alpha1",
			Kind:       "TrafficRouting",
		},
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
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rolloutapi.AddToScheme(scheme)
}

func TestTrafficRoutingTest(t *testing.T) {
	cases := []struct {
		name              string
		getObj            func() ([]*corev1.Service, []*netv1.Ingress)
		getTrafficRouting func() *v1alpha1.TrafficRouting
		expectObj         func() ([]*corev1.Service, []*netv1.Ingress, *v1alpha1.TrafficRouting)
		expectDone        bool
	}{
		{
			name: "TrafficRouting reconcile Initial->Healthy",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getTrafficRouting: func() *v1alpha1.TrafficRouting {
				return demoTR.DeepCopy()
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress, *v1alpha1.TrafficRouting) {
				s1 := demoService.DeepCopy()
				tr := demoTR.DeepCopy()
				tr.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseHealthy,
				}
				return []*corev1.Service{s1}, []*netv1.Ingress{demoIngress.DeepCopy()}, tr
			},
			expectDone: true,
		},
		{
			name: "TrafficRouting reconcile Initial->Progressing",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getTrafficRouting: func() *v1alpha1.TrafficRouting {
				obj := demoTR.DeepCopy()
				obj.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseHealthy,
				}
				obj.Finalizers = []string{fmt.Sprintf("%s/rollout-test", v1alpha1.ProgressingRolloutFinalizerPrefix),
					util.TrafficRoutingFinalizer}
				return obj
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress, *v1alpha1.TrafficRouting) {
				s1 := demoService.DeepCopy()
				tr := demoTR.DeepCopy()
				tr.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				return []*corev1.Service{s1}, []*netv1.Ingress{demoIngress.DeepCopy()}, tr
			},
			expectDone: true,
		},
		{
			name: "TrafficRouting reconcile Progressing, and create canary ingress",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getTrafficRouting: func() *v1alpha1.TrafficRouting {
				obj := demoTR.DeepCopy()
				obj.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				obj.Finalizers = []string{fmt.Sprintf("%s/rollout-test", v1alpha1.ProgressingRolloutFinalizerPrefix),
					util.TrafficRoutingFinalizer}
				return obj
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress, *v1alpha1.TrafficRouting) {
				s1 := demoService.DeepCopy()
				i1 := demoIngress.DeepCopy()
				i2 := demoIngress.DeepCopy()
				i2.Name = "echoserver-canary"
				i2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				i2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "0"
				tr := demoTR.DeepCopy()
				tr.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				return []*corev1.Service{s1}, []*netv1.Ingress{i1, i2}, tr
			},
			expectDone: false,
		},
		{
			name: "TrafficRouting reconcile Progressing, and set ingress headers, and return false",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				i1 := demoIngress.DeepCopy()
				i2 := demoIngress.DeepCopy()
				i2.Name = "echoserver-canary"
				i2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				i2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "0"
				return []*corev1.Service{s1}, []*netv1.Ingress{i1, i2}
			},
			getTrafficRouting: func() *v1alpha1.TrafficRouting {
				obj := demoTR.DeepCopy()
				obj.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				obj.Finalizers = []string{fmt.Sprintf("%s/rollout-test", v1alpha1.ProgressingRolloutFinalizerPrefix),
					util.TrafficRoutingFinalizer}
				return obj
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress, *v1alpha1.TrafficRouting) {
				s1 := demoService.DeepCopy()
				i1 := demoIngress.DeepCopy()
				i2 := demoIngress.DeepCopy()
				i2.Name = "echoserver-canary"
				i2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				i2.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				i2.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = "123456"
				tr := demoTR.DeepCopy()
				tr.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				return []*corev1.Service{s1}, []*netv1.Ingress{i1, i2}, tr
			},
			expectDone: false,
		},
		{
			name: "TrafficRouting reconcile Progressing, and set ingress headers, and return true",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				i1 := demoIngress.DeepCopy()
				i2 := demoIngress.DeepCopy()
				i2.Name = "echoserver-canary"
				i2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				i2.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				i2.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = "123456"
				return []*corev1.Service{s1}, []*netv1.Ingress{i1, i2}
			},
			getTrafficRouting: func() *v1alpha1.TrafficRouting {
				obj := demoTR.DeepCopy()
				obj.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				obj.Finalizers = []string{fmt.Sprintf("%s/rollout-test", v1alpha1.ProgressingRolloutFinalizerPrefix),
					util.TrafficRoutingFinalizer}
				return obj
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress, *v1alpha1.TrafficRouting) {
				s1 := demoService.DeepCopy()
				i1 := demoIngress.DeepCopy()
				i2 := demoIngress.DeepCopy()
				i2.Name = "echoserver-canary"
				i2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				i2.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				i2.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = "123456"
				tr := demoTR.DeepCopy()
				tr.Status = v1alpha1.TrafficRoutingStatus{
					Phase: v1alpha1.TrafficRoutingPhaseProgressing,
				}
				return []*corev1.Service{s1}, []*netv1.Ingress{i1, i2}, tr
			},
			expectDone: true,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			ss, ig := cs.getObj()
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ig[0], ss[0], demoConf.DeepCopy()).Build()
			if len(ss) == 2 {
				_ = client.Create(context.TODO(), ss[1])
			}
			if len(ig) == 2 {
				_ = client.Create(context.TODO(), ig[1])
			}
			tr := cs.getTrafficRouting()
			_ = client.Create(context.TODO(), tr)
			manager := TrafficRoutingReconciler{
				Client:                client,
				Scheme:                scheme,
				trafficRoutingManager: trafficrouting.NewTrafficRoutingManager(client),
			}
			result, err := manager.Reconcile(context.TODO(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: tr.Namespace,
					Name:      tr.Name,
				},
			})
			if err != nil {
				t.Fatalf("TrafficRouting Reconcile failed: %s", err)
			}
			if cs.expectDone != (result.RequeueAfter == 0) {
				t.Fatalf("TrafficRouting Reconcile expect(%v), but get(%v)", cs.expectDone, result.RequeueAfter)
			}
			ss, ig, tr = cs.expectObj()
			checkObjEqual(client, t, tr)
			for _, obj := range ss {
				checkObjEqual(client, t, obj)
			}
			for _, obj := range ig {
				checkObjEqual(client, t, obj)
			}
		})
	}
}

func checkObjEqual(c client.WithWatch, t *testing.T, expect client.Object) {
	gvk := expect.GetObjectKind().GroupVersionKind()
	obj := getEmptyObject(gvk)
	err := c.Get(context.TODO(), client.ObjectKey{Namespace: expect.GetNamespace(), Name: expect.GetName()}, obj)
	if err != nil {
		t.Fatalf("get object failed: %s", err.Error())
	}
	switch gvk.Kind {
	case "Service":
		s1 := obj.(*corev1.Service)
		s2 := expect.(*corev1.Service)
		if !reflect.DeepEqual(s1.Spec, s2.Spec) {
			t.Fatalf("expect(%s), but get object(%s)", util.DumpJSON(s2.Spec), util.DumpJSON(s1.Spec))
		}
	case "Ingress":
		s1 := obj.(*netv1.Ingress)
		s2 := expect.(*netv1.Ingress)
		if !reflect.DeepEqual(s1.Spec, s2.Spec) || !reflect.DeepEqual(s1.Annotations, s2.Annotations) {
			t.Fatalf("expect(%s), but get object(%s)", util.DumpJSON(s2), util.DumpJSON(s1))
		}

	case "TrafficRouting":
		s1 := obj.(*v1alpha1.TrafficRouting)
		s1.Status.Message = ""
		s2 := expect.(*v1alpha1.TrafficRouting)
		if !reflect.DeepEqual(s1.Status, s2.Status) {
			t.Fatalf("expect(%s), but get object(%s)", util.DumpJSON(s2), util.DumpJSON(s1))
		}
	}
}

func getEmptyObject(gvk schema.GroupVersionKind) client.Object {
	switch gvk.Kind {
	case "Service":
		return &corev1.Service{}
	case "Ingress":
		return &netv1.Ingress{}
	case "TrafficRouting":
		return &v1alpha1.TrafficRouting{}
	}
	return nil
}
