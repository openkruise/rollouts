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
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	"github.com/openkruise/rollouts/pkg/util/grace"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

	demoRollout = &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
			Annotations: map[string]string{
				util.RolloutHashAnnotation: "rollout-hash-v1",
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
							// webhook doesn't work in unit test, we manually set gracePeriodSeconds to 1
							GracePeriodSeconds: 1,
						},
					},
				},
			},
		},
		Status: v1beta1.RolloutStatus{
			Phase: v1beta1.RolloutPhaseProgressing,
			CanaryStatus: &v1beta1.CanaryStatus{
				CommonStatus: v1beta1.CommonStatus{
					ObservedWorkloadGeneration: 1,
					RolloutHash:                "rollout-hash-v1",
					ObservedRolloutID:          "rollout-id-1",
					StableRevision:             "podtemplatehash-v1",
					CurrentStepIndex:           1,
					CurrentStepState:           v1beta1.CanaryStepStateTrafficRouting,
					PodTemplateHash:            "podtemplatehash-v2",
					LastUpdateTime:             &metav1.Time{Time: time.Now()},
				},
				CanaryRevision: "revision-v2",
			},
			Conditions: []v1beta1.RolloutCondition{
				{
					Type:   v1beta1.RolloutConditionProgressing,
					Reason: v1alpha1.ProgressingReasonInRolling,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}

	demoIstioRollout = &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
			Annotations: map[string]string{
				util.RolloutHashAnnotation: "rollout-hash-v1",
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
					DisableGenerateCanaryService: false,
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
							CustomNetworkRefs: []v1beta1.ObjectRef{
								{
									APIVersion: "networking.istio.io/v1alpha3",
									Kind:       "VirtualService",
									Name:       "echoserver",
								},
								{
									APIVersion: "networking.istio.io/v1alpha3",
									Kind:       "DestinationRule",
									Name:       "dr-demo",
								},
							},
						},
					},
				},
			},
		},
		Status: v1beta1.RolloutStatus{
			Phase: v1beta1.RolloutPhaseProgressing,
			CanaryStatus: &v1beta1.CanaryStatus{
				CommonStatus: v1beta1.CommonStatus{
					ObservedWorkloadGeneration: 1,
					RolloutHash:                "rollout-hash-v1",
					ObservedRolloutID:          "rollout-id-1",
					StableRevision:             "podtemplatehash-v1",
					CurrentStepIndex:           1,
					CurrentStepState:           v1beta1.CanaryStepStateTrafficRouting,
					PodTemplateHash:            "podtemplatehash-v2",
					LastUpdateTime:             &metav1.Time{Time: time.Now()},
				},
				CanaryRevision: "revision-v2",
			},
			Conditions: []v1beta1.RolloutCondition{
				{
					Type:   v1beta1.RolloutConditionProgressing,
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

	istioLuaConfig = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configuration.RolloutConfigurationName,
			Namespace: util.GetRolloutNamespace(),
		},
		Data: map[string]string{
			"lua.traffic.routing.VirtualService.networking.istio.io": `
			spec = obj.data.spec

			if obj.canaryWeight == -1 then
				obj.canaryWeight = 100
				obj.stableWeight = 0
			end
			
			function GetHost(destination)
				local host = destination.destination.host
				dot_position = string.find(host, ".", 1, true)
				if (dot_position) then
					host = string.sub(host, 1, dot_position - 1)
				end
				return host
			end
			
			-- find routes of VirtualService with stableService
			function GetRulesToPatch(spec, stableService, protocol)
				local matchedRoutes = {}
				if (spec[protocol] ~= nil) then
					for _, rule in ipairs(spec[protocol]) do
						-- skip routes contain matches
						if (rule.match == nil) then
							for _, route in ipairs(rule.route) do
								if GetHost(route) == stableService then
									table.insert(matchedRoutes, rule)
								end
							end
						end
					end
				end
				return matchedRoutes
			end
			
			function CalculateWeight(route, stableWeight, n)
				local weight
				if (route.weight) then
					weight = math.floor(route.weight * stableWeight / 100)
				else
					weight = math.floor(stableWeight / n)
				end
				return weight
			end
			
			-- generate routes with matches, insert a rule before other rules, only support http headers, cookies etc.
			function GenerateRoutesWithMatches(spec, matches, stableService, canaryService)
				for _, match in ipairs(matches) do
					local route = {}
					route["match"] = {}
					for key, value in pairs(match) do
						local vsMatch = {}
						vsMatch[key] = {}
						for _, rule in ipairs(value) do
							if rule["type"] == "RegularExpression" then
								matchType = "regex"
							elseif rule["type"] == "Exact" then
								matchType = "exact"
							elseif rule["type"] == "Prefix" then
								matchType = "prefix"
							end
							if key == "headers" then
								vsMatch[key][rule["name"]] = {}
								vsMatch[key][rule["name"]][matchType] = rule.value
							else
								vsMatch[key][matchType] = rule.value
							end
						end
						table.insert(route["match"], vsMatch)
					end
					route.route = {
						{
							destination = {}
						}
					}
					-- stableService == canaryService indicates DestinationRule exists and subset is set to be canary by default
					if stableService == canaryService then
						route.route[1].destination.host = stableService
						route.route[1].destination.subset = "canary"
					else
						route.route[1].destination.host = canaryService
					end
					table.insert(spec.http, 1, route)
				end
			end
			
			-- generate routes without matches, change every rule whose host is stableService
			function GenerateRoutes(spec, stableService, canaryService, stableWeight, canaryWeight, protocol)
				local matchedRules = GetRulesToPatch(spec, stableService, protocol)
				for _, rule in ipairs(matchedRules) do
					local canary
					if stableService ~= canaryService then
						canary = {
							destination = {
								host = canaryService,
							},
							weight = canaryWeight,
						}
					else
						canary = {
							destination = {
								host = stableService,
								subset = "canary",
							},
							weight = canaryWeight,
						}
					end
			
					-- incase there are multiple versions traffic already, do a for-loop
					for _, route in ipairs(rule.route) do
						-- update stable service weight
						route.weight = CalculateWeight(route, stableWeight, #rule.route)
					end
					table.insert(rule.route, canary)
				end
			end
			
			if (obj.matches and next(obj.matches) ~= nil)
			then
				GenerateRoutesWithMatches(spec, obj.matches, obj.stableService, obj.canaryService)
			else
				GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, "http")
				GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, "tcp")
				GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, "tls")
			end
			return obj.data
			
 			`,
			"lua.traffic.routing.DestinationRule.networking.istio.io": `
			local spec = obj.data.spec
			local canary = {}
			canary.labels = {}
			canary.name = "canary"
			local podLabelKey = "istio.service.tag"
			canary.labels[podLabelKey] = "gray"
			table.insert(spec.subsets, canary)
			return obj.data
			`,
		},
	}

	virtualServiceDemo = `
						{
							"apiVersion": "networking.istio.io/v1alpha3",
							"kind": "VirtualService",
							"metadata": {
								"name": "echoserver",
								"annotations": {
									"virtual": "test"
								}
							},
							"spec": {
								"hosts": [
									"echoserver.example.com"
								],
								"http": [
									{
										"route": [
											{
												"destination": {
													"host": "echoserver"
												}
											}
										]
									}
								]
							}
						}
						`
	destinationRuleDemo = `
						{
							"apiVersion": "networking.istio.io/v1alpha3",
							"kind": "DestinationRule",
							"metadata": {
								"name": "dr-demo"
							},
							"spec": {
								"host": "echoserver",
								"subsets": [
									{
										"labels": {
											"version": "base"
										},
										"name": "version-base"
									}
								],
								"trafficPolicy": {
									"loadBalancer": {
										"simple": "ROUND_ROBIN"
									}
								}
							}
						}
						`
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rolloutapi.AddToScheme(scheme)
}

func TestDoTrafficRouting(t *testing.T) {
	cases := []struct {
		name       string
		getObj     func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout func() (*v1beta1.Rollout, *util.Workload)
		expectObj  func() ([]*corev1.Service, []*netv1.Ingress)
		expectDone bool
	}{
		{
			name: "DoTrafficRouting test1",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				rollout := demoRollout.DeepCopy()
				workload := &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
				rollout.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return rollout, workload
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
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
				OwnerRef:         *metav1.NewControllerRef(rollout, v1beta1.SchemeGroupVersion.WithKind("Rollout")),
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

func TestDoTrafficRoutingWithIstio(t *testing.T) {
	const (
		OriginalSpecAnnotation = "rollouts.kruise.io/original-spec-configuration"
	)
	cases := []struct {
		name                string
		getService          func() []*corev1.Service
		getUnstructureds    func() []*unstructured.Unstructured
		getRollout          func() (*v1beta1.Rollout, *util.Workload)
		expectUnstructureds func() []*unstructured.Unstructured
		expectDone          bool
	}{
		{
			name: "Test with DisableGenerateCanaryService: false",
			getService: func() []*corev1.Service {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				return []*corev1.Service{s1}
			},
			getUnstructureds: func() []*unstructured.Unstructured {
				objects := make([]*unstructured.Unstructured, 0)
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				objects = append(objects, u)

				u = &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(destinationRuleDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				objects = append(objects, u)
				return objects
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoIstioRollout.DeepCopy()
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-10 * time.Second)}
				obj.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds = 1
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectUnstructureds: func() []*unstructured.Unstructured {
				objects := make([]*unstructured.Unstructured, 0)
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"spec":{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]},"annotations":{"virtual":"test"}}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver-canary"},"weight":5}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)

				u = &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(destinationRuleDemo))
				annotations = map[string]string{
					OriginalSpecAnnotation: `{"spec":{"host":"echoserver","subsets":[{"labels":{"version":"base"},"name":"version-base"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}}`,
				}
				u.SetAnnotations(annotations)
				specStr = `{"host":"echoserver","subsets":[{"labels":{"version":"base"},"name":"version-base"},{"labels":{"istio.service.tag":"gray"},"name":"canary"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}`
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)
				return objects
			},
			expectDone: true,
		},
		{
			name: "Test with DisableGenerateCanaryService: true",
			getService: func() []*corev1.Service {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				// s2 := demoService.DeepCopy()
				// s2.Name = "echoserver-canary"
				// s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				return []*corev1.Service{s1}
			},
			getUnstructureds: func() []*unstructured.Unstructured {
				objects := make([]*unstructured.Unstructured, 0)
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				objects = append(objects, u)

				u = &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(destinationRuleDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				objects = append(objects, u)
				return objects
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoIstioRollout.DeepCopy()
				// set DisableGenerateCanaryService as true
				obj.Spec.Strategy.Canary.DisableGenerateCanaryService = true
				obj.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds = 1
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-10 * time.Second)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectUnstructureds: func() []*unstructured.Unstructured {
				objects := make([]*unstructured.Unstructured, 0)
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"spec":{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]},"annotations":{"virtual":"test"}}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver","subset":"canary"},"weight":5}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)

				u = &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(destinationRuleDemo))
				annotations = map[string]string{
					OriginalSpecAnnotation: `{"spec":{"host":"echoserver","subsets":[{"labels":{"version":"base"},"name":"version-base"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}}`,
				}
				u.SetAnnotations(annotations)
				specStr = `{"host":"echoserver","subsets":[{"labels":{"version":"base"},"name":"version-base"},{"labels":{"istio.service.tag":"gray"},"name":"canary"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}`
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)
				return objects
			},
			expectDone: true,
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			ss := cs.getService()
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ss[0], istioLuaConfig.DeepCopy()).Build()
			for _, obj := range cs.getUnstructureds() {
				err := client.Create(context.TODO(), obj)
				if err != nil {
					t.Fatalf("failed to create objects: %s", err.Error())
				}
			}
			rollout, workload := cs.getRollout()
			newStatus := rollout.Status.DeepCopy()
			currentStep := rollout.Spec.Strategy.Canary.Steps[newStatus.CanaryStatus.CurrentStepIndex-1]
			c := &TrafficRoutingContext{
				Key:                          fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:                    rollout.Namespace,
				ObjectRef:                    rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:                     currentStep.TrafficRoutingStrategy,
				OwnerRef:                     *metav1.NewControllerRef(rollout, v1beta1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey:             workload.RevisionLabelKey,
				StableRevision:               newStatus.CanaryStatus.StableRevision,
				CanaryRevision:               newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:               newStatus.CanaryStatus.LastUpdateTime,
				DisableGenerateCanaryService: rollout.Spec.Strategy.Canary.DisableGenerateCanaryService,
			}
			manager := NewTrafficRoutingManager(client)
			err := manager.InitializeTrafficRouting(c)
			if err != nil {
				t.Fatalf("InitializeTrafficRouting failed: %s", err)
			}
			// now we need to wait at least 2x grace time to keep traffic stable:
			// create the canary service -> grace time -> update the gateway -> grace time
			// therefore, before both grace times are over, DoTrafficRouting should return false
			// firstly, create the canary Service, before the grace time over, return false
			_, err = manager.DoTrafficRouting(c)
			if err != nil {
				t.Fatalf("DoTrafficRouting failed: %s", err)
			}
			time.Sleep(1 * time.Second)
			// secondly, update the gateway, before the grace time over, return false
			_, err = manager.DoTrafficRouting(c)
			if err != nil {
				t.Fatalf("DoTrafficRouting failed: %s", err)
			}
			time.Sleep(1 * time.Second)
			// now, both grace times are over, it should be true
			done, err := manager.DoTrafficRouting(c)
			if err != nil {
				t.Fatalf("DoTrafficRouting failed: %s", err)
			}
			if cs.expectDone != done {
				t.Fatalf("DoTrafficRouting expect(%v), but get(%v)", cs.expectDone, done)
			}
			unst := cs.expectUnstructureds()
			for _, obj := range unst {
				checkUnstructureEqual(client, t, obj)
			}
		})
	}

}

func TestFinalisingTrafficRouting(t *testing.T) {
	cases := []struct {
		name               string
		getObj             func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout         func() (*v1beta1.Rollout, *util.Workload)
		onlyTrafficRouting bool
		expectObj          func() ([]*corev1.Service, []*netv1.Ingress)
		expectNotFound     func() ([]*corev1.Service, []*netv1.Ingress)
		expectDone         bool
	}{
		{
			name: "FinalisingTrafficRouting test1", // will restore the stable service
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
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
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				return nil, nil
			},
			expectDone: false,
		},
		{
			name: "stable Service already clear", // will restore the gateway
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				return nil, []*netv1.Ingress{c2}
			},
			expectDone: false,
		},
		{
			name: "gateway already restored", // will remove the canary service
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1}
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				return []*corev1.Service{s2}, []*netv1.Ingress{c2}
			},
			expectDone: false,
		},
		{
			name: "canary Service already clear", // all is done
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				return []*corev1.Service{s2}, []*netv1.Ingress{c2}
			},
			expectDone: true,
		},
		{
			name: "OnlyTrafficRouting true - test 1",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyTrafficRouting: true,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				return nil, []*netv1.Ingress{c2}
			},
			expectDone: false,
		},
		{
			name: "OnlyTrafficRouting true - test 2",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			onlyTrafficRouting: true,
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1}, []*netv1.Ingress{c1}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				return nil, []*netv1.Ingress{c2}
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
				Key:                fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:          rollout.Namespace,
				ObjectRef:          rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:           currentStep.TrafficRoutingStrategy,
				OwnerRef:           *metav1.NewControllerRef(rollout, v1beta1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey:   workload.RevisionLabelKey,
				StableRevision:     newStatus.CanaryStatus.StableRevision,
				CanaryRevision:     newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:     newStatus.CanaryStatus.LastUpdateTime,
				OnlyTrafficRouting: cs.onlyTrafficRouting,
			}
			manager := NewTrafficRoutingManager(client)
			done, err := manager.FinalisingTrafficRouting(c)
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

			ss, ig = cs.expectNotFound()
			for _, obj := range ss {
				checkNotFound(client, t, obj)
			}
			for _, obj := range ig {
				checkNotFound(client, t, obj)
			}
			// empty the grace expectations to avoid making effects on the following test
			grace.ResetExpectations()
		})
	}
}

func TestRestoreGateway(t *testing.T) {
	cases := []struct {
		name               string
		getObj             func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout         func() (*v1beta1.Rollout, *util.Workload)
		onlyTrafficRouting bool
		expectObj          func() ([]*corev1.Service, []*netv1.Ingress)
		expectNotFound     func() ([]*corev1.Service, []*netv1.Ingress)
		retry              bool
	}{
		{
			name: "Restore Gateway test1",
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				// stable service and canary service remain unchanged
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				return nil, []*netv1.Ingress{c2}
			},
			retry: true,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			ss, ig := cs.getObj()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ig[0], ss[0], demoConf.DeepCopy()).Build()
			if len(ss) == 2 {
				_ = cli.Create(context.TODO(), ss[1])
			}
			if len(ig) == 2 {
				_ = cli.Create(context.TODO(), ig[1])
			}
			rollout, workload := cs.getRollout()
			newStatus := rollout.Status.DeepCopy()
			currentStep := rollout.Spec.Strategy.Canary.Steps[newStatus.CanaryStatus.CurrentStepIndex-1]
			c := &TrafficRoutingContext{
				Key:                fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:          rollout.Namespace,
				ObjectRef:          rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:           currentStep.TrafficRoutingStrategy,
				OwnerRef:           *metav1.NewControllerRef(rollout, v1beta1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey:   workload.RevisionLabelKey,
				StableRevision:     newStatus.CanaryStatus.StableRevision,
				CanaryRevision:     newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:     newStatus.CanaryStatus.LastUpdateTime,
				OnlyTrafficRouting: cs.onlyTrafficRouting,
			}
			manager := NewTrafficRoutingManager(cli)
			retry, err := manager.RestoreGateway(c)
			if err != nil {
				t.Fatalf("RestoreGateway failed: %s", err)
			}
			if retry != cs.retry {
				t.Fatalf("RestoreGateway expect(%v), but get(%v)", cs.retry, retry)
			}
			ss, ig = cs.expectObj()
			for _, obj := range ss {
				checkObjEqual(cli, t, obj)
			}
			for _, obj := range ig {
				checkObjEqual(cli, t, obj)
			}

			ss, ig = cs.expectNotFound()
			for _, obj := range ss {
				checkNotFound(cli, t, obj)
			}
			for _, obj := range ig {
				checkNotFound(cli, t, obj)
			}
			// the second call, it should be no error and no retry
			time.Sleep(1 * time.Second)
			retry, err = manager.RestoreGateway(c)
			if err != nil {
				t.Fatalf("RestoreGateway failed: %s", err.Error())
			} else if retry {
				t.Fatalf("RestoreGateway failed: retry should be false")
			}
		})
	}
}

func TestRemoveCanaryService(t *testing.T) {
	cases := []struct {
		name               string
		getObj             func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout         func() (*v1beta1.Rollout, *util.Workload)
		onlyTrafficRouting bool
		expectObj          func() ([]*corev1.Service, []*netv1.Ingress)
		expectNotFound     func() ([]*corev1.Service, []*netv1.Ingress)
		retry              bool
	}{
		{
			name: "Restore Gateway test1",
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				// stable service and ingress remain unchanged
				s1 := demoService.DeepCopy()
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "100"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1}, []*netv1.Ingress{c1, c2}
			},
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				return []*corev1.Service{s2}, nil
			},
			retry: true,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			ss, ig := cs.getObj()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ig[0], ss[0], demoConf.DeepCopy()).Build()
			if len(ss) == 2 {
				_ = cli.Create(context.TODO(), ss[1])
			}
			if len(ig) == 2 {
				_ = cli.Create(context.TODO(), ig[1])
			}
			rollout, workload := cs.getRollout()
			newStatus := rollout.Status.DeepCopy()
			currentStep := rollout.Spec.Strategy.Canary.Steps[newStatus.CanaryStatus.CurrentStepIndex-1]
			c := &TrafficRoutingContext{
				Key:                fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:          rollout.Namespace,
				ObjectRef:          rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:           currentStep.TrafficRoutingStrategy,
				OwnerRef:           *metav1.NewControllerRef(rollout, v1beta1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey:   workload.RevisionLabelKey,
				StableRevision:     newStatus.CanaryStatus.StableRevision,
				CanaryRevision:     newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:     newStatus.CanaryStatus.LastUpdateTime,
				OnlyTrafficRouting: cs.onlyTrafficRouting,
			}
			manager := NewTrafficRoutingManager(cli)
			retry, err := manager.RemoveCanaryService(c)
			if err != nil {
				t.Fatalf("RemoveCanaryService failed: %s", err)
			}
			if retry != cs.retry {
				t.Fatalf("RemoveCanaryService expect(%v), but get(%v)", false, retry)
			}
			ss, ig = cs.expectObj()
			for _, obj := range ss {
				checkObjEqual(cli, t, obj)
			}
			for _, obj := range ig {
				checkObjEqual(cli, t, obj)
			}

			ss, ig = cs.expectNotFound()
			for _, obj := range ss {
				checkNotFound(cli, t, obj)
			}
			for _, obj := range ig {
				checkNotFound(cli, t, obj)
			}
			// the second call, it should be no error and no retry
			time.Sleep(1 * time.Second)
			retry, err = manager.RemoveCanaryService(c)
			if err != nil {
				t.Fatalf("RemoveCanaryService failed: %s", err.Error())
			} else if retry {
				t.Fatalf("RemoveCanaryService failed: retry should be false")
			}
		})
	}
}

func TestRouteAllTrafficToNewVersion(t *testing.T) {
	cases := []struct {
		name               string
		getObj             func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout         func() (*v1beta1.Rollout, *util.Workload)
		onlyTrafficRouting bool
		expectObj          func() ([]*corev1.Service, []*netv1.Ingress)
		expectNotFound     func() ([]*corev1.Service, []*netv1.Ingress)
		retry              bool
	}{
		{
			name: "Route all traffic test1",
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
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				// service and ingress remain unchanged
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
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				return nil, nil
			},
			retry: false,
		},
		{
			name: "Route all traffic test2",
			getObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s2 := demoService.DeepCopy()
				s2.Name = "echoserver-canary"
				s2.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v2"
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = "echoserver-canary"
				c2.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
				c2.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = "50"
				c2.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1beta1.Rollout, *util.Workload) {
				obj := demoRollout.DeepCopy()
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
				return obj, &util.Workload{RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey}
			},
			expectObj: func() ([]*corev1.Service, []*netv1.Ingress) {
				// service and ingress remain unchanged
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
			expectNotFound: func() ([]*corev1.Service, []*netv1.Ingress) {
				return nil, nil
			},
			retry: true,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			ss, ig := cs.getObj()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ig[0], ss[0], demoConf.DeepCopy()).Build()
			if len(ss) == 2 {
				_ = cli.Create(context.TODO(), ss[1])
			}
			if len(ig) == 2 {
				_ = cli.Create(context.TODO(), ig[1])
			}
			rollout, workload := cs.getRollout()
			newStatus := rollout.Status.DeepCopy()
			currentStep := rollout.Spec.Strategy.Canary.Steps[newStatus.CanaryStatus.CurrentStepIndex-1]
			c := &TrafficRoutingContext{
				Key:                fmt.Sprintf("Rollout(%s/%s)", rollout.Namespace, rollout.Name),
				Namespace:          rollout.Namespace,
				ObjectRef:          rollout.Spec.Strategy.Canary.TrafficRoutings,
				Strategy:           currentStep.TrafficRoutingStrategy,
				OwnerRef:           *metav1.NewControllerRef(rollout, v1beta1.SchemeGroupVersion.WithKind("Rollout")),
				RevisionLabelKey:   workload.RevisionLabelKey,
				StableRevision:     newStatus.CanaryStatus.StableRevision,
				CanaryRevision:     newStatus.CanaryStatus.PodTemplateHash,
				LastUpdateTime:     newStatus.CanaryStatus.LastUpdateTime,
				OnlyTrafficRouting: cs.onlyTrafficRouting,
			}
			manager := NewTrafficRoutingManager(cli)
			retry, err := manager.RouteAllTrafficToNewVersion(c)
			if err != nil {
				t.Fatalf("RouteAllTrafficToNewVersion first failed: %s", err.Error())
			}
			if retry != cs.retry {
				t.Fatalf("RouteAllTrafficToNewVersion expect(%v), but get(%v)", cs.retry, retry)
			}
			ss, ig = cs.expectObj()
			for _, obj := range ss {
				checkObjEqual(cli, t, obj)
			}
			for _, obj := range ig {
				checkObjEqual(cli, t, obj)
			}

			ss, ig = cs.expectNotFound()
			for _, obj := range ss {
				checkNotFound(cli, t, obj)
			}
			for _, obj := range ig {
				checkNotFound(cli, t, obj)
			}
			// if done, no need check again
			if !cs.retry {
				return
			}
			// the second call, it should be no error and no retry
			time.Sleep(1 * time.Second)
			retry, err = manager.RouteAllTrafficToNewVersion(c)
			if err != nil {
				t.Fatalf("RouteAllTrafficToNewVersion failed: %s", err.Error())
			} else if retry {
				t.Fatalf("RouteAllTrafficToNewVersion failed: retry should be false")
			}
			ss, ig = cs.expectObj()
			for _, obj := range ss {
				checkObjEqual(cli, t, obj)
			}
			for _, obj := range ig {
				checkObjEqual(cli, t, obj)
			}
		})
	}
}

func checkNotFound(c client.WithWatch, t *testing.T, expect client.Object) {
	gvk := expect.GetObjectKind().GroupVersionKind()
	obj := getEmptyObject(gvk)
	err := c.Get(context.TODO(), client.ObjectKey{Namespace: expect.GetNamespace(), Name: expect.GetName()}, obj)
	if !errors.IsNotFound(err) {
		t.Fatalf("get object failed: %s", err.Error())
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

func checkUnstructureEqual(cli client.Client, t *testing.T, expect *unstructured.Unstructured) {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(expect.GetAPIVersion())
	obj.SetKind(expect.GetKind())
	if err := cli.Get(context.TODO(), types.NamespacedName{Namespace: expect.GetNamespace(), Name: expect.GetName()}, obj); err != nil {
		t.Fatalf("Get object failed: %s", err.Error())
	}
	if !reflect.DeepEqual(obj.GetAnnotations(), expect.GetAnnotations()) {
		fmt.Println(util.DumpJSON(obj.GetAnnotations()), util.DumpJSON(expect.GetAnnotations()))
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect.GetAnnotations()), util.DumpJSON(obj.GetAnnotations()))
	}
	if util.DumpJSON(expect.Object["spec"]) != util.DumpJSON(obj.Object["spec"]) {
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect.Object["spec"]), util.DumpJSON(obj.Object["spec"]))
	}
}
