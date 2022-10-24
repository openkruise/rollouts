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

package nginx

import (
	"context"
	"fmt"
	"strconv"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nginxIngressAnnotationDefaultPrefix = "nginx.ingress.kubernetes.io"
	k8sIngressClassAnnotation           = "kubernetes.io/ingress.class"
)

type nginxController struct {
	client.Client
	//stableIngress *netv1.Ingress
	conf      Config
	newStatus *rolloutv1alpha1.RolloutStatus
}

type Config struct {
	RolloutName   string
	RolloutNs     string
	CanaryService string
	StableService string
	TrafficConf   *rolloutv1alpha1.IngressTrafficRouting
	OwnerRef      metav1.OwnerReference
}

func NewNginxTrafficRouting(client client.Client, newStatus *rolloutv1alpha1.RolloutStatus, conf Config) (trafficrouting.Controller, error) {
	r := &nginxController{
		Client:    client,
		conf:      conf,
		newStatus: newStatus,
	}
	return r, nil
}

func (r *nginxController) Initialize(ctx context.Context) error {
	ingress := &netv1.Ingress{}
	return r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.Name}, ingress)
}

func (r *nginxController) EnsureRoutes(ctx context.Context, desiredWeight int32) (bool, error) {
	canaryIngress := &netv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.defaultCanaryIngressName()}, canaryIngress)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary ingress failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return false, err
	}
	// When desiredWeight=0, it means that rollout has been completed and the final traffic switching process is in progress,
	// and the canary weight should be set to 0 at this time.
	if desiredWeight == 0 {
		// canary ingress
		if errors.IsNotFound(err) || !canaryIngress.DeletionTimestamp.IsZero() {
			klog.Infof("rollout(%s/%s) verify canary ingress has been deleted", r.conf.RolloutNs, r.conf.RolloutName)
			return true, nil
		}
	} else if errors.IsNotFound(err) {
		// create canary ingress
		stableIngress := &netv1.Ingress{}
		err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.Name}, stableIngress)
		if err != nil {
			return false, err
		} else if !stableIngress.DeletionTimestamp.IsZero() {
			klog.Warningf("rollout(%s/%s) stable ingress is deleting", r.conf.RolloutNs, r.conf.RolloutName)
			return false, nil
		}
		canaryIngress = r.buildCanaryIngress(stableIngress, desiredWeight)
		if err = r.Create(ctx, canaryIngress); err != nil {
			klog.Errorf("rollout(%s/%s) create canary ingress failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
			return false, err
		}
		data := util.DumpJSON(canaryIngress)
		klog.Infof("rollout(%s/%s) create canary ingress(%s) success", r.conf.RolloutNs, r.conf.RolloutName, data)
		return false, nil
	}

	// check whether canary weight equals desired weight
	currentWeight := getIngressCanaryWeight(canaryIngress)
	if desiredWeight == currentWeight {
		return true, nil
	}

	cloneObj := canaryIngress.DeepCopy()
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s/canary-weight":"%s"}}}`, nginxIngressAnnotationDefaultPrefix, fmt.Sprintf("%d", desiredWeight))
	if err = r.Patch(ctx, cloneObj, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
		klog.Errorf("rollout(%s/%s) set canary ingress(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) set ingress routes(weight:%d) success", r.conf.RolloutNs, r.conf.RolloutName, desiredWeight)
	return false, nil
}

func (r *nginxController) Finalise(ctx context.Context) error {
	canaryIngress := &netv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.defaultCanaryIngressName()}, canaryIngress)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary ingress(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.defaultCanaryIngressName(), err.Error())
		return err
	}

	if errors.IsNotFound(err) || !canaryIngress.DeletionTimestamp.IsZero() {
		return nil
	}

	// immediate delete canary ingress
	if err = r.Delete(ctx, canaryIngress); err != nil {
		klog.Errorf("rollout(%s/%s) remove canary ingress(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) remove canary ingress(%s) success", r.conf.RolloutNs, r.conf.RolloutName, canaryIngress.Name)
	return nil
}

func (r *nginxController) buildCanaryIngress(stableIngress *netv1.Ingress, desiredWeight int32) *netv1.Ingress {
	desiredCanaryIngress := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.defaultCanaryIngressName(),
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

	// Preserve ingressClassName from stable ingress
	if stableIngress.Spec.IngressClassName != nil {
		desiredCanaryIngress.Spec.IngressClassName = stableIngress.Spec.IngressClassName
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
			if ingressRule.HTTP.Paths[ip].Backend.Service.Name == r.conf.StableService {
				hasStableServiceBackendRule = true
				ingressRule.HTTP.Paths[ip].Backend.Service.Name = r.conf.CanaryService
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
	return desiredCanaryIngress
}

func (r *nginxController) defaultCanaryIngressName() string {
	return fmt.Sprintf("%s-canary", r.conf.TrafficConf.Name)
}

func getIngressCanaryWeight(ing *netv1.Ingress) int32 {
	weightStr, ok := ing.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]
	if !ok {
		return 0
	}
	weight, _ := strconv.Atoi(weightStr)
	return int32(weight)
}
