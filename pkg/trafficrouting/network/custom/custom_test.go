/*
Copyright 2021.

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

package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme             *runtime.Scheme
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
								"host": "mockb",
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
	// lua script for this resource contains error and cannot be executed
	luaErrorDemo = `
						{
							"apiVersion": "networking.error.io/v1alpha3",
							"kind": "LuaError",
							"metadata": {
								"name": "error-demo"
							},
							"spec": {
								"error": true
							}
						}
						`
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rolloutsv1alpha1.AddToScheme(scheme)
}

func TestInitialize(t *testing.T) {
	cases := []struct {
		name               string
		getUnstructured    func() *unstructured.Unstructured
		getConfig          func() Config
		getConfigMap       func() *corev1.ConfigMap
		expectUnstructured func() *unstructured.Unstructured
	}{
		{
			name: "test1, find lua script locally",
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.CustomNetworkRef{
						{
							APIVersion: "networking.istio.io/v1alpha3",
							Kind:       "VirtualService",
							Name:       "echoserver",
						},
					},
				}
			},
			getConfigMap: func() *corev1.ConfigMap {
				return &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      LuaConfigMap,
						Namespace: util.GetRolloutNamespace(),
					},
					Data: map[string]string{
						fmt.Sprintf("%s.%s.%s", configuration.LuaTrafficRoutingCustomTypePrefix, "VirtualService", "networking.istio.io"): "ExpectedLuaScript",
					},
				}
			},
			expectUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"spec":{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]},"annotations":{"virtual":"test"}}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				return u
			},
		},
		{
			name: "test2, find lua script in ConfigMap",
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.CustomNetworkRef{
						{
							APIVersion: "networking.istio.io/v1alpha3",
							Kind:       "VirtualService",
							Name:       "echoserver",
						},
					},
				}
			},
			getConfigMap: func() *corev1.ConfigMap {
				return &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      LuaConfigMap,
						Namespace: util.GetRolloutNamespace(),
					},
					Data: map[string]string{
						fmt.Sprintf("%s.%s.%s", configuration.LuaTrafficRoutingIngressTypePrefix, "VirtualService", "networking.istio.io"): "ExpectedLuaScript",
					},
				}
			},
			expectUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"spec":{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]},"annotations":{"virtual":"test"}}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				return u
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
			err := fakeCli.Create(context.TODO(), cs.getUnstructured())
			if err != nil {
				klog.Errorf(err.Error())
				return
			}
			if err := fakeCli.Create(context.TODO(), cs.getConfigMap()); err != nil {
				klog.Errorf(err.Error())
			}
			c, _ := NewCustomController(fakeCli, cs.getConfig())
			err = c.Initialize(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
			}
			checkEqual(fakeCli, t, cs.expectUnstructured())
		})
	}
}

func checkEqual(cli client.Client, t *testing.T, expect *unstructured.Unstructured) {
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

func TestEnsureRoutes(t *testing.T) {
	cases := []struct {
		name                string
		getLua              func() map[string]string
		getRoutes           func() *rolloutsv1alpha1.TrafficRoutingStrategy
		getUnstructureds    func() []*unstructured.Unstructured
		getConfig           func() Config
		expectState         func() (bool, bool)
		expectUnstructureds func() []*unstructured.Unstructured
	}{
		{
			name: "test1, do traffic routing for VirtualService and DestinationRule",
			getRoutes: func() *rolloutsv1alpha1.TrafficRoutingStrategy {
				return &rolloutsv1alpha1.TrafficRoutingStrategy{
					Weight: utilpointer.Int32(5),
				}
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
			getConfig: func() Config {
				return Config{
					Key:           "rollout-demo",
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.CustomNetworkRef{
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
				}
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
					OriginalSpecAnnotation: `{"spec":{"host":"mockb","subsets":[{"labels":{"version":"base"},"name":"version-base"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}}`,
				}
				u.SetAnnotations(annotations)
				specStr = `{"host":"mockb","subsets":[{"labels":{"version":"base"},"name":"version-base"},{"labels":{"istio.service.tag":"gray"},"name":"canary"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}`
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)
				return objects
			},
			expectState: func() (bool, bool) {
				done := false
				hasError := false
				return done, hasError
			},
		},
		{
			name: "test2, do traffic routing but failed to execute lua",
			getRoutes: func() *rolloutsv1alpha1.TrafficRoutingStrategy {
				return &rolloutsv1alpha1.TrafficRoutingStrategy{
					Weight: utilpointer.Int32(5),
				}
			},
			getUnstructureds: func() []*unstructured.Unstructured {
				objects := make([]*unstructured.Unstructured, 0)
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				objects = append(objects, u)

				u = &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(luaErrorDemo))
				u.SetAPIVersion("networking.error.io/v1alpha3")
				objects = append(objects, u)
				return objects
			},
			getConfig: func() Config {
				return Config{
					Key:           "rollout-demo",
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.CustomNetworkRef{
						{
							APIVersion: "networking.istio.io/v1alpha3",
							Kind:       "VirtualService",
							Name:       "echoserver",
						},
						{
							APIVersion: "networking.error.io/v1alpha3",
							Kind:       "LuaError",
							Name:       "error-demo",
						},
					},
				}
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
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)

				u = &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(luaErrorDemo))
				annotations = map[string]string{
					OriginalSpecAnnotation: `{"spec":{"error":true}}`,
				}
				u.SetAnnotations(annotations)
				specStr = `{"error":true}`
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)
				return objects
			},
			expectState: func() (bool, bool) {
				done := false
				hasError := true
				return done, hasError
			},
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
			for _, obj := range cs.getUnstructureds() {
				err := fakeCli.Create(context.TODO(), obj)
				if err != nil {
					t.Fatalf("failed to create objects: %s", err.Error())
				}
			}
			c, _ := NewCustomController(fakeCli, cs.getConfig())
			strategy := cs.getRoutes()
			expectDone, expectHasError := cs.expectState()
			err := c.Initialize(context.TODO())
			if err != nil {
				t.Fatalf("failed to initialize custom controller")
			}
			done, err := c.EnsureRoutes(context.TODO(), strategy)
			if !expectHasError && err != nil {
				t.Fatalf("EnsureRoutes failed: %s", err.Error())
			} else if expectHasError && err == nil {
				t.Fatalf("expect error occured but not")
			} else if done != expectDone {
				t.Fatalf("expect(%v), but get(%v)", expectDone, done)
			}
			for _, expectUnstructured := range cs.expectUnstructureds() {
				checkEqual(fakeCli, t, expectUnstructured)
			}
		})
	}
}

func TestFinalise(t *testing.T) {
	cases := []struct {
		name               string
		getUnstructured    func() *unstructured.Unstructured
		getConfig          func() Config
		expectUnstructured func() *unstructured.Unstructured
	}{
		{
			name: "test1, finalise VirtualService",
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"spec":{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]},"annotations":{"virtual":"test"}}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver-canary"},"weight":5}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.CustomNetworkRef{
						{
							APIVersion: "networking.istio.io/v1alpha3",
							Kind:       "VirtualService",
							Name:       "echoserver",
						},
					},
				}
			},
			expectUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				return u
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fakeCli := fake.NewClientBuilder().WithScheme(scheme).Build()
			err := fakeCli.Create(context.TODO(), cs.getUnstructured())
			if err != nil {
				klog.Errorf(err.Error())
				return
			}
			c, _ := NewCustomController(fakeCli, cs.getConfig())
			err = c.Finalise(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
			}
			checkEqual(fakeCli, t, cs.expectUnstructured())
		})
	}
}
