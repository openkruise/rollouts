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

package luamanager

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilpointer "k8s.io/utils/pointer"
	luajson "layeh.com/gopher-json"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

type LuaData struct {
	Data             Data
	CanaryWeight     int32
	StableWeight     int32
	Matches          []rolloutv1alpha1.HttpRouteMatch
	CanaryService    string
	StableService    string
	RevisionLabelKey string
	StableRevision   string
	CanaryRevision   string
}
type Data struct {
	Spec        interface{}       `json:"spec,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func TestRunLuaScript(t *testing.T) {
	cases := []struct {
		name         string
		getObj       func() *unstructured.Unstructured
		getLuaScript func() string
		expectResult func() string
	}{
		{
			name: "lua script test 1",
			getObj: func() *unstructured.Unstructured {
				type LuaData struct {
					Annotations map[string]string
					Weight      string
					Matches     []rolloutv1alpha1.HttpRouteMatch
				}
				data := &LuaData{
					Annotations: map[string]string{
						"kubernetes.io/ingress.class": "nginx",
					},
					Weight: "0",
					Matches: []rolloutv1alpha1.HttpRouteMatch{
						{
							Headers: []gatewayv1alpha2.HTTPHeaderMatch{
								{
									Name:  "user_id",
									Value: "123456",
								},
							},
						},
					},
				}
				unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(data)
				if err != nil {
					fmt.Println("to unstructured failed", err.Error())
				}
				obj := &unstructured.Unstructured{Object: unObj}
				return obj
			},
			getLuaScript: func() string {
				return `
					annotations = obj.annotations
					annotations["weight"] = obj.weight
					if ( not obj.matches )
					then
						return annotations
					end
					for _,match in pairs(obj.matches)
					do
						if ( not (match or match.headers) )
						then
							return annotations
						end
						for _,header in pairs(match.headers)
						do
							if ( not header )
							then
								return annotations
							end
							condition = {}
							condition["name"] = header.name
							condition["value"] = header.value
							annotations["condition"] = json.encode(condition)
						end
					end
					return annotations
				`
			},
			expectResult: func() string {
				obj := map[string]string{
					"weight":                      "0",
					"kubernetes.io/ingress.class": "nginx",
					"condition":                   `{"name":"user_id","value":"123456"}`,
				}
				return util.DumpJSON(obj)
			},
		},
	}

	luaManager := &LuaManager{}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			fmt.Println(cs.name)
			l, err := luaManager.RunLuaScript(cs.getObj(), cs.getLuaScript())
			if err != nil {
				t.Fatalf("RunLuaScript failed: %s", err.Error())
				return
			}
			returnValue := l.Get(-1)
			if returnValue.Type() != lua.LTTable {
				t.Fatalf("expect table output from Lua script, not %s", returnValue.Type().String())
			}
			jsonBytes, err := luajson.Encode(returnValue)
			if err != nil {
				t.Fatalf("encode failed: %s", err.Error())
				return
			}
			newObj := map[string]string{}
			err = json.Unmarshal(jsonBytes, &newObj)
			if err != nil {
				t.Fatalf("Unmarshal failed: %s", err.Error())
				return
			}
			if util.DumpJSON(newObj) != cs.expectResult() {
				t.Fatalf("expect(%s), but get (%s)", cs.expectResult(), util.DumpJSON(newObj))
			}
		})
	}
}

type LuaTestCase struct {
	Matches       []rolloutv1alpha1.HttpRouteMatch `yaml:"matches"`
	StableService string                           `yaml:"stableService"`
	CanaryService string                           `yaml:"canaryService"`
	StableWeight  int                              `yaml:"stableWeight"`
	CanaryWeight  int                              `yaml:"canaryWeight"`
	Spec          interface{}                      `yaml:"spec"`
	NSpec         interface{}                      `yaml:"nSpec"`
}

type TestCase struct {
	Rollout        *rolloutv1alpha1.Rollout        `json:"rollout,omitempty"`
	TrafficRouting *rolloutv1alpha1.TrafficRouting `json:"trafficRouting,omitempty"`
	Original       *unstructured.Unstructured      `json:"original,omitempty"`
	Expected       []*unstructured.Unstructured    `json:"expected,omitempty"`
}

// test if the lua script run as expected
func TestLuaScript(t *testing.T) {
	err := filepath.Walk("../../../lua_configuration", func(path string, f os.FileInfo, err error) error {
		if !strings.Contains(path, "trafficRouting.lua") {
			return nil
		}
		if err != nil {
			return err
		}
		script, err := readScript(t, path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		err = filepath.Walk(filepath.Join(dir, "testdata"), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() && filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" {
				testCase := getLuaTestCase(t, path)
				rollout := testCase.Rollout
				trafficRouting := testCase.TrafficRouting
				if rollout != nil {
					steps := rollout.Spec.Strategy.Canary.Steps
					for i, step := range steps {
						weight := step.TrafficRoutingStrategy.Weight
						if weight == nil {
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
							Matches:          step.TrafficRoutingStrategy.Matches,
							CanaryWeight:     *weight,
							StableWeight:     100 - *weight,
							CanaryService:    canaryService,
							StableService:    stableService,
							RevisionLabelKey: "pod-template-hash",
							StableRevision:   "pod-template-hash-stable",
							CanaryRevision:   "pod-template-hash-canary",
						}
						nSpec, err := executeLua(data, script)
						if err != nil {
							return err
						}
						eSpec := Data{
							Spec:        testCase.Expected[i].Object["spec"],
							Annotations: testCase.Expected[i].GetAnnotations(),
							Labels:      testCase.Expected[i].GetLabels(),
						}
						if util.DumpJSON(eSpec) != util.DumpJSON(nSpec) {
							return fmt.Errorf("expect %s, but get %s", util.DumpJSON(eSpec), util.DumpJSON(nSpec))
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
					data := &LuaData{
						Data: Data{
							Labels:      testCase.Original.GetLabels(),
							Annotations: testCase.Original.GetAnnotations(),
							Spec:        testCase.Original.Object["spec"],
						},
						Matches:          trafficRouting.Spec.Strategy.Matches,
						CanaryWeight:     *weight,
						StableWeight:     100 - *weight,
						CanaryService:    canaryService,
						StableService:    stableService,
						RevisionLabelKey: "pod-template-hash",
						StableRevision:   "pod-template-hash-stable",
						CanaryRevision:   "pod-template-hash-canary",
					}
					nSpec, err := executeLua(data, script)
					if err != nil {
						return err
					}
					eSpec := Data{
						Spec:        testCase.Expected[0].Object["spec"],
						Annotations: testCase.Expected[0].GetAnnotations(),
						Labels:      testCase.Expected[0].GetLabels(),
					}
					if util.DumpJSON(eSpec) != util.DumpJSON(nSpec) {
						return fmt.Errorf("expect %s, but get %s", util.DumpJSON(eSpec), util.DumpJSON(nSpec))
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

func readScript(t *testing.T, path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return string(data), err
}

func getLuaTestCase(t *testing.T, path string) *TestCase {
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s", path)
	}
	luaTestCase := &TestCase{}
	err = yaml.Unmarshal(yamlFile, luaTestCase)
	if err != nil {
		t.Fatalf("test case %s format error", path)
	}
	return luaTestCase
}

func executeLua(data *LuaData, script string) (Data, error) {
	luaManager := &LuaManager{}
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
