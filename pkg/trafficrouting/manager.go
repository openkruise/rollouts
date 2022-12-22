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
	rolloutControllerKind           = v1alpha1.SchemeGroupVersion.WithKind("Rollout")
)

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
func (m *Manager) InitializeTrafficRouting(c *util.RolloutContext) error {
	if len(c.Rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
		return nil
	}
	sService := c.Rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service
	// check service
	service := &corev1.Service{}
	if err := m.Get(context.TODO(), types.NamespacedName{Namespace: c.Rollout.Namespace, Name: sService}, service); err != nil {
		return err
	}
	cService := fmt.Sprintf("%s-canary", sService)
	// new network provider, ingress or gateway
	trController, err := newNetworkProvider(m.Client, c.Rollout, c.NewStatus, sService, cService)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newNetworkProvider failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return err
	}
	return trController.Initialize(context.TODO())
}

func (m *Manager) DoTrafficRouting(c *util.RolloutContext) (bool, error) {
	if len(c.Rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
		return true, nil
	}
	trafficRouting := c.Rollout.Spec.Strategy.Canary.TrafficRoutings[0]
	if trafficRouting.GracePeriodSeconds <= 0 {
		trafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}
	canaryStatus := c.NewStatus.CanaryStatus
	if canaryStatus.StableRevision == "" || canaryStatus.PodTemplateHash == "" {
		klog.Warningf("rollout(%s/%s) stableRevision or podTemplateHash can not be empty, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
		return false, nil
	}
	//fetch stable service
	stableService := &corev1.Service{}
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Rollout.Namespace, Name: trafficRouting.Service}, stableService)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get stable service(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, trafficRouting.Service, err.Error())
		// not found, wait a moment, retry
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	// canary service name
	canaryServiceName := fmt.Sprintf("%s-canary", trafficRouting.Service)
	// fetch canary service
	canaryService := &corev1.Service{}
	err = m.Get(context.TODO(), client.ObjectKey{Namespace: c.Rollout.Namespace, Name: canaryServiceName}, canaryService)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary service(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, canaryServiceName, err.Error())
		return false, err
	} else if errors.IsNotFound(err) {
		canaryService, err = m.createCanaryService(c, canaryServiceName, *stableService.Spec.DeepCopy())
		if err != nil {
			return false, err
		}
	}

	// patch canary service only selector the canary pods
	if canaryService.Spec.Selector[c.Workload.RevisionLabelKey] != canaryStatus.PodTemplateHash {
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.Workload.RevisionLabelKey, canaryStatus.PodTemplateHash)
		if err = m.Patch(context.TODO(), canaryService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch canary service(%s) selector failed: %s", c.Rollout.Namespace, c.Rollout.Name, canaryService.Name, err.Error())
			return false, err
		}
		// update canary service time, and wait 3 seconds, just to be safe
		canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.Infof("rollout(%s/%s) patch canary service(%s) selector(%s=%s) success",
			c.Rollout.Namespace, c.Rollout.Name, canaryService.Name, c.Workload.RevisionLabelKey, canaryStatus.PodTemplateHash)
	}
	// patch stable service only selector the stable pods
	if stableService.Spec.Selector[c.Workload.RevisionLabelKey] != canaryStatus.StableRevision {
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, c.Workload.RevisionLabelKey, canaryStatus.StableRevision)
		if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch stable service(%s) selector failed: %s", c.Rollout.Namespace, c.Rollout.Name, stableService.Name, err.Error())
			return false, err
		}
		// update stable service time, and wait 3 seconds, just to be safe
		canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.Infof("add rollout(%s/%s) stable service(%s) selector(%s=%s) success",
			c.Rollout.Namespace, c.Rollout.Name, stableService.Name, c.Workload.RevisionLabelKey, canaryStatus.StableRevision)
		return false, nil
	}
	// After modify stable service configuration, give the network provider 3 seconds to react
	if verifyTime := canaryStatus.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("rollout(%s/%s) update service selector, and wait 3 seconds", c.Rollout.Namespace, c.Rollout.Name)
		return false, nil
	}

	// new network provider, ingress or gateway
	trController, err := newNetworkProvider(m.Client, c.Rollout, c.NewStatus, stableService.Name, canaryService.Name)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newNetworkProvider failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return false, err
	}
	cStep := c.Rollout.Spec.Strategy.Canary.Steps[canaryStatus.CurrentStepIndex-1]
	steps := len(c.Rollout.Spec.Strategy.Canary.Steps)
	cond := util.GetRolloutCondition(*c.NewStatus, v1alpha1.RolloutConditionProgressing)
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and doing traffic routing", canaryStatus.CurrentStepIndex, steps)
	verify, err := trController.EnsureRoutes(context.TODO(), cStep.Weight, cStep.Matches)
	if err != nil {
		return false, err
	} else if !verify {
		klog.Infof("rollout(%s/%s) is doing step(%d) trafficRouting(%s)", c.Rollout.Namespace, c.Rollout.Name, canaryStatus.CurrentStepIndex, util.DumpJSON(cStep))
		return false, nil
	}
	klog.Infof("rollout(%s/%s) do step(%d) trafficRouting(%s) success", c.Rollout.Namespace, c.Rollout.Name, canaryStatus.CurrentStepIndex, util.DumpJSON(cStep))
	return true, nil
}

func (m *Manager) FinalisingTrafficRouting(c *util.RolloutContext, onlyRestoreStableService bool) (bool, error) {
	if len(c.Rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
		return true, nil
	}
	trafficRouting := c.Rollout.Spec.Strategy.Canary.TrafficRoutings[0]
	if trafficRouting.GracePeriodSeconds <= 0 {
		trafficRouting.GracePeriodSeconds = defaultGracePeriodSeconds
	}
	if c.NewStatus.CanaryStatus == nil {
		c.NewStatus.CanaryStatus = &v1alpha1.CanaryStatus{}
	}
	klog.Infof("rollout(%s/%s) start finalising traffic routing", c.Rollout.Namespace, c.Rollout.Name)
	// remove stable service the pod revision selector, so stable service will be selector all version pods.
	verify, err := m.restoreStableService(c)
	if err != nil || !verify {
		return false, err
	} else if onlyRestoreStableService {
		return true, nil
	}

	cServiceName := fmt.Sprintf("%s-canary", trafficRouting.Service)
	trController, err := newNetworkProvider(m.Client, c.Rollout, c.NewStatus, trafficRouting.Service, cServiceName)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newTrafficRoutingController failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return false, err
	}
	// First route 100% traffic to stable service
	verify, err = trController.EnsureRoutes(context.TODO(), utilpointer.Int32(0), nil)
	if err != nil {
		return false, err
	} else if !verify {
		c.NewStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		return false, nil
	}
	if c.NewStatus.CanaryStatus.LastUpdateTime != nil {
		// After restore the stable service configuration, give network provider 3 seconds to react
		if verifyTime := c.NewStatus.CanaryStatus.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
			klog.Infof("rollout(%s/%s) route 100% traffic to stable service, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
			return false, nil
		}
	}

	// modify network(ingress & gateway api) configuration, route all traffic to stable service
	if err = trController.Finalise(context.TODO()); err != nil {
		return false, err
	}
	// remove canary service
	cService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Rollout.Namespace, Name: cServiceName}}
	err = m.Delete(context.TODO(), cService)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) remove canary service(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, cService.Name, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) remove canary service(%s) success", c.Rollout.Namespace, c.Rollout.Name, cService.Name)
	return true, nil
}

func newNetworkProvider(c client.Client, rollout *v1alpha1.Rollout, newStatus *v1alpha1.RolloutStatus, sService, cService string) (network.NetworkProvider, error) {
	trafficRouting := rollout.Spec.Strategy.Canary.TrafficRoutings[0]
	if trafficRouting.Ingress != nil {
		switch trafficRouting.Ingress.ClassType {
		case "apisix":
			return ingress.NewApisixIngressTrafficRouting(c, ingress.Config{
				RolloutName:   rollout.Name,
				RolloutNs:     rollout.Namespace,
				CanaryService: cService,
				StableService: sService,
				TrafficConf:   trafficRouting.Ingress,
				OwnerRef:      *metav1.NewControllerRef(rollout, rolloutControllerKind),
			})
		case "nginx":
		case "alb":
		default:
			return ingress.NewNginxIngressTrafficRouting(c, ingress.Config{
				RolloutName:   rollout.Name,
				RolloutNs:     rollout.Namespace,
				CanaryService: cService,
				StableService: sService,
				TrafficConf:   trafficRouting.Ingress,
				OwnerRef:      *metav1.NewControllerRef(rollout, rolloutControllerKind),
			})
		}
	}
	if trafficRouting.Gateway != nil {
		return gateway.NewGatewayTrafficRouting(c, gateway.Config{
			RolloutName:   rollout.Name,
			RolloutNs:     rollout.Namespace,
			CanaryService: cService,
			StableService: sService,
			TrafficConf:   trafficRouting.Gateway,
		})
	}
	return nil, fmt.Errorf("TrafficRouting current only support Ingress or Gateway API")
}

func (m *Manager) createCanaryService(c *util.RolloutContext, cService string, spec corev1.ServiceSpec) (*corev1.Service, error) {
	canaryService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       c.Rollout.Namespace,
			Name:            cService,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(c.Rollout, rolloutControllerKind)},
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
	canaryService.Spec.Selector[c.Workload.RevisionLabelKey] = c.NewStatus.CanaryStatus.PodTemplateHash
	err := m.Create(context.TODO(), canaryService)
	if err != nil && !errors.IsAlreadyExists(err) {
		klog.Errorf("rollout(%s/%s) create canary service(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, cService, err.Error())
		return nil, err
	}
	klog.Infof("rollout(%s/%s) create canary service(%s) success", c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(canaryService))
	return canaryService, nil
}

// remove stable service the pod revision selector, so stable service will be selector all version pods.
func (m *Manager) restoreStableService(c *util.RolloutContext) (bool, error) {
	if c.Workload == nil {
		return true, nil
	}
	trafficRouting := c.Rollout.Spec.Strategy.Canary.TrafficRoutings[0]
	//fetch stable service
	stableService := &corev1.Service{}
	err := m.Get(context.TODO(), client.ObjectKey{Namespace: c.Rollout.Namespace, Name: trafficRouting.Service}, stableService)
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		klog.Errorf("rollout(%s/%s) get stable service(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, trafficRouting.Service, err.Error())
		return false, err
	}
	if stableService.Spec.Selector[c.Workload.RevisionLabelKey] != "" {
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":null}}}`, c.Workload.RevisionLabelKey)
		if err = m.Patch(context.TODO(), stableService, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch stable service(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, trafficRouting.Service, err.Error())
			return false, err
		}
		klog.Infof("remove rollout(%s/%s) stable service(%s) pod revision selector, and wait a moment", c.Rollout.Namespace, c.Rollout.Name, trafficRouting.Service)
		c.NewStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		return false, nil
	}
	if c.NewStatus.CanaryStatus.LastUpdateTime == nil {
		return true, nil
	}
	// After restore the stable service configuration, give network provider 3 seconds to react
	if verifyTime := c.NewStatus.CanaryStatus.LastUpdateTime.Add(time.Second * time.Duration(trafficRouting.GracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("rollout(%s/%s) restoring stable service(%s), and wait a moment", c.Rollout.Namespace, c.Rollout.Name, trafficRouting.Service)
		return false, nil
	}
	klog.Infof("rollout(%s/%s) doFinalising stable service(%s) success", c.Rollout.Namespace, c.Rollout.Name, trafficRouting.Service)
	return true, nil
}
