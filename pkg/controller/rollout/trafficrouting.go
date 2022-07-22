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

package rollout

import (
	"context"
	"fmt"
	"time"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting/gateway"
	"github.com/openkruise/rollouts/pkg/controller/rollout/trafficrouting/nginx"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *rolloutContext) doCanaryTrafficRouting() (bool, error) {
	if len(r.rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
		return true, nil
	}

	if r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds <= 0 {
		r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds = defaultGracePeriodSeconds
	}
	canaryStatus := r.newStatus.CanaryStatus
	if r.newStatus.StableRevision == "" || canaryStatus.PodTemplateHash == "" {
		klog.Warningf("rollout(%s/%s) stableRevision or podTemplateHash can't be empty, and wait a moment", r.rollout.Namespace, r.rollout.Name)
		return false, nil
	}
	canaryStatus.CanaryService = r.canaryService
	//fetch stable service
	stableService := &corev1.Service{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.stableService}, stableService)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get stable service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.stableService, err.Error())
		// not found, wait a moment, retry
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	// fetch canary service
	// todo for the time being, we do not consider the scenario where the user only changes the stable service definition during rollout progressing
	canaryService := &corev1.Service{}
	err = r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.canaryService}, canaryService)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.canaryService, err.Error())
		return false, err
	} else if errors.IsNotFound(err) {
		klog.Infof("rollout(%s/%s) canary service(%s) Not Found, and create it", r.rollout.Namespace, r.rollout.Name, r.canaryService)
		return false, r.createCanaryService(stableService)
	}

	// update service selector
	// update service selector specific revision pods
	if canaryService.Spec.Selector[r.podRevisionLabelKey()] != canaryStatus.PodTemplateHash {
		cloneObj := canaryService.DeepCopy()
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, r.podRevisionLabelKey(), canaryStatus.PodTemplateHash)
		if err = r.Patch(context.TODO(), cloneObj, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch canary service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.canaryService, err.Error())
			return false, err
		}
		// update canary service time, and wait 3 seconds, just to be safe
		canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.Infof("add rollout(%s/%s) canary service(%s) selector(%s=%s) success",
			r.rollout.Namespace, r.rollout.Name, r.canaryService, r.podRevisionLabelKey(), canaryStatus.PodTemplateHash)
	}
	if stableService.Spec.Selector[r.podRevisionLabelKey()] != r.newStatus.StableRevision {
		cloneObj := stableService.DeepCopy()
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":"%s"}}}`, r.podRevisionLabelKey(), r.newStatus.StableRevision)
		if err = r.Patch(context.TODO(), cloneObj, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch stable service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.stableService, err.Error())
			return false, err
		}
		// update stable service time, and wait 3 seconds, just to be safe
		canaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		klog.Infof("add rollout(%s/%s) stable service(%s) selector(%s=%s) success",
			r.rollout.Namespace, r.rollout.Name, r.stableService, r.podRevisionLabelKey(), r.newStatus.StableRevision)
		return false, nil
	}

	// After restore stable service configuration, give the ingress provider 3 seconds to take effect
	if verifyTime := canaryStatus.LastUpdateTime.Add(time.Second * time.Duration(r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds)); verifyTime.After(time.Now()) {
		klog.Infof("update rollout(%s/%s) stable service(%s) done, and wait 3 seconds", r.rollout.Namespace, r.rollout.Name, r.stableService)
		return false, nil
	}

	// route traffic configuration
	trController, err := r.newTrafficRoutingController(r)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newTrafficRoutingController failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}
	var desiredWeight int32
	if len(r.rollout.Spec.Strategy.Canary.Steps) > 0 {
		desiredWeight = *r.rollout.Spec.Strategy.Canary.Steps[r.newStatus.CanaryStatus.CurrentStepIndex-1].Weight
	}
	steps := len(r.rollout.Spec.Strategy.Canary.Steps)
	cond := util.GetRolloutCondition(*r.newStatus, rolloutv1alpha1.RolloutConditionProgressing)
	cond.Message = fmt.Sprintf("Rollout is in step(%d/%d), and route traffic weight(%d)", canaryStatus.CurrentStepIndex, steps, desiredWeight)
	verify, err := trController.EnsureRoutes(context.TODO(), desiredWeight)
	if err != nil {
		return false, err
	} else if !verify {
		klog.Infof("rollout(%s/%s) is doing step(%d) trafficRouting(%d)", r.rollout.Namespace, r.rollout.Name, r.newStatus.CanaryStatus.CurrentStepIndex, desiredWeight)
		return false, nil
	}
	klog.Infof("rollout(%s/%s) do step(%d) trafficRouting(%d) success", r.rollout.Namespace, r.rollout.Name, r.newStatus.CanaryStatus.CurrentStepIndex, desiredWeight)
	return true, nil
}

func (r *rolloutContext) restoreStableService() (bool, error) {
	if len(r.rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
		return true, nil
	}

	if r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds <= 0 {
		r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds = defaultGracePeriodSeconds
	}
	//fetch stable service
	stableService := &corev1.Service{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.stableService}, stableService)
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		klog.Errorf("rollout(%s/%s) get stable service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.stableService, err.Error())
		return false, err
	}

	if r.newStatus.CanaryStatus == nil {
		r.newStatus.CanaryStatus = &rolloutv1alpha1.CanaryStatus{}
	}
	//restore stable service configuration，remove hash revision selector
	if stableService.Spec.Selector != nil && stableService.Spec.Selector[r.podRevisionLabelKey()] != "" {
		cloneObj := stableService.DeepCopy()
		body := fmt.Sprintf(`{"spec":{"selector":{"%s":null}}}`, r.podRevisionLabelKey())
		if err = r.Patch(context.TODO(), cloneObj, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
			klog.Errorf("rollout(%s/%s) patch stable service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.stableService, err.Error())
			return false, err
		}
		klog.Infof("remove rollout(%s/%s) stable service(%s) pod revision selector success, and retry later", r.rollout.Namespace, r.rollout.Name, r.stableService)
		r.newStatus.CanaryStatus.LastUpdateTime = &metav1.Time{Time: time.Now()}
		return false, nil
	}
	// After restore stable service configuration, give the ingress provider 3 seconds to take effect
	if r.newStatus.CanaryStatus.LastUpdateTime != nil {
		if verifyTime := r.newStatus.CanaryStatus.LastUpdateTime.Add(time.Second * time.Duration(r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds)); verifyTime.After(time.Now()) {
			klog.Infof("restore rollout(%s/%s) stable service(%s) done, and wait a moment", r.rollout.Namespace, r.rollout.Name, r.stableService)
			return false, nil
		}
	}
	klog.Infof("rollout(%s/%s) doFinalising restore stable service(%s) success", r.rollout.Namespace, r.rollout.Name, r.stableService)
	return true, nil
}

func (r *rolloutContext) doFinalisingTrafficRouting() (bool, error) {
	if len(r.rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
		return true, nil
	}
	klog.Infof("rollout(%s/%s) start finalising traffic routing", r.rollout.Namespace, r.rollout.Name)
	if r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds <= 0 {
		r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds = defaultGracePeriodSeconds
	}
	if r.newStatus.CanaryStatus == nil {
		r.newStatus.CanaryStatus = &rolloutv1alpha1.CanaryStatus{}
	}
	// 1. restore ingress and route traffic to stable service
	trController, err := r.newTrafficRoutingController(r)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newTrafficRoutingController failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}
	verify, err := trController.EnsureRoutes(context.TODO(), 0)
	if err != nil {
		return false, err
	} else if !verify {
		klog.Infof("rollout(%s/%s) do finalising: ensure canary routes(weight:0)", r.rollout.Namespace, r.rollout.Name)
		return false, nil
	}

	// DoFinalising, such as delete nginx canary ingress
	if err = trController.Finalise(context.TODO()); err != nil {
		return false, err
	}

	// 2. remove canary service
	if r.newStatus.CanaryStatus.CanaryService == "" {
		return true, nil
	}
	cService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.rollout.Namespace,
			Name:      r.newStatus.CanaryStatus.CanaryService,
		},
	}
	err = r.Delete(context.TODO(), cService)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) remove canary service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, cService.Name, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) remove canary service(%s) success", r.rollout.Namespace, r.rollout.Name, cService.Name)
	return true, nil
}

func (r *rolloutContext) newTrafficRoutingController(roCtx *rolloutContext) (trafficrouting.Controller, error) {
	canary := roCtx.rollout.Spec.Strategy.Canary
	if canary.TrafficRoutings[0].Ingress != nil {
		gvk := schema.GroupVersionKind{Group: rolloutv1alpha1.GroupVersion.Group, Version: rolloutv1alpha1.GroupVersion.Version, Kind: "Rollout"}
		return nginx.NewNginxTrafficRouting(r.Client, r.newStatus, nginx.Config{
			RolloutName:   r.rollout.Name,
			RolloutNs:     r.rollout.Namespace,
			CanaryService: r.canaryService,
			StableService: r.stableService,
			TrafficConf:   r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress,
			OwnerRef:      *metav1.NewControllerRef(r.rollout, gvk),
		})
	}
	if canary.TrafficRoutings[0].Gateway != nil {
		return gateway.NewGatewayTrafficRouting(r.Client, r.newStatus, gateway.Config{
			RolloutName:   r.rollout.Name,
			RolloutNs:     r.rollout.Namespace,
			CanaryService: r.canaryService,
			StableService: r.stableService,
			TrafficConf:   r.rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway,
		})
	}
	return nil, fmt.Errorf("TrafficRouting only support Ingress or Gateway")
}

func (r *rolloutContext) createCanaryService(stableService *corev1.Service) error {
	canaryService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.rollout.Namespace,
			Name:      r.canaryService,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         r.rollout.APIVersion,
					Kind:               r.rollout.Kind,
					Name:               r.rollout.Name,
					UID:                r.rollout.UID,
					Controller:         utilpointer.BoolPtr(true),
					BlockOwnerDeletion: utilpointer.BoolPtr(true),
				},
			},
		},
		Spec: *stableService.Spec.DeepCopy(),
	}
	// set field nil
	canaryService.Spec.ClusterIP = ""
	canaryService.Spec.ClusterIPs = nil
	canaryService.Spec.ExternalIPs = nil
	canaryService.Spec.IPFamilyPolicy = nil
	canaryService.Spec.IPFamilies = nil
	canaryService.Spec.LoadBalancerIP = ""
	canaryService.Spec.Selector[r.podRevisionLabelKey()] = r.newStatus.CanaryStatus.PodTemplateHash
	err := r.Create(context.TODO(), canaryService)
	if err != nil && !errors.IsAlreadyExists(err) {
		klog.Errorf("create rollout(%s/%s) canary service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.canaryService, err.Error())
		return err
	}
	klog.Infof("create rollout(%s/%s) canary service(%s) success", r.rollout.Namespace, r.rollout.Name, util.DumpJSON(canaryService))
	return nil
}
