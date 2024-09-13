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
	"flag"
	"sync"
	"time"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	concurrentReconciles = 3
	runtimeController    controller.Controller
	workloadHandler      handler.EventHandler
	watchedWorkload      sync.Map

	rolloutControllerKind = v1beta1.SchemeGroupVersion.WithKind("Rollout")
)

func init() {
	flag.IntVar(&concurrentReconciles, "rollout-workers", 3, "Max concurrent workers for rollout controller.")

	watchedWorkload = sync.Map{}
	watchedWorkload.LoadOrStore(util.ControllerKindDep.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKindSts.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseKindCS.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseKindSts.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseOldKindSts.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseKindDS.String(), struct{}{})
}

// RolloutReconciler reconciles a Rollout object
type RolloutReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	finder                *util.ControllerFinder
	trafficRoutingManager *trafficrouting.Manager
	canaryManager         *canaryReleaseManager
	blueGreenManager      *blueGreenReleaseManager
}

//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouts/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.kruise.io,resources=daemonsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps.kruise.io,resources=daemonsets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices;destinationrules,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Rollout object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *RolloutReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the Rollout instance
	rollout := &v1beta1.Rollout{}
	err := r.Get(context.TODO(), req.NamespacedName, rollout)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	klog.Infof("Begin to reconcile Rollout %v", klog.KObj(rollout))

	//  If workload watcher does not exist, then add the watcher dynamically
	workloadRef := rollout.Spec.WorkloadRef
	workloadGVK := util.GetGVKFrom(&workloadRef)
	_, exists := watchedWorkload.Load(workloadGVK.String())
	if !exists {
		succeeded, err := util.AddWatcherDynamically(runtimeController, workloadHandler, workloadGVK)
		if err != nil {
			return ctrl.Result{}, err
		} else if succeeded {
			watchedWorkload.LoadOrStore(workloadGVK.String(), struct{}{})
			klog.Infof("Rollout controller begin to watch workload type: %s", workloadGVK.String())
			// return, and wait informer cache to be synced
			return ctrl.Result{}, nil
		}
	}

	// handle finalizer
	err = r.handleFinalizer(rollout)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Patch rollout webhook objectSelector in workload labels[rollouts.kruise.io/workload-type],
	// then rollout only webhook the workload which contains labels[rollouts.kruise.io/workload-type].
	if err = r.patchWorkloadRolloutWebhookLabel(rollout); err != nil {
		return ctrl.Result{}, err
	}
	// sync rollout status
	retry, newStatus, err := r.calculateRolloutStatus(rollout)
	if err != nil {
		return ctrl.Result{}, err
	} else if retry {
		recheckTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		return ctrl.Result{RequeueAfter: time.Until(recheckTime)}, nil
	}
	var recheckTime *time.Time

	switch rollout.Status.Phase {
	case v1beta1.RolloutPhaseProgressing:
		recheckTime, err = r.reconcileRolloutProgressing(rollout, newStatus)
	case v1beta1.RolloutPhaseTerminating:
		recheckTime, err = r.reconcileRolloutTerminating(rollout, newStatus)
	case v1beta1.RolloutPhaseDisabling:
		recheckTime, err = r.reconcileRolloutDisabling(rollout, newStatus)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if newStatus != nil {
		err = r.updateRolloutStatusInternal(rollout, *newStatus)
		if err != nil {
			klog.Errorf("update rollout(%s/%s) status failed: %s", rollout.Namespace, rollout.Name, err.Error())
			return ctrl.Result{}, err
		}
	}
	if recheckTime != nil {
		return ctrl.Result{RequeueAfter: time.Until(*recheckTime)}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RolloutReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("rollout-controller", mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}
	// Watch for changes to rollout
	if err = c.Watch(&source.Kind{Type: &v1beta1.Rollout{}}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	// Watch for changes to batchRelease
	if err = c.Watch(&source.Kind{Type: &v1beta1.BatchRelease{}}, &enqueueRequestForBatchRelease{reader: mgr.GetCache()}); err != nil {
		return err
	}
	runtimeController = c
	workloadHandler = &enqueueRequestForWorkload{reader: mgr.GetCache(), scheme: r.Scheme}
	if err = util.AddWorkloadWatcher(c, workloadHandler); err != nil {
		return err
	}
	r.finder = util.NewControllerFinder(mgr.GetClient())
	r.trafficRoutingManager = trafficrouting.NewTrafficRoutingManager(mgr.GetClient())
	r.canaryManager = &canaryReleaseManager{
		Client:                mgr.GetClient(),
		trafficRoutingManager: r.trafficRoutingManager,
		recorder:              r.Recorder,
	}
	r.blueGreenManager = &blueGreenReleaseManager{
		Client:                mgr.GetClient(),
		trafficRoutingManager: r.trafficRoutingManager,
		recorder:              r.Recorder,
	}
	return nil
}
