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
								Value: utilpointer.String("/storev2"),
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
		canaryPercent int32
		desiredRules  func() []gatewayv1alpha2.HTTPRouteRule
	}{
		{
			name: "canary weight: 20",
			getRouteRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
				return rules
			},
			canaryPercent: 20,
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
			name: "canary weight: 0",
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
			canaryPercent: 0,
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
				return rules
			},
			canaryPercent: -1,
			desiredRules: func() []gatewayv1alpha2.HTTPRouteRule {
				rules := routeDemo.DeepCopy().Spec.Rules
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
			desired := controller.buildDesiredHTTPRoute(cs.getRouteRules(), cs.canaryPercent)
			if !reflect.DeepEqual(desired, cs.desiredRules()) {
				t.Fatalf("expect: %v, but get %v", cs.desiredRules(), desired)
			}
		})
	}
}
