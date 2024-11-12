/*
Copyright 2023 The Kruise Authors.

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
	"strings"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const (
	OriginalSpecAnnotation = "rollouts.kruise.io/original-spec-configuration"
	LuaConfigMap           = "kruise-rollout-configuration"
)

type LuaData struct {
	Data                  Data
	CanaryWeight          int32
	StableWeight          int32
	Matches               []v1beta1.HttpRouteMatch
	CanaryService         string
	StableService         string
	RequestHeaderModifier *gatewayv1beta1.HTTPHeaderFilter
}
type Data struct {
	Spec        interface{}       `json:"spec,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type customController struct {
	client.Client
	conf       Config
	luaManager *luamanager.LuaManager
}

type Config struct {
	Key           string
	RolloutNs     string
	CanaryService string
	StableService string
	// network providers need to be created
	TrafficConf                  []v1beta1.ObjectRef
	OwnerRef                     metav1.OwnerReference
	DisableGenerateCanaryService bool
}

func NewCustomController(client client.Client, conf Config) (network.NetworkProvider, error) {
	r := &customController{
		Client:     client,
		conf:       conf,
		luaManager: &luamanager.LuaManager{},
	}
	return r, nil
}

// when initializing, first check lua and get all custom providers, then store custom providers
func (r *customController) Initialize(ctx context.Context) error {
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			klog.Errorf("failed to get custom network provider %s(%s/%s): %s", ref.Kind, r.conf.RolloutNs, ref.Name, err.Error())
			return err
		}

		// check if lua script exists
		_, err := r.getLuaScript(ctx, ref)
		if err != nil {
			klog.Errorf("failed to get lua script for custom network provider %s(%s/%s): %s", ref.Kind, r.conf.RolloutNs, ref.Name, err.Error())
			return err
		}
	}
	return nil
}

// when ensuring routes, first execute lua for all custom providers, then update
func (r *customController) EnsureRoutes(ctx context.Context, strategy *v1beta1.TrafficRoutingStrategy) (bool, error) {
	done := true
	var err error
	customNetworkRefList := make([]*unstructured.Unstructured, len(r.conf.TrafficConf))

	// first get all custom network provider object
	for i, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			return false, err
		}
		customNetworkRefList[i] = obj
	}

	// check if original configuration is stored in annotation, store it if not.
	for i := 0; i < len(customNetworkRefList); i++ {
		obj := customNetworkRefList[i]
		if _, ok := obj.GetAnnotations()[OriginalSpecAnnotation]; !ok {
			err := r.storeObject(obj)
			if err != nil {
				klog.Errorf("failed to store custom network provider %s(%s/%s): %s", customNetworkRefList[i].GetKind(), r.conf.RolloutNs, customNetworkRefList[i].GetName(), err.Error())
				return false, err
			}
		}
	}

	// first execute lua for new spec
	nSpecList := make([]Data, len(r.conf.TrafficConf))
	for i := 0; i < len(customNetworkRefList); i++ {
		obj := customNetworkRefList[i]
		ref := r.conf.TrafficConf[i]
		specStr := obj.GetAnnotations()[OriginalSpecAnnotation]
		if specStr == "" {
			return false, fmt.Errorf("failed to get original spec from annotation for %s(%s/%s)", ref.Kind, r.conf.RolloutNs, ref.Name)
		}
		var oSpec Data
		_ = json.Unmarshal([]byte(specStr), &oSpec)
		luaScript, err := r.getLuaScript(ctx, ref)
		if err != nil {
			klog.Errorf("failed to get lua script for %s(%s/%s): %s", ref.Kind, r.conf.RolloutNs, ref.Name, err.Error())
			return false, err
		}
		nSpec, err := r.executeLuaForCanary(oSpec, strategy, luaScript)
		if err != nil {
			klog.Errorf("failed to execute lua for %s(%s/%s): %s", ref.Kind, r.conf.RolloutNs, ref.Name, err.Error())
			return false, err
		}
		nSpecList[i] = nSpec
	}

	// update CustomNetworkRefs then
	for i := 0; i < len(nSpecList); i++ {
		nSpec := nSpecList[i]
		updated, err := r.compareAndUpdateObject(nSpec, customNetworkRefList[i])
		if err != nil {
			klog.Errorf("failed to update object %s(%s/%s) when ensure routes: %s", customNetworkRefList[i].GetKind(), r.conf.RolloutNs, customNetworkRefList[i].GetName(), err.Error())
			return false, err
		}
		if updated {
			done = false
		}
	}
	return done, nil
}

func (r *customController) Finalise(ctx context.Context) (bool, error) {
	modified := false
	errList := field.ErrorList{}
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			if errors.IsNotFound(err) {
				klog.Infof("custom network provider %s(%s/%s) not found when finalising", ref.Kind, r.conf.RolloutNs, ref.Name)
				continue
			}
			errList = append(errList, field.InternalError(field.NewPath("GetCustomNetworkProvider"), err))
			klog.Errorf("failed to get %s(%s/%s) when finalising, process next first", ref.Kind, r.conf.RolloutNs, ref.Name)
			continue
		}
		if updated, err := r.restoreObject(obj); err != nil {
			errList = append(errList, field.InternalError(field.NewPath("RestoreCustomNetworkProvider"), err))
			klog.Errorf("failed to restore %s(%s/%s) when finalising: %s", ref.Kind, r.conf.RolloutNs, ref.Name, err.Error())
		} else if updated {
			modified = true
		}
	}

	return modified, errList.ToAggregate()
}

// store spec of an object in OriginalSpecAnnotation
func (r *customController) storeObject(obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	labels := obj.GetLabels()
	oSpecStr := annotations[OriginalSpecAnnotation]
	delete(annotations, OriginalSpecAnnotation)
	data := Data{
		Spec:        obj.Object["spec"],
		Labels:      labels,
		Annotations: annotations,
	}
	cSpecStr := util.DumpJSON(data)
	if oSpecStr == cSpecStr {
		return nil
	}
	annotations[OriginalSpecAnnotation] = cSpecStr
	obj.SetAnnotations(annotations)
	if err := r.Update(context.TODO(), obj); err != nil {
		klog.Errorf("failed to store custom network provider %s(%s/%s): %s", obj.GetKind(), r.conf.RolloutNs, obj.GetName(), err.Error())
		return err
	}
	klog.Infof("store old configuration of custom network provider %s(%s/%s) in annotation(%s) success", obj.GetKind(), r.conf.RolloutNs, obj.GetName(), OriginalSpecAnnotation)
	return nil
}

// restore an object from spec stored in OriginalSpecAnnotation
func (r *customController) restoreObject(obj *unstructured.Unstructured) (modified bool, err error) {
	annotations := obj.GetAnnotations()
	if annotations == nil || annotations[OriginalSpecAnnotation] == "" {
		klog.Infof("OriginalSpecAnnotation not found in custom network provider %s(%s/%s)", obj.GetKind(), r.conf.RolloutNs, obj.GetName())
		return false, nil
	}
	oSpecStr := annotations[OriginalSpecAnnotation]
	var oSpec Data
	_ = json.Unmarshal([]byte(oSpecStr), &oSpec)
	obj.Object["spec"] = oSpec.Spec
	obj.SetAnnotations(oSpec.Annotations)
	obj.SetLabels(oSpec.Labels)
	if err := r.Update(context.TODO(), obj); err != nil {
		klog.Errorf("failed to restore object %s(%s/%s) from annotation(%s): %s", obj.GetKind(), r.conf.RolloutNs, obj.GetName(), OriginalSpecAnnotation, err.Error())
		return false, err
	}
	klog.Infof("restore custom network provider %s(%s/%s) from annotation(%s) success", obj.GetKind(), obj.GetNamespace(), obj.GetName(), OriginalSpecAnnotation)
	return true, nil
}

func (r *customController) executeLuaForCanary(spec Data, strategy *v1beta1.TrafficRoutingStrategy, luaScript string) (Data, error) {
	var weight *int32
	if strategy.Traffic != nil {
		is := intstr.FromString(*strategy.Traffic)
		weightInt, _ := intstr.GetScaledValueFromIntOrPercent(&is, 100, true)
		weight = utilpointer.Int32(int32(weightInt))
	}
	matches := strategy.Matches
	if weight == nil {
		// the lua script does not have a pointer type,
		// so we need to pass weight=-1 to indicate the case where weight is nil.
		weight = utilpointer.Int32(-1)
	}

	data := &LuaData{
		Data:                  spec,
		CanaryWeight:          *weight,
		StableWeight:          100 - *weight,
		Matches:               matches,
		CanaryService:         r.conf.CanaryService,
		StableService:         r.conf.StableService,
		RequestHeaderModifier: strategy.RequestHeaderModifier,
	}

	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(data)
	if err != nil {
		return Data{}, err
	}
	u := &unstructured.Unstructured{Object: unObj}
	l, err := r.luaManager.RunLuaScript(u, luaScript)
	if err != nil {
		return Data{}, err
	}
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTTable {
		jsonBytes, err := luamanager.Encode(returnValue)
		if err != nil {
			return Data{}, err
		}
		var obj Data
		err = json.Unmarshal(jsonBytes, &obj)
		if err != nil {
			return Data{}, err
		}
		return obj, nil
	}
	return Data{}, fmt.Errorf("expect table output from Lua script, not %s", returnValue.Type().String())
}

func (r *customController) getLuaScript(ctx context.Context, ref v1beta1.ObjectRef) (string, error) {
	// get local lua script
	// luaScript.Provider: CRDGroupt/Kind
	group := strings.Split(ref.APIVersion, "/")[0]
	key := fmt.Sprintf("lua_configuration/%s/trafficRouting.lua", fmt.Sprintf("%s/%s", group, ref.Kind))
	script := util.GetLuaConfigurationContent(key)
	if script != "" {
		return script, nil
	}

	// if lua script is not found locally, then try ConfigMap
	nameSpace := util.GetRolloutNamespace() // kruise-rollout
	name := LuaConfigMap
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: nameSpace, Name: name}, configMap)
	if err != nil {
		return "", fmt.Errorf("failed to get ConfigMap(%s/%s)", nameSpace, name)
	} else {
		// in format like "lua.traffic.routing.ingress.aliyun-alb"
		key = fmt.Sprintf("%s.%s.%s", configuration.LuaTrafficRoutingCustomTypePrefix, ref.Kind, group)
		if script, ok := configMap.Data[key]; ok {
			return script, nil
		} else {
			return "", fmt.Errorf("expected script not found neither locally nor in ConfigMap")
		}
	}
}

// compare and update obj, return whether the obj is updated
func (r *customController) compareAndUpdateObject(data Data, obj *unstructured.Unstructured) (bool, error) {
	spec := data.Spec
	annotations := data.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[OriginalSpecAnnotation] = obj.GetAnnotations()[OriginalSpecAnnotation]
	labels := data.Labels
	if util.DumpJSON(obj.Object["spec"]) == util.DumpJSON(spec) &&
		reflect.DeepEqual(obj.GetAnnotations(), annotations) &&
		reflect.DeepEqual(obj.GetLabels(), labels) {
		return false, nil
	}
	nObj := obj.DeepCopy()
	nObj.Object["spec"] = spec
	nObj.SetAnnotations(annotations)
	nObj.SetLabels(labels)
	if err := r.Update(context.TODO(), nObj); err != nil {
		klog.Errorf("failed to update custom network provider %s(%s/%s) from (%s) to (%s)", nObj.GetKind(), r.conf.RolloutNs, nObj.GetName(), util.DumpJSON(obj), util.DumpJSON(nObj))
		return false, err
	}
	klog.Infof("update custom network provider %s(%s/%s) from (%s) to (%s) success", nObj.GetKind(), r.conf.RolloutNs, nObj.GetName(), util.DumpJSON(obj), util.DumpJSON(nObj))
	return true, nil
}
