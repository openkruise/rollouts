package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	utilpointer "k8s.io/utils/pointer"
)

type LuaData struct {
	Data             Data
	CanaryWeight     int32
	StableWeight     int32
	Matches          []v1alpha1.HttpRouteMatch
	CanaryService    string
	StableService    string
	PatchPodMetadata *v1alpha1.PatchPodTemplateMetadata
}

type Data struct {
	Spec        interface{}       `json:"spec,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type TestCase struct {
	Rollout  *v1alpha1.Rollout            `json:"rollout,omitempty"`
	Original *unstructured.Unstructured   `json:"original,omitempty"`
	Expected []*unstructured.Unstructured `json:"expected,omitempty"`
}

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
	luaManager := &luamanager.LuaManager{}
	dir, file := filepath.Split(path)
	testCase, err := getLuaTestCase(path)
	if err != nil {
		return fmt.Errorf("failed to get lua testcase: %s", err)
	}
	uList := make(map[string]interface{})
	rollout := testCase.Rollout
	steps := rollout.Spec.Strategy.Canary.Steps
	for i, step := range steps {
		weight := step.TrafficRoutingStrategy.Weight
		if step.TrafficRoutingStrategy.Weight == nil {
			weight = utilpointer.Int32(-1)
		}
		var canaryService string
		stableService := rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service
		if rollout.Spec.Strategy.Canary.TrafficRoutings[0].CreateCanaryService {
			canaryService = fmt.Sprintf("%s-canary", stableService)
		} else {
			canaryService = stableService
		}
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
			PatchPodMetadata: rollout.Spec.Strategy.Canary.PatchPodTemplateMetadata,
		}
		uList[fmt.Sprintf("step_%d", i)] = data
	}
	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&uList)
	if err != nil {
		return fmt.Errorf("failed to convert to unstructured: %s", err)
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
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTString {
		filePath := fmt.Sprintf("%s%s_obj.lua", dir, strings.Split(file, ".")[0])
		file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0666)
		if err != nil {
			return fmt.Errorf("failed to open file: %s", err)
		}
		defer file.Close()
		v := returnValue.String()
		_, err = io.WriteString(file, v)
		if err != nil {
			return fmt.Errorf("failed to WriteString %s", err)
		}
	}
	if err != nil {
		return fmt.Errorf("failed to run lua script: %s", err)
	}
	return nil
}

func getLuaTestCase(path string) (*TestCase, error) {
	yamlFile, err := ioutil.ReadFile(path)
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
