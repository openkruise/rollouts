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
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

var (
	scheme  *runtime.Scheme
	luaDemo = `
				function split(input, delimiter)
					local arr = {}
					string.gsub(input, '[^' .. delimiter ..']+', function(w) table.insert(arr, w) end)
					return arr
				end

				spec = obj.spec
				for _,http in ipairs(spec.http) do
					if ( http.route[1].name == obj.service )
					then
						if ( obj.weight ~= "-1" )
						then
							route = {}
							route.destination = {}
							route.destination.host = obj.canaryService
							route.destination.weight = obj.canaryWeight
							http.route[2] = route
							http.route[1].weight = obj.stableWeight
						end
					end
				end
				return spec
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
		getLua             func() string
		getUnstructured    func() *unstructured.Unstructured
		getConfig          func() Config
		expectUnstructured func() *unstructured.Unstructured
	}{
		{
			name: "test1",
			getLua: func() string {
				demo := luaDemo
				return demo
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
					TrafficConf: []rolloutsv1alpha1.NetworkTrafficRouting{
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
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
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
			c, _ := NewCustomController(fakeCli, cs.getConfig(), cs.getLua())
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
		getLua          func() string
		getRoutes       func() (*int32, []rolloutsv1alpha1.HttpRouteMatch)
		getUnstructured func() *unstructured.Unstructured
		expectInfo      func() (bool, *unstructured.Unstructured)
	}{
		{
			name: "test1",
			getLua: func() string {
				demo := luaDemo
				return demo
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(5), nil
			},
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				return u
			},
			expectInfo: func() (bool, *unstructured.Unstructured) {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":"95"},{"destination":{"host":"echoserver-canary","weight":"5"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return false, u
			},
		},
		{
			name: "test2",
			getLua: func() string {
				demo := luaDemo
				return demo
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(5), nil
			},
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":"95"},{"destination":{"host":"echoserver-canary","weight":"5"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return u
			},
			expectInfo: func() (bool, *unstructured.Unstructured) {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":"95"},{"destination":{"host":"echoserver-canary","weight":"5"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return true, u
			},
		},
		{
			name: "test3",
			getLua: func() string {
				demo := luaDemo
				return demo
			},
			getRoutes: func() (*int32, []rolloutsv1alpha1.HttpRouteMatch) {
				return utilpointer.Int32(0), nil
			},
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":"95"},{"destination":{"host":"echoserver-canary","weight":"5"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return u
			},
			expectInfo: func() (bool, *unstructured.Unstructured) {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(networkDemo))
				annotations := map[string]string{
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":"100"},{"destination":{"host":"echoserver-canary","weight":"0"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return false, u
			},
		},
	}
	config := Config{
		RolloutName:   "rollout-demo",
		StableService: "echoserver",
		CanaryService: "echoserver-canary",
		TrafficConf: []rolloutsv1alpha1.NetworkTrafficRouting{
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
			c, _ := NewCustomController(fakeCli, config, cs.getLua())
			weight, matches := cs.getRoutes()
			expect1, expect2 := cs.expectInfo()
			done, err := c.EnsureRoutes(context.TODO(), weight, matches)
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
					OriginSpecConfigurationAnnotation: `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`,
					"virtual":                         "test",
				}
				u.SetAnnotations(annotations)
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":"100"},{"destination":{"host":"echoserver-canary","weight":"0"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []rolloutsv1alpha1.NetworkTrafficRouting{
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
			c, _ := NewCustomController(fakeCli, cs.getConfig(), "")
			err = c.Finalise(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
			}
			checkEqual(fakeCli, t, cs.expectUnstructured())
		})
	}
}
