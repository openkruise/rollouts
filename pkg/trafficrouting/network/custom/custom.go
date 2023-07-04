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

	"k8s.io/apimachinery/pkg/api/errors"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const OriginSpecConfigurationAnnotation = "rollouts.kruise.io/origin-spec-configuration"

type NetworkTrafficRouting struct {
	// API Version of the referent
	APIVersion string `json:"apiVersion"`
	// Kind of the referent
	Kind string `json:"kind"`
	// Name of the referent
	Name string `json:"name"`
	// Name of the lua script
	Lua string `json:"lua"`
}

type customController struct {
	client.Client
	conf       Config
	luaManager *luamanager.LuaManager
	luaScript  map[string]string
}

type Config struct {
	RolloutName   string
	RolloutNs     string
	CanaryService string
	StableService string
	// network providers need to be created
	TrafficConf []NetworkTrafficRouting
	OwnerRef    metav1.OwnerReference
}

func NewCustomController(client client.Client, conf Config, lua map[string]string) (network.NetworkProvider, error) {
	r := &customController{
		Client:     client,
		conf:       conf,
		luaManager: &luamanager.LuaManager{},
		luaScript:  lua,
	}
	return r, nil
}

func (r *customController) Initialize(ctx context.Context) error {
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			return err
		}
		annotations := obj.GetAnnotations()
		oSpec := annotations[OriginSpecConfigurationAnnotation]
		cSpec := util.DumpJSON(obj.Object["spec"])
		if oSpec == cSpec {
			continue
		}
		if annotations == nil {
			annotations = map[string]string{}
		}
		// record origin object.spec in annotations[OriginSpecConfigurationAnnotation]
		annotations[OriginSpecConfigurationAnnotation] = cSpec
		obj.SetAnnotations(annotations)
		if err := r.Update(context.TODO(), obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *customController) EnsureRoutes(ctx context.Context, strategy *rolloutv1alpha1.TrafficRoutingStrategy) (bool, error) {
	var err error
	var done = true
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			return false, err
		}
		// origin spec configuration
		specStr := obj.GetAnnotations()[OriginSpecConfigurationAnnotation]
		var oSpec interface{}
		_ = json.Unmarshal([]byte(specStr), &oSpec)
		nSpec, err := r.executeLuaForCanary(oSpec, strategy, r.luaScript[ref.Lua])
		if err != nil {
			return false, err
		}
		if reflect.DeepEqual(obj.Object["spec"], nSpec) {
			continue
		}
		obj.Object["spec"] = nSpec
		if err = r.Update(context.TODO(), obj); err != nil {
			return false, err
		}
		done = false
	}
	return done, nil
}

func (r *customController) Finalise(ctx context.Context) error {
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
		annotations := obj.GetAnnotations()
		if annotations[OriginSpecConfigurationAnnotation] == "" {
			continue
		}
		// origin spec configuration
		specStr := annotations[OriginSpecConfigurationAnnotation]
		var oSpec interface{}
		_ = json.Unmarshal([]byte(specStr), &oSpec)
		obj.Object["spec"] = oSpec
		delete(annotations, OriginSpecConfigurationAnnotation)
		obj.SetAnnotations(annotations)
		if err := r.Update(context.TODO(), obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *customController) executeLuaForCanary(spec interface{}, strategy *rolloutv1alpha1.TrafficRoutingStrategy, luaScript string) (interface{}, error) {
	weight := strategy.Weight
	matches := strategy.Matches
	if weight == nil {
		// the lua script does not have a pointer type,
		// so we need to pass weight=-1 to indicate the case where weight is nil.
		weight = utilpointer.Int32(-1)
	}
	type LuaData struct {
		Spec          interface{}
		CanaryWeight  string
		StableWeight  string
		Matches       []rolloutv1alpha1.HttpRouteMatch
		CanaryService string
		StableService string
	}
	data := &LuaData{
		Spec:          spec,
		CanaryWeight:  fmt.Sprintf("%d", *weight),
		StableWeight:  fmt.Sprintf("%d", 100-*weight),
		Matches:       matches,
		CanaryService: r.conf.CanaryService,
		StableService: r.conf.StableService,
	}

	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(data)
	if err != nil {
		return nil, err
	}
	u := &unstructured.Unstructured{Object: unObj}
	l, err := r.luaManager.RunLuaScript(u, luaScript)
	if err != nil {
		return nil, err
	}
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTTable {
		jsonBytes, err := luamanager.Encode(returnValue)
		if err != nil {
			return nil, err
		}
		var obj interface{}
		err = json.Unmarshal(jsonBytes, &obj)
		if err != nil {
			return nil, err
		}
		return obj, nil
	}
	return nil, fmt.Errorf("expect table output from Lua script, not %s", returnValue.Type().String())
}
