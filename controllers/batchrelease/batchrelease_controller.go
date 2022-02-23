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

package batchrelease

import (
	"context"
	"flag"
	"reflect"
	"time"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	concurrentReconciles = 3
)

const ReleaseFinalizer = "rollouts.kruise.io/batch-release-finalizer"

func init() {
	flag.IntVar(&concurrentReconciles, "batchrelease-workers", concurrentReconciles, "Max concurrent workers for BatchRelease controller.")
}

// Add creates a new Rollout Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	recorder := mgr.GetEventRecorderFor("batchrelease-controller")
	cli := mgr.GetClient()
	return &BatchReleaseReconciler{
		Client:   cli,
		Scheme:   mgr.GetScheme(),
		recorder: recorder,
		executor: NewReleasePlanExecutor(cli, recorder),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("batchrelease-controller", mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}

	// Watch for changes to BatchRelease
	err = c.Watch(&source.Kind{Type: &v1alpha1.BatchRelease{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObject := e.ObjectOld.(*v1alpha1.BatchRelease)
			newObject := e.ObjectNew.(*v1alpha1.BatchRelease)
			if oldObject.Generation != newObject.Generation || newObject.DeletionTimestamp != nil {
				klog.V(3).Infof("Observed updated Spec for BatchRelease: %s/%s", newObject.Namespace, newObject.Name)
				return true
			}
			return false
		},
	})
	if err != nil {
		return err
	}

	if util.DiscoverGVK(util.CloneSetGVK) {
		// Watch changes to CloneSet
		err = c.Watch(&source.Kind{Type: &kruiseappsv1alpha1.CloneSet{}}, &workloadEventHandler{Reader: mgr.GetCache()})
		if err != nil {
			return err
		}
	}

	// Watch changes to Deployment
	err = c.Watch(&source.Kind{Type: &apps.Deployment{}}, &workloadEventHandler{Reader: mgr.GetCache()})
	if err != nil {
		return err
	}
	return nil
}

var _ reconcile.Reconciler = &BatchReleaseReconciler{}

// BatchReleaseReconciler reconciles a BatchRelease object
type BatchReleaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
	executor *Executor
}

// +kubebuilder:rbac:groups="*",resources="events",verbs=create;update;patch
// +kubebuilder:rbac:groups=rollouts.kruise.io,resources=batchreleases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rollouts.kruise.io,resources=batchreleases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.kruise.io,resources=clonesets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps.kruise.io,resources=clonesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=replicasets/status,verbs=get;update;patch

// Reconcile reads that state of the cluster for a Rollout object and makes changes based on the state read
// and what is in the Rollout.Spec
func (r *BatchReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	release := new(v1alpha1.BatchRelease)
	err := r.Get(context.TODO(), req.NamespacedName, release)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	klog.Infof("Begin to reconcile BatchRelease(%v/%v), release-phase: %v", release.Namespace, release.Name, release.Status.Phase)

	// finalizer will block the deletion of batchRelease
	// util all canary resources and settings are cleaned up.
	reconcileDone, err := r.handleFinalizer(release)
	if reconcileDone {
		return reconcile.Result{}, err
	}

	// set the release info for executor before executing.
	r.executor.SetReleaseInfo(release)

	// executor start to execute the batch release plan.
	startTimestamp := time.Now()
	result, currentStatus := r.executor.Do()

	defer func() {
		klog.InfoS("Finished one round of reconciling release plan",
			"BatchRelease", client.ObjectKeyFromObject(release),
			"phase", currentStatus.Phase,
			"current-batch", currentStatus.CanaryStatus.CurrentBatch,
			"current-batch-state", currentStatus.CanaryStatus.CurrentBatchState,
			"reconcile-result ", result, "time-cost", time.Since(startTimestamp))
	}()

	return result, r.updateStatus(ctx, release, currentStatus)
}

func (r *BatchReleaseReconciler) updateStatus(ctx context.Context, release *v1alpha1.BatchRelease, newStatus *v1alpha1.BatchReleaseStatus) error {
	var err error
	defer func() {
		if err != nil {
			klog.Errorf("Failed to update status for BatchRelease(%v), error: %v", client.ObjectKeyFromObject(release), err)
		}
	}()

	// observe and record the latest changes for generation and release plan
	newStatus.ObservedGeneration = release.Generation
	newStatus.ObservedReleasePlanHash = hashReleasePlanBatches(&release.Spec.ReleasePlan)

	// do not retry
	if !reflect.DeepEqual(release.Status, *newStatus) {
		releaseClone := release.DeepCopy()
		releaseClone.Status = *newStatus
		return r.Status().Update(ctx, releaseClone)
	}

	return err
}

func (r *BatchReleaseReconciler) handleFinalizer(release *v1alpha1.BatchRelease) (bool, error) {
	var err error
	defer func() {
		if err != nil {
			klog.Errorf("Failed to handle finalizer for BatchRelease(%v), error: %v", client.ObjectKeyFromObject(release), err)
		}
	}()

	// remove the release finalizer if it needs
	if !release.DeletionTimestamp.IsZero() &&
		HasTerminatingCondition(release.Status) &&
		controllerutil.ContainsFinalizer(release, ReleaseFinalizer) {
		finalizers := sets.NewString(release.Finalizers...).Delete(ReleaseFinalizer).List()
		err = util.PatchFinalizer(r.Client, release, finalizers)
		if client.IgnoreNotFound(err) != nil {
			return true, err
		}
		return true, nil
	}

	// add the release finalizer if it needs
	if !controllerutil.ContainsFinalizer(release, ReleaseFinalizer) {
		finalizers := append(release.Finalizers, ReleaseFinalizer)
		err = util.PatchFinalizer(r.Client, release, finalizers)
		if client.IgnoreNotFound(err) != nil {
			return true, err
		}
	}

	return false, nil
}
