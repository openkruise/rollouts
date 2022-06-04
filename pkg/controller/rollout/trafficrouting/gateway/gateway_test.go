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
	"testing"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func TestBuildCanaryHTTPRoute(t *testing.T) {
	var c = gatewayController{
		conf: Config{
			TrafficConf: &rolloutv1alpha1.GatewayTrafficRouting{
				HTTPRouteName: "",
			},
			StableService: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			CanaryService: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-canary",
				},
			},
		},
	}
	var service = v1alpha2.Kind("Service")
	var defaultWeight int32 = 1
	route := &v1alpha2.HTTPRoute{
		Spec: v1alpha2.HTTPRouteSpec{
			Rules: []v1alpha2.HTTPRouteRule{
				{
					BackendRefs: []v1alpha2.HTTPBackendRef{
						{
							BackendRef: v1alpha2.BackendRef{
								BackendObjectReference: v1alpha2.BackendObjectReference{
									Kind: &service,
									Name: "test",
								},
								Weight: &defaultWeight,
							},
						},
					},
				},
			},
		},
	}
	c.buildCanaryHTTPRoute(route, 20)
	if c.getHTTPRouteCanaryWeight(*route) != 20 {
		t.Fatal("build canary failure")
	}

	c.buildCanaryHTTPRoute(route, -1)
	if c.getHTTPRouteCanaryWeight(*route) != -1 {
		t.Fatal("build canary failure for the delete case")
	}

	c.buildCanaryHTTPRoute(route, 101)
	if c.getHTTPRouteCanaryWeight(*route) != 100 {
		t.Fatal("build canary failure the greater than 100 case")
	}
}
