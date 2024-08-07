/*
Copyright 2023 The Kruise Authors.

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
	"flag"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	concurrentReconciles            = 3
	defaultGracePeriodSeconds int32 = 3
	trControllerKind                = v1alpha1.SchemeGroupVersion.WithKind("TrafficRouting")
)

func init() {
	flag.IntVar(&concurrentReconciles, "trafficrouting-workers", 3, "Max concurrent workers for trafficrouting controller.")
}

// TrafficRoutingReconciler reconciles a TrafficRouting object
type TrafficRoutingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	trafficRoutingManager *trafficrouting.Manager
}

//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=trafficroutings,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=trafficroutings/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=trafficroutings/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Rollout object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *TrafficRoutingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the Rollout instance
	tr := &v1alpha1.TrafficRouting{}
	err := r.Get(context.TODO(), req.NamespacedName, tr)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}
	klog.Infof("Begin to reconcile TrafficRouting %v", util.DumpJSON(tr))

	// handle finalizer
	err = r.handleFinalizer(tr)
	if err != nil {
		return ctrl.Result{}, err
	}
	newStatus := tr.Status.DeepCopy()
	if newStatus.Phase == "" {
		newStatus.Phase = v1alpha1.TrafficRoutingPhaseInitial
		newStatus.Message = "TrafficRouting is Initializing"
	}
	if !tr.DeletionTimestamp.IsZero() {
		newStatus.Phase = v1alpha1.TrafficRoutingPhaseTerminating
	}
	var done = true
	switch newStatus.Phase {
	case v1alpha1.TrafficRoutingPhaseInitial:
		err = r.trafficRoutingManager.InitializeTrafficRouting(newTrafficRoutingContext(tr))
		if err == nil {
			newStatus.Phase = v1alpha1.TrafficRoutingPhaseHealthy
			newStatus.Message = "TrafficRouting is Healthy"
		}
	case v1alpha1.TrafficRoutingPhaseHealthy:
		if rolloutProgressingFinalizer(tr).Len() > 0 {
			newStatus.Phase = v1alpha1.TrafficRoutingPhaseProgressing
			newStatus.Message = "TrafficRouting is Progressing"
		}
	case v1alpha1.TrafficRoutingPhaseProgressing:
		if rolloutProgressingFinalizer(tr).Len() == 0 {
			newStatus.Phase = v1alpha1.TrafficRoutingPhaseFinalizing
			newStatus.Message = "TrafficRouting is Finalizing"
		} else {
			done, err = r.trafficRoutingManager.DoTrafficRouting(newTrafficRoutingContext(tr))
		}
	case v1alpha1.TrafficRoutingPhaseFinalizing:
		done, err = r.trafficRoutingManager.FinalisingTrafficRouting(newTrafficRoutingContext(tr))
		if done {
			newStatus.Phase = v1alpha1.TrafficRoutingPhaseHealthy
			newStatus.Message = "TrafficRouting is Healthy"
		}
	case v1alpha1.TrafficRoutingPhaseTerminating:
		done, err = r.trafficRoutingManager.FinalisingTrafficRouting(newTrafficRoutingContext(tr))
		if done {
			// remove trafficRouting finalizer
			err = r.handleFinalizer(tr)
		}
	}
	if err != nil {
		return ctrl.Result{}, err
	} else if !done {
		recheckTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
		return ctrl.Result{RequeueAfter: time.Until(recheckTime)}, nil
	}
	newStatus.ObservedGeneration = tr.Generation
	return ctrl.Result{}, r.updateTrafficRoutingStatus(tr, *newStatus)
}

func (r *TrafficRoutingReconciler) updateTrafficRoutingStatus(tr *v1alpha1.TrafficRouting, newStatus v1alpha1.TrafficRoutingStatus) error {
	if reflect.DeepEqual(tr.Status, newStatus) {
		return nil
	}
	var err error
	trClone := tr.DeepCopy()
	if err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, trClone); err != nil {
		klog.Errorf("error getting updated trafficRouting(%s/%s) from client", tr.Namespace, tr.Name)
		return err
	}
	trClone.Status = newStatus
	if err = r.Client.Status().Update(context.TODO(), trClone); err != nil {
		klog.Errorf("update trafficRouting(%s/%s) status failed: %s", tr.Namespace, tr.Name, err.Error())
		return err
	}
	klog.Infof("trafficRouting(%s/%s) status from(%s) -> to(%s) success", tr.Namespace, tr.Name, util.DumpJSON(tr.Status), util.DumpJSON(newStatus))
	return nil
}

// handle adding and handle finalizer logic, it turns if we should continue to reconcile
func (r *TrafficRoutingReconciler) handleFinalizer(tr *v1alpha1.TrafficRouting) error {
	// delete trafficRouting crd, remove finalizer
	if !tr.DeletionTimestamp.IsZero() {
		err := util.UpdateFinalizer(r.Client, tr, util.RemoveFinalizerOpType, util.TrafficRoutingFinalizer)
		if err != nil {
			klog.Errorf("remove trafficRouting(%s/%s) finalizer failed: %s", tr.Namespace, tr.Name, err.Error())
			return err
		}
		klog.Infof("remove trafficRouting(%s/%s) finalizer success", tr.Namespace, tr.Name)
		return nil
	}

	// create trafficRouting crd, add finalizer
	if !controllerutil.ContainsFinalizer(tr, util.TrafficRoutingFinalizer) {
		err := util.UpdateFinalizer(r.Client, tr, util.AddFinalizerOpType, util.TrafficRoutingFinalizer)
		if err != nil {
			klog.Errorf("register trafficRouting(%s/%s) finalizer failed: %s", tr.Namespace, tr.Name, err.Error())
			return err
		}
		klog.Infof("register trafficRouting(%s/%s) finalizer success", tr.Namespace, tr.Name)
	}
	return nil
}

func rolloutProgressingFinalizer(tr *v1alpha1.TrafficRouting) sets.String {
	progressing := sets.String{}
	for _, s := range tr.GetFinalizers() {
		if strings.Contains(s, v1alpha1.ProgressingRolloutFinalizerPrefix) {
			progressing.Insert(s)
		}
	}
	return progressing
}

func (r *TrafficRoutingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("trafficrouting-controller", mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}
	// Watch for changes to trafficrouting
	if err = c.Watch(&source.Kind{Type: &v1alpha1.TrafficRouting{}}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	r.trafficRoutingManager = trafficrouting.NewTrafficRoutingManager(mgr.GetClient())
	return nil
}

func newTrafficRoutingContext(tr *v1alpha1.TrafficRouting) *trafficrouting.TrafficRoutingContext {
	c := &trafficrouting.TrafficRoutingContext{
		Key:                fmt.Sprintf("TrafficRouting(%s/%s)", tr.Namespace, tr.Name),
		Namespace:          tr.Namespace,
		Strategy:           v1alpha1.ConversionToV1beta1TrafficRoutingStrategy(tr.Spec.Strategy),
		OwnerRef:           *metav1.NewControllerRef(tr, trControllerKind),
		OnlyTrafficRouting: true,
	}
	for _, ref := range tr.Spec.ObjectRef {
		c.ObjectRef = append(c.ObjectRef, v1alpha1.ConversionToV1beta1TrafficRoutingRef(ref))
	}
	return c
}
