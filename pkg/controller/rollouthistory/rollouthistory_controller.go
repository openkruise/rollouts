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

package rollouthistory

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"reflect"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/feature"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var (
	concurrentReconciles = 3
)

func init() {
	flag.IntVar(&concurrentReconciles, "rollouthistory-workers", 3, "Max concurrent workers for rolloutHistory controller.")
}

// Add creates a new Rollout Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	if utilfeature.DefaultFeatureGate.Enabled(feature.RolloutHistoryGate) {
		return add(mgr, newReconciler(mgr))
	}
	return nil
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &RolloutHistoryReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Finder: newControllerFinder2(mgr.GetClient()),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("rollouthistory-controller", mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}
	// Watch for changes to rollout
	if err = c.Watch(&source.Kind{Type: &rolloutv1alpha1.Rollout{}}, &enqueueRequestForRollout{}); err != nil {
		return err
	}
	// watch for changes to rolloutHistory
	if err = c.Watch(&source.Kind{Type: &rolloutv1alpha1.RolloutHistory{}}, &enqueueRequestForRolloutHistory{}); err != nil {
		return err
	}
	return nil
}

// RolloutHistoryReconciler reconciles a RolloutHistory object
type RolloutHistoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Finder *controllerFinder2
}

//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouthistories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouthistories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouthistories/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RolloutHistory object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *RolloutHistoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// get rollout
	rollout := &rolloutv1alpha1.Rollout{}
	err := r.Get(ctx, req.NamespacedName, rollout)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	klog.Infof("Begin to reconcile Rollout %v", klog.KObj(rollout))

	// ignore rollout without ObservedRolloutID
	if rollout.Status.CanaryStatus == nil ||
		rollout.Status.CanaryStatus.ObservedRolloutID == "" {
		return ctrl.Result{}, nil
	}
	// get RolloutHistory which is not completed and related to the rollout (only one or zero)
	var rolloutHistory *rolloutv1alpha1.RolloutHistory
	if rolloutHistory, err = r.getRolloutHistoryForRollout(rollout); err != nil {
		klog.Errorf("get rollout(%s/%s) rolloutHistory(%s=%s) failed: %s", rollout.Namespace, rollout.Name, rolloutIDLabel, rollout.Status.CanaryStatus.ObservedRolloutID, err.Error())
		return ctrl.Result{}, err
	}
	// create a rolloutHistory when user does a new rollout
	if rolloutHistory == nil {
		// just create rolloutHistory for rollouts which are progressing, otherwise it's possible to create more than one rollouthistory when user does one rollout
		if rollout.Status.Phase != rolloutv1alpha1.RolloutPhaseProgressing {
			return ctrl.Result{}, nil
		}
		if err = r.createRolloutHistoryForProgressingRollout(rollout); err != nil {
			klog.Errorf("create rollout(%s/%s) rolloutHistory(%s=%s) failed: %s", rollout.Namespace, rollout.Name, rolloutIDLabel, rollout.Status.CanaryStatus.ObservedRolloutID, err.Error())
			return ctrl.Result{}, err
		}
		klog.Infof("create rollout(%s/%s) rolloutHistory success", rollout.Namespace, rollout.Name)
		return ctrl.Result{}, nil
	}

	klog.Infof("get rollout(%s/%s) rolloutHistory(%s) success", rollout.Namespace, rollout.Name, rolloutHistory.Name)

	// update RolloutHistory which is waiting for rollout completed
	if rolloutHistory.Status.Phase != rolloutv1alpha1.PhaseCompleted {
		// update RolloutHistory when rollout .status.phase is equl to RolloutPhaseHealthy
		if err = r.updateRolloutHistoryWhenRolloutIsCompeleted(rollout, rolloutHistory); err != nil {
			klog.Errorf("update rollout(%s/%s) rolloutHistory(%s=%s) failed: %s", rollout.Namespace, rollout.Name, rolloutIDLabel, rollout.Status.CanaryStatus.ObservedRolloutID, err.Error())
			return ctrl.Result{}, err
		}
		// update rollouthistory success
		klog.Infof("update rollout(%s/%s) rolloutHistory(%s) success", rollout.Namespace, rollout.Name, rolloutHistory.Name)
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

// getRolloutHistoryForRollout get rolloutHistory according to rolloutID and rolloutName for this new rollout.
func (r *RolloutHistoryReconciler) getRolloutHistoryForRollout(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.RolloutHistory, error) {
	// get labelSelector including rolloutBathID, rolloutID
	lableSelectorString := fmt.Sprintf("%v=%v,%v=%v", rolloutIDLabel, rollout.Status.CanaryStatus.ObservedRolloutID, rolloutNameLabel, rollout.Name)
	labelSelector, err := labels.Parse(lableSelectorString)
	if err != nil {
		return nil, err
	}
	// get rollouthistories according to labels, in fact there is only one or zero rolloutHistory with the labelSelector
	rollouthistories := &rolloutv1alpha1.RolloutHistoryList{}
	err = r.List(context.TODO(), rollouthistories, &client.ListOptions{LabelSelector: labelSelector}, client.InNamespace(rollout.Namespace))
	if err != nil {
		return nil, err
	}
	// if there is no rollouthistory found, return
	if len(rollouthistories.Items) == 0 {
		return nil, nil
	}
	// find the rollouthistory
	return &rollouthistories.Items[0], nil
}

// createRolloutHistoryForProgressingRollout create a new rolloutHistory, which indicates that user does a new rollout
func (r *RolloutHistoryReconciler) createRolloutHistoryForProgressingRollout(rollout *rolloutv1alpha1.Rollout) error {
	// init the rolloutHistory
	rolloutHistory := &rolloutv1alpha1.RolloutHistory{
		ObjectMeta: v1.ObjectMeta{
			Name:      rollout.Name + "-" + randAllString(6),
			Namespace: rollout.Namespace,
			Labels: map[string]string{
				rolloutIDLabel:   rollout.Status.CanaryStatus.ObservedRolloutID,
				rolloutNameLabel: rollout.Name,
			},
		},
	}
	// create the rolloutHistory
	return r.Create(context.TODO(), rolloutHistory, &client.CreateOptions{})
}

// getRolloutHistorySpec get RolloutHistorySpec for rolloutHistory according to rollout
func (r *RolloutHistoryReconciler) getRolloutHistorySpec(rollout *rolloutv1alpha1.Rollout) (rolloutv1alpha1.RolloutHistorySpec, error) {
	rolloutHistorySpec := rolloutv1alpha1.RolloutHistorySpec{}
	var err error
	// get rolloutInfo
	if rolloutHistorySpec.Rollout, err = r.getRolloutInfo(rollout); err != nil {
		return rolloutHistorySpec, err
	}
	// get workloadInfo
	var workload *rolloutv1alpha1.WorkloadInfo
	if workload, err = r.Finder.getWorkloadInfoForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef); err != nil {
		return rolloutHistorySpec, err
	}
	rolloutHistorySpec.Workload = *workload
	// get serviceInfo
	if rolloutHistorySpec.Service, err = r.getServiceInfo(rollout); err != nil {
		return rolloutHistorySpec, err
	}
	// get trafficRoutingInfo
	if rolloutHistorySpec.TrafficRouting, err = r.getTrafficRoutingInfo(rollout); err != nil {
		return rolloutHistorySpec, err
	}
	return rolloutHistorySpec, nil
}

// getRolloutInfo get RolloutInfo to for rolloutHistorySpec
func (r *RolloutHistoryReconciler) getRolloutInfo(rollout *rolloutv1alpha1.Rollout) (rolloutv1alpha1.RolloutInfo, error) {
	rolloutInfo := rolloutv1alpha1.RolloutInfo{}
	var err error
	rolloutInfo.Name = rollout.Name
	rolloutInfo.RolloutID = rollout.Status.CanaryStatus.ObservedRolloutID
	if rolloutInfo.Data.Raw, err = json.Marshal(rollout.Spec); err != nil {
		return rolloutInfo, err
	}
	return rolloutInfo, nil
}

// getServiceInfo get ServiceInfo for rolloutHistorySpec
func (r *RolloutHistoryReconciler) getServiceInfo(rollout *rolloutv1alpha1.Rollout) (rolloutv1alpha1.ServiceInfo, error) {
	// get service
	service := &corev1.Service{}
	serviceInfo := rolloutv1alpha1.ServiceInfo{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service}, service)
	if err != nil {
		return serviceInfo, errors.New("service not find")
	}
	// marshal service into serviceInfo
	serviceInfo.Name = service.Name
	if serviceInfo.Data.Raw, err = json.Marshal(service.Spec); err != nil {
		return serviceInfo, err
	}
	return serviceInfo, nil
}

// getTrafficRoutingInfo get TrafficRoutingInfo for rolloutHistorySpec
func (r *RolloutHistoryReconciler) getTrafficRoutingInfo(rollout *rolloutv1alpha1.Rollout) (rolloutv1alpha1.TrafficRoutingInfo, error) {
	trafficRoutingInfo := rolloutv1alpha1.TrafficRoutingInfo{}
	var err error
	// if gateway is configured, get it
	if rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway != nil &&
		rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway.HTTPRouteName != nil {
		trafficRoutingInfo.HTTPRoute, err = r.getGateWayInfo(rollout)
		if err != nil {
			return trafficRoutingInfo, err
		}
	}
	// if ingress is configured, get it
	if rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress != nil &&
		rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.Name != "" {
		trafficRoutingInfo.Ingress, err = r.getIngressInfo(rollout)
		if err != nil {
			return trafficRoutingInfo, err
		}
	}
	return trafficRoutingInfo, nil
}

// getGateWayInfo get HTTPRouteInfo for TrafficRoutingInfo
func (r *RolloutHistoryReconciler) getGateWayInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.HTTPRouteInfo, error) {
	// get HTTPRoute
	gatewayName := *rollout.Spec.Strategy.Canary.TrafficRoutings[0].Gateway.HTTPRouteName
	HTTPRoute := &v1alpha2.HTTPRoute{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: gatewayName}, HTTPRoute)
	if err != nil {
		return nil, errors.New("initGateway error: HTTPRoute " + gatewayName + " not find")
	}
	// marshal HTTPRoute into HTTPRouteInfo for rolloutHistory
	gatewayInfo := &rolloutv1alpha1.HTTPRouteInfo{}
	gatewayInfo.Name = HTTPRoute.Name
	if gatewayInfo.Data.Raw, err = json.Marshal(HTTPRoute.Spec); err != nil {
		return nil, err
	}
	return gatewayInfo, nil
}

// getIngressInfo get IngressInfo for TrafficRoutingInfo
func (r *RolloutHistoryReconciler) getIngressInfo(rollout *rolloutv1alpha1.Rollout) (*rolloutv1alpha1.IngressInfo, error) {
	// get Ingress
	ingressName := rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.Name
	ingress := &networkingv1.Ingress{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: rollout.Namespace, Name: ingressName}, ingress)
	if err != nil {
		return nil, errors.New("initIngressInfo error: Ingress " + ingressName + " not find")
	}
	// marshal ingress into ingressInfo
	ingressInfo := &rolloutv1alpha1.IngressInfo{}
	ingressInfo.Name = ingressName
	if ingressInfo.Data.Raw, err = json.Marshal(ingress.Spec); err != nil {
		return nil, err
	}
	return ingressInfo, nil
}

// updateRolloutHistoryWhenRolloutIsCompeleted record all pods released when rollout phase is healthy
func (r *RolloutHistoryReconciler) updateRolloutHistoryWhenRolloutIsCompeleted(rollout *rolloutv1alpha1.Rollout, rolloutHistory *rolloutv1alpha1.RolloutHistory) error {
	// do update until rollout.status.Phase is equl to RolloutPhaseHealthy
	if rollout.Status.Phase != rolloutv1alpha1.RolloutPhaseHealthy {
		return nil
	}
	// when this rollot's phase has been healthy, rolloutHistory record steps information and rollout.spec
	// record .spec for rolloutHistory
	var err error
	spec, err := r.getRolloutHistorySpec(rollout)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(rolloutHistory.Spec.Rollout.RolloutID, spec.Rollout.RolloutID) {
		// update rolloutHistory Spec
		rolloutHistory.Spec = spec
		err = r.Update(context.TODO(), rolloutHistory, &client.UpdateOptions{})
		if err != nil {
			return err
		}
		return nil
	}
	// make .status.phase PhaseCompleted
	rolloutHistory.Status.Phase = rolloutv1alpha1.PhaseCompleted
	// record all pods information for rolloutHistory .status.canarySteps
	err = r.recordStatusCanarySteps(rollout, rolloutHistory)
	if err != nil {
		return err
	}
	// update rolloutHistory subresource
	return r.Status().Update(context.TODO(), rolloutHistory, &client.SubResourceUpdateOptions{})
}

// recordStatusCanarySteps record all pods information which are canary released
func (r *RolloutHistoryReconciler) recordStatusCanarySteps(rollout *rolloutv1alpha1.Rollout, rolloutHistory *rolloutv1alpha1.RolloutHistory) error {
	rolloutHistory.Status.CanarySteps = make([]rolloutv1alpha1.CanaryStepInfo, 0)
	for i := 0; i < len(rollout.Spec.Strategy.Canary.Steps); i++ {
		podList := &corev1.PodList{}
		var extraSelector labels.Selector
		// get workload labelSelector
		workloadLabelSelector, _ := r.Finder.getLabelSelectorForRef(rollout.Namespace, rollout.Spec.ObjectRef.WorkloadRef)
		selector, err := v1.LabelSelectorAsSelector(workloadLabelSelector)
		if err != nil {
			return err
		}
		// get extra labelSelector including rolloutBathID, rolloutID and workload selector
		lableSelectorString := fmt.Sprintf("%v=%v,%v=%v,%v", rolloutv1alpha1.RolloutBatchIDLabel, len(rolloutHistory.Status.CanarySteps)+1, rolloutv1alpha1.RolloutIDLabel, rolloutHistory.Spec.Rollout.RolloutID, selector.String())
		extraSelector, err = labels.Parse(lableSelectorString)
		if err != nil {
			return err
		}
		// get pods according to extra lableSelector
		err = r.List(context.TODO(), podList, &client.ListOptions{LabelSelector: extraSelector}, client.InNamespace(rollout.Namespace))
		if err != nil {
			return err
		}
		// if num of pods is empty, append a empty CanaryStepInfo{} to canarySteps
		if len(podList.Items) == 0 {
			index := int32(len(rolloutHistory.Status.CanarySteps)) + 1
			rolloutHistory.Status.CanarySteps = append(rolloutHistory.Status.CanarySteps, rolloutv1alpha1.CanaryStepInfo{CanaryStepIndex: index})
			continue
		}
		// get current step pods released
		currentStepInfo := rolloutv1alpha1.CanaryStepInfo{}
		var pods []rolloutv1alpha1.Pod
		// get pods name, ip, node and add them to .status.canarySteps
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.DeletionTimestamp.IsZero() {
				cur := rolloutv1alpha1.Pod{Name: pod.Name, IP: pod.Status.PodIP, NodeName: pod.Spec.NodeName}
				pods = append(pods, cur)
			}
		}
		currentStepInfo.Pods = pods
		currentStepInfo.CanaryStepIndex = int32(len(rolloutHistory.Status.CanarySteps)) + 1
		// add current step pods to .status.canarySteps
		rolloutHistory.Status.CanarySteps = append(rolloutHistory.Status.CanarySteps, currentStepInfo)
	}
	return nil
}
