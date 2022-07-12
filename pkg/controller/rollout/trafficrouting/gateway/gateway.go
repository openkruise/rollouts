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

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type Config struct {
	RolloutName   string
	RolloutNs     string
	CanaryService string
	StableService string
	TrafficConf   *rolloutv1alpha1.GatewayTrafficRouting
}

type gatewayController struct {
	client.Client
	//stableIngress *netv1.Ingress
	conf      Config
	newStatus *rolloutv1alpha1.RolloutStatus
}

// NewGatewayTrafficRouting The Gateway API is a part of the SIG Network.
func NewGatewayTrafficRouting(client client.Client, newStatus *rolloutv1alpha1.RolloutStatus, conf Config) (trafficrouting.Controller, error) {
	r := &gatewayController{
		Client:    client,
		conf:      conf,
		newStatus: newStatus,
	}
	return r, nil
}

func (r *gatewayController) Initialize(ctx context.Context) error {
	route := &gatewayv1alpha2.HTTPRoute{}
	return r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: *r.conf.TrafficConf.HTTPRouteName}, route)
}

func (r *gatewayController) EnsureRoutes(ctx context.Context, desiredWeight int32) (bool, error) {
	var httpRoute gatewayv1alpha2.HTTPRoute
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: *r.conf.TrafficConf.HTTPRouteName}, &httpRoute)
	if err != nil {
		// When desiredWeight=0, it means that rollout has been completed and the final traffic switching process is in progress,
		// and The removal of httpRoute is expected.
		if desiredWeight == 0 && errors.IsNotFound(err) {
			klog.Infof("rollout(%s/%s) verify canary HTTPRoute has been deleted", r.conf.RolloutNs, r.conf.RolloutName)
			return true, nil
		}
		return false, err
	}

	// desired route
	desiredRule := r.buildDesiredHTTPRoute(httpRoute.Spec.Rules, desiredWeight)
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
		klog.Errorf("update rollout(%s/%s) httpRoute(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, httpRoute.Name, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) set HTTPRoute(name:%s weight:%d) success", r.conf.RolloutNs, r.conf.RolloutName, *r.conf.TrafficConf.HTTPRouteName, desiredWeight)
	return false, nil
}

func (r *gatewayController) Finalise(ctx context.Context) error {
	httpRoute := &gatewayv1alpha2.HTTPRoute{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: *r.conf.TrafficConf.HTTPRouteName}, httpRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		klog.Errorf("rollout(%s/%s) get HTTPRoute failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}
	// desired rule
	desiredRule := r.buildDesiredHTTPRoute(httpRoute.Spec.Rules, -1)
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
		klog.Errorf("update rollout(%s/%s) httpRoute(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, httpRoute.Name, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) TrafficRouting Finalise success", r.conf.RolloutNs, r.conf.RolloutName)
	return nil
}

func (r *gatewayController) buildDesiredHTTPRoute(rules []gatewayv1alpha2.HTTPRouteRule, canaryPercent int32) []gatewayv1alpha2.HTTPRouteRule {
	var desired []gatewayv1alpha2.HTTPRouteRule
	for i := range rules {
		rule := rules[i]
		_, stableRef := getServiceBackendRef(rule, r.conf.StableService)
		if stableRef == nil {
			desired = append(desired, rule)
			continue
		}
		// If canaryPercent = -1, delete canary backendRef
		if canaryPercent == -1 {
			filterOutServiceBackendRef(&rule, r.conf.CanaryService)
			stableRef.Weight = nil
			setServiceBackendRef(&rule, *stableRef)
			// canaryPercent[0,100]
		} else {
			_, canaryRef := getServiceBackendRef(rule, r.conf.CanaryService)
			if canaryRef == nil {
				canaryRef = stableRef.DeepCopy()
				canaryRef.Name = gatewayv1alpha2.ObjectName(r.conf.CanaryService)
			}
			stableWeight, canaryWeight := generateCanaryWeight(canaryPercent)
			stableRef.Weight = &stableWeight
			canaryRef.Weight = &canaryWeight
			setServiceBackendRef(&rule, *stableRef)
			setServiceBackendRef(&rule, *canaryRef)
		}
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
