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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting"
)

type Config struct {
	RolloutName   string
	RolloutNs     string
	CanaryService *corev1.Service
	StableService *corev1.Service
	TrafficConf   *rolloutv1alpha1.GatewayTrafficRouting
	OwnerRef      metav1.OwnerReference
}

type gatewayController struct {
	client.Client
	//stableIngress *netv1.Ingress
	conf      Config
	newStatus *rolloutv1alpha1.RolloutStatus
}

// NewGatewayTrafficRouting The Gateway API is a part of the SIG Network.
func NewGatewayTrafficRouting(client client.Client, newStatus *rolloutv1alpha1.RolloutStatus, albConf Config) (trafficrouting.Controller, error) {
	r := &gatewayController{
		Client:    client,
		conf:      albConf,
		newStatus: newStatus,
	}
	return r, nil
}

func (r *gatewayController) SetRoutes(ctx context.Context, desiredWeight int32) error {
	var httpRoute gatewayv1alpha2.HTTPRoute
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.HTTPRouteName}, &httpRoute)
	if err != nil {
		return err
	} else if !httpRoute.DeletionTimestamp.IsZero() {
		klog.Warningf("rollout(%s/%s) stable HTTPRoute is deleting", r.conf.RolloutNs, r.conf.RolloutName)
		return nil
	}

	// create canary http route

	currentWeight := r.getHTTPRouteCanaryWeight(httpRoute)
	if currentWeight != -1 && currentWeight == desiredWeight {
		return nil
	}

	r.buildCanaryHTTPRoute(&httpRoute, desiredWeight)
	if err := r.Update(ctx, &httpRoute); err != nil {
		klog.Errorf("rollout(%s/%s) update the canary HTTPRoute failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) set HTTPRoute(name:%s weight:%d) success", r.conf.RolloutNs, r.conf.RolloutName, r.conf.TrafficConf.HTTPRouteName, desiredWeight)
	return nil
}

func (r *gatewayController) Verify(desiredWeight int32) (bool, error) {
	// verify set weight routing
	httpRoute := &gatewayv1alpha2.HTTPRoute{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.HTTPRouteName}, httpRoute)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary HTTPRoute failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return false, err
	}
	// when desiredWeight == -1, verify set canary HTTPRoute weight=0%
	if desiredWeight == -1 {
		// canary HTTPRoute
		if errors.IsNotFound(err) || !httpRoute.DeletionTimestamp.IsZero() {
			klog.Infof("rollout(%s/%s) verify canary HTTPRoute has been deleted", r.conf.RolloutNs, r.conf.RolloutName)
			return true, nil
		}

		cWeight := r.getHTTPRouteCanaryWeight(*httpRoute)
		if cWeight != -1 {
			klog.Infof("rollout(%s/%s) verify canary HTTPRoute weight(-1) invalid, and reset", r.conf.RolloutNs, r.conf.RolloutName)
			return false, nil
		}
		klog.Infof("rollout(%s/%s) verify canary HTTPRoute weight(0)", r.conf.RolloutNs, r.conf.RolloutName)
		return true, nil
	}

	// verify set canary ingress desiredWeight
	if errors.IsNotFound(err) {
		klog.Infof("rollout(%s/%s) canary HTTPRoute not found, and create", r.conf.RolloutNs, r.conf.RolloutName)
		return false, nil
	}
	cWeight := r.getHTTPRouteCanaryWeight(*httpRoute)
	if cWeight != desiredWeight {
		klog.Infof("rollout(%s/%s) verify trafficRouting(%d) invalid weight(current:%d desired:%d), and update", r.conf.RolloutNs, r.conf.RolloutName, desiredWeight, cWeight, desiredWeight)
		return false, nil
	}
	klog.Infof("rollout(%s/%s) verify trafficRouting(%d) success", r.conf.RolloutNs, r.conf.RolloutName, desiredWeight)
	return true, nil
}

func (r *gatewayController) Finalise() error {
	canaryRoute := &gatewayv1alpha2.HTTPRoute{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.HTTPRouteName}, canaryRoute)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary HTTPRoute(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.TrafficConf.HTTPRouteName, err.Error())
		return err
	}

	// immediate delete canary ingress
	if err = r.Delete(context.TODO(), canaryRoute); err != nil {
		klog.Errorf("rollout(%s/%s) remove canary HTTPRoute(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, canaryRoute.Name, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) remove canary HTTPRoute(%s) success", r.conf.RolloutNs, r.conf.RolloutName, canaryRoute.Name)
	return nil
}

func (r *gatewayController) TrafficRouting() (bool, error) {
	canaryRoute := &gatewayv1alpha2.HTTPRoute{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.HTTPRouteName}, canaryRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Errorf("Ingress(%s/%s) is Not Found", r.conf.RolloutNs, r.conf.TrafficConf.HTTPRouteName)
		}
		return false, err
	}
	return true, nil
}

func (r *gatewayController) buildCanaryHTTPRoute(stable *gatewayv1alpha2.HTTPRoute, weight int32) {
	var rules []gatewayv1alpha2.HTTPRouteRule
	for i := range stable.Spec.Rules {
		rule := stable.Spec.Rules[i]
		var canaryBackendRefs []gatewayv1alpha2.HTTPBackendRef
		var stableBackendRefs []gatewayv1alpha2.HTTPBackendRef
		for j := range rule.BackendRefs {
			kind := rule.BackendRefs[j].Kind
			if kind != nil && *kind == "Service" && string(rule.BackendRefs[j].Name) == r.conf.StableService.Name {
				if weight != -1 {
					canaryBackendRef := rule.BackendRefs[j].DeepCopy()
					canaryBackendRef.Name = gatewayv1alpha2.ObjectName(r.conf.CanaryService.Name)
					canaryBackendRef.Weight = generateCanaryWeight(weight)
					canaryBackendRefs = append(canaryBackendRefs, *canaryBackendRef)
				}
				stableBackendRef := rule.BackendRefs[j]
				stableBackendRef.Weight = generateStableWeight(weight)
				stableBackendRefs = append(stableBackendRefs, stableBackendRef)
			} else if string(rule.BackendRefs[j].Name) != r.conf.CanaryService.Name {
				canaryBackendRef := rule.BackendRefs[j]
				canaryBackendRef.Weight = &weight
				stableBackendRefs = append(stableBackendRefs, rule.BackendRefs[j])
			}
		}
		rule.BackendRefs = append(stableBackendRefs, canaryBackendRefs...)
		rules = append(rules, rule)
	}
	stable.Spec.Rules = rules
}

func (r *gatewayController) getHTTPRouteCanaryWeight(route gatewayv1alpha2.HTTPRoute) int32 {
	for i := range route.Spec.Rules {
		rules := route.Spec.Rules[i]
		for j := range rules.BackendRefs {
			kind := rules.BackendRefs[j].Kind
			if kind != nil && *kind == "Service" && string(rules.BackendRefs[j].Name) == r.conf.CanaryService.Name {
				return *rules.BackendRefs[j].Weight
			}
		}
	}
	return -1
}

func generateStableWeight(weight int32) *int32 {
	var stableWeight int32
	if weight <= 0 {
		stableWeight = 100
	} else if weight <= 100 {
		stableWeight = 100 - weight
	}
	return &stableWeight
}

func generateCanaryWeight(weight int32) *int32 {
	var canaryWeight int32 = weight
	if weight > 100 {
		canaryWeight = 100
	}
	return &canaryWeight
}
