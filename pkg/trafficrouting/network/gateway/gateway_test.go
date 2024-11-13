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

package gateway

import (
	"reflect"
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	utilpointer "k8s.io/utils/pointer"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

var (
	kindSvc = gatewayv1beta1.Kind("Service")
	portNum = gatewayv1beta1.PortNumber(8080)

	routeDemo = gatewayv1beta1.HTTPRoute{
		Spec: gatewayv1beta1.HTTPRouteSpec{
			Rules: []gatewayv1beta1.HTTPRouteRule{
				{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/web"),
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "web-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
				{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
				{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/list"),
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "list-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
				{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
			},
		},
	}
)

func TestBuildDesiredHTTPRoute(t *testing.T) {
	cases := []struct {
		name          string
		getRouteRules func() []gatewayv1beta1.HTTPRouteRule
		getRoutes     func() (*int32, []v1beta1.HttpRouteMatch)
		desiredRules  func() []gatewayv1beta1.HTTPRouteRule
	}{
		{
			name: "test headers",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				iType := gatewayv1beta1.HeaderMatchRegularExpression
				return nil, []v1beta1.HttpRouteMatch{
					// header
					{
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
					}, {
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{
								Name:  "user_id",
								Value: "234*",
								Type:  &iType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
					},
				}
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				iType := gatewayv1beta1.HeaderMatchRegularExpression
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
								{
									Name:  "user_id",
									Value: "234*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "234*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "234*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				return rules
			},
		},
		{
			name: "test query params",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				iType := gatewayv1beta1.QueryParamMatchRegularExpression
				return nil, []v1beta1.HttpRouteMatch{
					// queryparams
					{
						QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
					}, {
						QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
							{
								Name:  "user_id",
								Value: "234*",
								Type:  &iType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
					},
				}
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				iType := gatewayv1beta1.QueryParamMatchRegularExpression
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "234*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "234*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "234*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				return rules
			},
		},
		{
			name: "test query params and headers",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				iQueryParamType := gatewayv1beta1.QueryParamMatchRegularExpression
				iHeaderType := gatewayv1beta1.HeaderMatchRegularExpression
				return nil, []v1beta1.HttpRouteMatch{
					// queryParams + headers
					{
						QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iQueryParamType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iHeaderType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
					},
				}
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				iQueryParamType := gatewayv1beta1.QueryParamMatchRegularExpression
				iHeaderType := gatewayv1beta1.HeaderMatchRegularExpression
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iHeaderType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iQueryParamType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iHeaderType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iQueryParamType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iHeaderType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iQueryParamType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				return rules
			},
		},
		{
			name: "test path replace",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				iQueryParamType := gatewayv1beta1.QueryParamMatchRegularExpression
				iHeaderType := gatewayv1beta1.HeaderMatchRegularExpression
				return nil, []v1beta1.HttpRouteMatch{
					// queryParams + headers + path
					{
						QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iQueryParamType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iHeaderType,
							},
							{
								Name:  "canary",
								Value: "true",
							},
						},
						Path: &gatewayv1beta1.HTTPPathMatch{
							Value: utilpointer.String("/storage/v2"),
						},
					},
				}
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				iQueryParamType := gatewayv1beta1.QueryParamMatchRegularExpression
				iHeaderType := gatewayv1beta1.HeaderMatchRegularExpression
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage/v2"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iHeaderType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iQueryParamType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				return rules
			},
		},
		{
			name: "canary weight: 20",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(20),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(20),
						},
					},
				}
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				return utilpointer.Int32(20), nil
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(20),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(20),
						},
					},
				}
				return rules
			},
		},
		{
			name: "canary weight: -1",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(100),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(0),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(100),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(0),
						},
					},
				}
				iType := gatewayv1beta1.HeaderMatchRegularExpression
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123*",
									Type:  &iType,
								},
								{
									Name:  "canary",
									Value: "true",
								},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				return utilpointer.Int32(-1), nil
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[3].BackendRefs[0].Weight = utilpointer.Int32(1)
				rules[1].BackendRefs[0].Weight = utilpointer.Int32(1)
				return rules
			},
		},
	}

	conf := Config{
		Key:           "rollout-demo",
		CanaryService: "store-svc-canary",
		StableService: "store-svc",
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			controller := &gatewayController{conf: conf}
			weight, matches := cs.getRoutes()
			current := controller.buildDesiredHTTPRoute(cs.getRouteRules(), weight, matches)
			desired := cs.desiredRules()
			if !reflect.DeepEqual(current, desired) {
				t.Fatalf("expect: %v, but get %v", util.DumpJSON(desired), util.DumpJSON(current))
			}
		})
	}
}
