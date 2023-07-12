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
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"

	"github.com/openkruise/rollouts/api/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	OriginalSpecAnnotation = "rollouts.kruise.io/origin-spec-configuration"
	LuaConfigMap           = "kruise-rollout-configuration"
)

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
	TrafficConf []v1alpha1.NetworkRef
	OwnerRef    metav1.OwnerReference
}

func NewCustomController(client client.Client, conf Config) (network.NetworkProvider, error) {
	r := &customController{
		Client:     client,
		conf:       conf,
		luaManager: &luamanager.LuaManager{},
		luaScript:  map[string]string{},
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
		// check if lua script exists
		if _, ok := r.luaScript[ref.Kind]; !ok {
			script := r.getLuascript(ctx, ref)
			if script == "" {
				klog.Errorf("failed to get lua script for %s", ref.Kind)
				return nil
			}
			// is it necessary to consider same kind but different apiversion?
			r.luaScript[ref.Kind] = script
		}
		annotations := obj.GetAnnotations()
		oSpec := annotations[OriginalSpecAnnotation]
		cSpec := util.DumpJSON(obj.Object["spec"])
		if oSpec == cSpec {
			continue
		}
		if err := r.storeObject(obj); err != nil {
			klog.Errorf("failed to store object: %s/%s", ref.Kind, ref.Name)
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
		specStr := obj.GetAnnotations()[OriginalSpecAnnotation]
		var oSpec interface{}
		_ = json.Unmarshal([]byte(specStr), &oSpec)
		nSpec, err := r.executeLuaForCanary(oSpec, strategy, r.luaScript[ref.Kind])
		if err != nil {
			return false, err
		}
		// cannot use reflect.DeepEqual since json.Unmarshal convert number to float64
		if util.DumpJSON(nSpec) == util.DumpJSON(obj.Object["spec"]) {
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
		if err := r.restoreObject(obj); err != nil {
			klog.Errorf("failed to restore object: %s/%s", ref.Kind, ref.Name)
			return err
		}
	}
	return nil
}

// store spec of an object in OriginalSpecAnnotation
func (r *customController) storeObject(obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	oSpec := annotations[OriginalSpecAnnotation]
	cSpec := util.DumpJSON(obj.Object["spec"])
	if oSpec == cSpec {
		return nil
	}
	annotations[OriginalSpecAnnotation] = cSpec
	obj.SetAnnotations(annotations)
	if err := r.Update(context.TODO(), obj); err != nil {
		return err
	}
	return nil
}

// restore an object from spec stored in OriginalSpecAnnotation
func (r *customController) restoreObject(obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	if annotations[OriginalSpecAnnotation] == "" {
		return nil
	}
	specStr := annotations[OriginalSpecAnnotation]
	var oSpec interface{}
	_ = json.Unmarshal([]byte(specStr), &oSpec)
	obj.Object["spec"] = oSpec
	delete(annotations, OriginalSpecAnnotation)
	obj.SetAnnotations(annotations)
	if err := r.Update(context.TODO(), obj); err != nil {
		return err
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
		CanaryWeight  int32
		StableWeight  int32
		Matches       []rolloutv1alpha1.HttpRouteMatch
		CanaryService string
		StableService string
	}
	data := &LuaData{
		Spec:          spec,
		CanaryWeight:  *weight,
		StableWeight:  100 - *weight,
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

func (r *customController) getLuascript(ctx context.Context, ref v1alpha1.NetworkRef) string {
	// get local lua script
	// luaScript.Provider: CRDGroupt/Kind
	group := strings.Split(ref.APIVersion, "/")[0]
	key := fmt.Sprintf("lua_configuration/%s/trafficRouting.lua", fmt.Sprintf("%s/%s", group, ref.Kind))
	script := util.GetLuaConfigurationContent(key)
	if script != "" {
		return script
	}

	// if lua script is not found locally, then try ConfigMap
	nameSpace := util.GetRolloutNamespace() // kruise-rollout
	name := LuaConfigMap
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: nameSpace, Name: name}, configMap)
	if err != nil {
		klog.Errorf("failed to get configMap %s/%s", nameSpace, name)
	} else {
		// in format like "lua.traffic.routing.ingress.aliyun-alb"
		key = fmt.Sprintf("%s.%s.%s", configuration.LuaTrafficRoutingIngressTypePrefix, ref.Kind, group)
		if script, ok := configMap.Data[key]; ok {
			return script
		}
	}
	return ""
}
