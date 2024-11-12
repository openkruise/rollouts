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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	rolloutapi "github.com/openkruise/rollouts/api"
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	"github.com/stretchr/testify/assert"
	lua "github.com/yuin/gopher-lua"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	luajson "layeh.com/gopher-json"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	"sigs.k8s.io/yaml"
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
	_ = rolloutapi.AddToScheme(scheme)
}

func TestInitialize(t *testing.T) {
	cases := []struct {
		name            string
		getUnstructured func() *unstructured.Unstructured
		getConfig       func() Config
		getConfigMap    func() *corev1.ConfigMap
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
					TrafficConf: []v1beta1.ObjectRef{
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
		},
		{
			name: "test2, find lua script in ConfigMap",
			getUnstructured: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.test.io/v1alpha3")
				return u
			},
			getConfig: func() Config {
				return Config{
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []v1beta1.ObjectRef{
						{
							APIVersion: "networking.test.io/v1alpha3",
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
						fmt.Sprintf("%s.%s.%s", configuration.LuaTrafficRoutingCustomTypePrefix, "VirtualService", "networking.test.io"): "ExpectedLuaScript",
					},
				}
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
	if !assert.JSONEq(t, util.DumpJSON(expect.Object["spec"]), util.DumpJSON(obj.Object["spec"])) {
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect.Object["spec"]), util.DumpJSON(obj.Object["spec"]))
	}
}

func TestEnsureRoutes(t *testing.T) {
	cases := []struct {
		name                string
		getLua              func() map[string]string
		getRoutes           func() *v1beta1.TrafficRoutingStrategy
		getUnstructureds    func() []*unstructured.Unstructured
		getConfig           func() Config
		expectState         func() (bool, bool)
		expectUnstructureds func() []*unstructured.Unstructured
	}{
		{
			name: "Do weight-based traffic routing for VirtualService and DestinationRule",
			getRoutes: func() *v1beta1.TrafficRoutingStrategy {
				return &v1beta1.TrafficRoutingStrategy{
					Traffic: utilpointer.String("5%"),
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
					TrafficConf: []v1beta1.ObjectRef{
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
			expectState: func() (done bool, hasError bool) {
				return false, false
			},
		},
		{
			name: "Do header/queryParam-based traffic routing for VirtualService and DestinationRule",
			getRoutes: func() *v1beta1.TrafficRoutingStrategy {
				pathTypePrefix := gatewayv1beta1.PathMatchPathPrefix
				headerTypeExact := gatewayv1beta1.HeaderMatchExact
				queryParamRegex := gatewayv1beta1.QueryParamMatchRegularExpression
				return &v1beta1.TrafficRoutingStrategy{
					Matches: []v1beta1.HttpRouteMatch{
						{
							Path: &gatewayv1beta1.HTTPPathMatch{
								Type:  &pathTypePrefix,
								Value: utilpointer.String("/api/v2"),
							},
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Type:  &headerTypeExact,
									Name:  "user_id",
									Value: "123456",
								},
							},
							QueryParams: []gatewayv1beta1.HTTPQueryParamMatch{
								{
									Type:  &queryParamRegex,
									Name:  "user_id",
									Value: "123*",
								},
							},
						},
					},
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
					TrafficConf: []v1beta1.ObjectRef{
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
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"match":[{"headers":{"user_id":{"exact":"123456"}},"queryParams":{"user_id":{"regex":"123*"}},"uri":{"prefix":"/api/v2"}}],"route":[{"destination":{"host":"echoserver-canary"}}]},{"route":[{"destination":{"host":"echoserver"}}]}]}`
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
			expectState: func() (done bool, hasError bool) {
				return false, false
			},
		},
		{
			name: "Do header-based traffic routing and set header for VirtualService",
			getRoutes: func() *v1beta1.TrafficRoutingStrategy {
				headerTypeExact := gatewayv1beta1.HeaderMatchExact
				return &v1beta1.TrafficRoutingStrategy{
					Matches: []v1beta1.HttpRouteMatch{
						{
							Headers: []gatewayv1beta1.HTTPHeaderMatch{
								{
									Type:  &headerTypeExact,
									Name:  "user_id",
									Value: "123456",
								},
							},
						},
					},
					RequestHeaderModifier: &gatewayv1beta1.HTTPHeaderFilter{
						Set: []gatewayv1beta1.HTTPHeader{
							{
								Name:  "x-env-flag",
								Value: "canary",
							},
						},
					},
				}
			},
			getUnstructureds: func() []*unstructured.Unstructured {
				objects := make([]*unstructured.Unstructured, 0)
				u := &unstructured.Unstructured{}
				_ = u.UnmarshalJSON([]byte(virtualServiceDemo))
				u.SetAPIVersion("networking.istio.io/v1alpha3")
				objects = append(objects, u)

				return objects
			},
			getConfig: func() Config {
				return Config{
					Key:           "rollout-demo",
					StableService: "echoserver",
					CanaryService: "echoserver-canary",
					TrafficConf: []v1beta1.ObjectRef{
						{
							APIVersion: "networking.istio.io/v1alpha3",
							Kind:       "VirtualService",
							Name:       "echoserver",
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
				specStr := `{"hosts":["echoserver.example.com"],"http":[{"headers":{"request":{"set":{"x-env-flag":"canary"}}},"match":[{"headers":{"user_id":{"exact":"123456"}}}],"route":[{"destination":{"host":"echoserver-canary"}}]},{"route":[{"destination":{"host":"echoserver"}}]}]}`
				var spec interface{}
				_ = json.Unmarshal([]byte(specStr), &spec)
				u.Object["spec"] = spec
				objects = append(objects, u)

				return objects
			},
			expectState: func() (done bool, hasError bool) {
				return false, false
			},
		},
		{
			name: "test2, do traffic routing but failed to execute lua",
			getRoutes: func() *v1beta1.TrafficRoutingStrategy {
				return &v1beta1.TrafficRoutingStrategy{
					Traffic: utilpointer.String("5%"),
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
					TrafficConf: []v1beta1.ObjectRef{
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
			expectState: func() (done bool, hasError bool) {
				return false, true
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
				t.Fatalf("expect error occurred but not")
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
		modified           bool
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
					TrafficConf: []v1beta1.ObjectRef{
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
			modified: true,
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
			modified, err := c.Finalise(context.TODO())
			if err != nil {
				t.Fatalf("Initialize failed: %s", err.Error())
			}
			if cs.modified != modified {
				t.Fatalf("is modified: expect(%v), but get(%v)", cs.modified, modified)
			}
			checkEqual(fakeCli, t, cs.expectUnstructured())
		})
	}
}

type TestCase struct {
	Rollout        *v1beta1.Rollout                 `json:"rollout,omitempty"`
	TrafficRouting *rolloutsv1alpha1.TrafficRouting `json:"trafficRouting,omitempty"`
	Original       *unstructured.Unstructured       `json:"original,omitempty"`
	Expected       []*unstructured.Unstructured     `json:"expected,omitempty"`
}

// test if the lua script of a network provider run as expected
func TestLuaScript(t *testing.T) {
	err := filepath.Walk("../../../../lua_configuration", func(path string, f os.FileInfo, err error) error {
		if !strings.Contains(path, "trafficRouting.lua") {
			return nil
		}
		if err != nil {
			t.Errorf("failed to walk lua script dir")
			return err
		}
		script, err := readScript(path)
		if err != nil {
			t.Errorf("failed to read lua script from: %s", path)
			return err
		}
		dir := filepath.Dir(path)
		err = filepath.Walk(filepath.Join(dir, "testdata"), func(path string, info os.FileInfo, err error) error {
			klog.Infof("testing lua script: %s", path)
			if err != nil {
				t.Errorf("fail to walk testdata dir")
				return err
			}

			if !info.IsDir() && filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" {
				testCase, err := getLuaTestCase(t, path)
				if err != nil {
					t.Errorf("faied to get lua test case: %s", path)
					return err
				}
				rollout := testCase.Rollout
				trafficRouting := testCase.TrafficRouting
				if rollout != nil {
					steps := rollout.Spec.Strategy.Canary.Steps
					for i, step := range steps {
						var weight *int32
						if step.TrafficRoutingStrategy.Traffic != nil {
							is := intstr.FromString(*step.TrafficRoutingStrategy.Traffic)
							weightInt, _ := intstr.GetScaledValueFromIntOrPercent(&is, 100, true)
							weight = utilpointer.Int32(int32(weightInt))
						} else {
							weight = utilpointer.Int32(-1)
						}
						var canaryService string
						stableService := rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service
						canaryService = fmt.Sprintf("%s-canary", stableService)
						data := &LuaData{
							Data: Data{
								Labels:      testCase.Original.GetLabels(),
								Annotations: testCase.Original.GetAnnotations(),
								Spec:        testCase.Original.Object["spec"],
							},
							Matches:               step.TrafficRoutingStrategy.Matches,
							CanaryWeight:          *weight,
							StableWeight:          100 - *weight,
							CanaryService:         canaryService,
							StableService:         stableService,
							RequestHeaderModifier: step.TrafficRoutingStrategy.RequestHeaderModifier,
						}
						nSpec, err := executeLua(data, script)
						if err != nil {
							t.Errorf("failed to execute lua for test case: %s", path)
							return err
						}
						eSpec := Data{
							Spec:        testCase.Expected[i].Object["spec"],
							Annotations: testCase.Expected[i].GetAnnotations(),
							Labels:      testCase.Expected[i].GetLabels(),
						}
						if util.DumpJSON(eSpec) != util.DumpJSON(nSpec) {
							return fmt.Errorf("expect %s, but get %s for test case: %s", util.DumpJSON(eSpec), util.DumpJSON(nSpec), path)
						}
					}
				} else if trafficRouting != nil {
					weight := trafficRouting.Spec.Strategy.Weight
					if weight == nil {
						weight = utilpointer.Int32(-1)
					}
					var canaryService string
					stableService := trafficRouting.Spec.ObjectRef[0].Service
					canaryService = stableService
					matches := make([]v1beta1.HttpRouteMatch, 0)
					for _, match := range trafficRouting.Spec.Strategy.Matches {
						obj := v1beta1.HttpRouteMatch{}
						obj.Headers = match.Headers
						matches = append(matches, obj)
					}
					data := &LuaData{
						Data: Data{
							Labels:      testCase.Original.GetLabels(),
							Annotations: testCase.Original.GetAnnotations(),
							Spec:        testCase.Original.Object["spec"],
						},
						Matches:               matches,
						CanaryWeight:          *weight,
						StableWeight:          100 - *weight,
						CanaryService:         canaryService,
						StableService:         stableService,
						RequestHeaderModifier: trafficRouting.Spec.Strategy.RequestHeaderModifier,
					}
					nSpec, err := executeLua(data, script)
					if err != nil {
						t.Errorf("failed to execute lua for test case: %s", path)
						return err
					}
					eSpec := Data{
						Spec:        testCase.Expected[0].Object["spec"],
						Annotations: testCase.Expected[0].GetAnnotations(),
						Labels:      testCase.Expected[0].GetLabels(),
					}
					if util.DumpJSON(eSpec) != util.DumpJSON(nSpec) {
						return fmt.Errorf("expect %s, but get %s for test case: %s", util.DumpJSON(eSpec), util.DumpJSON(nSpec), path)
					}
				} else {
					return fmt.Errorf("neither rollout nor trafficRouting defined in test case: %s", path)
				}
			}
			return nil
		})
		return err
	})
	if err != nil {
		t.Fatalf("failed to test lua scripts: %s", err.Error())
	}
}

func readScript(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return string(data), err
}

func getLuaTestCase(t *testing.T, path string) (*TestCase, error) {
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("failed to read file %s", path)
		return nil, err
	}
	fmt.Println(string(yamlFile))
	luaTestCase := &TestCase{}
	err = yaml.Unmarshal(yamlFile, luaTestCase)
	if err != nil {
		t.Errorf("test case %s format error", path)
		return nil, err
	}
	return luaTestCase, nil
}

func executeLua(data *LuaData, script string) (Data, error) {
	luaManager := &luamanager.LuaManager{}
	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(data)
	if err != nil {
		return Data{}, err
	}
	u := &unstructured.Unstructured{Object: unObj}
	l, err := luaManager.RunLuaScript(u, script)
	if err != nil {
		return Data{}, err
	}
	returnValue := l.Get(-1)
	var nSpec Data
	if returnValue.Type() == lua.LTTable {
		jsonBytes, err := luajson.Encode(returnValue)
		if err != nil {
			return Data{}, err
		}
		err = json.Unmarshal(jsonBytes, &nSpec)
		if err != nil {
			return Data{}, err
		}
	}
	return nSpec, nil
}
