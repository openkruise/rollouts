/*
Copyright 2022.

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

package rollouthistory

import (
	"context"
	"errors"
	"fmt"
	"sort"

	kruiseapi "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruiseapi_beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
)

// get rollouthistory according to rollout. The rollouthistory should be not completed now
func (r *RolloutHistoryReconciler) getRHByRollout(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.RolloutHistory, error) {
	// get all rollouthistories
	rhList := &rolloutv1alpha1.RolloutHistoryList{}
	err := r.List(context.TODO(), rhList, &client.ListOptions{}, client.InNamespace(rollout.Namespace))
	if err != nil {
		return nil, err
	}

	// if the num of rollouthistory is greater than MaxRolloutHistoryNum, it will delete excess rollouthistories.
	// sort rollouthistories by creationTimeStamp
	result := rhList.Items
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreationTimestamp.Before(&result[j].CreationTimestamp)
	})

	lenth := len(result)
	if lenth > rolloutv1alpha1.MaxRolloutHistoryNum {
		for _, rollouthistory := range result[:lenth-rolloutv1alpha1.MaxRolloutHistoryNum] {
			thisRolloutHistory := rollouthistory
			err = r.Delete(context.TODO(), &thisRolloutHistory, &client.DeleteOptions{})
			if err != nil {
				return nil, err
			}
		}
		result = append(result[:0], result[lenth-rolloutv1alpha1.MaxRolloutHistoryNum:]...)
	}

	// get target rollouthistory according to rollout.Spec.RolloutID and rollout.Name
	for i := range result {
		RH := result[i]
		if rollout.Spec.RolloutID == RH.Spec.RolloutWrapper.Rollout.Spec.RolloutID &&
			rollout.Name == RH.Spec.RolloutWrapper.Name &&
			RH.Status.Phase != rolloutv1alpha1.PhaseCompleted {
			r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.getRHByRollout successed", " ")
			return &RH, nil
		}
	}

	return nil, errors.New("getRHByRollout can't find RH")
}

// create a rollouthistory which happens when user do a rollout
func (r *RolloutHistoryReconciler) createRHByRollout(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.RolloutHistory, error) {
	logger := log.FromContext(context.TODO())
	// init .spec.rolloutWrapper
	rolloutWrapper, err := r.initRolloutWrapper(rollout)
	if err != nil {
		logger.Info(err.Error() + " after r.initRolloutInfo")
		return nil, err
	}

	// init .spec.workload
	workload, err := r.initWorkloadInfo(rollout)
	if err != nil {
		logger.Info(err.Error() + " after r.initWorkloadInfo")
		return nil, err
	}

	// init .spec.serviceWrapper
	serviceWrapper, err := r.initServiceWrapper(rollout, workload)
	if err != nil {
		logger.Info(err.Error() + " after r.initServiceInfo")
		return nil, err
	}

	// init .spec.traffcRoutingWrapper
	trafficRoutingWrapper, err := r.initTrafficRoutingWrapper(rollout)
	if err != nil {
		logger.Info(err.Error() + " after r.initIngressInfo")
		return nil, err
	}

	// init rollouthistory
	RH := &rolloutv1alpha1.RolloutHistory{
		TypeMeta: v1.TypeMeta{
			Kind:       "RolloutHistory",
			APIVersion: "rollouts.kruise.io/v1alpha1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      rollout.Name + "-rh-" + RandAllString(6),
			Namespace: rollout.Namespace,
		},
		Spec: rolloutv1alpha1.RolloutHistorySpec{
			RolloutWrapper:        *rolloutWrapper,
			Workload:              *workload,
			ServiceWrapper:        *serviceWrapper,
			TrafficRoutingWrapper: *trafficRoutingWrapper,
		},
		Status: rolloutv1alpha1.RolloutHistoryStatus{},
	}

	// create this rollouthistory
	err = r.Create(context.TODO(), RH, &client.CreateOptions{})
	if err != nil {
		logger.Info(err.Error() + " after r.Create")
		return nil, err
	}
	r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.createRHByRollout.1 successed", RH.Status.RolloutState.Message)

	// init .status for rollouthistory
	if err = r.initRHStatus(rollout, RH); err != nil {
		logger.Info(err.Error() + " after r.initRHStatus")
		return nil, err
	}
	r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.createRHByRollout.2 successed", RH.Status.RolloutState.Message)

	return RH, nil
}

// handle function, when rollouthistory .status.canaryStepState is CanaryStateInit
func (r *RolloutHistoryReconciler) handleInit(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	// when rollouthistory is meet the need in CanaryStateInit, move it to next canaryStepState
	if *RH.Status.CanaryStepIndex == 0 &&
		RT.Status.CanaryStatus.CurrentStepState == rolloutv1alpha1.CanaryStepStateUpgrade &&
		*RH.Status.CanaryStepIndex+1 == RT.Status.CanaryStatus.CurrentStepIndex {
		RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStatePending
		*RH.Status.CanaryStepIndex += 1
		RH.Status.Phase = rolloutv1alpha1.PhaseProgressing

		// update this rollouthistory
		err := r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.handleInit successed", RH.Status.RolloutState.Message)
	}
	return nil
}

// handle function, when rollouthistory .status.canaryStepState is CanaryStatePending
func (r *RolloutHistoryReconciler) handlePending(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	// if rollouthistory .status.canaryStepIndex is equal to rollout .status.canaryStatus.currentStepIndex, and
	// rollout .status.canaryStatus.currentStepState is CanaryStepStatePaused, rollouthistory need to record pod released information
	// in this canary step
	if RT.Status.CanaryStatus.CurrentStepIndex == int32(*RH.Status.CanaryStepIndex) &&
		RT.Status.CanaryStatus.CurrentStepState == rolloutv1alpha1.CanaryStepStatePaused {
		// rollout is in StepPaused, update info
		var err error
		// update .status.rolloutState
		err = r.updateStatusRolloutState(RT, RH)
		if err != nil {
			return err
		}
		// update .status.canaryStepPods information in this step
		err = r.updateCanaryStepPods(RT, RH)
		if err != nil {
			return err
		}

		// when update done, rollouthistory .status.canaryStepState should be CanaryStateUpdated
		RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStateUpdated

		err = r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.handlePending successed", RH.Status.RolloutState.Message)
	}

	// if rollout .status.canaryStatus.currentStepIndex is 0 now, it means that user do a continuous rollout v1 -> v2(not completed) -> v3
	if RT.Status.CanaryStatus.CurrentStepIndex == 0 {
		err := r.doTerminated(RT, RH)
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.doTerminated successed", RH.Name)
	}

	// if rollout .status.phase is RolloutPhaseHealthy now, it means that user do a continuous rollout v1 -> v2(not completed) -> v1(back)
	if RT.Status.Phase == rolloutv1alpha1.RolloutPhaseHealthy &&
		RT.Status.CanaryStatus.CurrentStepState != rolloutv1alpha1.CanaryStepStateCompleted {
		err := r.doCancelled(RT, RH)
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.doCancelled successed", RH.Name)
	}

	return nil
}

// handle function, when rollouthistory .status.canaryStepState is CanaryStateUpdated
func (r *RolloutHistoryReconciler) handleUpdated(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	// if rollout .status.canaryStatus.currentStepState is canaryStepStateUpgrade, it means that the last canary step has been approved
	if RT.Status.CanaryStatus.CurrentStepState == rolloutv1alpha1.CanaryStepStateUpgrade &&
		RT.Status.CanaryStatus.CurrentStepIndex == int32(*RH.Status.CanaryStepIndex)+1 {
		RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStatePending
		*RH.Status.CanaryStepIndex++

		err := r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.handleUpdate successed", RH.Status.RolloutState.Message)
	}

	// if rollout .status.canaryStatus.currentStepState is CanaryStepStateCompleted now , it means that this rollout has completed
	if RT.Status.CanaryStatus.CurrentStepState == rolloutv1alpha1.CanaryStepStateCompleted &&
		RT.Status.CanaryStatus.CurrentStepIndex == int32(*RH.Status.CanaryStepIndex) {
		err := r.doCompleted(RT, RH)
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.doCompleted successed", RH.Status.RolloutState.Message)
	}

	// if rollout .status.phase is RolloutPhaseHealthy now, it means that user do a continuous rollout v1 -> v2(not completed) -> v1(back)
	if RT.Status.Phase == rolloutv1alpha1.RolloutPhaseHealthy &&
		RT.Status.CanaryStatus.CurrentStepState != rolloutv1alpha1.CanaryStepStateCompleted {
		err := r.doCancelled(RT, RH)
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.doCancelled successed", RH.Name)
	}

	// if rollout .status.canaryStatus.currentStepIndex is 0 now, it means that user do a continuous rollout v1 -> v2(not completed) -> v3
	if RT.Status.CanaryStatus.CurrentStepIndex == 0 {
		err := r.doTerminated(RT, RH)
		if err != nil {
			return err
		}

		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.doTerminated successed", RH.Name)
	}

	return nil
}

// handle function, when rollouthistory .status.canaryStepState is CanaryStateTerminated, when user do a continuous rollout v1 -> v2(not completed) -> v3
func (r *RolloutHistoryReconciler) handleTerminated(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	if RH.Status.Phase == rolloutv1alpha1.PhaseCompleted {
		return nil
	}

	// update this terminated-rollout related rollouthistory(v2 not completed), make .status.phase completed
	RH.Status.Phase = rolloutv1alpha1.PhaseCompleted
	err := r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
	if err != nil {
		return err
	}

	// when user do a continuous rollout v1 -> v2(not completed) -> v3, it will create a new rollouthistory for this rollout
	newWorkload, err := r.initWorkloadInfo(RT)
	if err != nil {
		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.handleTerminated and initWorKload failed", RH.Status.RolloutState.Message)
		return err
	}

	// get this new rollout which rolloutID is different and rollout name is same
	newRollout := &rolloutv1alpha1.Rollout{}
	err = r.Get(context.TODO(), types.NamespacedName{Namespace: RT.Namespace, Name: RT.Name}, newRollout)
	if err != nil {
		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.handleTerminated and get newRollout failed", RH.Status.RolloutState.Message)
		return err
	}

	// init rolloutWrapper for the new rollouthistory
	newRolloutWrapper, err := r.initRolloutWrapper(newRollout)
	if err != nil {
		r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.handleTerminated and initRolloutInfo failed", RH.Status.RolloutState.Message)
		return err
	}

	// init this new rollouthistory for rollout(v3)
	newRolloutHistory := &rolloutv1alpha1.RolloutHistory{
		TypeMeta: v1.TypeMeta{
			Kind:       RH.Kind,
			APIVersion: RH.APIVersion,
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      RT.Name + "-rh-" + RandAllString(6),
			Namespace: RT.Namespace,
		},
		Spec: rolloutv1alpha1.RolloutHistorySpec{
			RolloutWrapper:        *newRolloutWrapper,
			Workload:              *newWorkload,
			TrafficRoutingWrapper: RH.Spec.TrafficRoutingWrapper,
			ServiceWrapper:        RH.Spec.ServiceWrapper,
		},
		Status: rolloutv1alpha1.RolloutHistoryStatus{},
	}

	// create this rollouthistory, which record information of rollout(v3)
	err = r.Create(context.TODO(), newRolloutHistory, &client.CreateOptions{})
	if err != nil {
		return err
	}
	r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.createRHByRollout.1 successed", RH.Status.RolloutState.Message)

	// init .status for rollouthistory
	if err = r.initRHStatus(RT, newRolloutHistory); err != nil {
		return err
	}

	r.Recorder.Event(newRolloutHistory.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.createRHByRollout.2 successed", RH.Status.RolloutState.Message)

	return nil
}

// handle function, when rollouthistory .status.canaryStepState is CanaryStateCompleted
func (r *RolloutHistoryReconciler) handleCompleted(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	if RH.Status.Phase != rolloutv1alpha1.PhaseCompleted && RT.Status.Phase == rolloutv1alpha1.RolloutPhaseHealthy {
		var err error
		err = r.updateStatusRolloutState(RT, RH)
		if err != nil {
			return err
		}

		// update the final step status
		if *RT.Spec.Strategy.Canary.Steps[*RH.Status.CanaryStepIndex-1].Weight != 100 {
			*RH.Status.CanaryStepIndex += 1
			err = r.updateCanaryStepPods(RT, RH)
			if err != nil {
				return err
			}
		}

		RH.Status.Phase = rolloutv1alpha1.PhaseCompleted
		err = r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// handle function when rollout process is terminated, which means that user do a new rollout
func (r *RolloutHistoryReconciler) doTerminated(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	// just make .status.canaryStepState CanaryStateTerminated
	RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStateTerminated
	err := r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

// handle function when rollout process is cancelled, which means that user do a rollback
func (r *RolloutHistoryReconciler) doCancelled(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	// if rollout is cancelled, make rollouthistory .status.canaryStepState CanaryStateCompleted and .status.phase PhaseCompleted
	RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStateCompleted
	RH.Status.Phase = rolloutv1alpha1.PhaseCompleted
	err := r.updateStatusRolloutState(RT, RH)
	if err != nil {
		return err
	}

	// if rollout is cancelled, make rollouthistory .status.canaryStepIndex value "-1"
	*RH.Status.CanaryStepIndex = -1
	err = r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

// handle function doComplated, usually it be called when something unexpected happens and rollout .status.canaryStatus.currentStepState become CanaryStateCompleted
func (r *RolloutHistoryReconciler) doCompleted(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStateCompleted

	err := r.updateStatusRolloutState(RT, RH)
	if err != nil {
		return err
	}

	err = r.Status().Update(context.TODO(), RH, &client.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

// init .status for rollouthistory
func (r *RolloutHistoryReconciler) initRHStatus(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	RH.Status.Phase = rolloutv1alpha1.PhaseInit
	RH.Status.CanaryStepState = rolloutv1alpha1.CanaryStateInit
	RH.Status.CanaryStepIndex = new(int32)
	*RH.Status.CanaryStepIndex = 0
	RH.Status.RolloutState = rolloutv1alpha1.RolloutState{RolloutPhase: RT.Status.Phase, Message: RT.Status.Message}
	RH.Status.CanaryStepPods = make([]rolloutv1alpha1.CanaryStepPods, 0)

	if err := r.Status().Update(context.TODO(), RH, &client.UpdateOptions{}); err != nil {
		return err
	}
	r.Recorder.Event(RH.DeepCopy().DeepCopyObject(), corev1.EventTypeNormal, "r.initRHStatus succeed", " ")
	return nil
}

// init .spec.rolloutWrapper
func (r *RolloutHistoryReconciler) initRolloutWrapper(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.RolloutWrapper, error) {
	RolloutWrapper := &rolloutv1alpha1.RolloutWrapper{
		Name:    rollout.Name,
		Rollout: *rollout,
	}
	return RolloutWrapper, nil
}

// init .spec.traffcRoutingWrapper
func (r *RolloutHistoryReconciler) initTrafficRoutingWrapper(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.TrafficRoutingWrapper, error) {
	trafficRoutingWrapper := &rolloutv1alpha1.TrafficRoutingWrapper{}
	var err error
	// if gateway is configured, init it
	if rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway != nil &&
		rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway.HTTPRouteName != nil {
		trafficRoutingWrapper.HTTPRouteWrapper, err = r.initGateWayInfo(rollout)
		if err != nil {
			return nil, err
		}
	}

	// if ingress is configured, init it
	if rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.Name != "" {
		trafficRoutingWrapper.IngressWrapper, err = r.initIngressInfo(rollout)
		if err != nil {
			return nil, err
		}
	}

	return trafficRoutingWrapper, nil
}

// init Gateway, especially HTTPRoute
func (r *RolloutHistoryReconciler) initGateWayInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.HTTPRouteWrapper, error) {
	GatewayName := *rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway.HTTPRouteName
	HTTPRoute := &v1alpha2.HTTPRoute{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: GatewayName}, HTTPRoute)
	if err != nil {
		return nil, errors.New("initGateway error: HTTPRoute " + GatewayName + " not find")
	}

	return &rolloutv1alpha1.HTTPRouteWrapper{Name: GatewayName, HTTPRoute: HTTPRoute}, err
}

// init Ingress
func (r *RolloutHistoryReconciler) initIngressInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.IngressWrapper, error) {
	IngressName := rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.Name
	Ingress := &networkingv1.Ingress{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: IngressName}, Ingress)
	if err != nil {
		return nil, errors.New("initIngressInfo error: Ingress " + IngressName + " not find")
	}

	return &rolloutv1alpha1.IngressWrapper{Name: IngressName, Ingress: Ingress}, err
}

// init .spec.workload
func (r *RolloutHistoryReconciler) initWorkloadInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.Workload, error) {
	// init specific workload, according to rollout.spec.objectRef.workloadRef.kind, now rollouthistory support Deployment, CloneSet, native StatefulSet and Advanced StatefulSet
	switch rollout.Spec.ObjectRef.WorkloadRef.Kind {
	case "Deployment":
		return r.initDeploymentInfo(rollout)
	case "CloneSet":
		return r.initCloneSetInfo(rollout)
	case "StatefulSet":
		// According to rollout.spec.objectRef.workload.APIVersion, select Advanced StatefulSet or Native StatefulSet
		if rollout.Spec.ObjectRef.WorkloadRef.APIVersion == "apps.kruise.io/v1beta1" {
			return r.initAdvancedStatefulSetInfo(rollout)
		}
		if rollout.Spec.ObjectRef.WorkloadRef.APIVersion == "apps/v1" {
			return r.initNativeStatefulSetInfo(rollout)
		}
		return nil, errors.New("StatefulSet is invalid")
	default:
		return nil, errors.New("workload is invalid")
	}
}

// init workload deployment
func (r *RolloutHistoryReconciler) initDeploymentInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.Workload, error) {
	deployment := &appsv1.Deployment{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Spec.ObjectRef.WorkloadRef.Name}, deployment)
	if err != nil {
		return nil, errors.New("deployment not find")
	}

	DeploymentInfo := rolloutv1alpha1.Workload{
		TypeMeta: deployment.TypeMeta,
		Name:     deployment.Name,
		Selector: deployment.Spec.Selector,
		Replicas: deployment.Spec.Replicas,
		Type:     string(deployment.Spec.Strategy.Type),
		Template: deployment.Spec.Template,
	}

	return &DeploymentInfo, nil
}

// init workload cloneset
func (r *RolloutHistoryReconciler) initCloneSetInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.Workload, error) {
	cloneSet := &kruiseapi.CloneSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Spec.ObjectRef.WorkloadRef.Name}, cloneSet)
	if err != nil {
		return &rolloutv1alpha1.Workload{}, err
	}

	CloneSetInfo := rolloutv1alpha1.Workload{
		TypeMeta: cloneSet.TypeMeta,
		Name:     cloneSet.Name,
		Selector: cloneSet.Spec.Selector,
		Replicas: cloneSet.Spec.Replicas,
		Type:     string(cloneSet.Spec.UpdateStrategy.Type),
		Template: cloneSet.Spec.Template,
	}

	return &CloneSetInfo, nil
}

// init Native StatefulSet
func (r *RolloutHistoryReconciler) initNativeStatefulSetInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.Workload, error) {
	cloneSet := &appsv1.StatefulSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Spec.ObjectRef.WorkloadRef.Name}, cloneSet)
	if err != nil {
		return &rolloutv1alpha1.Workload{}, err
	}

	CloneSetInfo := rolloutv1alpha1.Workload{
		TypeMeta: cloneSet.TypeMeta,
		Name:     cloneSet.Name,
		Selector: cloneSet.Spec.Selector,
		Replicas: cloneSet.Spec.Replicas,
		Type:     string(cloneSet.Spec.UpdateStrategy.Type),
		Template: cloneSet.Spec.Template,
	}

	return &CloneSetInfo, nil
}

// init Advanced StatefulSet
func (r *RolloutHistoryReconciler) initAdvancedStatefulSetInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.Workload, error) {
	cloneSet := &kruiseapi_beta1.StatefulSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Spec.ObjectRef.WorkloadRef.Name}, cloneSet)
	if err != nil {
		return &rolloutv1alpha1.Workload{}, err
	}

	CloneSetInfo := rolloutv1alpha1.Workload{
		TypeMeta: cloneSet.TypeMeta,
		Name:     cloneSet.Name,
		Selector: cloneSet.Spec.Selector,
		Replicas: cloneSet.Spec.Replicas,
		Type:     string(cloneSet.Spec.UpdateStrategy.Type),
		Template: cloneSet.Spec.Template,
	}

	return &CloneSetInfo, nil
}

// init .spec.serviceWrapper
func (r *RolloutHistoryReconciler) initServiceWrapper(rollout *rolloutv1alpha1.Rollout, workloadInfo *rolloutv1alpha1.Workload) (*rolloutv1alpha1.ServiceWrapper, error) {
	Service := &corev1.Service{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service}, Service)
	if err != nil {
		return nil, errors.New("service not find")
	}

	return &rolloutv1alpha1.ServiceWrapper{Name: Service.Name, Service: Service}, nil
}

// update rollouthistory .status.rolloutState
func (r *RolloutHistoryReconciler) updateStatusRolloutState(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	RH.Status.RolloutState = rolloutv1alpha1.RolloutState{RolloutPhase: RT.Status.Phase, Message: RT.Status.Message}
	return nil
}

// update canaryStepPods,
func (r *RolloutHistoryReconciler) updateCanaryStepPods(RT *rolloutv1alpha1.Rollout, RH *rolloutv1alpha1.RolloutHistory) error {
	podList := &corev1.PodList{}
	var err error
	var extraSelector labels.Selector

	// get labelSelector including rolloutBathID, rolloutID and workload selector
	selector, _ := v1.LabelSelectorAsSelector(RH.Spec.Workload.Selector)
	lableSelectorString := fmt.Sprintf("%v=%v,%v=%v,%v", util.RolloutBatchIDLabel, *RH.Status.CanaryStepIndex, util.RolloutIDLabel, RH.Spec.RolloutWrapper.Rollout.Spec.RolloutID, selector.String())
	extraSelector, err = labels.Parse(lableSelectorString)
	if err != nil {
		return err
	}

	// get pods according to lableSelector
	err = r.List(context.TODO(), podList, &client.ListOptions{LabelSelector: extraSelector}, client.InNamespace(RT.Namespace))
	if err != nil {
		return err
	}

	// if num of pods is empty, append a empty CanaryStepPods{}
	if len(podList.Items) == 0 {
		RH.Status.CanaryStepPods = append(RH.Status.CanaryStepPods, rolloutv1alpha1.CanaryStepPods{})
		return nil
	}

	// get currentStepPods
	currentStepPods := rolloutv1alpha1.CanaryStepPods{}
	var pods []rolloutv1alpha1.Pod
	// get pods name, ip, node and add them to CanaryStepPods.Pods
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.DeletionTimestamp.IsZero() {
			cur := rolloutv1alpha1.Pod{Name: pod.Name, IP: pod.Status.PodIP, Node: pod.Spec.NodeName}
			pods = append(pods, cur)
		}
	}
	currentStepPods.Pods = pods
	currentStepPods.StepIndex = *RH.Status.CanaryStepIndex
	currentStepPods.PodsInStep = int32(len(pods))

	// sum pods released by now
	currentStepPods.PodsInTotal = int32(len(pods))
	for _, stepStatus := range RH.Status.CanaryStepPods {
		currentStepPods.PodsInTotal += stepStatus.PodsInStep
	}

	// add currentStepPods to .status.canaryStepPods
	RH.Status.CanaryStepPods = append(RH.Status.CanaryStepPods, currentStepPods)

	return nil
}
