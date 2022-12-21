/*
Copyright 2022 The Kruise Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file expect in compliance with the License.
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
	"reflect"
	"testing"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	a6v2 "github.com/openkruise/rollouts/pkg/apis/apisix/v2"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	config = Config{
		RolloutName:   "rollout-demo",
		StableService: "echoserver",
		CanaryService: "echoserver-canary",
		TrafficConf: &rolloutsv1alpha1.IngressTrafficRouting{
			Name:      "echoserver",
			ClassType: "apisix",
		},
	}
	demoApisixRoute = a6v2.ApisixRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver",
		},
		Spec: a6v2.ApisixRouteSpec{
			HTTP: []a6v2.ApisixRouteHTTP{
				{
					Name: "echo",
					Match: a6v2.ApisixRouteHTTPMatch{
						Paths: []string{
							"/apis/echo",
						},
						Hosts: []string{
							"echoserver.example.com",
						},
					},
					Backends: []a6v2.ApisixRouteHTTPBackend{
						{
							ServiceName: "echoserver",
							ServicePort: intstr.IntOrString{Type: 0, IntVal: 80},
						},
					},
				}, {
					Name: "other",
					Match: a6v2.ApisixRouteHTTPMatch{
						Paths: []string{
							"/apis/other",
						},
						Hosts: []string{
							"echoserver.example.com",
						},
					},
					Backends: []a6v2.ApisixRouteHTTPBackend{
						{
							ServiceName: "other",
							ServicePort: intstr.IntOrString{Type: 0, IntVal: 80},
						},
					},
				}, {
					Name: "log",
					Match: a6v2.ApisixRouteHTTPMatch{
						Paths: []string{
							"/apis/logs",
						},
						Hosts: []string{
							"log.example.com",
						},
					},
					Backends: []a6v2.ApisixRouteHTTPBackend{
						{
							ServiceName: "echoserver",
							ServicePort: intstr.IntOrString{Type: 0, IntVal: 8899},
						},
					},
				},
			},
		},
	}
	demoApisixUpstream = a6v2.ApisixUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver",
		},
		Spec: &a6v2.ApisixUpstreamSpec{
			ApisixUpstreamConfig: a6v2.ApisixUpstreamConfig{
				LoadBalancer: &a6v2.LoadBalancer{
					Type: "roundrobin",
				},
			},
		},
	}
)

func TestApisixInitialize(t *testing.T) {
	cases := []struct {
		name                 string
		getApisixRoute       func() []*a6v2.ApisixRoute
		getApisixUpstream    func() []*a6v2.ApisixUpstream
		expectApisixRoute    func() *a6v2.ApisixRoute
		expectApisixUpstream func() *a6v2.ApisixUpstream
	}{
		{
			name: "init apisix test1",
			getApisixRoute: func() []*a6v2.ApisixRoute {
				return []*a6v2.ApisixRoute{demoApisixRoute.DeepCopy()}
			},
			getApisixUpstream: func() []*a6v2.ApisixUpstream {
				return []*a6v2.ApisixUpstream{demoApisixUpstream.DeepCopy()}
			},
			expectApisixRoute: func() *a6v2.ApisixRoute {
				expect := demoApisixRoute.DeepCopy()

				expect.Spec.HTTP[0].Backends[0].Weight = utilpointer.Int(100)
				expect.Spec.HTTP[0].Backends = append(expect.Spec.HTTP[0].Backends, *expect.Spec.HTTP[0].Backends[0].DeepCopy())
				expect.Spec.HTTP[0].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[0].Backends[1].Weight = utilpointer.Int(0)

				expect.Spec.HTTP[2].Backends[0].Weight = utilpointer.Int(100)
				expect.Spec.HTTP[2].Backends = append(expect.Spec.HTTP[2].Backends, *expect.Spec.HTTP[2].Backends[0].DeepCopy())
				expect.Spec.HTTP[2].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[2].Backends[1].Weight = utilpointer.Int(0)

				return expect
			},
			expectApisixUpstream: func() *a6v2.ApisixUpstream {
				expect := demoApisixUpstream.DeepCopy()
				expect.Name = "echoserver-canary"
				return expect
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(apisixScheme).Build()
			for _, route := range cs.getApisixRoute() {
				fakeCli.Create(context.TODO(), route)
			}

			for _, upstream := range cs.getApisixUpstream() {
				fakeCli.Create(context.TODO(), upstream)
			}

			controller, err := NewApisixIngressTrafficRouting(fakeCli, config)
			if err != nil {
				t.Fatalf("NewApisixIngressTrafficRouting failed: %s", err.Error())
				return
			}

			err = controller.Initialize(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
				return
			}

			canaryApisixRoute := &a6v2.ApisixRoute{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver"}, canaryApisixRoute)
			if err != nil {
				t.Fatalf("Get canary apisix route failed: %s", err.Error())
				return
			}

			expectApisixRoute := cs.expectApisixRoute()
			if !reflect.DeepEqual(canaryApisixRoute.Annotations, expectApisixRoute.Annotations) ||
				!reflect.DeepEqual(canaryApisixRoute.Spec, expectApisixRoute.Spec) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expectApisixRoute), util.DumpJSON(canaryApisixRoute))
			}

			canaryApisixUpstream := &a6v2.ApisixUpstream{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver-canary"}, canaryApisixUpstream)
			if err != nil {
				t.Fatalf("Get canary apisix upstream failed: %s", err.Error())
				return
			}

			expectApisixUpstream := cs.expectApisixUpstream()
			if !reflect.DeepEqual(canaryApisixUpstream.Annotations, expectApisixUpstream.Annotations) ||
				!reflect.DeepEqual(canaryApisixUpstream.Spec, expectApisixUpstream.Spec) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expectApisixUpstream), util.DumpJSON(canaryApisixUpstream))
			}
		})
	}
}

func TestApisixEnsureRoutes(t *testing.T) {
	cases := []struct {
		name              string
		getApisixRoute    func() []*a6v2.ApisixRoute
		getRoutes         func() (*int32, []rolloutsv1alpha1.HttpRouteMatch)
		expectApisixRoute func() *a6v2.ApisixRoute
		ingressType       string
	}{
		{
			name: "ensure apisix routes test1 with 0 weight",
			getApisixRoute: func() []*a6v2.ApisixRoute {
				return []*a6v2.ApisixRoute{demoApisixRoute.DeepCopy()}
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(0), nil
			},
			expectApisixRoute: func() *a6v2.ApisixRoute {
				expect := demoApisixRoute.DeepCopy()

				expect.Spec.HTTP[0].Backends[0].Weight = utilpointer.Int(100)
				expect.Spec.HTTP[0].Backends = append(expect.Spec.HTTP[0].Backends, *expect.Spec.HTTP[0].Backends[0].DeepCopy())
				expect.Spec.HTTP[0].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[0].Backends[1].Weight = utilpointer.Int(0)

				expect.Spec.HTTP[2].Backends[0].Weight = utilpointer.Int(100)
				expect.Spec.HTTP[2].Backends = append(expect.Spec.HTTP[2].Backends, *expect.Spec.HTTP[2].Backends[0].DeepCopy())
				expect.Spec.HTTP[2].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[2].Backends[1].Weight = utilpointer.Int(0)

				return expect
			},
		},
		{
			name: "ensure apisix routes test1 with 10 weight",
			getApisixRoute: func() []*a6v2.ApisixRoute {
				return []*a6v2.ApisixRoute{demoApisixRoute.DeepCopy()}
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(10), nil
			},
			expectApisixRoute: func() *a6v2.ApisixRoute {
				expect := demoApisixRoute.DeepCopy()

				expect.Spec.HTTP[0].Backends[0].Weight = utilpointer.Int(90)
				expect.Spec.HTTP[0].Backends = append(expect.Spec.HTTP[0].Backends, *expect.Spec.HTTP[0].Backends[0].DeepCopy())
				expect.Spec.HTTP[0].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[0].Backends[1].Weight = utilpointer.Int(10)

				expect.Spec.HTTP[2].Backends[0].Weight = utilpointer.Int(90)
				expect.Spec.HTTP[2].Backends = append(expect.Spec.HTTP[2].Backends, *expect.Spec.HTTP[2].Backends[0].DeepCopy())
				expect.Spec.HTTP[2].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[2].Backends[1].Weight = utilpointer.Int(10)

				return expect
			},
		},
		{
			name: "ensure apisix routes test1 with 100 weight",
			getApisixRoute: func() []*a6v2.ApisixRoute {
				return []*a6v2.ApisixRoute{demoApisixRoute.DeepCopy()}
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(100), nil
			},
			expectApisixRoute: func() *a6v2.ApisixRoute {
				expect := demoApisixRoute.DeepCopy()

				expect.Spec.HTTP[0].Backends[0].Weight = utilpointer.Int(0)
				expect.Spec.HTTP[0].Backends = append(expect.Spec.HTTP[0].Backends, *expect.Spec.HTTP[0].Backends[0].DeepCopy())
				expect.Spec.HTTP[0].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[0].Backends[1].Weight = utilpointer.Int(100)

				expect.Spec.HTTP[2].Backends[0].Weight = utilpointer.Int(0)
				expect.Spec.HTTP[2].Backends = append(expect.Spec.HTTP[2].Backends, *expect.Spec.HTTP[2].Backends[0].DeepCopy())
				expect.Spec.HTTP[2].Backends[1].ServiceName = "echoserver-canary"
				expect.Spec.HTTP[2].Backends[1].Weight = utilpointer.Int(100)

				return expect
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(apisixScheme).Build()
			for _, route := range cs.getApisixRoute() {
				fakeCli.Create(context.TODO(), route)
			}

			controller, err := NewApisixIngressTrafficRouting(fakeCli, config)
			if err != nil {
				t.Fatalf("NewApisixIngressTrafficRouting failed: %s", err.Error())
				return
			}

			err = controller.Initialize(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
				return
			}

			weight, matches := cs.getRoutes()
			result, err := controller.EnsureRoutes(context.TODO(), weight, matches)
			if result {
				return
			}
			if err != nil {
				t.Fatalf("EnsureRoutes failed: %s", err.Error())
				return
			}

			canaryApisixRoute := &a6v2.ApisixRoute{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver"}, canaryApisixRoute)
			if err != nil {
				t.Fatalf("Get canary apisix route failed: %s", err.Error())
				return
			}

			expectApisixRoute := cs.expectApisixRoute()
			if !reflect.DeepEqual(canaryApisixRoute.Annotations, expectApisixRoute.Annotations) ||
				!reflect.DeepEqual(canaryApisixRoute.Spec, expectApisixRoute.Spec) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expectApisixRoute), util.DumpJSON(canaryApisixRoute))
			}
		})
	}
}

func TestApisixFinalise(t *testing.T) {
	cases := []struct {
		name                 string
		getApisixRoute       func() []*a6v2.ApisixRoute
		getApisixUpstream    func() []*a6v2.ApisixUpstream
		expectApisixRoute    func() *a6v2.ApisixRoute
		expectApisixUpstream func() *a6v2.ApisixUpstream
	}{
		{
			name: "finalise apisix routes test",
			getApisixRoute: func() []*a6v2.ApisixRoute {
				return []*a6v2.ApisixRoute{demoApisixRoute.DeepCopy()}
			},
			getApisixUpstream: func() []*a6v2.ApisixUpstream {
				return []*a6v2.ApisixUpstream{demoApisixUpstream.DeepCopy()}
			},
			expectApisixRoute: func() *a6v2.ApisixRoute {
				expect := demoApisixRoute.DeepCopy()
				expect.Spec.HTTP[0].Backends[0].Weight = utilpointer.Int(100)
				expect.Spec.HTTP[2].Backends[0].Weight = utilpointer.Int(100)
				return expect
			},
			expectApisixUpstream: func() *a6v2.ApisixUpstream {
				return nil
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(apisixScheme).Build()
			for _, route := range cs.getApisixRoute() {
				fakeCli.Create(context.TODO(), route)
			}

			for _, upstream := range cs.getApisixUpstream() {
				fakeCli.Create(context.TODO(), upstream)
			}

			controller, err := NewApisixIngressTrafficRouting(fakeCli, config)
			if err != nil {
				t.Fatalf("NewIngressTrafficRouting failed: %s", err.Error())
				return
			}

			err = controller.Initialize(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
				return
			}

			_, err = controller.Finalise(context.TODO())
			if err != nil {
				t.Fatalf("Finalise failed: %s", err.Error())
				return
			}

			canaryApisixRoute := &a6v2.ApisixRoute{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver"}, canaryApisixRoute)
			if err != nil {
				if cs.expectApisixRoute() == nil && errors.IsNotFound(err) {
					return
				}
				t.Fatalf("Get canary apisix route failed: %s", err.Error())
				return
			}

			expectApisixRoute := cs.expectApisixRoute()
			if !reflect.DeepEqual(canaryApisixRoute.Annotations, expectApisixRoute.Annotations) ||
				!reflect.DeepEqual(canaryApisixRoute.Spec, expectApisixRoute.Spec) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expectApisixRoute), util.DumpJSON(canaryApisixRoute))
			}

			canaryApisixUpstream := &a6v2.ApisixUpstream{}
			err = fakeCli.Get(context.TODO(), client.ObjectKey{Name: "echoserver-canary"}, canaryApisixUpstream)
			if err != nil {
				if cs.expectApisixUpstream() == nil && errors.IsNotFound(err) {
					return
				}
				t.Fatalf("Get canary apisix upstream failed: %s", err.Error())
				return
			}
		})
	}
}
