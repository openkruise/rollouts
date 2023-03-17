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

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network/gateway"
	"github.com/openkruise/rollouts/pkg/trafficrouting/network/ingress"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	defaultGracePeriodSeconds int32 = 3
)

type TrafficRoutingContext struct {
	// only for log info
	Key       string
	Namespace string
	ObjectRef []v1alpha1.TrafficRoutingRef
	Strategy  v1alpha1.TrafficRoutingStrategy
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
	cService := getCanaryServiceName(sService, c.OnlyTrafficRouting)
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
	if c.Strategy.Weight == nil && len(c.Strategy.Matches) == 0 {
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
	canaryServiceName := getCanaryServiceName(trafficRouting.Service, c.OnlyTrafficRouting)
	canaryService := &corev1.Service{}
	canaryService.Namespace = stableService.Namespace
	canaryService.Name = canaryServiceName
	// In full-link grayscale scenario(a -> b -> c), if only b or c is released,
	//and a is not released in this scenario, then the canary service is not needed.
	if c.OnlyTrafficRouting {
		goto HandlerNetworkProvider
	}

	if c.StableRevision == "" || c.CanaryRevision == "" {
		klog.Warningf("%s stableRevision or podTemplateHash can not be empty, and wait a moment", c.Key)
		return false, nil
	}
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
	}

	// patch canary service only selector the canary pods
	if canaryService.Spec.Selector[c.RevisionLabelKey] != c.CanaryRevision {
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.RevisionLabelKey, c.CanaryRevision)
		if err = m.Patch(context.TODO(), canaryService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("%s patch canary service(%s) selector failed: %s", c.Key, canaryService.Name, err.Error())
			return false, err
		}
		// update canary service time, and wait 3 seconds, just to be safe
		c.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.Infof("%s patch canary service(%s) selector(%s=%s) success",
			c.Key, canaryService.Name, c.RevisionLabelKey, c.CanaryRevision)
	}
	// patch stable service only selector the stable pods
	if stableService.Spec.Selector[c.RevisionLabelKey] != c.StableRevision {
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.RevisionLabelKey, c.StableRevision)
		if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("%s patch stable service(%s) selector failed: %s", c.Key, stableService.Name, err.Error())
			return false, err
		}
		// update stable service time, and wait 3 seconds, just to be safe
		c.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.Infof("add %s stable service(%s) selector(%s=%s) success",
			c.Key, stableService.Name, c.RevisionLabelKey, c.StableRevision)
		return false, nil
	}
	// After modify stable service configuration, give the network provider 3 seconds to react
	if verifyTime := c.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("%s update service selector, and wait 3 seconds", c.Key)
		return false, nil
	}

HandlerNetworkProvider:
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

func (m *Manager) FinalisingTrafficRouting(c *TrafficRoutingContext, onlyRestoreStableService bool) (bool, error) {
	if len(c.ObjectRef) == 0 {
		return true, nil
	}
	trafficRouting := c.ObjectRef[0]
	if trafficRouting.GracePeriodSeconds <= 0 {
		trafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}

	cServiceName := getCanaryServiceName(trafficRouting.Service, c.OnlyTrafficRouting)
	trController, err := newNetworkProvider(m.Client, c, trafficRouting.Service, cServiceName)
	if err != nil {
		klog.Errorf("%s newTrafficRoutingController failed: %s", c.Key, err.Error())
		return false, err
	}

	cService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: cServiceName}}
	// if canary svc has been already cleaned up, just return
	if err = m.Get(context.TODO(), client.ObjectKeyFromObject(cService), cService); err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("%s get canary service(%s) failed: %s", c.Key, cServiceName, err.Error())
			return false, err
		}
		// In rollout failure case, no canary-service will be created, this step ensures that the canary-ingress can be deleted in a time.
		if err = trController.Finalise(context.TODO()); err != nil {
			return false, err
		}
		return true, nil
	}

	klog.Infof("%s start finalising traffic routing", c.Key)
	// remove stable service the pod revision selector, so stable service will be selector all version pods.
	verify, err := m.restoreStableService(c)
	if err != nil || !verify {
		return false, err
	} else if onlyRestoreStableService {
		return true, nil
	}

	// First route 100% traffic to stable service
	c.Strategy.Weight = utilpointer.Int32(0)
	verify, err = trController.EnsureRoutes(context.TODO(), &c.Strategy)
	if err != nil {
		return false, err
	} else if !verify {
		c.LastUpdateTime = &metav1.Time{Time: time.Now()}
		return false, nil
	}
	if c.LastUpdateTime != nil {
		// After restore the stable service configuration, give network provider 3 seconds to react
		if verifyTime := c.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
			klog.Infof("%s route 100% traffic to stable service, and wait a moment", c.Key)
			return false, nil
		}
	}

	// modify network(ingress & gateway api) configuration, route all traffic to stable service
	if err = trController.Finalise(context.TODO()); err != nil {
		return false, err
	}
	// remove canary service
	err = m.Delete(context.TODO(), cService)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("%s remove canary service(%s) failed: %s", c.Key, cService.Name, err.Error())
		return false, err
	}
	klog.Infof("%s remove canary service(%s) success", c.Key, cService.Name)
	return true, nil
}

func newNetworkProvider(c client.Client, con *TrafficRoutingContext, sService, cService string) (network.NetworkProvider, error) {
	trafficRouting := con.ObjectRef[0]
	if trafficRouting.Ingress != nil {
		return ingress.NewIngressTrafficRouting(c, ingress.Config{
			Key:           con.Key,
			Namespace:     con.Namespace,
			CanaryService: cService,
			StableService: sService,
			TrafficConf:   trafficRouting.Ingress,
			OwnerRef:      con.OwnerRef,
		})
	}
	if trafficRouting.Gateway != nil {
		return gateway.NewGatewayTrafficRouting(c, gateway.Config{
			Key:           con.Key,
			Namespace:     con.Namespace,
			CanaryService: cService,
			StableService: sService,
			TrafficConf:   trafficRouting.Gateway,
		})
	}
	return nil, fmt.Errorf("TrafficRouting current only support Ingress or Gateway API")
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
	err := m.Create(context.TODO(), canaryService)
	if err != nil && !errors.IsAlreadyExists(err) {
		klog.Errorf("%s create canary service(%s) failed: %s", c.Key, cService, err.Error())
		return nil, err
	}
	klog.Infof("%s create canary service(%s) success", c.Key, util.DumpJSON(canaryService))
	return canaryService, nil
}

// remove stable service the pod revision selector, so stable service will be selector all version pods.
func (m *Manager) restoreStableService(c *TrafficRoutingContext) (bool, error) {
	trafficRouting := c.ObjectRef[0]
	if trafficRouting.GracePeriodSeconds <= 0 {
		trafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}
	//fetch stable service
	stableService := &corev1.Service{}
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Namespace, Name: trafficRouting.Service}, stableService)
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		klog.Errorf("%s get stable service(%s) failed: %s", c.Key, trafficRouting.Service, err.Error())
		return false, err
	}
	if stableService.Spec.Selector[c.RevisionLabelKey] != "" {
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":null}}}`, c.RevisionLabelKey)
		if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("%s patch stable service(%s) failed: %s", c.Key, trafficRouting.Service, err.Error())
			return false, err
		}
		klog.Infof("remove %s stable service(%s) pod revision selector, and wait a moment", c.Key, trafficRouting.Service)
		c.LastUpdateTime = &metav1.Time{Time: time.Now()}
		return false, nil
	}
	if c.LastUpdateTime == nil {
		return true, nil
	}
	// After restore the stable service configuration, give network provider 3 seconds to react
	if verifyTime := c.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("%s restoring stable service(%s), and wait a moment", c.Key, trafficRouting.Service)
		return false, nil
	}
	klog.Infof("%s doFinalising stable service(%s) success", c.Key, trafficRouting.Service)
	return true, nil
}

func getCanaryServiceName(sService string, onlyTrafficRouting bool) string {
	if onlyTrafficRouting {
		return sService
	}
	return fmt.Sprintf("%s-canary", sService)
}
