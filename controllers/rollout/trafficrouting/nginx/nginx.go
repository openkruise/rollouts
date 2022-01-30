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

package nginx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/controllers/rollout/trafficrouting"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nginxIngressAnnotationDefaultPrefix = "nginx.ingress.kubernetes.io"

	k8sIngressClassAnnotation = "kubernetes.io/ingress.class"

	ingressTrafficRoutingState = "rollouts.kruise.io/traffic-routing-state"
)

type trafficRoutingState struct {
	UpdateTimestamp metav1.Time `json:"updateTimestamp"`
	Weight          int32       `json:"weight"`
}

type nginxController struct {
	client.Client
	//stableIngress *netv1.Ingress
	conf      NginxConfig
	newStatus *appsv1alpha1.RolloutStatus
}

type NginxConfig struct {
	RolloutName   string
	RolloutNs     string
	CanaryService *corev1.Service
	StableService *corev1.Service
	TrafficConf   *appsv1alpha1.NginxTrafficRouting
	OwnerRef      metav1.OwnerReference
}

func NewNginxTrafficRouting(client client.Client, newStatus *appsv1alpha1.RolloutStatus, albConf NginxConfig) (trafficrouting.TrafficRoutingController, error) {
	r := &nginxController{
		Client:    client,
		conf:      albConf,
		newStatus: newStatus,
	}
	return r, nil
}

func (r *nginxController) SetRoutes(desiredWeight int32) error {
	stableIngress := &netv1.Ingress{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.Ingress}, stableIngress)
	if err != nil {
		return err
	}
	canaryIngress := &netv1.Ingress{}
	err = r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.defaultCanaryIngressName()}, canaryIngress)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary ingress failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	} else if errors.IsNotFound(err) {
		// create canary ingress
		canaryIngress = r.buildCanaryIngress(stableIngress, desiredWeight)
		if err = r.Create(context.TODO(), canaryIngress); err != nil {
			klog.Errorf("rollout(%s/%s) create canary ingress failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
			return err
		}
		by, _ := json.Marshal(canaryIngress)
		klog.Infof("rollout(%s/%s) create canary ingress(%s) success", r.conf.RolloutNs, r.conf.RolloutName, string(by))
		return nil
	}

	currentWeight := getIngressCanaryWeight(canaryIngress)
	if desiredWeight == currentWeight {
		return nil
	}
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Namespace: canaryIngress.Namespace, Name: canaryIngress.Name}, canaryIngress); err != nil {
			klog.Errorf("error getting updated ingress(%s/%s) from client", canaryIngress.Namespace, canaryIngress.Name)
			return err
		}
		state := trafficRoutingState{
			Weight:          desiredWeight,
			UpdateTimestamp: metav1.Now(),
		}
		by, _ := json.Marshal(state)
		canaryIngress.Annotations[ingressTrafficRoutingState] = string(by)
		canaryIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = fmt.Sprintf("%d", desiredWeight)
		if err := r.Client.Update(context.TODO(), canaryIngress); err != nil {
			return err
		}
		return nil
	}); err != nil {
		klog.Errorf("rollout(%s/%s) set ingress routes failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}
	klog.Errorf("rollout(%s/%s) set ingress routes(weight:%d) success", r.conf.RolloutNs, r.conf.RolloutName, desiredWeight)
	return nil
}

func (r *nginxController) VerifyTrafficRouting(desiredWeight int32) (bool, error) {
	// verify set weight routing
	canaryIngress := &netv1.Ingress{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.defaultCanaryIngressName()}, canaryIngress)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary ingress failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return false, err
	} else if errors.IsNotFound(err) {
		return false, nil
	}
	data, ok := canaryIngress.Annotations[ingressTrafficRoutingState]
	if !ok {
		return false, nil
	}

	var state *trafficRoutingState
	if err = json.Unmarshal([]byte(data), &state); err != nil {
		klog.Errorf("rollout(%s/%s) Unmarshal annotation[%s] failed: %s", r.conf.RolloutNs, r.conf.RolloutName, ingressTrafficRoutingState, err.Error())
		return false, err
	}
	if state.Weight != desiredWeight {
		return false, nil
	}

	// After setting up traffic routing, give the ingress provider 5 seconds to take effect
	if verifyTime := state.UpdateTimestamp.Add(time.Second * 5); verifyTime.Before(time.Now()) {
		klog.Infof("rollout(%s/%s) verify routes(%d) success", r.conf.RolloutNs, r.conf.RolloutName, desiredWeight)
		return true, nil
	}

	klog.Infof("rollout(%s/%s) verify routes(%d) incomplete, and wait a moment", r.conf.RolloutNs, r.conf.RolloutName, desiredWeight)
	return false, nil
}

func (r *nginxController) DoFinalising() (bool, error) {
	canaryIngress := &netv1.Ingress{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.defaultCanaryIngressName()}, canaryIngress)
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		klog.Errorf("rollout(%s/%s) get canary ingress(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.defaultCanaryIngressName(), err.Error())
		return false, err
	}

	var state *trafficRoutingState
	if data, ok := canaryIngress.Annotations[ingressTrafficRoutingState]; !ok {
		// immediate delete canary ingress
		if err = r.Delete(context.TODO(), canaryIngress); err != nil {
			klog.Errorf("rollout(%s/%s) remove canary ingress(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name, err.Error())
			return false, err
		}
		klog.Infof("rollout(%s/%s) remove canary ingress(%s) success", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name)
		return true, nil
	} else {
		if err = json.Unmarshal([]byte(data), &state); err != nil {
			klog.Errorf("rollout(%s/%s) Unmarshal annotation[%s] failed: %s", r.conf.RolloutNs, r.conf.RolloutName, ingressTrafficRoutingState, err.Error())
			return false, err
		}
	}

	if state.Weight == 0 {
		// After setting up traffic routing, give the ingress provider 5 seconds to take effect
		if verifyTime := state.UpdateTimestamp.Add(time.Second * 5); verifyTime.After(time.Now()) {
			klog.Errorf("rollout(%s/%s) set ingress routes(weight:0) success, and wait a moment", r.conf.RolloutNs, r.conf.RolloutName)
			return false, nil
		}
		if err = r.Delete(context.TODO(), canaryIngress); err != nil {
			klog.Errorf("rollout(%s/%s) remove canary ingress(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name, err.Error())
			return false, err
		}
		klog.Infof("rollout(%s/%s) remove canary ingress(%s) success", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name)
		return true, nil
	}

	// first, set canary ingress weight = 0
	if err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: canaryIngress.Namespace, Name: canaryIngress.Name}, canaryIngress); err != nil {
			klog.Errorf("error getting updated ingress(%s/%s) from client", canaryIngress.Namespace, canaryIngress.Name)
			return err
		}
		state = &trafficRoutingState{
			Weight:          0,
			UpdateTimestamp: metav1.Now(),
		}
		by, _ := json.Marshal(state)
		canaryIngress.Annotations[ingressTrafficRoutingState] = string(by)
		canaryIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = fmt.Sprintf("%d", 0)
		if err = r.Client.Update(context.TODO(), canaryIngress); err != nil {
			return err
		}
		return nil
	}); err != nil {
		klog.Errorf("rollout(%s/%s) set ingress routes failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) set ingress routes(weight:0) success, and wait a moment", r.conf.RolloutNs, r.conf.RolloutName)
	return false, nil
}

func (r *nginxController) buildCanaryIngress(stableIngress *netv1.Ingress, desiredWeight int32) *netv1.Ingress {
	desiredCanaryIngress := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.defaultCanaryIngressName(),
			Namespace:   stableIngress.Namespace,
			Annotations: map[string]string{},
		},
		Spec: netv1.IngressSpec{
			Rules: make([]netv1.IngressRule, 0),
		},
	}

	// Preserve ingressClassName from stable ingress
	if stableIngress.Spec.IngressClassName != nil {
		desiredCanaryIngress.Spec.IngressClassName = stableIngress.Spec.IngressClassName
	}

	// Must preserve ingress.class on canary ingress, no other annotations matter
	// See: https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/#canary
	if val, ok := stableIngress.Annotations[k8sIngressClassAnnotation]; ok {
		desiredCanaryIngress.Annotations[k8sIngressClassAnnotation] = val
	}

	// Ensure canaryIngress is owned by this Rollout for cleanup
	desiredCanaryIngress.SetOwnerReferences([]metav1.OwnerReference{r.conf.OwnerRef})

	// Copy only the rules which reference the stableService from the stableIngress to the canaryIngress
	// and change service backend to canaryService. Rules **not** referencing the stableIngress will be ignored.
	for ir := 0; ir < len(stableIngress.Spec.Rules); ir++ {
		var hasStableServiceBackendRule bool
		ingressRule := stableIngress.Spec.Rules[ir].DeepCopy()

		// Update all backends pointing to the stableService to point to the canaryService now
		for ip := 0; ip < len(ingressRule.HTTP.Paths); ip++ {
			if ingressRule.HTTP.Paths[ip].Backend.Service.Name == r.conf.StableService.Name {
				hasStableServiceBackendRule = true
				ingressRule.HTTP.Paths[ip].Backend.Service.Name = r.conf.CanaryService.Name
			}
		}

		// If this rule was using the specified stableService backend, append it to the canary Ingress spec
		if hasStableServiceBackendRule {
			desiredCanaryIngress.Spec.Rules = append(desiredCanaryIngress.Spec.Rules, *ingressRule)
		}
	}

	// Always set `canary` and `canary-weight` - `canary-by-header` and `canary-by-cookie`, if set,  will always take precedence
	desiredCanaryIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)] = "true"
	desiredCanaryIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)] = fmt.Sprintf("%d", desiredWeight)
	state := trafficRoutingState{
		Weight:          desiredWeight,
		UpdateTimestamp: metav1.Now(),
	}
	by, _ := json.Marshal(state)
	desiredCanaryIngress.Annotations[ingressTrafficRoutingState] = string(by)
	return desiredCanaryIngress
}

func (r *nginxController) defaultCanaryIngressName() string {
	return fmt.Sprintf("%s-canary", r.conf.TrafficConf.Ingress)
}

func getIngressCanaryWeight(ing *netv1.Ingress) int32 {
	weightStr, ok := ing.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]
	if !ok {
		return 0
	}
	weight, _ := strconv.Atoi(weightStr)
	return int32(weight)
}
