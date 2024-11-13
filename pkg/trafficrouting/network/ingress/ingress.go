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

package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/configuration"
	"github.com/openkruise/rollouts/pkg/util/luamanager"
	lua "github.com/yuin/gopher-lua"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

type ingressController struct {
	client.Client
	conf              Config
	luaManager        *luamanager.LuaManager
	canaryIngressName string
	luaScript         string
}

type Config struct {
	// only for log info
	Key           string
	Namespace     string
	CanaryService string
	StableService string
	TrafficConf   *v1beta1.IngressTrafficRouting
	OwnerRef      metav1.OwnerReference
}

func NewIngressTrafficRouting(client client.Client, conf Config) (network.NetworkProvider, error) {
	r := &ingressController{
		Client:            client,
		conf:              conf,
		canaryIngressName: defaultCanaryIngressName(conf.TrafficConf.Name),
		luaManager:        &luamanager.LuaManager{},
	}
	luaScript, err := r.getTrafficRoutingIngressLuaScript(conf.TrafficConf.ClassType)
	if err != nil {
		return nil, err
	}
	r.luaScript = luaScript
	return r, nil
}

// Initialize only determine if the network resources(ingress & gateway api) exist.
// If error is nil, then the network resources exist.
func (r *ingressController) Initialize(ctx context.Context) error {
	ingress := &netv1.Ingress{}
	return r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: r.conf.TrafficConf.Name}, ingress)
}

func (r *ingressController) EnsureRoutes(ctx context.Context, strategy *v1beta1.TrafficRoutingStrategy) (bool, error) {
	var weight *int32
	if strategy.Traffic != nil {
		is := intstr.FromString(*strategy.Traffic)
		weightInt, _ := intstr.GetScaledValueFromIntOrPercent(&is, 100, true)
		weight = utilpointer.Int32(int32(weightInt))
	}
	matches := strategy.Matches
	headerModifier := strategy.RequestHeaderModifier

	canaryIngress := &netv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: defaultCanaryIngressName(r.conf.TrafficConf.Name)}, canaryIngress)
	if errors.IsNotFound(err) {
		// finalizer scenario, canary ingress maybe not found
		if weight != nil && *weight == 0 {
			return true, nil
		}
		// create canary ingress
		ingress := &netv1.Ingress{}
		err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: r.conf.TrafficConf.Name}, ingress)
		if err != nil {
			return false, err
		}
		// build and create canary ingress
		canaryIngress = r.buildCanaryIngress(ingress)
		canaryIngress.Annotations, err = r.executeLuaForCanary(canaryIngress.Annotations, utilpointer.Int32(0), nil, nil)
		if err != nil {
			klog.Errorf("%s execute lua failed: %s", r.conf.Key, err.Error())
			return false, err
		}
		if err = r.Create(ctx, canaryIngress); err != nil {
			klog.Errorf("%s create canary ingress failed: %s", r.conf.Key, err.Error())
			return false, err
		}
		klog.Infof("%s create canary ingress(%s) success", r.conf.Key, util.DumpJSON(canaryIngress))
		return false, nil
	} else if err != nil {
		klog.Errorf("%s get canary ingress failed: %s", r.conf.Key, err.Error())
		return false, err
	}
	newAnnotations, err := r.executeLuaForCanary(canaryIngress.Annotations, weight, matches, headerModifier)
	if err != nil {
		klog.Errorf("%s execute lua failed: %s", r.conf.Key, err.Error())
		return false, err
	}
	if reflect.DeepEqual(canaryIngress.Annotations, newAnnotations) {
		return true, nil
	}
	byte1, _ := json.Marshal(metav1.ObjectMeta{Annotations: canaryIngress.Annotations})
	byte2, _ := json.Marshal(metav1.ObjectMeta{Annotations: newAnnotations})
	patch, err := jsonpatch.CreateMergePatch(byte1, byte2)
	if err != nil {
		klog.Errorf("%s create merge patch failed: %s", r.conf.Key, err.Error())
		return false, err
	}
	body := fmt.Sprintf(`{"metadata":%s}`, string(patch))
	if err = r.Patch(ctx, canaryIngress, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
		klog.Errorf("%s set canary ingress(%s) failed: %s", r.conf.Key, canaryIngress.Name, err.Error())
		return false, err
	}
	klog.Infof("%s set canary ingress(%s) annotations(%s) success", r.conf.Key, canaryIngress.Name, util.DumpJSON(newAnnotations))
	return false, nil
}

func (r *ingressController) Finalise(ctx context.Context) (bool, error) {
	canaryIngress := &netv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: r.canaryIngressName}, canaryIngress)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("%s get canary ingress(%s) failed: %s", r.conf.Key, r.canaryIngressName, err.Error())
		return false, err
	}
	if errors.IsNotFound(err) || !canaryIngress.DeletionTimestamp.IsZero() {
		return false, nil
	}
	// immediate delete canary ingress
	if err = r.Delete(ctx, canaryIngress); err != nil {
		klog.Errorf("%s remove canary ingress(%s) failed: %s", r.conf.Key, canaryIngress.Name, err.Error())
		return false, err
	}
	klog.Infof("%s remove canary ingress(%s) success", r.conf.Key, canaryIngress.Name)
	return true, nil
}

func (r *ingressController) buildCanaryIngress(stableIngress *netv1.Ingress) *netv1.Ingress {
	desiredCanaryIngress := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.canaryIngressName,
			Namespace:   stableIngress.Namespace,
			Annotations: stableIngress.Annotations,
			Labels:      stableIngress.Labels,
		},
		Spec: netv1.IngressSpec{
			Rules:            make([]netv1.IngressRule, 0),
			IngressClassName: stableIngress.Spec.IngressClassName,
			TLS:              stableIngress.Spec.TLS,
		},
	}
	hosts := sets.NewString()
	// Ensure canaryIngress is owned by this Rollout for cleanup
	desiredCanaryIngress.SetOwnerReferences([]metav1.OwnerReference{r.conf.OwnerRef})
	// Copy only the rules which reference the stableService from the stableIngress to the canaryIngress
	// and change service backend to canaryService. Rules **not** referencing the stableIngress will be ignored.
	for ir := 0; ir < len(stableIngress.Spec.Rules); ir++ {
		var hasStableServiceBackendRule bool
		stableRule := stableIngress.Spec.Rules[ir]
		canaryRule := netv1.IngressRule{
			Host: stableRule.Host,
			IngressRuleValue: netv1.IngressRuleValue{
				HTTP: &netv1.HTTPIngressRuleValue{},
			},
		}
		// Update all backends pointing to the stableService to point to the canaryService now
		for ip := 0; ip < len(stableRule.HTTP.Paths); ip++ {
			if stableRule.HTTP.Paths[ip].Backend.Service.Name == r.conf.StableService {
				hasStableServiceBackendRule = true
				if stableRule.Host != "" {
					hosts.Insert(stableRule.Host)
				}
				canaryPath := netv1.HTTPIngressPath{
					Path:     stableRule.HTTP.Paths[ip].Path,
					PathType: stableRule.HTTP.Paths[ip].PathType,
					Backend:  stableRule.HTTP.Paths[ip].Backend,
				}
				canaryPath.Backend.Service.Name = r.conf.CanaryService
				canaryRule.HTTP.Paths = append(canaryRule.HTTP.Paths, canaryPath)
			}
		}
		// If this rule was using the specified stableService backend, append it to the canary Ingress spec
		if hasStableServiceBackendRule {
			desiredCanaryIngress.Spec.Rules = append(desiredCanaryIngress.Spec.Rules, canaryRule)
		}
	}

	return desiredCanaryIngress
}

func defaultCanaryIngressName(name string) string {
	return fmt.Sprintf("%s-canary", name)
}

func (r *ingressController) executeLuaForCanary(annotations map[string]string, weight *int32, matches []v1beta1.HttpRouteMatch,
	headerModifier *gatewayv1beta1.HTTPHeaderFilter) (map[string]string, error) {

	if weight == nil {
		// the lua script does not have a pointer type,
		// so we need to pass weight=-1 to indicate the case where weight is nil.
		weight = utilpointer.Int32(-1)
	}
	type LuaData struct {
		Annotations           map[string]string
		Weight                string
		Matches               []v1beta1.HttpRouteMatch
		CanaryService         string
		RequestHeaderModifier *gatewayv1beta1.HTTPHeaderFilter
	}
	data := &LuaData{
		Annotations:           annotations,
		Weight:                fmt.Sprintf("%d", *weight),
		Matches:               matches,
		CanaryService:         r.conf.CanaryService,
		RequestHeaderModifier: headerModifier,
	}
	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(data)
	if err != nil {
		return nil, err
	}
	obj := &unstructured.Unstructured{Object: unObj}
	l, err := r.luaManager.RunLuaScript(obj, r.luaScript)
	if err != nil {
		return nil, err
	}
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTTable {
		jsonBytes, err := luamanager.Encode(returnValue)
		if err != nil {
			return nil, err
		}
		newAnnotations := map[string]string{}
		err = json.Unmarshal(jsonBytes, &newAnnotations)
		if err != nil {
			return nil, err
		}
		return newAnnotations, nil
	}
	return nil, fmt.Errorf("expect table output from Lua script, not %s", returnValue.Type().String())
}

func (r *ingressController) getTrafficRoutingIngressLuaScript(iType string) (string, error) {
	if iType == "" {
		iType = "nginx"
	}
	luaScript, err := configuration.GetTrafficRoutingIngressLuaScript(r.Client, iType)
	if err != nil {
		return "", err
	}
	if luaScript != "" {
		return luaScript, nil
	}
	key := fmt.Sprintf("lua_configuration/trafficrouting_ingress/%s.lua", iType)
	script := util.GetLuaConfigurationContent(key)
	if script == "" {
		return "", fmt.Errorf("%s lua script is not found", iType)
	}
	return script, nil
}
