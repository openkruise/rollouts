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
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
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
		// Bug #17/#19 regression: BackendRef without an explicit Kind must be
		// treated as a Service per the Gateway API spec (Kind defaults to
		// "Service" when nil). Some controllers (e.g. Envoy Gateway) emit
		// HTTPRoute objects with Kind unset.
		{
			name: "kind nil: weight canary",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				// Drop Kind from rules[1] (store-svc) and rules[3] (store-svc).
				rules[1].BackendRefs[0].Kind = nil
				rules[3].BackendRefs[0].Kind = nil
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				return utilpointer.Int32(20), nil
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				// Stable refs keep their original (nil) Kind; canary refs
				// inherit it because they are derived via DeepCopy from the
				// stable ref then renamed.
				rules[1].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
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
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
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
			name: "kind nil: header canary",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs[0].Kind = nil
				rules[3].BackendRefs[0].Kind = nil
				return rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				iType := gatewayv1beta1.HeaderMatchRegularExpression
				return nil, []v1beta1.HttpRouteMatch{
					{
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{
								Name:  "user_id",
								Value: "123*",
								Type:  &iType,
							},
						},
					},
				}
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs[0].Kind = nil
				rules[3].BackendRefs[0].Kind = nil
				iType := gatewayv1beta1.HeaderMatchRegularExpression
				// Canary rule for /store + /v2/store (rules[1]).
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "version", Value: "v2"},
								{Name: "user_id", Value: "123*", Type: &iType},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "user_id", Value: "123*", Type: &iType},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				// Canary rule for /storage (rules[3]).
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "user_id", Value: "123*", Type: &iType},
							},
						},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
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
			name: "kind nil: finalise restores stable",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				// Mid-rollout shape: weight has been split to 80/20, stable
				// ref has nil Kind, canary ref also has nil Kind.
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
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
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
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
				return utilpointer.Int32(-1), nil
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				// Canary refs gone; stable refs restored to weight=1, with
				// Kind still nil.
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(1),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1beta1.HTTPBackendRef{
					{
						BackendRef: gatewayv1beta1.BackendRef{
							BackendObjectReference: gatewayv1beta1.BackendObjectReference{
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(1),
						},
					},
				}
				return rules
			},
		},
		// Bug #13 regression: nonPathMatches must be indexed by their own
		// position, not the position they had in the original matches slice.
		// Place a path match first and a non-path match second so the indices
		// diverge: pathMatches=[matches[0]], nonPathMatches=[matches[1]].
		{
			name: "matches mixed: path first, non-path second",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				return routeDemo.DeepCopy().Spec.Rules
			},
			getRoutes: func() (*int32, []v1beta1.HttpRouteMatch) {
				iHeaderType := gatewayv1beta1.HeaderMatchRegularExpression
				return nil, []v1beta1.HttpRouteMatch{
					{
						// Path match — consumed once, attached to the first
						// stable rule (/store + /v2/store).
						Path: &gatewayv1beta1.HTTPPathMatch{
							Value: utilpointer.String("/storage/v2"),
						},
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{Name: "from-path-match", Value: "PATH"},
						},
					},
					{
						// Non-path match — must contribute to every stable
						// rule. Its Headers must NOT be confused with the
						// path match's Headers above.
						Headers: []gatewayv1beta1.HTTPHeaderMatch{
							{Name: "from-nonpath-match", Value: "NONPATH", Type: &iHeaderType},
						},
					},
				}
			},
			desiredRules: func() []gatewayv1beta1.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				iHeaderType := gatewayv1beta1.HeaderMatchRegularExpression
				// First stable rule (rules[1] for /store+/v2/store) gets:
				//   * the standalone path match (single entry, from pathMatches)
				//   * each existing match × each nonPathMatch
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage/v2"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "from-path-match", Value: "PATH"},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "version", Value: "v2"},
								{Name: "from-nonpath-match", Value: "NONPATH", Type: &iHeaderType},
							},
						},
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "from-nonpath-match", Value: "NONPATH", Type: &iHeaderType},
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
				// Second stable rule (rules[3] for /storage) gets only the
				// nonPath match (pathMatches were consumed by the first rule).
				rules = append(rules, gatewayv1beta1.HTTPRouteRule{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{Name: "from-nonpath-match", Value: "NONPATH", Type: &iHeaderType},
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
		// Test #23: from the original routeDemo (no canary injected yet) to
		// canary@20. The existing "canary weight: 20" case feeds in the final
		// 80/20 shape and only proves idempotence; this case proves the
		// transformation from clean state.
		{
			name: "canary weight: 20 from clean state",
			getRouteRules: func() []gatewayv1beta1.HTTPRouteRule {
				return routeDemo.DeepCopy().Spec.Rules
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

// newGatewayTestScheme registers the types needed by the fake client for the
// end-to-end Initialize/EnsureRoutes/Finalise tests.
func newGatewayTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := gatewayv1beta1.AddToScheme(s); err != nil {
		t.Fatalf("add gatewayv1beta1 to scheme: %v", err)
	}
	return s
}

func newTestHTTPRoute(name, namespace, stableSvc string) *gatewayv1beta1.HTTPRoute {
	port := gatewayv1beta1.PortNumber(8080)
	kind := gatewayv1beta1.Kind("Service")
	return &gatewayv1beta1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: gatewayv1beta1.HTTPRouteSpec{
			Rules: []gatewayv1beta1.HTTPRouteRule{
				{
					Matches: []gatewayv1beta1.HTTPRouteMatch{
						{Path: &gatewayv1beta1.HTTPPathMatch{Value: utilpointer.String("/api")}},
					},
					BackendRefs: []gatewayv1beta1.HTTPBackendRef{
						{
							BackendRef: gatewayv1beta1.BackendRef{
								BackendObjectReference: gatewayv1beta1.BackendObjectReference{
									Kind: &kind,
									Name: gatewayv1beta1.ObjectName(stableSvc),
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	}
}

// TestInitialize covers Initialize against a real fake client: the route must
// exist, otherwise an error propagates up.
func TestInitialize(t *testing.T) {
	const ns = "default"
	const routeName = "demo-route"
	scheme := newGatewayTestScheme(t)

	t.Run("route exists", func(t *testing.T) {
		fakeCli := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(newTestHTTPRoute(routeName, ns, "store-svc")).Build()
		ctrl, err := NewGatewayTrafficRouting(fakeCli, Config{
			Key:           "rollout/demo",
			Namespace:     ns,
			CanaryService: "store-svc-canary",
			StableService: "store-svc",
			TrafficConf:   &v1beta1.GatewayTrafficRouting{HTTPRouteName: utilpointer.String(routeName)},
		})
		if err != nil {
			t.Fatalf("NewGatewayTrafficRouting: %v", err)
		}
		if err := ctrl.Initialize(context.TODO()); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
	})

	t.Run("route missing returns error", func(t *testing.T) {
		fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
		ctrl, err := NewGatewayTrafficRouting(fakeCli, Config{
			Key:           "rollout/demo",
			Namespace:     ns,
			CanaryService: "store-svc-canary",
			StableService: "store-svc",
			TrafficConf:   &v1beta1.GatewayTrafficRouting{HTTPRouteName: utilpointer.String(routeName)},
		})
		if err != nil {
			t.Fatalf("NewGatewayTrafficRouting: %v", err)
		}
		if err := ctrl.Initialize(context.TODO()); err == nil {
			t.Fatalf("expected Initialize to fail with NotFound")
		}
	})
}

// TestEnsureRoutesAndFinalise drives a weight-based canary through
// EnsureRoutes and then rolls it back via Finalise on the same fake client,
// verifying the persisted HTTPRoute at each step.
func TestEnsureRoutesAndFinalise(t *testing.T) {
	const ns = "default"
	const routeName = "demo-route"
	scheme := newGatewayTestScheme(t)
	original := newTestHTTPRoute(routeName, ns, "store-svc")
	fakeCli := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(original.DeepCopy()).Build()
	ctrl, err := NewGatewayTrafficRouting(fakeCli, Config{
		Key:           "rollout/demo",
		Namespace:     ns,
		CanaryService: "store-svc-canary",
		StableService: "store-svc",
		TrafficConf:   &v1beta1.GatewayTrafficRouting{HTTPRouteName: utilpointer.String(routeName)},
	})
	if err != nil {
		t.Fatalf("NewGatewayTrafficRouting: %v", err)
	}

	// First EnsureRoutes: should write canary backend with weight 20.
	traffic := "20%"
	strategy := &v1beta1.TrafficRoutingStrategy{Traffic: &traffic}
	done, err := ctrl.EnsureRoutes(context.TODO(), strategy)
	if err != nil {
		t.Fatalf("EnsureRoutes: %v", err)
	}
	if done {
		t.Fatalf("EnsureRoutes returned done=true on first apply, expected false")
	}

	got := &gatewayv1beta1.HTTPRoute{}
	if err := fakeCli.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: routeName}, got); err != nil {
		t.Fatalf("Get after EnsureRoutes: %v", err)
	}
	if len(got.Spec.Rules[0].BackendRefs) != 2 {
		t.Fatalf("expected 2 backend refs after EnsureRoutes, got %d (%s)", len(got.Spec.Rules[0].BackendRefs), util.DumpJSON(got.Spec.Rules))
	}
	if string(got.Spec.Rules[0].BackendRefs[0].Name) != "store-svc" || *got.Spec.Rules[0].BackendRefs[0].Weight != 80 {
		t.Fatalf("expected stable@80, got %s", util.DumpJSON(got.Spec.Rules[0].BackendRefs[0]))
	}
	if string(got.Spec.Rules[0].BackendRefs[1].Name) != "store-svc-canary" || *got.Spec.Rules[0].BackendRefs[1].Weight != 20 {
		t.Fatalf("expected canary@20, got %s", util.DumpJSON(got.Spec.Rules[0].BackendRefs[1]))
	}

	// Second EnsureRoutes with the same input: should be idempotent → done=true, no write.
	done, err = ctrl.EnsureRoutes(context.TODO(), strategy)
	if err != nil {
		t.Fatalf("EnsureRoutes (idempotent): %v", err)
	}
	if !done {
		t.Fatalf("EnsureRoutes idempotent call returned done=false, expected true")
	}

	// Step 2: bump to 40% — this is the e2e step that broke when the
	// in-place update aliased the input slice with the desired slice and
	// caused EnsureRoutes to short-circuit on a stale "already done" check.
	traffic2 := "40%"
	strategy2 := &v1beta1.TrafficRoutingStrategy{Traffic: &traffic2}
	done, err = ctrl.EnsureRoutes(context.TODO(), strategy2)
	if err != nil {
		t.Fatalf("EnsureRoutes (step 2): %v", err)
	}
	if done {
		t.Fatalf("EnsureRoutes returned done=true when bumping 20%%->40%%, expected false")
	}
	if err := fakeCli.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: routeName}, got); err != nil {
		t.Fatalf("Get after step 2: %v", err)
	}
	if len(got.Spec.Rules[0].BackendRefs) != 2 {
		t.Fatalf("expected 2 backend refs after step 2, got %d", len(got.Spec.Rules[0].BackendRefs))
	}
	if w := got.Spec.Rules[0].BackendRefs[0].Weight; w == nil || *w != 60 {
		t.Fatalf("expected stable@60 after step 2, got %v", w)
	}
	if w := got.Spec.Rules[0].BackendRefs[1].Weight; w == nil || *w != 40 {
		t.Fatalf("expected canary@40 after step 2, got %v", w)
	}

	// Finalise: should drop canary ref and reset stable weight to 1.
	modified, err := ctrl.Finalise(context.TODO())
	if err != nil {
		t.Fatalf("Finalise: %v", err)
	}
	if !modified {
		t.Fatalf("Finalise returned modified=false on a route still carrying canary, expected true")
	}
	if err := fakeCli.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: routeName}, got); err != nil {
		t.Fatalf("Get after Finalise: %v", err)
	}
	if len(got.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("expected 1 backend ref after Finalise, got %d", len(got.Spec.Rules[0].BackendRefs))
	}
	if string(got.Spec.Rules[0].BackendRefs[0].Name) != "store-svc" {
		t.Fatalf("expected stable ref to remain, got %s", got.Spec.Rules[0].BackendRefs[0].Name)
	}
	if got.Spec.Rules[0].BackendRefs[0].Weight == nil || *got.Spec.Rules[0].BackendRefs[0].Weight != 1 {
		t.Fatalf("expected stable weight=1 after finalise, got %v", got.Spec.Rules[0].BackendRefs[0].Weight)
	}

	// Second Finalise: route already clean, should report no further work.
	modified, err = ctrl.Finalise(context.TODO())
	if err != nil {
		t.Fatalf("Finalise (idempotent): %v", err)
	}
	if modified {
		t.Fatalf("Finalise idempotent call returned modified=true, expected false")
	}
}

// TestEnsureRoutesInvalidTraffic verifies that a non-numeric traffic string
// surfaces as an error rather than being silently treated as 0%.
func TestEnsureRoutesInvalidTraffic(t *testing.T) {
	const ns = "default"
	const routeName = "demo-route"
	scheme := newGatewayTestScheme(t)
	fakeCli := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(newTestHTTPRoute(routeName, ns, "store-svc")).Build()
	ctrl, err := NewGatewayTrafficRouting(fakeCli, Config{
		Key:           "rollout/demo",
		Namespace:     ns,
		CanaryService: "store-svc-canary",
		StableService: "store-svc",
		TrafficConf:   &v1beta1.GatewayTrafficRouting{HTTPRouteName: utilpointer.String(routeName)},
	})
	if err != nil {
		t.Fatalf("NewGatewayTrafficRouting: %v", err)
	}
	bogus := "not-a-percent"
	if _, err := ctrl.EnsureRoutes(context.TODO(), &v1beta1.TrafficRoutingStrategy{Traffic: &bogus}); err == nil {
		t.Fatalf("expected EnsureRoutes to reject invalid traffic %q, got nil error", bogus)
	}
}
