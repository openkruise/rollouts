/*
Copyright 2019 The Kruise Authors.

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

package deployment

import (
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	deploymentutil "github.com/openkruise/rollouts/pkg/controller/deployment/util"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
	clientutil "github.com/openkruise/rollouts/pkg/util/client"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
	"github.com/openkruise/rollouts/pkg/util/patch"
	"github.com/openkruise/rollouts/pkg/webhook/util/configuration"
)

func init() {
	flag.IntVar(&concurrentReconciles, "deployment-workers", concurrentReconciles, "Max concurrent workers for advanced deployment controller.")
}

const (
	DefaultRetryDuration = 2 * time.Second
)

var (
	concurrentReconciles = 3
)

// Add creates a new StatefulSet Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	if !utilfeature.DefaultFeatureGate.Enabled(feature.AdvancedDeploymentGate) {
		klog.Warningf("Advanced deployment controller is disabled")
		return nil
	}
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}
	return add(mgr, r)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	cacher := mgr.GetCache()
	dInformer, err := cacher.GetInformerForKind(context.TODO(), appsv1.SchemeGroupVersion.WithKind("Deployment"))
	if err != nil {
		return nil, err
	}
	rsInformer, err := cacher.GetInformerForKind(context.TODO(), appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))
	if err != nil {
		return nil, err
	}

	// Lister
	dLister := appslisters.NewDeploymentLister(dInformer.(toolscache.SharedIndexInformer).GetIndexer())
	rsLister := appslisters.NewReplicaSetLister(rsInformer.(toolscache.SharedIndexInformer).GetIndexer())

	// Client & Recorder
	genericClient := clientutil.GetGenericClientWithName("advanced-deployment-controller")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: genericClient.KubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "advanced-deployment-controller"})

	// Deployment controller factory
	factory := &controllerFactory{
		client:           genericClient.KubeClient,
		eventBroadcaster: eventBroadcaster,
		eventRecorder:    recorder,
		dLister:          dLister,
		rsLister:         rsLister,
	}
	return &ReconcileDeployment{Client: mgr.GetClient(), controllerFactory: factory}, nil
}

var _ reconcile.Reconciler = &ReconcileDeployment{}

// ReconcileDeployment reconciles a Deployment object
type ReconcileDeployment struct {
	// client interface
	client.Client
	controllerFactory *controllerFactory
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("advanced-deployment-controller", mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}

	// Watch for changes to ReplicaSet
	if err = c.Watch(&source.Kind{Type: &appsv1.ReplicaSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true, OwnerType: &appsv1.Deployment{}}, predicate.Funcs{}); err != nil {
		return err
	}

	// Watch for changes to MutatingWebhookConfigurations of kruise-rollout operator
	if err = c.Watch(&source.Kind{Type: &admissionregistrationv1.MutatingWebhookConfiguration{}}, &MutatingWebhookEventHandler{mgr.GetCache()}); err != nil {
		return err
	}

	// TODO: handle deployment only when the deployment is under our control
	updateHandler := func(e event.UpdateEvent) bool {
		oldObject := e.ObjectOld.(*appsv1.Deployment)
		newObject := e.ObjectNew.(*appsv1.Deployment)
		if !deploymentutil.IsUnderRolloutControl(newObject) {
			return false
		}
		if oldObject.Generation != newObject.Generation || newObject.DeletionTimestamp != nil {
			klog.V(3).Infof("Observed updated Spec for Deployment: %s/%s", newObject.Namespace, newObject.Name)
			return true
		}
		if len(oldObject.Annotations) != len(newObject.Annotations) || !reflect.DeepEqual(oldObject.Annotations, newObject.Annotations) {
			klog.V(3).Infof("Observed updated Annotation for Deployment: %s/%s", newObject.Namespace, newObject.Name)
			return true
		}
		return false
	}

	// Watch for changes to Deployment
	return c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{UpdateFunc: updateHandler})
}

// Reconcile reads that state of the cluster for a Deployment object and makes changes based on the state read
// and what is in the Deployment.Spec and Deployment.Annotations
// Automatically generate RBAC rules to allow the Controller to read and write ReplicaSets
func (r *ReconcileDeployment) Reconcile(_ context.Context, request reconcile.Request) (reconcile.Result, error) {
	deployment := new(appsv1.Deployment)
	err := r.Get(context.TODO(), request.NamespacedName, deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// TODO: create new controller only when deployment is under our control
	dc := r.controllerFactory.NewController(deployment)
	if dc == nil {
		return reconcile.Result{}, nil
	}

	// If MutatingWebhookConfiguration is deleted, the Deployment may be set paused=false,
	// which will increase the risk of release. To prevent such a risk, in such a case, we
	// will update the Deployment strategy type field to RollingUpdate.
	invalid, err := r.mutatingProtectionInvalid(deployment)
	if err != nil {
		return reconcile.Result{}, err
	} else if invalid {
		return reconcile.Result{}, nil
	}

	errList := field.ErrorList{}
	err = dc.syncDeployment(context.Background(), deployment)
	if err != nil {
		errList = append(errList, field.InternalError(field.NewPath("syncDeployment"), err))
	}
	err = dc.patchExtraStatus(deployment)
	if err != nil {
		errList = append(errList, field.InternalError(field.NewPath("patchExtraStatus"), err))
	}
	if len(errList) > 0 {
		return ctrl.Result{}, errList.ToAggregate()
	}
	err = deploymentutil.DeploymentRolloutSatisfied(deployment, dc.strategy.Partition)
	if err != nil {
		klog.V(3).Infof("Deployment %v is still rolling: %v", klog.KObj(deployment), err)
		return reconcile.Result{RequeueAfter: DefaultRetryDuration}, nil
	}
	return reconcile.Result{}, nil
}

// mutatingProtectionInvalid check if mutating webhook configuration not exists, if not exists,
// we should update deployment strategy type tpo 'RollingUpdate' to avoid release risk.
func (r *ReconcileDeployment) mutatingProtectionInvalid(deployment *appsv1.Deployment) (bool, error) {
	configKey := types.NamespacedName{Name: configuration.MutatingWebhookConfigurationName}
	mutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err := r.Get(context.TODO(), configKey, mutatingWebhookConfiguration)
	if client.IgnoreNotFound(err) != nil {
		return false, err
	}
	if errors.IsNotFound(err) || !mutatingWebhookConfiguration.DeletionTimestamp.IsZero() {
		if deployment.Spec.Strategy.Type == appsv1.RollingUpdateDeploymentStrategyType {
			return true, nil
		}
		strategy := util.GetDeploymentStrategy(deployment)
		d := deployment.DeepCopy()
		patchData := patch.NewDeploymentPatch()
		patchData.UpdateStrategy(appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: strategy.RollingUpdate})
		klog.Warningf("Kruise-Rollout mutating webhook configuration is deleted, update Deployment %v strategy to 'RollingUpdate'", klog.KObj(deployment))
		return true, r.Patch(context.TODO(), d, patchData)
	}
	return false, nil
}

type controllerFactory DeploymentController

// NewController create a new DeploymentController
// TODO: create new controller only when deployment is under our control
func (f *controllerFactory) NewController(deployment *appsv1.Deployment) *DeploymentController {
	if !deploymentutil.IsUnderRolloutControl(deployment) {
		klog.Warningf("Deployment %v is not under rollout control, ignore", klog.KObj(deployment))
		return nil
	}

	strategy := rolloutsv1alpha1.DeploymentStrategy{}
	strategyAnno := deployment.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation]
	if err := json.Unmarshal([]byte(strategyAnno), &strategy); err != nil {
		klog.Errorf("Failed to unmarshal strategy for deployment %v: %v", klog.KObj(deployment), strategyAnno)
		return nil
	}

	// We do NOT process such deployment with canary rolling style
	if strategy.RollingStyle == rolloutsv1alpha1.CanaryRollingStyle {
		return nil
	}

	marshaled, _ := json.Marshal(&strategy)
	klog.V(4).Infof("Processing deployment %v strategy %v", klog.KObj(deployment), string(marshaled))

	return &DeploymentController{
		client:           f.client,
		eventBroadcaster: f.eventBroadcaster,
		eventRecorder:    f.eventRecorder,
		dLister:          f.dLister,
		rsLister:         f.rsLister,
		strategy:         strategy,
	}
}
