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

package trafficrouting

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	kruisev1aplphal "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

	demoRollout = &v1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
			Annotations: map[string]string{
				util.RolloutHashAnnotation: "rollout-hash-v1",
			},
		},
		Spec: v1alpha1.RolloutSpec{
			ObjectRef: v1alpha1.ObjectRef{
				WorkloadRef: &v1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "echoserver",
				},
			},
			Strategy: v1alpha1.RolloutStrategy{
				Canary: &v1alpha1.CanaryStrategy{
					Steps: []v1alpha1.CanaryStep{
						{
							TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(5),
							},
							Replicas: &intstr.IntOrString{IntVal: 1},
						},
						{
							TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(20),
							},
							Replicas: &intstr.IntOrString{IntVal: 2},
						},
						{
							TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(60),
							},
							Replicas: &intstr.IntOrString{IntVal: 6},
						},
						{
							TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
								Weight: utilpointer.Int32(100),
							},
							Replicas: &intstr.IntOrString{IntVal: 10},
						},
					},
					TrafficRoutings: []v1alpha1.TrafficRoutingRef{
						{
							Service: "echoserver",
							Ingress: &v1alpha1.IngressTrafficRouting{
								Name: "echoserver",
							},
						},
					},
				},
			},
		},
		Status: v1alpha1.RolloutStatus{
			Phase: v1alpha1.RolloutPhaseProgressing,
			CanaryStatus: &v1alpha1.CanaryStatus{
				ObservedWorkloadGeneration: 1,
				RolloutHash:                "rollout-hash-v1",
				ObservedRolloutID:          "rollout-id-1",
				StableRevision:             "podtemplatehash-v1",
				CanaryRevision:             "revision-v2",
				CurrentStepIndex:           1,
				CurrentStepState:           v1alpha1.CanaryStepStateTrafficRouting,
				PodTemplateHash:            "podtemplatehash-v2",
				LastUpdateTime:             &metav1.Time{Time: time.Now()},
			},
			Conditions: []v1alpha1.RolloutCondition{
				{
					Type:   v1alpha1.RolloutConditionProgressing,
					Reason: v1alpha1.ProgressingReasonInRolling,
					Status: corev1.ConditionFalse,
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
	_ = kruisev1aplphal.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
}

func TestDoTrafficRouting(t *testing.T) {
	cases := []struct {
		name       string
		getObj     func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout func() (*v1alpha1.Rollout, *util.Workload)
		expectObj  func() ([]*corev1.Service, []*netv1.Ingress)
		expectDone bool
	}{
		{
			name: "DoTrafficRouting test1",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				return demoRollout.DeepCopy(), &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			expectDone: false,
		},
		{
			name: "DoTrafficRouting test2",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1}
			},
			expectDone: false,
		},
		{
			name: "DoTrafficRouting test3",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "0"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-10 * time.Second)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "5"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: false,
		},
		{
			name: "DoTrafficRouting test4",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "5"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-10 * time.Second)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "5"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: true,
		},
		{
			name: "DoTrafficRouting test5",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "5"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-10 * time.Second)}
				obj.Status.CanaryStatus.CurrentStepIndex = 2
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "20"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: false,
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
			rollout, workload := cs.getRollout()
			newStatus := rollout.Status.DeepCopy()
			currentStep := rollout.Spec.Strategy.Canary.Steps[newStatus.CanaryStatus.CurrentStepIndex-1]
			c := &TrafficRoutingContext{
				Key:              fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:        rollout.Namespace,
				ObjectRef:        rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:         currentStep.TrafficRoutingStrategy,
				OwnerRef:         *metav1.NewControllerRef(rollout, v1alpha1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey: workload.RevisionLabelKey,
				StableRevision:   newStatus.CanaryStatus.StableRevision,
				CanaryRevision:   newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:   newStatus.CanaryStatus.LastUpdateTime,
			}
			manager := NewTrafficRoutingManager(client)
			err := manager.InitializeTrafficRouting(c)
			if err != nil {
				t.Fatalf("InitializeTrafficRouting failed: %s", err)
			}
			done, err := manager.DoTrafficRouting(c)
			if err != nil {
				t.Fatalf("DoTrafficRouting failed: %s", err)
			}
			if cs.expectDone != done {
				t.Fatalf("DoTrafficRouting expect(%v), but get(%v)", cs.expectDone, done)
			}
			ss, ig = cs.expectObj()
			for _, obj := range ss {
				checkObjEqual(client, t, obj)
			}
			for _, obj := range ig {
				checkObjEqual(client, t, obj)
			}
		})
	}
}

func TestFinalisingTrafficRouting(t *testing.T) {
	cases := []struct {
		name                     string
		getObj                   func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout               func() (*v1alpha1.Rollout, *util.Workload)
		onlyRestoreStableService bool
		expectObj                func() ([]*corev1.Service, []*netv1.Ingress)
		expectDone               bool
	}{
		{
			name: "FinalisingTrafficRouting test1",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyRestoreStableService: true,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: false,
		},
		{
			name: "FinalisingTrafficRouting test2",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyRestoreStableService: true,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: false,
		},
		{
			name: "FinalisingTrafficRouting test3",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-10 * time.Second)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyRestoreStableService: true,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: true,
		},
		{
			name: "FinalisingTrafficRouting test4",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-3 * time.Second)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyRestoreStableService: false,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "0"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			expectDone: false,
		},
		{
			name: "FinalisingTrafficRouting test5",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "0"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1alpha1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-3 * time.Second)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyRestoreStableService: false,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
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
			rollout, workload := cs.getRollout()
			newStatus := rollout.Status.DeepCopy()
			currentStep := rollout.Spec.Strategy.Canary.Steps[newStatus.CanaryStatus.CurrentStepIndex-1]
			c := &TrafficRoutingContext{
				Key:              fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:        rollout.Namespace,
				ObjectRef:        rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:         currentStep.TrafficRoutingStrategy,
				OwnerRef:         *metav1.NewControllerRef(rollout, v1alpha1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey: workload.RevisionLabelKey,
				StableRevision:   newStatus.CanaryStatus.StableRevision,
				CanaryRevision:   newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:   newStatus.CanaryStatus.LastUpdateTime,
			}
			manager := NewTrafficRoutingManager(client)
			done, err := manager.FinalisingTrafficRouting(c, cs.onlyRestoreStableService)
			if err != nil {
				t.Fatalf("DoTrafficRouting failed: %s", err)
			}
			if cs.expectDone != done {
				t.Fatalf("DoTrafficRouting expect(%v), but get(%v)", cs.expectDone, done)
			}
			ss, ig = cs.expectObj()
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
	}
}

func getEmptyObject(gvk schema.GroupVersionKind) client.Object {
	switch gvk.Kind {
	case "Service":
		return &corev1.Service{}
	case "Ingress":
		return &netv1.Ingress{}
	}
	return nil
}
