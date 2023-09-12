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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"

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
	TrafficConf []rolloutv1alpha1.CustomNetworkRef
	OwnerRef    metav1.OwnerReference
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
// once fails to store a custom provider, roll back custom providers previously (assume error should not occur)
func (r *customController) Initialize(ctx context.Context) error {
	customNetworkRefList := make([]*unstructured.Unstructured, 0)
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
		customNetworkRefList = append(customNetworkRefList, obj)
	}
	for i := 0; i < len(customNetworkRefList); i++ {
		nObj := customNetworkRefList[i].DeepCopy()
		err := r.storeObject(nObj)
		if err != nil {
			klog.Errorf("failed to store custom network provider %s(%s/%s): %s", customNetworkRefList[i].GetKind(), r.conf.RolloutNs, customNetworkRefList[i].GetName(), err.Error())
			klog.Errorf("roll back custom network providers previously")
			r.rollBack(customNetworkRefList, i)
			return err
		}
		customNetworkRefList[i] = nObj
	}
	return nil
}

// when ensuring routes, first execute lua for all custom providers, then update
// once fails to update a custom provider, roll back custom providers previously (assume error should not occur)
func (r *customController) EnsureRoutes(ctx context.Context, strategy *rolloutv1alpha1.TrafficRoutingStrategy) (bool, error) {
	done := true
	// *strategy.Weight == 0 indicates traffic routing is doing finalising and tries to route whole traffic to stable service
	// then restore custom network provider directly
	if strategy.Weight != nil && *strategy.Weight == 0 {
		for _, ref := range r.conf.TrafficConf {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion(ref.APIVersion)
			obj.SetKind(ref.Kind)
			if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
				klog.Errorf("failed to get %s(%s/%s) when routing whole traffic to stable service", ref.Kind, r.conf.RolloutNs, ref.Name)
				return false, err
			}
			changed, err := r.restoreObject(obj, true)
			if err != nil {
				klog.Errorf("failed to restore %s(%s/%s) when routing whole traffic to stable service", ref.Kind, r.conf.RolloutNs, ref.Name)
				return false, err
			}
			if changed {
				done = false
			}
		}
		return done, nil
	}
	var err error
	nSpecList := make([]Data, 0)
	customNetworkRefList := make([]*unstructured.Unstructured, 0)
	// first execute lua for new spec
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			return false, err
		}
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
		nSpecList = append(nSpecList, nSpec)
		customNetworkRefList = append(customNetworkRefList, obj)
	}
	// update CustomNetworkRefs then
	for i := 0; i < len(nSpecList); i++ {
		nObj := customNetworkRefList[i].DeepCopy()
		nSpec := nSpecList[i]
		if compareAndSetObject(nSpec, nObj) {
			continue
		}
		if err = r.Update(context.TODO(), nObj); err != nil {
			klog.Errorf("failed to update custom network provider %s(%s/%s) from (%s) to (%s)", nObj.GetKind(), r.conf.RolloutNs, nObj.GetName(), util.DumpJSON(customNetworkRefList[i]), util.DumpJSON(nObj))
			// if fails to update, restore the previous CustomNetworkRefs
			klog.Errorf("roll back custom network providers previously")
			r.rollBack(customNetworkRefList, i)
			return false, err
		}
		klog.Infof("update custom network provider %s(%s/%s) from (%s) to (%s) success", nObj.GetKind(), r.conf.RolloutNs, nObj.GetName(), util.DumpJSON(customNetworkRefList[i]), util.DumpJSON(nObj))
		customNetworkRefList[i] = nObj
		done = false
	}
	return done, nil
}

func (r *customController) Finalise(ctx context.Context) error {
	done := true
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			if errors.IsNotFound(err) {
				klog.Infof("custom network provider %s(%s/%s) not found when finalising", ref.Kind, r.conf.RolloutNs, ref.Name)
				continue
			}
			klog.Errorf("failed to get %s(%s/%s) when finalising, proccess next first", ref.Kind, r.conf.RolloutNs, ref.Name)
			done = false
			continue
		}
		if _, err := r.restoreObject(obj, false); err != nil {
			done = false
			klog.Errorf("failed to restore %s(%s/%s) when finalising: %s", ref.Kind, r.conf.RolloutNs, ref.Name, err.Error())
		}
	}
	if !done {
		return fmt.Errorf("finalising work is not done")
	}
	return nil
}

// roll back custom network provider previous to failedIdx
func (r *customController) rollBack(customNetworkRefList []*unstructured.Unstructured, failedIdx int) error {
	for j := 0; j < failedIdx; j++ {
		if _, err := r.restoreObject(customNetworkRefList[j].DeepCopy(), true); err != nil {
			klog.Errorf("failed to roll back custom network provider %s(%s/%s): %s", customNetworkRefList[j].GetKind(), r.conf.RolloutNs, customNetworkRefList[j].GetName(), err.Error())
			continue
		}
		klog.Infof("roll back custom network provider %s(%s/%s) success", customNetworkRefList[j].GetKind(), r.conf.RolloutNs, customNetworkRefList[j].GetName())
	}
	return nil
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

// restore an object from spec stored in OriginalSpecAnnotation, needStoreOriginalSpec indicates whether the original spec should be stored
// if needStoreOriginalSpec is false, indicates traffic routing is doing finalising
// if needStoreOriginalSpec is true, indicates traffic routing failed when ensuring routes and is rolling back or in finalising
func (r *customController) restoreObject(obj *unstructured.Unstructured, needStoreOriginalSpec bool) (bool, error) {
	changed := false
	annotations := obj.GetAnnotations()
	if annotations == nil || annotations[OriginalSpecAnnotation] == "" {
		klog.Infof("OriginalSpecAnnotation not found in custom network provider %s(%s/%s)")
		return changed, nil
	}
	oSpecStr := annotations[OriginalSpecAnnotation]
	var oSpec Data
	_ = json.Unmarshal([]byte(oSpecStr), &oSpec)
	if needStoreOriginalSpec {
		// when the traffic routing is rolling back, first check whether current spec equals to original spec, if equals, then just return
		// this is not a concern when traffic routing is doing finalising, since the OriginalSpecAnnotation should be removed and network provider always need to be updated
		delete(annotations, OriginalSpecAnnotation)
		data := Data{
			Spec:        obj.Object["spec"],
			Labels:      obj.GetLabels(),
			Annotations: annotations,
		}
		cSpecStr := util.DumpJSON(data)
		if oSpecStr == cSpecStr {
			return changed, nil
		}
		oSpec.Annotations[OriginalSpecAnnotation] = oSpecStr
	}
	obj.Object["spec"] = oSpec.Spec
	obj.SetAnnotations(oSpec.Annotations)
	obj.SetLabels(oSpec.Labels)
	if err := r.Update(context.TODO(), obj); err != nil {
		klog.Errorf("failed to restore object %s(%s/%s) from annotation(%s): %s", obj.GetKind(), r.conf.RolloutNs, obj.GetName(), OriginalSpecAnnotation, err.Error())
		return changed, err
	}
	changed = true
	klog.Infof("restore custom network provider %s(%s/%s) from annotation(%s) success", obj.GetKind(), obj.GetNamespace(), obj.GetName(), OriginalSpecAnnotation)
	return changed, nil
}

func (r *customController) executeLuaForCanary(spec Data, strategy *rolloutv1alpha1.TrafficRoutingStrategy, luaScript string) (Data, error) {
	weight := strategy.Weight
	matches := strategy.Matches
	if weight == nil {
		// the lua script does not have a pointer type,
		// so we need to pass weight=-1 to indicate the case where weight is nil.
		weight = utilpointer.Int32(-1)
	}
	data := &LuaData{
		Data:          spec,
		CanaryWeight:  *weight,
		StableWeight:  100 - *weight,
		Matches:       matches,
		CanaryService: r.conf.CanaryService,
		StableService: r.conf.StableService,
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

func (r *customController) getLuaScript(ctx context.Context, ref rolloutv1alpha1.CustomNetworkRef) (string, error) {
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
		} else if !ok {
			return "", fmt.Errorf("expected script not found neither locally nor in ConfigMap")
		}
	}
	return "", nil
}

// compare and update obj, return whether the obj is updated
func compareAndSetObject(data Data, obj *unstructured.Unstructured) bool {
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
		return true
	}
	obj.Object["spec"] = spec
	obj.SetAnnotations(annotations)
	obj.SetLabels(labels)
	return false
}
