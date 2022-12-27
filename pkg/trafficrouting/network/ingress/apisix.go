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
	"fmt"
	"reflect"

	"github.com/openkruise/rollouts/api/v1alpha1"
	a6v2 "github.com/openkruise/rollouts/pkg/apis/apisix/v2"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type apisixIngressController struct {
	client.Client
	conf Config
}

func NewApisixIngressTrafficRouting(client client.Client, conf Config) (network.NetworkProvider, error) {
	r := &apisixIngressController{
		Client: client,
		conf:   conf,
	}
	return r, nil
}

func (r *apisixIngressController) Initialize(ctx context.Context) error {
	apisixRoute := &a6v2.ApisixRoute{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.Name}, apisixRoute)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get apisix route(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.TrafficConf.Name, err.Error())
		return err
	}

	apisixUpstream := &a6v2.ApisixUpstream{}
	err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.StableService}, apisixUpstream)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get apisix upstream(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.StableService, err.Error())
		return err
	}

	apisixRoute, err = r.buildCanaryApisixRoute(apisixRoute)
	if err != nil {
		klog.Errorf("rollout(%s/%s) build canary apisix route failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}

	canaryApisixUpstream, err := r.buildCanaryApisixUpstream(apisixUpstream)
	if err != nil {
		klog.Errorf("rollout(%s/%s) build canary apisix upstream failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}

	if err = r.Create(ctx, canaryApisixUpstream); err != nil {
		klog.Errorf("rollout(%s/%s) create canary apisix upstream failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}

	if err = r.Update(ctx, apisixRoute); err != nil {
		klog.Errorf("rollout(%s/%s) update apisix route failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) update apisix route(%s) success", r.conf.RolloutNs, r.conf.RolloutName, util.DumpJSON(apisixRoute))

	return nil
}

func (r *apisixIngressController) EnsureRoutes(ctx context.Context, weight *int32, matches []v1alpha1.HttpRouteMatch) (bool, error) {
	if *weight < 0 || *weight > 100 {
		return true, fmt.Errorf("rollout(%s/%s) update failed: no valid weights", r.conf.RolloutNs, r.conf.RolloutName)
	}

	apisixRoute := &a6v2.ApisixRoute{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.Name}, apisixRoute)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get apisix route(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.TrafficConf.Name, err.Error())
		return false, err
	}

	apisixRouteClone := apisixRoute.DeepCopy()

	targetHTTPRoutes, _ := r.getTargetHTTPRoutes(apisixRouteClone, r.conf.StableService)
	for index, thr := range targetHTTPRoutes {
		backends := thr.Backends

		w := int(utilpointer.Int32Deref(weight, 0))
		for i, backend := range backends {
			if backend.ServiceName == r.conf.StableService {
				if backend.Weight == utilpointer.Int(0) {
					return false, fmt.Errorf("rollout(%s/%s) update failed: no valid stable service backend weights", r.conf.RolloutNs, r.conf.RolloutName)
				}
				backends[i].Weight = utilpointer.Int(100 - w)
			} else if backend.ServiceName == r.conf.CanaryService {
				backends[i].Weight = utilpointer.Int(w)
			}
		}
		apisixRouteClone.Spec.HTTP[index].Backends = backends
	}

	if reflect.DeepEqual(apisixRouteClone.Spec, apisixRoute.Spec) {
		return true, nil
	}

	if err = r.Update(ctx, apisixRouteClone); err != nil {
		klog.Errorf("rollout(%s/%s) update apisix route weight failed: %s", r.conf.RolloutNs, r.conf.RolloutName, err.Error())
		return false, err
	}

	return false, nil
}

func (r *apisixIngressController) Finalise(ctx context.Context) error {
	apisixRoute := &a6v2.ApisixRoute{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.TrafficConf.Name}, apisixRoute)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get apisix route(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.TrafficConf.Name, err.Error())
		return err
	}
	if errors.IsNotFound(err) || !apisixRoute.DeletionTimestamp.IsZero() {
		return nil
	}

	canaryApisixUpstream := &a6v2.ApisixUpstream{}
	err = r.Get(ctx, types.NamespacedName{Namespace: r.conf.RolloutNs, Name: r.conf.CanaryService}, canaryApisixUpstream)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get canary apisix upstream(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.CanaryService, err.Error())
		return err
	}

	// First, set canary backend 0 weight
	targetHTTPRoutes, _ := r.getTargetHTTPRoutes(apisixRoute, r.conf.StableService)
	for index, thr := range targetHTTPRoutes {
		backends := thr.Backends

		var canaryBackendIndex int
		var canaryBackendExists bool
		for i, backend := range backends {
			if backend.ServiceName == r.conf.StableService {
				backends[i].Weight = utilpointer.Int(100)
			} else if backend.ServiceName == r.conf.CanaryService {
				canaryBackendIndex = i
				canaryBackendExists = true
			}
		}

		if !canaryBackendExists {
			return fmt.Errorf("rollout(%s/%s) get apisix route canary backend(%s) failed", r.conf.RolloutNs, r.conf.RolloutName, r.conf.CanaryService)
		}

		// Remove canary backend from backends array by index
		if len(backends) == canaryBackendIndex+1 {
			backends = backends[:canaryBackendIndex]
		} else {
			backends = append(backends[:canaryBackendIndex], backends[canaryBackendIndex+1:]...)
		}
		apisixRoute.Spec.HTTP[index].Backends = backends
	}

	// Second, update apisix route object and remove canary backend
	if err = r.Update(ctx, apisixRoute); err != nil {
		klog.Errorf("rollout(%s/%s) remove apisix route canary backend(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.CanaryService, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) remove apisix route canary backend(%s) success", r.conf.RolloutNs, r.conf.RolloutName, r.conf.CanaryService)

	// Finally, remove canary apisix upstream object
	if err = r.Delete(ctx, canaryApisixUpstream); err != nil {
		klog.Errorf("rollout(%s/%s) remove canary apisix upstream(%s) failed: %s", r.conf.RolloutNs, r.conf.RolloutName, r.conf.CanaryService, err.Error())
		return err
	}
	klog.Infof("rollout(%s/%s) remove canary apisix upstream(%s) success", r.conf.RolloutNs, r.conf.RolloutName, r.conf.CanaryService)

	return nil
}

func (r *apisixIngressController) getTargetHTTPRoutes(ar *a6v2.ApisixRoute, serviceName string) (map[int]*a6v2.ApisixRouteHTTP, error) {
	thr := make(map[int]*a6v2.ApisixRouteHTTP)

	for index, item := range ar.Spec.HTTP {
		for _, backend := range item.Backends {
			if backend.ServiceName == serviceName {
				thr[index] = item.DeepCopy()
				break
			}
		}
	}

	if len(thr) <= 0 {
		return nil, fmt.Errorf("can not find %s backend on apisix route %s.%s ",
			serviceName, r.conf.RolloutNs, r.conf.TrafficConf.Name)
	}

	return thr, nil
}

func (r *apisixIngressController) buildCanaryApisixRoute(ar *a6v2.ApisixRoute) (*a6v2.ApisixRoute, error) {
	desiredApisixRoute := &a6v2.ApisixRoute{
		ObjectMeta: *ar.ObjectMeta.DeepCopy(),
		Spec: a6v2.ApisixRouteSpec{
			HTTP: ar.Spec.DeepCopy().HTTP,
		},
	}

	apisixRouteClone := ar.DeepCopy()
	if len(apisixRouteClone.Spec.HTTP) == 0 {
		return nil, fmt.Errorf("apisix route %s.%s's spec.http is empty",
			r.conf.RolloutNs, r.conf.TrafficConf.Name)
	}

	targetHTTPRoutes, err := r.getTargetHTTPRoutes(apisixRouteClone, r.conf.StableService)
	if err != nil {
		return nil, err
	}

	for index, thr := range targetHTTPRoutes {
		if len(thr.Backends) != 1 {
			return nil, fmt.Errorf("apisix route %s.%s's http route %s only one http backend is supported",
				r.conf.RolloutNs, r.conf.TrafficConf.Name, thr.Name)
		}

		primaryBackend := thr.Backends[0]
		primaryBackend.ServiceName = r.conf.StableService
		primaryWeight := 100
		primaryBackend.Weight = utilpointer.Int(primaryWeight)
		thr.Backends[0] = primaryBackend

		canaryWeight := 0
		canaryBackend := a6v2.ApisixRouteHTTPBackend{
			ServiceName:        r.conf.CanaryService,
			ServicePort:        primaryBackend.ServicePort,
			ResolveGranularity: primaryBackend.ResolveGranularity,
			Weight:             utilpointer.Int(canaryWeight),
		}

		desiredApisixRoute.Spec.HTTP[index].Backends = append(thr.Backends, canaryBackend)
	}

	return desiredApisixRoute, nil
}

func (r *apisixIngressController) buildCanaryApisixUpstream(au *a6v2.ApisixUpstream) (*a6v2.ApisixUpstream, error) {
	desiredApisixUpstream := &a6v2.ApisixUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:            au.Name,
			Namespace:       au.Namespace,
			Annotations:     au.Annotations,
			OwnerReferences: []metav1.OwnerReference{r.conf.OwnerRef},
		},
		Spec: au.Spec.DeepCopy(),
	}
	desiredApisixUpstream.ObjectMeta.Name = r.conf.CanaryService

	return desiredApisixUpstream, nil
}
