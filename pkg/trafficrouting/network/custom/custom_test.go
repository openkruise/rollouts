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
	scheme  *runtime.Scheme
	luaDemo = `
	if (obj.matches)
	then
		for _, match in ipairs(obj.matches) do
			local route = {}
			route["match"] = {}
			for key, value in pairs(match) do
				route["match"][key] = {}
				for _, rule in ipairs(value) do
					if rule["type"] == "RegularExpression"
					then
						matchType = "regex"
					else
						matchType = "exact"
					end
					route["match"][key][rule["name"]] = {}
					route["match"][key][rule["name"]][matchType] = rule["value"]
					-- table.concat(route["match"][key], { rule["name"], { matchType, value } })
				end
			end
			route["route"] = {
				{
					destination = {
						host = obj.stableService,
						weight = obj.stableWeight
					}
				},
				{
					destination = {
						host = obj.canaryService,
						weight = obj.canaryWeight
					}
				}
			}
			table.insert(obj.spec.http, 1, route)
		end
		return obj
	end
	
	for i, route in ipairs(obj.spec.http) do
		for _, destination in ipairs(route.route) do
			destination = destination.destination
			if destination.host == obj.stableService then
				destination.weight = obj.stableWeight
				local canary = {
					destination = {
						host = obj.canaryService,
						weight = obj.canaryWeight
					}
				}
				table.insert(obj.spec.http[i].route, canary)
			end
		end
	end
	return obj.spec
			`
	networkDemo = `
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
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rolloutsv1alpha1.AddToScheme(scheme)
}

func TestInitialize(t *testing.T) {
	cases := []struct {
		name               string
		getLua             func() map[string]string
		getUnstructured    func() *unstructured.Unstructured
		getConfig          func() Config
		expectUnstructured func() *unstructured.Unstructured
	}{
		{
			name: "test1",
			getLua: func() map[string]string {
				luaMap := map[string]string{
					"lua-demo": luaDemo,
				}
				return luaMap
			},
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.NetworkRef{
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
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
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
	if !reflect.DeepEqual(obj.Object["spec"], expect.Object["spec"]) {
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect.Object["spec"]), util.DumpJSON(obj.Object["spec"]))
	}
}

func TestEnsureRoutes(t *testing.T) {
	cases := []struct {
		name            string
		getLua          func() map[string]string
		getRoutes       func() *rolloutsv1alpha1.TrafficRoutingStrategy
		getUnstructured func() *unstructured.Unstructured
		expectInfo      func() (bool, *unstructured.Unstructured)
	}{
		{
			name: "test1",
			getLua: func() map[string]string {
				luaMap := map[string]string{
					"lua-demo": luaDemo,
				}
				return luaMap
			},
			getRoutes: func() *rolloutsv1alpha1.TrafficRoutingStrategy {
				return &rolloutsv1alpha1.TrafficRoutingStrategy{
					Weight: utilpointer.Int32(5),
				}
			},
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				return u
			},
			expectInfo: func() (bool, *unstructured.Unstructured) {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver-canary"},"weight":5}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return false, u
			},
		},
		{
			name: "test2",
			getLua: func() map[string]string {
				luaMap := map[string]string{
					"lua-demo": luaDemo,
				}
				return luaMap
			},
			getRoutes: func() *rolloutsv1alpha1.TrafficRoutingStrategy {
				return &rolloutsv1alpha1.TrafficRoutingStrategy{
					Weight: utilpointer.Int32(5),
				}
			},
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver-canary"},"weight":5}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return u
			},
			expectInfo: func() (bool, *unstructured.Unstructured) {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver-canary"},"weight":5}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return true, u
			},
		},
		// {
		// 	name: "test3",
		// 	getLua: func() map[string]string {
		// 		luaMap := map[string]string{
		// 			"lua-demo": luaDemo,
		// 		}
		// 		return luaMap
		// 	},
		// 	getRoutes: func() *rolloutsv1alpha1.TrafficRoutingStrategy {
		// 		return &rolloutsv1alpha1.TrafficRoutingStrategy{
		// 			Weight: utilpointer.Int32(0),
		// 		}
		// 	},
		// 	getUnstructured: func() *unstructured.Unstructured {
		// 		u := &unstructured.Unstructured{}
		// 		_ = u.UnmarshalJSON([]byte(networkDemo))
		// 		annotations := map[string]string{
		// 			OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
		// 			"virtual":              "test",
		// 		}
		// 		u.SetAnnotations(annotations)
		// 		specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":95},{"destination":{"host":"echoserver-canary"},"weight":5}]}]}`
		// 		var spec interface{}
		// 		_ = json.Unmarshal([]byte(specStr), &spec)
		// 		u.Object["spec"] = spec
		// 		return u
		// 	},
		// 	expectInfo: func() (bool, *unstructured.Unstructured) {
		// 		u := &unstructured.Unstructured{}
		// 		_ = u.UnmarshalJSON([]byte(networkDemo))
		// 		annotations := map[string]string{
		// 			OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
		// 			"virtual":              "test",
		// 		}
		// 		u.SetAnnotations(annotations)
		// 		specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":100},{"destination":{"host":"echoserver-canary"},"weight":0}]}]}`
		// 		var spec interface{}
		// 		_ = json.Unmarshal([]byte(specStr), &spec)
		// 		u.Object["spec"] = spec
		// 		return false, u
		// 	},
		// },
	}
	config := Config{
		RolloutName:   "rollout-demo",
		StableService: "echoserver",
		CanaryService: "echoserver-canary",
		TrafficConf: []rolloutsv1alpha1.NetworkRef{
			{
				APIVersion: "networking.istio.io/v1alpha3",
				Kind:       "VirtualService",
				Name:       "echoserver",
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
			c, _ := NewCustomController(fakeCli, config)
			strategy := cs.getRoutes()
			expect1, expect2 := cs.expectInfo()
			c.Initialize(context.TODO())
			done, err := c.EnsureRoutes(context.TODO(), strategy)
			if err != nil {
				t.Fatalf("EnsureRoutes failed: %s", err.Error())
			} else if done != expect1 {
				t.Fatalf("expect(%v), but get(%v)", expect1, done)
			}
			checkEqual(fakeCli, t, expect2)
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
			name: "test1",
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginalSpecAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":              "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":100},{"destination":{"host":"echoserver-canary"},"weight":0}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.NetworkRef{
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
				_ = u.UnmarshalJSON([]byte(networkDemo))
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
