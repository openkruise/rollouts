package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	custom "github.com/openkruise/rollouts/pkg/trafficrouting/network/customNetworkProvider"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"
)

type TestCase struct {
	Rollout        *v1beta1.Rollout             `json:"rollout,omitempty"`
	TrafficRouting *v1alpha1.TrafficRouting     `json:"trafficRouting,omitempty"`
	Original       *unstructured.Unstructured   `json:"original,omitempty"`
	Expected       []*unstructured.Unstructured `json:"expected,omitempty"`
}

// this function aims to convert testdata to lua object for debugging
// run `go run lua.go`, then this program will get all testdata and convert them into lua objects
// copy the generated objects to lua scripts and then you can start debugging your lua scripts
func main() {
	err := convertTestCaseToLuaObject()
	if err != nil {
		fmt.Println(err)
	}
}

func convertTestCaseToLuaObject() error {
	err := filepath.Walk("./", func(path string, f os.FileInfo, err error) error {
		if !strings.Contains(path, "trafficRouting.lua") {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to walk path: %s", err.Error())
		}
		dir := filepath.Dir(path)
		if _, err := os.Stat(filepath.Join(dir, "testdata")); err != nil {
			fmt.Printf("testdata not found in %s\n", dir)
			return nil
		}
		err = filepath.Walk(filepath.Join(dir, "testdata"), func(path string, info os.FileInfo, err error) error {
			if !info.IsDir() && filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" {
				fmt.Printf("--- walking path: %s ---\n", path)
				err = objectToTable(path)
				if err != nil {
					return fmt.Errorf("failed to convert object to table: %s", err)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to walk path: %s", err.Error())
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk path: %s", err)
	}
	return nil
}

// convert a testcase object to lua table for debug
func objectToTable(path string) error {
	dir, file := filepath.Split(path)
	testCase, err := getLuaTestCase(path)
	if err != nil {
		return fmt.Errorf("failed to get lua testcase: %s", err)
	}
	uList := make(map[string]interface{})
	rollout := testCase.Rollout
	trafficRouting := testCase.TrafficRouting
	if rollout != nil {
		steps := rollout.Spec.Strategy.GetSteps()
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
			stableService := rollout.Spec.Strategy.GetTrafficRouting()[0].Service
			canaryService = fmt.Sprintf("%s-canary", stableService)
			data := &custom.LuaData{
				Data: custom.Data{
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
			uList[fmt.Sprintf("step_%d", i)] = data
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
		data := &custom.LuaData{
			Data: custom.Data{
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
		uList["steps_0"] = data
	} else {
		return fmt.Errorf("neither rollout nor trafficRouting defined in test case: %s", path)
	}

	objStr, err := executeLua(uList)
	if err != nil {
		return fmt.Errorf("failed to execute lua: %s", err.Error())
	}
	filePath := fmt.Sprintf("%s%s_obj.lua", dir, strings.Split(file, ".")[0])
	fileStream, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0666)
	if err != nil {
		return fmt.Errorf("failed to open file: %s", err)
	}
	defer fileStream.Close()
	header := "-- THIS IS GENERATED BY CONVERT_TEST_CASE_TO_LUA_OBJECT.GO FOR DEBUGGING --\n"
	_, err = io.WriteString(fileStream, header+objStr)
	if err != nil {
		return fmt.Errorf("failed to WriteString %s", err)
	}
	return nil
}

func getLuaTestCase(path string) (*TestCase, error) {
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	luaTestCase := &TestCase{}
	err = yaml.Unmarshal(yamlFile, luaTestCase)
	if err != nil {
		return nil, err
	}
	return luaTestCase, nil
}

func executeLua(steps map[string]interface{}) (string, error) {
	luaManager := &luamanager.LuaManager{}
	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&steps)
	if err != nil {
		return "", fmt.Errorf("failed to convert to unstructured: %s", err)
	}
	u := &unstructured.Unstructured{Object: unObj}
	script := `
		function serialize(obj, isKey)
			local lua = ""  
			local t = type(obj)  
			if t == "number" then  
				lua = lua .. obj  
			elseif t == "boolean" then  
				lua = lua .. tostring(obj)  
			elseif t == "string" then
				if isKey then
					lua = lua .. string.format("%s", obj) 
				else
					lua = lua .. string.format("%q", obj) 
				end
			elseif t == "table" then  
				lua = lua .. "{"  
				for k, v in pairs(obj) do
					if type(k) == "string" then  
						lua = lua .. serialize(k, true) .. "=" .. serialize(v, false) .. ","
					else
						lua = lua .. serialize(v, false) .. ","
					end
				end  
				local metatable = getmetatable(obj)  
				if metatable ~= nil and type(metatable.__index) == "table" then  
					for k, v in pairs(metatable.__index) do  
						if type(k) == "string" then  
							lua = lua .. serialize(k, true) .. "=" .. serialize(v, false) .. ","
						else
							lua = lua .. serialize(v, false) .. ","
						end 
					end  
				end  
				lua = lua .. "}"  
			elseif t == "nil" then  
				return nil  
			else  
				error("can not serialize a " .. t .. " type.")  
			end  
			return lua  
		end

	function table2string(tablevalue)
		local stringtable = "steps=" .. serialize(tablevalue)
		print(stringtable)
		return stringtable
	end
	return table2string(obj)
	`
	l, err := luaManager.RunLuaScript(u, script)
	if err != nil {
		return "", fmt.Errorf("failed to run lua script: %s", err)
	}
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTString {
		return returnValue.String(), nil
	} else {
		return "", fmt.Errorf("unexpected lua output type")
	}
}
