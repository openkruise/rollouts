package advanceddeployment

import (
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/advanceddeployment/util"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	DefaultDuration = time.Second
)

var (
	concurrentReconciles = 3
)

func init() {
	flag.IntVar(&concurrentReconciles, "advanced-deployment-workers", 3, "Max concurrent workers for advanced deployment controller.")
}

// Add creates a new Rollout Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	recorder := mgr.GetEventRecorderFor("advanced-deployment-controller")
	cli := mgr.GetClient()
	return &AdvancedDeploymentReconciler{
		Client:   cli,
		Scheme:   mgr.GetScheme(),
		recorder: recorder,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("advanced-deployment-controller", mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}

	// Watch for changes to Deployment
	err = c.Watch(&source.Kind{Type: &apps.Deployment{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObject := e.ObjectOld.(*apps.Deployment)
			newObject := e.ObjectNew.(*apps.Deployment)
			if !util.IsUnderOurControl(newObject) || !util.DisableNativeController(newObject) {
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
		},
	})
	if err != nil {
		return err
	}

	return c.Watch(&source.Kind{Type: &apps.ReplicaSet{}}, &handler.EnqueueRequestForOwner{IsController: true, OwnerType: &apps.ReplicaSet{}}, predicate.Funcs{})
}

var _ reconcile.Reconciler = &AdvancedDeploymentReconciler{}

// AdvancedDeploymentReconciler reconciles a Deployment object
type AdvancedDeploymentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

// Reconcile reads that state of the cluster for a Rollout object and makes changes based on the state read
// and what is in the Rollout.Spec
func (r *AdvancedDeploymentReconciler) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	deployment := new(apps.Deployment)
	err := r.Get(context.TODO(), req.NamespacedName, deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	klog.Infof("Into Advanced Deployment controller for (%v)", klog.KObj(deployment))

	syncer, strategy := r.buildSyncer(deployment)
	if syncer == nil {
		klog.Infof("Failed to build syncer for (%v)", klog.KObj(deployment))
		return reconcile.Result{}, nil
	}

	klog.Infof("Begin to reconcile Advanced Deployment (%v), strategy: %v", klog.KObj(deployment), *strategy)

	completed, err := syncer.SyncDeployment(deployment)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = syncer.UpdateExtraStatus(deployment)
	if completed {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: DefaultDuration}, err
}

func (r *AdvancedDeploymentReconciler) buildSyncer(deployment *apps.Deployment) (*DeploymentController, *string) {
	if !util.IsUnderOurControl(deployment) {
		klog.Infof("Deployment %v is not under our control", klog.KObj(deployment))
		return nil, nil
	}
	if !util.DisableNativeController(deployment) {
		klog.Infof("Native Deployment controller is not disabled for %v", klog.KObj(deployment))
		return nil, nil
	}

	strategy := v1alpha1.AdvancedDeploymentStrategy{}
	strategyAnno := deployment.Annotations[v1alpha1.AdvancedDeploymentStrategyAnnotation]
	if strategyAnno != "" {
		err := json.Unmarshal([]byte(strategyAnno), &strategy)
		if err != nil {
			klog.Errorf("Failed to decode strategy for advanced deployment %v: %v", klog.KObj(deployment), strategyAnno)
			return nil, &strategyAnno
		}
	}
	v1alpha1.SetDefaultAdvancedDeploymentStrategy(&strategy)
	marshaled, _ := json.Marshal(&strategy)
	return NewSyncer(r.Client, r.recorder, strategy), pointer.String(string(marshaled))
}
