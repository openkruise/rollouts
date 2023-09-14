package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network/custom"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	utilpointer "k8s.io/utils/pointer"
)

type TestCase struct {
	Rollout        *v1alpha1.Rollout            `json:"rollout,omitempty"`
	TrafficRouting *v1alpha1.TrafficRouting     `json:"trafficRouting,omitempty"`
	Original       *unstructured.Unstructured   `json:"original,omitempty"`
	Expected       []*unstructured.Unstructured `json:"expected,omitempty"`
}

// this function aims to convert testdata to lua object for debugging
// run `go run lua.go`, then this program will get all testdata and convert them into lua objects
// copy the generated objects to lua scripts and then you can start debugging your lua scripts
func main() {
	err := PathWalk()
	if err != nil {
		fmt.Println(err)
	}
}

func PathWalk() error {
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
				err = ObjectToTable(path)
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
func ObjectToTable(path string) error {
	dir, file := filepath.Split(path)
	testCase, err := getLuaTestCase(path)
	if err != nil {
		return fmt.Errorf("failed to get lua testcase: %s", err)
	}
	uList := make(map[string]interface{})
	rollout := testCase.Rollout
	trafficRouting := testCase.TrafficRouting
	if rollout != nil {
		steps := rollout.Spec.Strategy.Canary.Steps
		for i, step := range steps {
			weight := step.TrafficRoutingStrategy.Weight
			if step.TrafficRoutingStrategy.Weight == nil {
				weight = utilpointer.Int32(-1)
			}
			var canaryService string
			stableService := rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service
			canaryService = fmt.Sprintf("%s-canary", stableService)
			data := &custom.LuaData{
				Data: custom.Data{
					Labels:      testCase.Original.GetLabels(),
					Annotations: testCase.Original.GetAnnotations(),
					Spec:        testCase.Original.Object["spec"],
				},
				Matches:       step.TrafficRoutingStrategy.Matches,
				CanaryWeight:  *weight,
				StableWeight:  100 - *weight,
				CanaryService: canaryService,
				StableService: stableService,
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
		data := &custom.LuaData{
			Data: custom.Data{
				Labels:      testCase.Original.GetLabels(),
				Annotations: testCase.Original.GetAnnotations(),
				Spec:        testCase.Original.Object["spec"],
			},
			Matches:       trafficRouting.Spec.Strategy.Matches,
			CanaryWeight:  *weight,
			StableWeight:  100 - *weight,
			CanaryService: canaryService,
			StableService: stableService,
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
	_, err = io.WriteString(fileStream, objStr)
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
