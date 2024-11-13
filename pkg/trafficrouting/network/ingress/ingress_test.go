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

package ingress

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	rolloutsapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

var (
	scheme *runtime.Scheme

	demoConf = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configuration.RolloutConfigurationName,
			Namespace: util.GetRolloutNamespace(),
		},
		Data: map[string]string{
			fmt.Sprintf("%s.nginx", configuration.LuaTrafficRoutingIngressTypePrefix): `
				function split(input, delimiter)
					local arr = {}
					string.gsub(input, '[^' .. delimiter ..']+', function(w) table.insert(arr, w) end)
					return arr
				end

				annotations = obj.annotations
				annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-by-header"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = nil
				-- MSE extended annotations
				annotations["mse.ingress.kubernetes.io/canary-by-query"] = nil
				annotations["mse.ingress.kubernetes.io/canary-by-query-pattern"] = nil
				annotations["mse.ingress.kubernetes.io/canary-by-query-value"] = nil
				annotations["nginx.ingress.kubernetes.io/canary-weight"] = nil
				if ( obj.weight ~= "-1" )
				then
					annotations["nginx.ingress.kubernetes.io/canary-weight"] = obj.weight
				end
				if ( annotations["mse.ingress.kubernetes.io/service-subset"] )
				then
					annotations["mse.ingress.kubernetes.io/service-subset"] = "gray"
				end
				if ( obj.requestHeaderModifier )
                then
					local str = ''
					for _,header in ipairs(obj.requestHeaderModifier.set) do
						str = str..string.format("%s %s\n", header.name, header.value)
					end
					annotations["mse.ingress.kubernetes.io/request-header-control-update"] = str
       			end
				if ( not obj.matches )
				then
					return annotations
				end
				for _,match in ipairs(obj.matches) do
					if match.headers and next(match.headers) ~= nil then
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
					if match.queryParams and next(match.queryParams) ~= nil then
						queryParam = match.queryParams[1]
						annotations["nginx.ingress.kubernetes.io/canary-by-query"] = queryParam.name
						if ( queryParam.type == "RegularExpression" )
						then
							annotations["nginx.ingress.kubernetes.io/canary-by-query-pattern"] = queryParam.value
						else
							annotations["nginx.ingress.kubernetes.io/canary-by-query-value"] = queryParam.value
						end
					end
				end
				return annotations
 			`,
			fmt.Sprintf("%s.aliyun-alb", configuration.LuaTrafficRoutingIngressTypePrefix): `
				function split(input, delimiter)
					local arr = {}
					string.gsub(input, '[^' .. delimiter ..']+', function(w) table.insert(arr, w) end)
					return arr
				end
				annotations = obj.annotations
				annotations["alb.ingress.kubernetes.io/canary"] = "true"
				annotations["alb.ingress.kubernetes.io/canary-by-cookie"] = nil
				annotations["alb.ingress.kubernetes.io/canary-by-header"] = nil
				annotations["alb.ingress.kubernetes.io/canary-by-header-pattern"] = nil
				annotations["alb.ingress.kubernetes.io/canary-by-header-value"] = nil
				annotations["alb.ingress.kubernetes.io/canary-weight"] = nil
				conditionKey = string.format("alb.ingress.kubernetes.io/conditions.%s", obj.canaryService)
				annotations[conditionKey] = nil
				if ( obj.weight ~= "-1" )
				then
					annotations["alb.ingress.kubernetes.io/canary-weight"] = obj.weight
				end
				if ( not obj.matches )
				then
					return annotations
				end
				if ( annotations["alb.ingress.kubernetes.io/backend-svcs-protocols"] )
				then
					protocolobj = json.decode(annotations["alb.ingress.kubernetes.io/backend-svcs-protocols"])
					newprotocolobj = {}
					for _, v in pairs(protocolobj) do
						newprotocolobj[obj.canaryService] = v
					end
					annotations["alb.ingress.kubernetes.io/backend-svcs-protocols"] = json.encode(newprotocolobj)
				end

				conditions = {}
				match = obj.matches[1]
				for _,header in ipairs(match.headers) do
					condition = {}
					if ( header.name == "Cookie" )
					then
						condition.type = "Cookie"
						condition.cookieConfig = {} 
						cookies = split(header.value, ";")
						values = {}
						for _,cookieStr in ipairs(cookies) do
							cookie = split(cookieStr, "=")
							value = {}
							value.key = cookie[1]
							value.value = cookie[2]
							table.insert(values, value)
						end
						condition.cookieConfig.values = values
					elseif ( header.name == "SourceIp" )
					then
						condition.type = "SourceIp"
						condition.sourceIpConfig = {} 
						ips = split(header.value, ";")
						values = {}
						for _,ip in ipairs(ips) do
							table.insert(values, ip)
						end
						condition.sourceIpConfig.values = values
					else
						condition.type = "Header"
						condition.headerConfig = {} 
						condition.headerConfig.key = header.name
						vals = split(header.value, ";")
						values = {}
						for _,val in ipairs(vals) do
							table.insert(values, val)
						end
						condition.headerConfig.values = values
					end
					table.insert(conditions, condition)
				end
				annotations[conditionKey] = json.encode(conditions)
				return annotations
 			`,
		},
	}

	demoIngress = netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver",
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "nginx",
			},
		},
		Spec: netv1.IngressSpec{
			IngressClassName: utilpointer.String("nginx"),
			TLS: []netv1.IngressTLS{
				{
					Hosts:      []string{"echoserver.example.com"},
					SecretName: "echoserver-name",
				},
				{
					Hosts:      []string{"log.example.com"},
					SecretName: "log-name",
				},
			},
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
												Number: 80,
											},
										},
									},
								},
								{
									Path: "/apis/other",
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: "other",
											Port: netv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Host: "log.example.com",
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path: "/apis/logs",
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: "echoserver",
											Port: netv1.ServiceBackendPort{
												Number: 8899,
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
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rolloutsapi.AddToScheme(scheme)
}

func TestInitialize(t *testing.T) {
	cases := []struct {
		name         string
		getConfigmap func() *corev1.ConfigMap
		getIngress   func() []*netv1.Ingress
	}{
		{
			name: "init test1",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				return []*netv1.Ingress{demoIngress.DeepCopy()}
			},
		},
	}

	config := Config{
		Key:           "rollout-demo",
		StableService: "echoserver",
		CanaryService: "echoserver-canary",
		TrafficConf: &v1beta1.IngressTrafficRouting{
			Name: "echoserver",
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
			_ = fakeCli.Create(context.TODO(), cs.getConfigmap())
			for _, ingress := range cs.getIngress() {
				_ = fakeCli.Create(context.TODO(), ingress)
			}
			controller, err := NewIngressTrafficRouting(fakeCli, config)
			if err != nil {
				t.Fatalf("NewIngressTrafficRouting failed: %s", err.Error())
				return
			}
			err = controller.Initialize(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
				return
			}
		})
	}
}

func TestEnsureRoutes(t *testing.T) {
	cases := []struct {
		name          string
		getConfigmap  func() *corev1.ConfigMap
		getIngress    func() []*netv1.Ingress
		getRoutes     func() *v1beta1.CanaryStep
		expectIngress func() *netv1.Ingress
		ingressType   string
	}{
		{
			name: "ensure routes test1",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-weight"] = "0"
				canary.Annotations["mse.ingress.kubernetes.io/service-subset"] = ""
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			getRoutes: func() *v1beta1.CanaryStep {
				return &v1beta1.CanaryStep{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: nil,
						Matches: []v1beta1.HttpRouteMatch{
							// header
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "user_id",
										Value: "123456",
									},
								},
							},
							// cookies
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "canary-by-cookie",
										Value: "demo",
									},
								},
							},
						},
						RequestHeaderModifier: &gatewayv1beta1.HTTPHeaderFilter{
							Set: []gatewayv1beta1.HTTPHeader{
								{
									Name:  "gray",
									Value: "blue",
								},
								{
									Name:  "gray",
									Value: "green",
								},
							},
						},
					},
				}
			},
			expectIngress: func() *netv1.Ingress {
				expect := demoIngress.DeepCopy()
				expect.Name = "echoserver-canary"
				expect.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = "demo"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = "123456"
				expect.Annotations["mse.ingress.kubernetes.io/request-header-control-update"] = "gray blue\ngray green\n"
				expect.Annotations["mse.ingress.kubernetes.io/service-subset"] = "gray"
				expect.Spec.Rules[0].HTTP.Paths = expect.Spec.Rules[0].HTTP.Paths[:1]
				expect.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				expect.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return expect
			},
		},
		{
			name: "ensure routes test2",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = "demo"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = "123456"
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			getRoutes: func() *v1beta1.CanaryStep {
				return &v1beta1.CanaryStep{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.String("40%"),
					},
				}
			},
			expectIngress: func() *netv1.Ingress {
				expect := demoIngress.DeepCopy()
				expect.Name = "echoserver-canary"
				expect.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-weight"] = "40"
				expect.Spec.Rules[0].HTTP.Paths = expect.Spec.Rules[0].HTTP.Paths[:1]
				expect.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				expect.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return expect
			},
		},
		{
			name: "ensure routes test3",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = "demo"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = "123456"
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			getRoutes: func() *v1beta1.CanaryStep {
				iType := gatewayv1beta1.HeaderMatchRegularExpression
				return &v1beta1.CanaryStep{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Matches: []v1beta1.HttpRouteMatch{
							// header
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "user_id",
										Value: "123*",
										Type:  &iType,
									},
								},
							},
						},
					},
				}
			},
			expectIngress: func() *netv1.Ingress {
				expect := demoIngress.DeepCopy()
				expect.Name = "echoserver-canary"
				expect.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-header"] = "user_id"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = "123*"
				expect.Spec.Rules[0].HTTP.Paths = expect.Spec.Rules[0].HTTP.Paths[:1]
				expect.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				expect.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return expect
			},
		},
		{
			name:        "ensure routes test4",
			ingressType: "aliyun-alb",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["alb.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["alb.ingress.kubernetes.io/canary-weight"] = "0"
				canary.Annotations["alb.ingress.kubernetes.io/backend-svcs-protocols"] = `{"echoserver":"http"}`
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			getRoutes: func() *v1beta1.CanaryStep {
				return &v1beta1.CanaryStep{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Matches: []v1beta1.HttpRouteMatch{
							// header
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "Cookie",
										Value: "demo1=value1;demo2=value2",
									},
									{
										Name:  "SourceIp",
										Value: "192.168.0.0/16;172.16.0.0/16",
									},
									{
										Name:  "headername",
										Value: "headervalue1;headervalue2",
									},
								},
							},
						},
					},
				}
			},
			expectIngress: func() *netv1.Ingress {
				expect := demoIngress.DeepCopy()
				expect.Name = "echoserver-canary"
				expect.Annotations["alb.ingress.kubernetes.io/canary"] = "true"
				expect.Annotations["alb.ingress.kubernetes.io/backend-svcs-protocols"] = `{"echoserver-canary":"http"}`
				expect.Annotations["alb.ingress.kubernetes.io/conditions.echoserver-canary"] = `[{"cookieConfig":{"values":[{"key":"demo1","value":"value1"},{"key":"demo2","value":"value2"}]},"type":"Cookie"},{"sourceIpConfig":{"values":["192.168.0.0/16","172.16.0.0/16"]},"type":"SourceIp"},{"headerConfig":{"key":"headername","values":["headervalue1","headervalue2"]},"type":"Header"}]`
				expect.Spec.Rules[0].HTTP.Paths = expect.Spec.Rules[0].HTTP.Paths[:1]
				expect.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				expect.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return expect
			},
		},
		{
			name: "ensure routes test5",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-weight"] = "0"
				canary.Annotations["mse.ingress.kubernetes.io/service-subset"] = ""
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			getRoutes: func() *v1beta1.CanaryStep {
				return &v1beta1.CanaryStep{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: nil,
						Matches: []v1beta1.HttpRouteMatch{
							// querystring
							{
								QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
									{
										Name:  "user_id",
										Value: "123456",
									},
								},
							},
						},
						RequestHeaderModifier: &gatewayv1beta1.HTTPHeaderFilter{
							Set: []gatewayv1beta1.HTTPHeader{
								{
									Name:  "gray",
									Value: "blue",
								},
								{
									Name:  "gray",
									Value: "green",
								},
							},
						},
					},
				}
			},
			expectIngress: func() *netv1.Ingress {
				expect := demoIngress.DeepCopy()
				expect.Name = "echoserver-canary"
				expect.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-query"] = "user_id"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-query-value"] = "123456"
				expect.Annotations["mse.ingress.kubernetes.io/request-header-control-update"] = "gray blue\ngray green\n"
				expect.Annotations["mse.ingress.kubernetes.io/service-subset"] = "gray"
				expect.Spec.Rules[0].HTTP.Paths = expect.Spec.Rules[0].HTTP.Paths[:1]
				expect.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				expect.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return expect
			},
		},
		{
			name: "ensure routes test5",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-weight"] = "0"
				canary.Annotations["mse.ingress.kubernetes.io/service-subset"] = ""
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			getRoutes: func() *v1beta1.CanaryStep {
				iType := gatewayv1beta1.QueryParamMatchRegularExpression
				return &v1beta1.CanaryStep{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: nil,
						Matches: []v1beta1.HttpRouteMatch{
							// querystring
							{
								QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
									{
										Name:  "user_id",
										Value: "123*",
										Type:  &iType,
									},
								},
							},
						},
						RequestHeaderModifier: &gatewayv1beta1.HTTPHeaderFilter{
							Set: []gatewayv1beta1.HTTPHeader{
								{
									Name:  "gray",
									Value: "blue",
								},
								{
									Name:  "gray",
									Value: "green",
								},
							},
						},
					},
				}
			},
			expectIngress: func() *netv1.Ingress {
				expect := demoIngress.DeepCopy()
				expect.Name = "echoserver-canary"
				expect.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-query"] = "user_id"
				expect.Annotations["nginx.ingress.kubernetes.io/canary-by-query-pattern"] = "123*"
				expect.Annotations["mse.ingress.kubernetes.io/request-header-control-update"] = "gray blue\ngray green\n"
				expect.Annotations["mse.ingress.kubernetes.io/service-subset"] = "gray"
				expect.Spec.Rules[0].HTTP.Paths = expect.Spec.Rules[0].HTTP.Paths[:1]
				expect.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				expect.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return expect
			},
		},
	}

	config := Config{
		Key:           "rollout-demo",
		StableService: "echoserver",
		CanaryService: "echoserver-canary",
		TrafficConf: &v1beta1.IngressTrafficRouting{
			Name: "echoserver",
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
			_ = fakeCli.Create(context.TODO(), cs.getConfigmap())
			for _, ingress := range cs.getIngress() {
				_ = fakeCli.Create(context.TODO(), ingress)
			}
			config.TrafficConf.ClassType = cs.ingressType
			controller, err := NewIngressTrafficRouting(fakeCli, config)
			if err != nil {
				t.Fatalf("NewIngressTrafficRouting failed: %s", err.Error())
				return
			}
			step := cs.getRoutes()
			_, err = controller.EnsureRoutes(context.TODO(), &step.TrafficRoutingStrategy)
			if err != nil {
				t.Fatalf("EnsureRoutes failed: %s", err.Error())
				return
			}
			canaryIngress := &netv1.Ingress{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver-canary"}, canaryIngress)
			if err != nil {
				t.Fatalf("Get canary ingress failed: %s", err.Error())
				return
			}
			expect := cs.expectIngress()
			if !reflect.DeepEqual(canaryIngress.Annotations, expect.Annotations) ||
				!reflect.DeepEqual(canaryIngress.Spec, expect.Spec) {
				t.Fatalf("but get(%s)", util.DumpJSON(canaryIngress))
			}
		})
	}
}

func TestFinalise(t *testing.T) {
	cases := []struct {
		name          string
		getConfigmap  func() *corev1.ConfigMap
		getIngress    func() []*netv1.Ingress
		expectIngress func() *netv1.Ingress
		modified      bool
	}{
		{
			name: "finalise test1",
			getConfigmap: func() *corev1.ConfigMap {
				return demoConf.DeepCopy()
			},
			getIngress: func() []*netv1.Ingress {
				canary := demoIngress.DeepCopy()
				canary.Name = "echoserver-canary"
				canary.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
				canary.Annotations["nginx.ingress.kubernetes.io/canary-weight"] = "0"
				canary.Spec.Rules[0].HTTP.Paths = canary.Spec.Rules[0].HTTP.Paths[:1]
				canary.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				canary.Spec.Rules[1].HTTP.Paths[0].Backend.Service.Name = "echoserver-canary"
				return []*netv1.Ingress{demoIngress.DeepCopy(), canary}
			},
			expectIngress: func() *netv1.Ingress {
				return nil
			},
			modified: true,
		},
	}

	config := Config{
		Key:           "rollout-demo",
		StableService: "echoserver",
		CanaryService: "echoserver-canary",
		TrafficConf: &v1beta1.IngressTrafficRouting{
			Name: "echoserver",
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
			_ = fakeCli.Create(context.TODO(), cs.getConfigmap())
			for _, ingress := range cs.getIngress() {
				_ = fakeCli.Create(context.TODO(), ingress)
			}
			controller, err := NewIngressTrafficRouting(fakeCli, config)
			if err != nil {
				t.Fatalf("NewIngressTrafficRouting failed: %s", err.Error())
				return
			}
			modified, err := controller.Finalise(context.TODO())
			if err != nil {
				t.Fatalf("EnsureRoutes failed: %s", err.Error())
				return
			}
			if modified != cs.modified {
				t.Fatalf("expect(%v), but get(%v)", cs.modified, modified)
			}
			canaryIngress := &netv1.Ingress{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver-canary"}, canaryIngress)
			if err != nil {
				if cs.expectIngress() == nil && errors.IsNotFound(err) {
					return
				}
				t.Fatalf("Get canary ingress failed: %s", err.Error())
				return
			}
			expect := cs.expectIngress()
			if !reflect.DeepEqual(canaryIngress.Annotations, expect.Annotations) ||
				!reflect.DeepEqual(canaryIngress.Spec, expect.Spec) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect), util.DumpJSON(canaryIngress))
			}
		})
	}
}
