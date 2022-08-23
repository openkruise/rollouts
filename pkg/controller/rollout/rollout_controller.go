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

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
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
)

func init() {
	flag.IntVar(&concurrentReconciles, "rollout-workers", 3, "Max concurrent workers for rollout controller.")

	watchedWorkload = sync.Map{}
	watchedWorkload.LoadOrStore(util.ControllerKindDep.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKindSts.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseKindCS.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseKindSts.String(), struct{}{})
	watchedWorkload.LoadOrStore(util.ControllerKruiseOldKindSts.String(), struct{}{})
}

// RolloutReconciler reconciles a Rollout object
type RolloutReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Recorder record.EventRecorder
	Finder   *util.ControllerFinder
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
	rollout := &rolloutv1alpha1.Rollout{}
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
	workloadRef := rollout.Spec.ObjectRef.WorkloadRef
	workloadGVK := util.GetGVKFrom(workloadRef)
	_, exists := watchedWorkload.Load(workloadGVK.String())
	if workloadRef != nil && !exists {
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
	// update rollout status
	done, err := r.updateRolloutStatus(rollout)
	if err != nil {
		return ctrl.Result{}, err
	} else if !done {
		return ctrl.Result{}, nil
	}

	var recheckTime *time.Time
	switch rollout.Status.Phase {
	case rolloutv1alpha1.RolloutPhaseProgressing:
		recheckTime, err = r.reconcileRolloutProgressing(rollout)
	case rolloutv1alpha1.RolloutPhaseTerminating:
		recheckTime, err = r.reconcileRolloutTerminating(rollout)
	}
	if err != nil {
		return ctrl.Result{}, err
	} else if recheckTime != nil {
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
	if err = c.Watch(&source.Kind{Type: &rolloutv1alpha1.Rollout{}}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	// Watch for changes to batchRelease
	if err = c.Watch(&source.Kind{Type: &rolloutv1alpha1.BatchRelease{}}, &enqueueRequestForBatchRelease{reader: mgr.GetCache()}); err != nil {
		return err
	}

	runtimeController = c
	workloadHandler = &enqueueRequestForWorkload{reader: mgr.GetCache(), scheme: r.Scheme}
	if err = util.AddWorkloadWatcher(c, workloadHandler); err != nil {
		return err
	}
	return nil
}
