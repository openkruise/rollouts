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
	luajson "layeh.com/gopher-json"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"io/ioutil"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

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

type LuaData struct {
	Data          Data
	CanaryWeight  int32
	StableWeight  int32
	Matches       []rolloutv1alpha1.HttpRouteMatch
	CanaryService string
	StableService string
}

type Data struct {
	Spec        interface{}       `json:"spec,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type TestCase struct {
	Rollout  *rolloutv1alpha1.Rollout     `json:"rollout,omitempty"`
	Original *unstructured.Unstructured   `json:"original,omitempty"`
	Expected []*unstructured.Unstructured `json:"expected,omitempty"`
}

// test if the lua script run as expected
func TestLuaScript(t *testing.T) {
	luaManager := &LuaManager{}
	err := filepath.Walk("../../../lua_configuration", func(path string, f os.FileInfo, err error) error {
		if !strings.Contains(path, "trafficRouting.lua") {
			return nil
		}
		if err != nil {
			t.Fatalf("Error: %s", err.Error())
		}
		script := readScript(t, path)
		dir := filepath.Dir(path)
		err = filepath.Walk(filepath.Join(dir, "testdata"), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				t.Fatalf("failed to walk current path")
				return nil
			}

			if !info.IsDir() && filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" {
				t.Logf("walking path: %s", path)
				testCase, err := getLuaTestCase(t, path)
				if err != nil {
					t.Fatalf("failed to load lua test cases")
					return err
				}
				rollout := testCase.Rollout
				steps := rollout.Spec.Strategy.Canary.Steps
				for i, step := range steps {
					data := &LuaData{
						Data: Data{
							Labels:      testCase.Original.GetLabels(),
							Annotations: testCase.Original.GetAnnotations(),
							Spec:        testCase.Original.Object["spec"],
						},
						Matches:       step.TrafficRoutingStrategy.Matches,
						CanaryWeight:  *step.TrafficRoutingStrategy.Weight,
						StableWeight:  100 - *step.TrafficRoutingStrategy.Weight,
						CanaryService: fmt.Sprintf("%s-canary", rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service),
						StableService: rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service,
					}
					unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(data)
					if err != nil {
						return err
					}
					u := &unstructured.Unstructured{Object: unObj}
					l, err := luaManager.RunLuaScript(u, script)
					if err != nil {
						t.Fatalf("failed to run lua script")
						return err
					}
					returnValue := l.Get(-1)
					if returnValue.Type() == lua.LTTable {
						jsonBytes, err := luajson.Encode(returnValue)
						if err != nil {
							t.Fatalf("failed to encode returnValue yo jsonBytes")
							return err
						}
						var nSpec Data
						err = json.Unmarshal(jsonBytes, &nSpec)
						if err != nil {
							t.Fatalf("failed to convert jsonBytes to object")
							return err
						}
						eSpec := Data{
							Spec:        testCase.Expected[i].Object["spec"],
							Annotations: testCase.Expected[i].GetAnnotations(),
							Labels:      testCase.Expected[i].GetLabels(),
						}
						if util.DumpJSON(eSpec) != util.DumpJSON(nSpec) {
							t.Fatalf("expect %s, but get %s", util.DumpJSON(eSpec), util.DumpJSON(nSpec))
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Error walking lua_configuration: %s", err.Error())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Error walking lua_configuration: %s", err.Error())
	}
}

func readScript(t *testing.T, path string) string {
	data, err := ioutil.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("failed to read lua script")
	}
	return string(data)
}

func getLuaTestCase(t *testing.T, path string) (*TestCase, error) {
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s", path)
		return nil, err
	}
	luaTestCase := &TestCase{}
	err = yaml.Unmarshal(yamlFile, luaTestCase)
	if err != nil {
		t.Fatalf("test case %s format error", path)
		return nil, err
	}
	return luaTestCase, nil
}
