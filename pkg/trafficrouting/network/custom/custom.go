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

func (r *customController) Initialize(ctx context.Context) error {
	for _, ref := range r.conf.TrafficConf {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: ref.Name}, obj); err != nil {
			klog.Errorf("failed to get custom network provider %s/%s", ref.Kind, ref.Name)
			return err
		}
		// check if lua script exists
		_, err := r.getLuaScript(ctx, ref)
		if err != nil {
			klog.Errorf("failed to get lua script for custom network provider %s: %s", ref.Kind, err.Error())
			return err
		}
		if err := r.storeObject(obj); err != nil {
			klog.Errorf("failed to store custom network provider %s/%s", ref.Kind, ref.Name)
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
		if specStr == "" {
			continue
		}
		var oSpec Data
		_ = json.Unmarshal([]byte(specStr), &oSpec)
		luaScript, err := r.getLuaScript(ctx, ref)
		if err != nil {
			klog.Errorf("failed to get lua script for %s", ref.Kind)
			return false, err
		}
		nSpec, err := r.executeLuaForCanary(oSpec, strategy, luaScript)
		if err != nil {
			return false, err
		}
		if cmpAndSetObject(nSpec, obj) {
			continue
		}
		if err = r.Update(context.TODO(), obj); err != nil {
			klog.Errorf("failed to update custom network provider")
			return false, err
		}
		klog.Infof("update custom network provider %s/%s success")
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
				klog.Infof("custom network provider %s/%s not found when finalising", ref.Kind, ref.Name)
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
	if annotations == nil {
		annotations = make(map[string]string)
	}
	labels := obj.GetLabels()
	oSpec := annotations[OriginalSpecAnnotation]
	delete(annotations, OriginalSpecAnnotation)
	data := Data{
		Spec:        obj.Object["spec"],
		Labels:      labels,
		Annotations: annotations,
	}
	cSpec := util.DumpJSON(data)
	if oSpec == cSpec {
		return nil
	}
	annotations[OriginalSpecAnnotation] = cSpec
	obj.SetAnnotations(annotations)
	if err := r.Update(context.TODO(), obj); err != nil {
		klog.Errorf("failed to store custom network provider %s/%s", obj.GetKind(), obj.GetName())
		return err
	}
	klog.Infof("store custom network provider %s/%s success", obj.GetKind(), obj.GetName())
	return nil
}

// restore an object from spec stored in OriginalSpecAnnotation
func (r *customController) restoreObject(obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	if annotations == nil || annotations[OriginalSpecAnnotation] == "" {
		return nil
	}
	specStr := annotations[OriginalSpecAnnotation]
	var oSpec Data
	_ = json.Unmarshal([]byte(specStr), &oSpec)
	obj.Object["spec"] = oSpec.Spec
	obj.SetAnnotations(oSpec.Annotations)
	obj.SetLabels(oSpec.Labels)
	if err := r.Update(context.TODO(), obj); err != nil {
		klog.Errorf("failed to restore custom network provider %s/%s", obj.GetKind(), obj.GetName())
		return err
	}
	klog.Infof("restore custom network provider %s/%s success", obj.GetKind(), obj.GetName())
	return nil
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
		return "", fmt.Errorf("failed to get configMap %s/%s", nameSpace, name)
	} else {
		// in format like "lua.traffic.routing.ingress.aliyun-alb"
		key = fmt.Sprintf("%s.%s.%s", configuration.LuaTrafficRoutingCustomTypePrefix, ref.Kind, group)
		if script, ok := configMap.Data[key]; ok {
			return script, nil
		} else if !ok {
			return "", fmt.Errorf("expected script of %s not found in ConfigMap", key)
		}
	}
	return "", nil
}

// compare and update obj, return if the obj is updated
func cmpAndSetObject(data Data, obj *unstructured.Unstructured) bool {
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
