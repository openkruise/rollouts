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

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	utilpointer "k8s.io/utils/pointer"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var (
	kindSvc = gatewayv1alpha2.Kind("Service")
	portNum = gatewayv1alpha2.PortNumber(8080)

	routeDemo = gatewayv1alpha2.HTTPRoute{
		Spec: gatewayv1alpha2.HTTPRouteSpec{
			Rules: []gatewayv1alpha2.HTTPRouteRule{
				{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/web"),
							},
						},
					},
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
									Kind: &kindSvc,
									Name: "web-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
				{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1alpha2.HTTPHeaderMatch{
								{
									Name:  "version",
									Value: "v2",
								},
							},
						},
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
						},
					},
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
				{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/list"),
							},
						},
					},
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
									Kind: &kindSvc,
									Name: "list-svc",
									Port: &portNum,
								},
							},
						},
					},
				},
				{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
						},
					},
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
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
		getRouteRules func() []gatewayv1alpha2.HTTPRouteRule
		getRoutes     func() (*int32, []rolloutsv1alpha1.HttpRouteMatch)
		desiredRules  func() []gatewayv1alpha2.HTTPRouteRule
	}{
		{
			name: "test1 headers",
			getRouteRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				return rules
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				iType := gatewayv1alpha2.HeaderMatchRegularExpression
				return nil, []rolloutsv1alpha1.HttpRouteMatch{
					// header
					{
						Headers: []gatewayv1alpha2.HTTPHeaderMatch{
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
				}
			},
			desiredRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				iType := gatewayv1alpha2.HeaderMatchRegularExpression
				rules = append(rules, gatewayv1alpha2.HTTPRouteRule{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/store"),
							},
							Headers: []gatewayv1alpha2.HTTPHeaderMatch{
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
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/v2/store"),
							},
							Headers: []gatewayv1alpha2.HTTPHeaderMatch{
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
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
									Kind: &kindSvc,
									Name: "store-svc-canary",
									Port: &portNum,
								},
							},
						},
					},
				})
				rules = append(rules, gatewayv1alpha2.HTTPRouteRule{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1alpha2.HTTPHeaderMatch{
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
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
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
			getRouteRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1alpha2.HTTPBackendRef{
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(20),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1alpha2.HTTPBackendRef{
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
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
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(20), nil
			},
			desiredRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1alpha2.HTTPBackendRef{
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(20),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1alpha2.HTTPBackendRef{
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(80),
						},
					},
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
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
			getRouteRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[1].BackendRefs = []gatewayv1alpha2.HTTPBackendRef{
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(100),
						},
					},
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(0),
						},
					},
				}
				rules[3].BackendRefs = []gatewayv1alpha2.HTTPBackendRef{
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(100),
						},
					},
					{
						BackendRef: gatewayv1alpha2.BackendRef{
							BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
								Kind: &kindSvc,
								Name: "store-svc-canary",
								Port: &portNum,
							},
							Weight: utilpointer.Int32(0),
						},
					},
				}
				iType := gatewayv1alpha2.HeaderMatchRegularExpression
				rules = append(rules, gatewayv1alpha2.HTTPRouteRule{
					Matches: []gatewayv1alpha2.HTTPRouteMatch{
						{
							Path: &gatewayv1alpha2.HTTPPathMatch{
								Value: utilpointer.String("/storage"),
							},
							Headers: []gatewayv1alpha2.HTTPHeaderMatch{
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
					BackendRefs: []gatewayv1alpha2.HTTPBackendRef{
						{
							BackendRef: gatewayv1alpha2.BackendRef{
								BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
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
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(-1), nil
			},
			desiredRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				rules[3].BackendRefs[0].Weight = utilpointer.Int32(1)
				rules[1].BackendRefs[0].Weight = utilpointer.Int32(1)
				return rules
			},
		},
	}

	conf := Config{
		RolloutName:   "rollout-demo",
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
