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

package gateway

import (
	"context"
	"reflect"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type Config struct {
	// only for log info
	Key           string
	Namespace     string
	CanaryService string
	StableService string
	TrafficConf   *v1beta1.GatewayTrafficRouting
}

type gatewayController struct {
	client.Client
	conf Config
}

// NewGatewayTrafficRouting The Gateway API is a part of the SIG Network.
func NewGatewayTrafficRouting(client client.Client, conf Config) (network.NetworkProvider, error) {
	r := &gatewayController{
		Client: client,
		conf:   conf,
	}
	return r, nil
}

func (r *gatewayController) Initialize(ctx context.Context) error {
	route := &gatewayv1alpha2.HTTPRoute{}
	return r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: *r.conf.TrafficConf.HTTPRouteName}, route)
}

func (r *gatewayController) EnsureRoutes(ctx context.Context, strategy *v1beta1.TrafficRoutingStrategy) (bool, error) {
	var weight *int32
	if strategy.Traffic != nil {
		is := intstr.FromString(*strategy.Traffic)
		weightInt, _ := intstr.GetScaledValueFromIntOrPercent(&is, 100, true)
		weight = utilpointer.Int32(int32(weightInt))
	}
	matches := strategy.Matches
	// headerModifier := strategy.RequestHeaderModifier
	var httpRoute gatewayv1alpha2.HTTPRoute
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: *r.conf.TrafficConf.HTTPRouteName}, &httpRoute)
	if err != nil {
		return false, err
	}
	// desired route
	desiredRule := r.buildDesiredHTTPRoute(httpRoute.Spec.Rules, weight, matches, nil)
	if reflect.DeepEqual(httpRoute.Spec.Rules, desiredRule) {
		return true, nil
	}
	// set route
	routeClone := &gatewayv1alpha2.HTTPRoute{}
	if err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: httpRoute.Namespace, Name: httpRoute.Name}, routeClone); err != nil {
			klog.Errorf("error getting updated httpRoute(%s/%s) from client", httpRoute.Namespace, httpRoute.Name)
			return err
		}
		routeClone.Spec.Rules = desiredRule
		return r.Client.Update(context.TODO(), routeClone)
	}); err != nil {
		klog.Errorf("update %s httpRoute(%s) failed: %s", r.conf.Key, httpRoute.Name, err.Error())
		return false, err
	}
	klog.Infof("%s set HTTPRoute(name:%s weight:%d) success", r.conf.Key, *r.conf.TrafficConf.HTTPRouteName, *weight)
	return false, nil
}

func (r *gatewayController) Finalise(ctx context.Context) error {
	httpRoute := &gatewayv1alpha2.HTTPRoute{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.Namespace, Name: *r.conf.TrafficConf.HTTPRouteName}, httpRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		klog.Errorf("%s get HTTPRoute failed: %s", r.conf.Key, err.Error())
		return err
	}
	// desired rule
	desiredRule := r.buildDesiredHTTPRoute(httpRoute.Spec.Rules, utilpointer.Int32(-1), nil, nil)
	if reflect.DeepEqual(httpRoute.Spec.Rules, desiredRule) {
		return nil
	}
	routeClone := &gatewayv1alpha2.HTTPRoute{}
	if err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: httpRoute.Namespace, Name: httpRoute.Name}, routeClone); err != nil {
			klog.Errorf("error getting updated httpRoute(%s/%s) from client", httpRoute.Namespace, httpRoute.Name)
			return err
		}
		routeClone.Spec.Rules = desiredRule
		return r.Client.Update(context.TODO(), routeClone)
	}); err != nil {
		klog.Errorf("update %s httpRoute(%s) failed: %s", r.conf.Key, httpRoute.Name, err.Error())
		return err
	}
	klog.Infof("%s TrafficRouting Finalise success", r.conf.Key)
	return nil
}

func (r *gatewayController) buildDesiredHTTPRoute(rules []gatewayv1alpha2.HTTPRouteRule, weight *int32, matches []v1beta1.HttpRouteMatch,
	rh *gatewayv1alpha2.HTTPRequestHeaderFilter) []gatewayv1alpha2.HTTPRouteRule {
	var desired []gatewayv1alpha2.HTTPRouteRule
	// Only when finalize method parameter weight=-1,
	// then we need to remove the canary route policy and restore to the original configuration
	if weight != nil && *weight == -1 {
		for i := range rules {
			rule := rules[i]
			filterOutServiceBackendRef(&rule, r.conf.CanaryService)
			_, stableRef := getServiceBackendRef(rule, r.conf.StableService)
			if stableRef != nil {
				stableRef.Weight = utilpointer.Int32(1)
				setServiceBackendRef(&rule, *stableRef)
			}
			if len(rule.BackendRefs) != 0 {
				desired = append(desired, rule)
			}
		}
		return desired
		// according to the Gateway API definition, weight and headers cannot be supported at the same time.
		// A/B Testing, according to headers. current only support one match
	} else if len(matches) > 0 {
		return r.buildCanaryHeaderHttpRoutes(rules, matches)
	}
	// canary release, according to percentage of traffic routing
	return r.buildCanaryWeightHttpRoutes(rules, weight)
}

func (r *gatewayController) buildCanaryHeaderHttpRoutes(rules []gatewayv1alpha2.HTTPRouteRule, matchs []v1beta1.HttpRouteMatch) []gatewayv1alpha2.HTTPRouteRule {
	var desired []gatewayv1alpha2.HTTPRouteRule
	var canarys []gatewayv1alpha2.HTTPRouteRule
	for i := range rules {
		rule := rules[i]
		if _, canaryRef := getServiceBackendRef(rule, r.conf.CanaryService); canaryRef != nil {
			continue
		}
		desired = append(desired, rule)
		if _, stableRef := getServiceBackendRef(rule, r.conf.StableService); stableRef == nil {
			continue
		}
		// according to stable rule to create canary rule
		canaryRule := rule.DeepCopy()
		_, canaryRef := getServiceBackendRef(*canaryRule, r.conf.StableService)
		canaryRef.Name = gatewayv1alpha2.ObjectName(r.conf.CanaryService)
		canaryRule.BackendRefs = []gatewayv1alpha2.HTTPBackendRef{*canaryRef}
		// set canary headers in httpRoute
		var newMatches []gatewayv1alpha2.HTTPRouteMatch
		for j := range canaryRule.Matches {
			canaryRuleMatch := &canaryRule.Matches[j]
			for k := range matchs {
				canaryRuleMatchBase := *canaryRuleMatch
				canaryRuleMatchBase.Headers = append(canaryRuleMatchBase.Headers, matchs[k].Headers...)
				newMatches = append(newMatches, canaryRuleMatchBase)
			}
		}
		canaryRule.Matches = newMatches
		canarys = append(canarys, *canaryRule)
	}
	desired = append(desired, canarys...)
	return desired
}

func (r *gatewayController) buildCanaryWeightHttpRoutes(rules []gatewayv1alpha2.HTTPRouteRule, weight *int32) []gatewayv1alpha2.HTTPRouteRule {
	var desired []gatewayv1alpha2.HTTPRouteRule
	for i := range rules {
		rule := rules[i]
		_, stableRef := getServiceBackendRef(rule, r.conf.StableService)
		if stableRef == nil {
			desired = append(desired, rule)
			continue
		}
		_, canaryRef := getServiceBackendRef(rule, r.conf.CanaryService)
		if canaryRef == nil {
			canaryRef = stableRef.DeepCopy()
			canaryRef.Name = gatewayv1alpha2.ObjectName(r.conf.CanaryService)
		}
		stableWeight, canaryWeight := generateCanaryWeight(*weight)
		stableRef.Weight = &stableWeight
		canaryRef.Weight = &canaryWeight
		setServiceBackendRef(&rule, *stableRef)
		setServiceBackendRef(&rule, *canaryRef)
		desired = append(desired, rule)
	}
	return desired
}

// canaryPercent[0,100]
func generateCanaryWeight(canaryPercent int32) (stableWeight int32, canaryWeight int32) {
	canaryWeight = canaryPercent
	stableWeight = 100 - canaryPercent
	return stableWeight, canaryWeight
}

// int indicates ref index
func getServiceBackendRef(rule gatewayv1alpha2.HTTPRouteRule, serviceName string) (int, *gatewayv1alpha2.HTTPBackendRef) {
	for i := range rule.BackendRefs {
		ref := rule.BackendRefs[i]
		if ref.Kind != nil && *ref.Kind == "Service" && string(ref.Name) == serviceName {
			return i, &ref
		}
	}
	return 0, nil
}

func setServiceBackendRef(rule *gatewayv1alpha2.HTTPRouteRule, ref gatewayv1alpha2.HTTPBackendRef) {
	if ref.Kind == nil || *ref.Kind != "Service" {
		return
	}
	index, currentRef := getServiceBackendRef(*rule, string(ref.Name))
	if currentRef == nil {
		rule.BackendRefs = append(rule.BackendRefs, ref)
		return
	}
	oldRefs := rule.BackendRefs
	rule.BackendRefs = []gatewayv1alpha2.HTTPBackendRef{}
	for i := range oldRefs {
		if i == index {
			rule.BackendRefs = append(rule.BackendRefs, ref)
		} else {
			rule.BackendRefs = append(rule.BackendRefs, oldRefs[i])
		}
	}
}

func filterOutServiceBackendRef(rule *gatewayv1alpha2.HTTPRouteRule, serviceName string) {
	index, ref := getServiceBackendRef(*rule, serviceName)
	if ref == nil {
		return
	}
	oldRefs := rule.BackendRefs
	rule.BackendRefs = []gatewayv1alpha2.HTTPBackendRef{}
	for i := range oldRefs {
		if i == index {
			continue
		}
		rule.BackendRefs = append(rule.BackendRefs, oldRefs[i])
	}
}
