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

package trafficrouting

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	custom "github.com/openkruise/rollouts/pkg/trafficrouting/network/customNetworkProvider"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network/gateway"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network/ingress"
	"github.com/openkruise/rollouts/pkg/util"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
	"github.com/openkruise/rollouts/pkg/util/grace"
)

var (
	defaultGracePeriodSeconds int32 = 3
)

type TrafficRoutingContext struct {
	// only for log info
	Key                        string
	Namespace                  string
	ObjectRef                  []v1beta1.TrafficRoutingRef
	Strategy                   v1beta1.TrafficRoutingStrategy
	CanaryServiceSelectorPatch map[string]string
	// OnlyTrafficRouting
	OnlyTrafficRouting bool
	OwnerRef           metav1.OwnerReference
	// workload.RevisionLabelKey
	RevisionLabelKey string
	// status.CanaryStatus.StableRevision
	StableRevision string
	// status.CanaryStatus.PodTemplateHash
	CanaryRevision string
	// newStatus.canaryStatus.LastUpdateTime
	LastUpdateTime *metav1.Time
	// won't work for Ingress and Gateway
	DisableGenerateCanaryService bool
	// recheck time
	RecheckDuration time.Duration
}

// Manager responsible for adjusting network resources
// such as Service, Ingress, Gateway API, etc., to achieve traffic grayscale.
type Manager struct {
	client.Client
}

func NewTrafficRoutingManager(c client.Client) *Manager {
	return &Manager{c}
}

// InitializeTrafficRouting determine if the network resources(service & ingress & gateway api) exist.
// If it is Ingress, init method will create the canary ingress resources, and set weight=0.
func (m *Manager) InitializeTrafficRouting(c *TrafficRoutingContext) error {
	if len(c.ObjectRef) == 0 {
		return nil
	}
	objectRef := c.ObjectRef[0]
	sService := objectRef.Service
	// check service
	service := &corev1.Service{}
	if err := m.Get(context.TODO(), types.NamespacedName{Namespace: c.Namespace, Name: sService}, service); err != nil {
		return err
	}
	cService := getCanaryServiceName(sService, c.OnlyTrafficRouting, c.DisableGenerateCanaryService)
	// new network provider, ingress or gateway
	trController, err := newNetworkProvider(m.Client, c, sService, cService)
	if err != nil {
		klog.Errorf("%s newNetworkProvider failed: %s", c.Key, err.Error())
		return err
	}
	return trController.Initialize(context.TODO())
}

func (m *Manager) DoTrafficRouting(c *TrafficRoutingContext) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return true, nil
	}
	trafficRouting := c.ObjectRef[0]
	if trafficRouting.GracePeriodSeconds <= 0 {
		trafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}
	if c.Strategy.Traffic == nil && len(c.Strategy.Matches) == 0 {
		return true, nil
	}

	//fetch stable service
	stableService := &corev1.Service{}
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Namespace, Name: trafficRouting.Service}, stableService)
	if err != nil {
		klog.Errorf("%s get stable service(%s) failed: %s", c.Key, trafficRouting.Service, err.Error())
		// not found, wait a moment, retry
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	// canary service name
	canaryServiceName := getCanaryServiceName(trafficRouting.Service, c.OnlyTrafficRouting, c.DisableGenerateCanaryService)
	canaryService := &corev1.Service{}
	canaryService.Namespace = stableService.Namespace
	canaryService.Name = canaryServiceName

	if c.LastUpdateTime != nil {
		// wait seconds for network providers to consume the modification about workload, service and so on.
		if verifyTime := c.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
			klog.Infof("%s update workload or service selector, and wait %d seconds", c.Key, trafficRouting.GracePeriodSeconds)
			return false, nil
		}
	}

	// end-to-end canary deployment scenario(a -> b -> c), if only b or c is released,
	// and a is not released in this scenario, then the canary service is not needed.
	// amend: if c.disableGenerateCanaryService is true, canary service is not needed either
	if !(c.OnlyTrafficRouting || c.DisableGenerateCanaryService) {
		if c.StableRevision == "" || c.CanaryRevision == "" {
			klog.Warningf("%s stableRevision or podTemplateHash can not be empty, and wait a moment", c.Key)
			return false, nil
		}
		serviceModified := false
		// fetch canary service
		err = m.Get(context.TODO(), client.ObjectKey{Namespace: c.Namespace, Name: canaryServiceName}, canaryService)
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("%s get canary service(%s) failed: %s", c.Key, canaryServiceName, err.Error())
			return false, err
		} else if errors.IsNotFound(err) {
			canaryService, err = m.createCanaryService(c, canaryServiceName, *stableService.Spec.DeepCopy())
			if err != nil {
				return false, err
			}
			serviceModified = true
		} else if canaryService.Spec.Selector[c.RevisionLabelKey] != c.CanaryRevision {
			// patch canary service to only select the canary pods
			body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.RevisionLabelKey, c.CanaryRevision)
			if err = m.Patch(context.TODO(), canaryService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
				klog.Errorf("%s patch canary service(%s) selector failed: %s", c.Key, canaryService.Name, err.Error())
				return false, err
			}
			klog.Infof("%s patch canary service(%s) selector(%s=%s) success",
				c.Key, canaryService.Name, c.RevisionLabelKey, c.CanaryRevision)
			serviceModified = true
		}
		// patch stable service to only select the stable pods
		if stableService.Spec.Selector[c.RevisionLabelKey] != c.StableRevision {
			body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.RevisionLabelKey, c.StableRevision)
			if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
				klog.Errorf("%s patch stable service(%s) selector failed: %s", c.Key, stableService.Name, err.Error())
				return false, err
			}
			serviceModified = true
			klog.Infof("add %s stable service(%s) selector(%s=%s) success",
				c.Key, stableService.Name, c.RevisionLabelKey, c.StableRevision)
		}
		if serviceModified {
			// modification occurred, wait a grace period
			c.LastUpdateTime = &metav1.Time{Time: time.Now()}
			return false, nil
		}
	}

	// new network provider, ingress or gateway
	trController, err := newNetworkProvider(m.Client, c, stableService.Name, canaryService.Name)
	if err != nil {
		klog.Errorf("%s newNetworkProvider failed: %s", c.Key, err.Error())
		return false, err
	}
	verify, err := trController.EnsureRoutes(context.TODO(), &c.Strategy)
	if err != nil {
		return false, err
	} else if !verify {
		klog.Infof("%s is doing trafficRouting(%s), and wait a moment", c.Key, util.DumpJSON(c.Strategy))
		return false, nil
	}
	klog.Infof("%s do trafficRouting(%s) success", c.Key, util.DumpJSON(c.Strategy))
	return true, nil
}

func (m *Manager) FinalisingTrafficRouting(c *TrafficRoutingContext) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return true, nil
	}
	klog.InfoS("start finalising traffic routing", "rollout", c.Key)
	// remove stable service the pod revision selector, so stable service will select pods of all versions.
	if retry, err := m.RestoreStableService(c); err != nil || retry {
		return false, err
	}
	klog.InfoS("restore stable service success", "rollout", c.Key)
	// modify network(ingress & gateway api) configuration, route all traffic to stable service
	if retry, err := m.RestoreGateway(c); err != nil || retry {
		return false, err
	}
	klog.InfoS("restore gateway success", "rollout", c.Key)
	// remove canary service
	if retry, err := m.RemoveCanaryService(c); err != nil || retry {
		return false, err
	}
	klog.InfoS("remove canary service success, finalising traffic routing is done", "rollout", c.Key)
	return true, nil
}

// returns:
//   - if error is not nil, usually we need to retry later. Only if error is nil, we consider the bool.
//   - The bool value indicates whether retry is needed. If true, it usually means
//     gateway resources have been updated and we need to wait for `graceSeconds`.
//
// only if error is nil AND retry is false, this calling can be considered as completed
func (m *Manager) RouteAllTrafficToNewVersion(c *TrafficRoutingContext) (bool, error) {
	klog.InfoS("route all traffic to new version", "rollout", c.Key)
	if len(c.ObjectRef) == 0 {
		return false, nil
	}
	// build up the network provider
	stableService := c.ObjectRef[0].Service
	cServiceName := getCanaryServiceName(stableService, c.OnlyTrafficRouting, c.DisableGenerateCanaryService)
	trController, err := newNetworkProvider(m.Client, c, stableService, cServiceName)
	if err != nil {
		klog.Errorf("%s newTrafficRoutingController failed: %s", c.Key, err.Error())
		return false, err
	}
	graceSeconds := GetGraceSeconds(c.ObjectRef, defaultGracePeriodSeconds)
	retry, remaining, err := grace.RunWithGraceSeconds(string(c.OwnerRef.UID), "updateRoute", graceSeconds, func() (bool, error) {
		// route all traffic to new version
		c.Strategy.Matches = nil
		c.Strategy.Traffic = utilpointer.String("100%")
		//NOTE - This return value "verified" has the opposite semantics with "modified"
		verified, err := trController.EnsureRoutes(context.TODO(), &c.Strategy)
		if !verified {
			c.LastUpdateTime = &metav1.Time{Time: time.Now()}
		}
		return !verified, err
	})
	UpdateRecheckDuration(c, remaining)
	return retry, err
}

// returns:
//   - if error is not nil, usually we need to retry later. Only if error is nil, we consider the bool.
//   - The bool value indicates whether retry is needed. If true, it usually means
//     gateway resources have been updated and we need to wait for `graceSeconds`.
//
// only if error is nil AND retry is false, this calling can be considered as completed
func (m *Manager) RestoreGateway(c *TrafficRoutingContext) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return false, nil
	}
	// build up the network provider
	stableService := c.ObjectRef[0].Service
	cServiceName := getCanaryServiceName(stableService, c.OnlyTrafficRouting, c.DisableGenerateCanaryService)
	trController, err := newNetworkProvider(m.Client, c, stableService, cServiceName)
	if err != nil {
		klog.Errorf("%s newTrafficRoutingController failed: %s", c.Key, err.Error())
		return false, err
	}
	// restore Gateway/Ingress/Istio
	graceSeconds := GetGraceSeconds(c.ObjectRef, defaultGracePeriodSeconds)
	retry, remaining, err := grace.RunWithGraceSeconds(string(c.OwnerRef.UID), "restoreGateway", graceSeconds, func() (bool, error) {
		modified, err := trController.Finalise(context.TODO())
		if modified {
			c.LastUpdateTime = &metav1.Time{Time: time.Now()}
		}
		return modified, err
	})
	UpdateRecheckDuration(c, remaining)
	return retry, err
}

// returns:
//   - if error is not nil, usually we need to retry later. Only if error is nil, we consider the bool.
//   - The bool value indicates whether retry is needed. If true, it usually means
//     canary service has been deleted and we need to wait for `graceSeconds`.
//
// only if error is nil AND retry is false, this calling can be considered as completed
func (m *Manager) RemoveCanaryService(c *TrafficRoutingContext) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return false, nil
	}
	// end to end deployment scenario OR disableGenerateCanaryService is true, don't remove the canary service;
	// because canary service is stable service (ie. canary service is never created from the beginning)
	if c.OnlyTrafficRouting || c.DisableGenerateCanaryService {
		return false, nil
	}
	cServiceName := getCanaryServiceName(c.ObjectRef[0].Service, c.OnlyTrafficRouting, c.DisableGenerateCanaryService)
	cService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: cServiceName}}
	key := types.NamespacedName{
		Namespace: c.Namespace,
		Name:      cServiceName,
	}
	// remove canary service
	graceSeconds := GetGraceSeconds(c.ObjectRef, defaultGracePeriodSeconds)
	retry, remaining, err := grace.RunWithGraceSeconds(key.String(), "removeCanaryService", graceSeconds, func() (bool, error) {
		err := m.Delete(context.TODO(), cService)
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			klog.Errorf("%s remove canary service(%s) failed: %s", c.Key, cService.Name, err.Error())
			return false, err
		}
		return true, nil
	})
	UpdateRecheckDuration(c, remaining)
	return retry, err
}

// returns:
//   - if error is not nil, usually we need to retry later. Only if error is nil, we consider the bool.
//   - The bool value indicates whether retry is needed. If true, it usually means
//     stable service has been updated (ie. patched) and we need to wait for `graceSeconds`.
//
// only if error is nil AND retry is false, this calling can be considered as completed
func (m *Manager) PatchStableService(c *TrafficRoutingContext) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return false, nil
	}
	if c.OnlyTrafficRouting || c.DisableGenerateCanaryService {
		return true, nil
	}

	// fetch stable service
	stableService := &corev1.Service{}
	serviceName := c.ObjectRef[0].Service
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Namespace, Name: serviceName}, stableService)
	if err != nil {
		klog.Errorf("%s get stable service(%s) failed: %s", c.Key, serviceName, err.Error())
		return false, err
	}

	// restore stable Service
	graceSeconds := GetGraceSeconds(c.ObjectRef, defaultGracePeriodSeconds)
	retry, remaining, err := grace.RunWithGraceSeconds(string(stableService.UID), "patchService", graceSeconds, func() (bool, error) {
		modified := false
		if stableService.Spec.Selector[c.RevisionLabelKey] != c.StableRevision {
			body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.RevisionLabelKey, c.StableRevision)
			if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
				klog.Errorf("%s patch stable service(%s) selector failed: %s", c.Key, stableService.Name, err.Error())
				return false, err
			}
			c.LastUpdateTime = &metav1.Time{Time: time.Now()}
			modified = true
		}
		return modified, nil
	})
	UpdateRecheckDuration(c, remaining)
	return retry, err
}

func newNetworkProvider(c client.Client, con *TrafficRoutingContext, sService, cService string) (network.NetworkProvider, error) {
	trafficRouting := con.ObjectRef[0]
	networkProviders := make([]network.NetworkProvider, 0, 3)
	if trafficRouting.CustomNetworkRefs != nil {
		np, innerErr := custom.NewCustomController(c, custom.Config{
			Key:           con.Key,
			RolloutNs:     con.Namespace,
			CanaryService: cService,
			StableService: sService,
			TrafficConf:   trafficRouting.CustomNetworkRefs,
			OwnerRef:      con.OwnerRef,
			//only set for CustomController, never work for Ingress and Gateway
			DisableGenerateCanaryService: con.DisableGenerateCanaryService,
		})
		if innerErr != nil {
			return nil, innerErr
		}
		networkProviders = append(networkProviders, np)
	}
	if trafficRouting.Ingress != nil {
		np, innerErr := ingress.NewIngressTrafficRouting(c, ingress.Config{
			Key:           con.Key,
			Namespace:     con.Namespace,
			CanaryService: cService,
			StableService: sService,
			TrafficConf:   trafficRouting.Ingress,
			OwnerRef:      con.OwnerRef,
		})
		if innerErr != nil {
			return nil, innerErr
		}
		networkProviders = append(networkProviders, np)
	}
	if trafficRouting.Gateway != nil {
		np, innerErr := gateway.NewGatewayTrafficRouting(c, gateway.Config{
			Key:           con.Key,
			Namespace:     con.Namespace,
			CanaryService: cService,
			StableService: sService,
			TrafficConf:   trafficRouting.Gateway,
		})
		if innerErr != nil {
			return nil, innerErr
		}
		networkProviders = append(networkProviders, np)
	}
	if len(networkProviders) == 0 {
		return nil, fmt.Errorf("TrafficRouting current only supports Ingress, Gateway API and CustomNetworkRefs")
	} else if len(networkProviders) == 1 {
		return networkProviders[0], nil
	}
	return network.CompositeController(networkProviders), nil
}

func (m *Manager) createCanaryService(c *TrafficRoutingContext, cService string, spec corev1.ServiceSpec) (*corev1.Service, error) {
	canaryService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       c.Namespace,
			Name:            cService,
			OwnerReferences: []metav1.OwnerReference{c.OwnerRef},
		},
		Spec: spec,
	}

	// set field nil
	canaryService.Spec.ClusterIP = ""
	canaryService.Spec.ClusterIPs = nil
	canaryService.Spec.ExternalIPs = nil
	canaryService.Spec.IPFamilyPolicy = nil
	canaryService.Spec.IPFamilies = nil
	canaryService.Spec.LoadBalancerIP = ""
	canaryService.Spec.Selector[c.RevisionLabelKey] = c.CanaryRevision

	// avoid port conflicts for NodePort-type service
	for i := range canaryService.Spec.Ports {
		canaryService.Spec.Ports[i].NodePort = 0
	}
	for key, val := range c.CanaryServiceSelectorPatch {
		if _, ok := canaryService.Spec.Selector[key]; ok {
			canaryService.Spec.Selector[key] = val
		} else if utilfeature.DefaultFeatureGate.Enabled(feature.AppendServiceSelectorGate) {
			canaryService.Spec.Selector[key] = val
		}
	}
	err := m.Create(context.TODO(), canaryService)
	if err != nil && !errors.IsAlreadyExists(err) {
		klog.Errorf("%s create canary service(%s) failed: %s", c.Key, cService, err.Error())
		return nil, err
	}
	klog.Infof("%s create canary service(%s) success", c.Key, util.DumpJSON(canaryService))
	return canaryService, nil
}

// remove stable service the pod revision selector, so stable service will be selector all version pods.
// returns:
//   - if error is not nil, usually we need to retry later. Only if error is nil, we consider the bool.
//   - The bool value indicates whether retry is needed. If true, it usually means
//     stable service has been updated (ie. restored) and we need to wait for `graceSeconds`.
//
// only if error is nil AND retry is false, this calling can be considered as completed
func (m *Manager) RestoreStableService(c *TrafficRoutingContext) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return false, nil
	}

	// fetch the stable Service
	stableService := &corev1.Service{}
	serviceName := c.ObjectRef[0].Service
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Namespace, Name: serviceName}, stableService)
	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		klog.Errorf("%s get stable service(%s) failed: %s", c.Key, serviceName, err.Error())
		return true, err
	}

	// restore stable Service
	graceSeconds := GetGraceSeconds(c.ObjectRef, defaultGracePeriodSeconds)
	retry, remaining, err := grace.RunWithGraceSeconds(string(stableService.UID), "restoreService", graceSeconds, func() (bool, error) {
		modified := false
		if stableService.Spec.Selector[c.RevisionLabelKey] != "" {
			body := fmt.Sprintf(`{"spec":{"selector":{"%s":null}}}`, c.RevisionLabelKey)
			if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
				klog.Errorf("%s patch stable service(%s) failed: %s", c.Key, serviceName, err.Error())
				return false, err
			}
			c.LastUpdateTime = &metav1.Time{Time: time.Now()}
			modified = true
		}
		return modified, nil
	})
	UpdateRecheckDuration(c, remaining)
	return retry, err
}

func getCanaryServiceName(sService string, onlyTrafficRouting bool, disableGenerateCanaryService bool) string {
	if onlyTrafficRouting || disableGenerateCanaryService {
		return sService
	}
	return fmt.Sprintf("%s-canary", sService)
}

func GetGraceSeconds(refs []v1beta1.TrafficRoutingRef, defaultSeconds int32) (graceSeconds int32) {
	if len(refs) == 0 {
		klog.Infof("no trafficRoutingRef, use defaultGracePeriodSeconds(%d)", defaultSeconds)
		return defaultSeconds
	}
	for i := range refs {
		graceSeconds = integer.Int32Max(graceSeconds, refs[i].GracePeriodSeconds)
	}
	// user may intentionally set graceSeconds as 0 (if not provided, defaults to 3)
	// we respect it
	if graceSeconds < 0 {
		klog.Infof("negative graceSeconds(%d), use defaultGracePeriodSeconds(%d)", graceSeconds, defaultSeconds)
		return defaultSeconds
	}
	klog.Infof("use graceSeconds(%d)", graceSeconds)
	return
}

func UpdateRecheckDuration(c *TrafficRoutingContext, remaining time.Duration) {
	if c.RecheckDuration < remaining {
		c.RecheckDuration = remaining
	}
}
