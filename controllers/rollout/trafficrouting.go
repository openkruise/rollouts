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

package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/openkruise/rollouts/pkg/util"
	"time"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/controllers/rollout/trafficrouting"
	"github.com/openkruise/rollouts/controllers/rollout/trafficrouting/nginx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *rolloutContext) doCanaryTrafficRouting() (bool, error) {
	//fetch stable service
	sName := r.rollout.Spec.Strategy.CanaryPlan.TrafficRouting.Service
	r.stableService = &corev1.Service{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: sName}, r.stableService)
	if err != nil {
		klog.Errorf("rollout(%s/%s) get stable service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, sName, err.Error())
		// not found, wait a moment, retry
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	r.newStatus.CanaryStatus.CanaryService = fmt.Sprintf("%s-canary", sName)
	// fetch canary stable
	canaryStatus := r.newStatus.CanaryStatus
	r.canaryService = &corev1.Service{}
	err = r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: canaryStatus.CanaryService}, r.canaryService)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) get canary service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, canaryStatus.CanaryService, err.Error())
		return false, err
	} else if errors.IsNotFound(err) {
		klog.Infof("rollout(%s/%s) canary service(%s) Not Found, and create it", r.rollout.Namespace, r.rollout.Name, canaryStatus.CanaryService)
		if err = r.createCanaryService(); err != nil {
			return false, err
		}
		by, _ := json.Marshal(r.canaryService)
		klog.Infof("create rollout(%s/%s) canary service(%s) success", r.rollout.Namespace, r.rollout.Name, string(by))
	}

	// update service selector
	// update service selector specific revision pods
	if r.canaryService.Spec.Selector[r.podRevisionLabelKey()] != r.canaryRevision {
		r.canaryService.Spec.Selector[r.podRevisionLabelKey()] = r.canaryRevision
		if err = r.retryUpdateService(r.canaryService); err != nil {
			return false, err
		}
		klog.Infof("add rollout(%s/%s) canary service(%s) selector(%s=%s) success",
			r.rollout.Namespace, r.rollout.Name, r.canaryService.Name, r.podRevisionLabelKey(), r.canaryRevision)
		// Adjust service and wait a while, just to be safe
		return false, nil
	}
	if r.stableService.Spec.Selector[r.podRevisionLabelKey()] != r.stableRevision {
		r.stableService.Spec.Selector[r.podRevisionLabelKey()] = r.stableRevision
		if err = r.retryUpdateService(r.stableService); err != nil {
			return false, err
		}
		klog.Infof("add rollout(%s/%s) stable service(%s) selector(%s=%s) success",
			r.rollout.Namespace, r.rollout.Name, r.stableService.Name, r.podRevisionLabelKey(), r.stableRevision)
		// Adjust service and wait a while, just to be safe
		return false, nil
	}

	trController, err := r.newTrafficRoutingController(r)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newTrafficRoutingController failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}

	var desiredWeight int32
	if len(r.rollout.Spec.Strategy.CanaryPlan.Steps) > 0 {
		desiredWeight = r.rollout.Spec.Strategy.CanaryPlan.Steps[r.newStatus.CanaryStatus.CurrentStepIndex].Weight
	}
	verify, err := trController.VerifyTrafficRouting(desiredWeight)
	if err != nil {
		return false, err
	} else if verify {
		klog.Infof("rollout(%s/%s) do step(%d) traffic routing success", r.rollout.Namespace, r.rollout.Name, r.newStatus.CanaryStatus.CurrentStepIndex)
		return true, nil
	}
	return false, trController.SetRoutes(desiredWeight)
}

func (r *rolloutContext) restoreStableService() (bool, error) {
	//fetch stable service
	sName := r.rollout.Spec.Strategy.CanaryPlan.TrafficRouting.Service
	r.stableService = &corev1.Service{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: sName}, r.stableService)
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		klog.Errorf("rollout(%s/%s) get stable service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, sName, err.Error())
		return false, err
	}
	cond := util.GetRolloutCondition(*r.newStatus, appsv1alpha1.RolloutConditionProgressing)
	if cond == nil {
		klog.Warningf("rollout(%s/%s) Progressing condition is nil, then create new", r.rollout.Namespace, r.rollout.Name)
		obj := util.NewRolloutCondition(appsv1alpha1.RolloutConditionProgressing, corev1.ConditionTrue, appsv1alpha1.ProgressingReasonFinalising, "")
		cond = &obj
	}

	//restore stable service configuration，remove hash revision selector
	if r.stableService.Spec.Selector != nil && r.stableService.Spec.Selector[r.podRevisionLabelKey()] != "" {
		delete(r.stableService.Spec.Selector, r.podRevisionLabelKey())
		if err := r.retryUpdateService(r.stableService); err != nil {
			return false, err
		}
		klog.Infof("remove rollout(%s/%s) stable service(%s) pod revision selector success, and retry later", r.rollout.Namespace, r.rollout.Name, r.stableService.Name)
		// update Progressing Condition LastUpdateTime = time.Now()
		cond.LastUpdateTime = metav1.Now()
		return false, nil
	}
	// After restore stable service configuration, give the ingress provider 5 seconds to take effect
	if verifyTime := cond.LastUpdateTime.Add(time.Second * 5); verifyTime.After(time.Now()) {
		klog.Infof("restore rollout(%s/%s) stable service(%s) done, and retry later", r.rollout.Namespace, r.rollout.Name, r.stableService.Name)
		return false, nil
	}
	klog.Infof("rollout(%s/%s) doFinalising restore stable service(%s) success", r.rollout.Namespace, r.rollout.Name, r.stableService.Name)
	return true, nil
}

func (r *rolloutContext) doFinalisingTrafficRouting() (bool, error) {
	// 1. restore ingress，traffic routing stable service
	trController, err := r.newTrafficRoutingController(r)
	if err != nil {
		klog.Errorf("rollout(%s/%s) newTrafficRoutingController failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}
	done, err := trController.DoFinalising()
	if err != nil {
		klog.Errorf("rollout(%s/%s) DoFinalising TrafficRouting failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	} else if !done {
		return false, nil
	}
	klog.Infof("rollout(%s/%s) DoFinalising TrafficRouting success", r.rollout.Namespace, r.rollout.Name)

	if r.newStatus.CanaryStatus.CanaryService == "" {
		return true, nil
	}
	// 2. remove canary service
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

func (r *rolloutContext) newTrafficRoutingController(roCtx *rolloutContext) (trafficrouting.TrafficRoutingController, error) {
	canary := roCtx.rollout.Spec.Strategy.CanaryPlan
	switch canary.TrafficRouting.Type {
	case appsv1alpha1.TrafficRoutingNginx:
		gvk := schema.GroupVersionKind{Group: appsv1alpha1.GroupVersion.Group, Version: appsv1alpha1.GroupVersion.Version, Kind: "Rollout"}
		return nginx.NewNginxTrafficRouting(r.Client, r.newStatus, nginx.NginxConfig{
			RolloutName:   r.rollout.Name,
			RolloutNs:     r.rollout.Namespace,
			CanaryService: r.canaryService,
			StableService: r.stableService,
			TrafficConf:   r.rollout.Spec.Strategy.CanaryPlan.TrafficRouting.Nginx,
			OwnerRef:      *metav1.NewControllerRef(r.rollout, gvk),
		})
	}

	return nil, fmt.Errorf("TrafficRouting(%s) not support", canary.TrafficRouting.Type)
}

func (r *rolloutContext) retryUpdateService(service *corev1.Service) error {
	obj := service.DeepCopy()
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, obj); err != nil {
			klog.Errorf("getting updated service(%s.%s) failed: %s", obj.Namespace, obj.Name, err.Error())
			return err
		}
		obj.Spec = *service.Spec.DeepCopy()
		return r.Update(context.TODO(), obj)
	})
	if err != nil {
		klog.Errorf("update rollout(%s/%s) service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, service.Name, err.Error())
		return err
	}
	return nil
}

func (r *rolloutContext) createCanaryService() error {
	r.canaryService = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.rollout.Namespace,
			Name:      r.newStatus.CanaryStatus.CanaryService,
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
		Spec: *r.stableService.Spec.DeepCopy(),
	}
	// set field nil
	r.canaryService.Spec.ClusterIP = ""
	r.canaryService.Spec.ClusterIPs = nil
	r.canaryService.Spec.ExternalIPs = nil
	r.canaryService.Spec.IPFamilyPolicy = nil
	r.canaryService.Spec.IPFamilies = nil
	r.canaryService.Spec.LoadBalancerIP = ""
	r.canaryService.Spec.Selector[r.podRevisionLabelKey()] = r.canaryRevision
	err := r.Create(context.TODO(), r.canaryService)
	if err != nil && !errors.IsAlreadyExists(err) {
		klog.Errorf("create rollout(%s/%s) canary service(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.canaryService.Name, err.Error())
		return err
	}
	return nil
}
