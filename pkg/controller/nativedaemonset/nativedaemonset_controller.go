/*
Copyright 2025 The Kruise Authors.

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

package nativedaemonset

import (
	"context"
	"flag"
	"fmt"
	"reflect"
	"sort"
	"time"

	daemonsetutil "github.com/openkruise/rollouts/pkg/controller/nativedaemonset/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
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

	"github.com/openkruise/rollouts/pkg/util"
	clientutil "github.com/openkruise/rollouts/pkg/util/client"
)

func init() {
	flag.IntVar(&concurrentReconciles, "nativedaemonset-workers", concurrentReconciles, "Max concurrent workers for native daemonset controller.")
}

const (
	DefaultRetryDuration = 2 * time.Second
	ControllerName       = "native-daemonset-controller"
)

var (
	concurrentReconciles = 3
)

// Add creates a new Native DaemonSet Controller and adds it to the Manager with default RBAC.
func Add(mgr manager.Manager) error {
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}
	return add(mgr, r)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	// Client & Recorder
	genericClient := clientutil.GetGenericClientWithName(ControllerName)
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: genericClient.KubeClient.CoreV1().Events("")})
	recorder := mgr.GetEventRecorderFor(ControllerName)

	return &ReconcileNativeDaemonSet{
		Client:        mgr.GetClient(),
		eventRecorder: recorder,
	}, nil
}

// ReconcileNativeDaemonSet reconciles a Native DaemonSet object
type ReconcileNativeDaemonSet struct {
	// client interface
	client.Client
	eventRecorder record.EventRecorder
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}

	// Watch for changes to DaemonSet with specific annotations
	updateHandler := func(e event.UpdateEvent) bool {
		oldObject := e.ObjectOld.(*appsv1.DaemonSet)
		newObject := e.ObjectNew.(*appsv1.DaemonSet)
		if len(oldObject.Annotations) != len(newObject.Annotations) || !reflect.DeepEqual(oldObject.Annotations, newObject.Annotations) {
			klog.V(3).Infof("Observed updated Annotation for native DaemonSet: %s/%s", newObject.Namespace, newObject.Name)
			return true
		}
		return false
	}

	// Watch for changes to DaemonSet
	return c.Watch(source.Kind(mgr.GetCache(), &appsv1.DaemonSet{}), &handler.EnqueueRequestForObject{}, predicate.Funcs{UpdateFunc: updateHandler})
}

// Reconcile reads that state of the cluster for a DaemonSet object and makes changes based on the annotations
// to implement progressive delivery by deleting pods according to batch requirements.
func (r *ReconcileNativeDaemonSet) Reconcile(_ context.Context, request reconcile.Request) (reconcile.Result, error) {
	daemon := new(appsv1.DaemonSet)
	err := r.Get(context.TODO(), request.NamespacedName, daemon)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Check for partition annotation
	partitionStr, hasPartition := daemon.Annotations[util.DaemonSetPartitionAnnotation]
	if !hasPartition {
		// No partition annotation, nothing to do
		return reconcile.Result{}, nil
	}

	// Get all pods owned by this DaemonSet using ListOwnedPods utility function
	pods, err := util.ListOwnedPods(r.Client, daemon)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Calculate desired updated replicas based on partition, which equals to total pods number - partition
	desiredUpdatedReplicas, err := daemonsetutil.CalculateDesiredUpdatedReplicas(partitionStr, len(pods))
	if err != nil {
		return ctrl.Result{}, err
	}

	klog.Infof("Processing DaemonSet %s/%s with desired updated replicas: %d", daemon.Namespace, daemon.Name, desiredUpdatedReplicas)

	// Process the pods according to partition requirements
	result, err := r.processBatch(daemon, pods, desiredUpdatedReplicas)
	if err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

// analyzePods analyzes the current pod state and determines how many pods need to be deleted
// Returns: podsToDelete slice, needToDelete count, batchCompleted bool, error
func (r *ReconcileNativeDaemonSet) analyzePods(pods []*corev1.Pod, updateRevision string, desiredUpdatedReplicas int32, daemon *appsv1.DaemonSet) ([]*corev1.Pod, int32, bool, error) {
	updatedPods := int32(0)
	podsToDelete := make([]*corev1.Pod, 0)

	// Count updated pods and identify pods to delete
	for _, pod := range pods {
		if util.IsConsistentWithRevision(pod.GetLabels(), updateRevision) {
			updatedPods++
			klog.Infof("Pod %s/%s matches update revision %s, counting as updated", pod.Namespace, pod.Name, updateRevision)
		} else {
			// Pods without the label or with an old revision need to be deleted
			podsToDelete = append(podsToDelete, pod)
		}
	}

	// Check if we already have enough updated pods
	if updatedPods >= desiredUpdatedReplicas {
		klog.Infof("Batch completed: have %d updated pods >= %d desired, marking batch as completed", updatedPods, desiredUpdatedReplicas)
		return podsToDelete, 0, true, nil
	}

	// Calculate how many pods need to be deleted
	needToDelete := desiredUpdatedReplicas - updatedPods

	// Get maxUnavailable constraint
	maxUnavailable := daemonsetutil.GetMaxUnavailable(daemon)

	klog.Infof("Current status - updatedPods: %d, desired: %d, needToDelete: %d, available: %d",
		updatedPods, desiredUpdatedReplicas, needToDelete, len(podsToDelete))

	// Apply constraints
	needToDelete = daemonsetutil.ApplyDeletionConstraints(needToDelete, maxUnavailable, len(podsToDelete))

	return podsToDelete, needToDelete, false, nil
}

// executePodDeletion executes the actual pod deletion based on analysis
func (r *ReconcileNativeDaemonSet) executePodDeletion(podsToDelete []*corev1.Pod, needToDelete int32, daemon *appsv1.DaemonSet) error {
	if needToDelete <= 0 {
		klog.Infof("No pods need to be deleted")
		return nil
	}

	maxUnavailable := daemonsetutil.GetMaxUnavailable(daemon)
	klog.Infof("Planning to delete %d pods (maxUnavailable: %d)", needToDelete, maxUnavailable)

	// Sort pods by creation timestamp to delete oldest first
	sort.Slice(podsToDelete, func(i, j int) bool {
		return podsToDelete[i].CreationTimestamp.Before(&podsToDelete[j].CreationTimestamp)
	})

	for i := int32(0); i < needToDelete && i < int32(len(podsToDelete)); i++ {
		pod := podsToDelete[i]
		klog.Infof("About to delete pod %s/%s (currentRevision=%s targetRevision=%s)",
			pod.Namespace, pod.Name,
			pod.Labels[appsv1.ControllerRevisionHashLabelKey], daemon.Annotations[util.DaemonSetBatchRevisionAnnotation])

		err := r.Delete(context.TODO(), pod)
		if err != nil {
			return fmt.Errorf("failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}

		r.eventRecorder.Event(daemon, corev1.EventTypeNormal, "PodDeleted",
			fmt.Sprintf("Deleted pod %s/%s for batch update", pod.Namespace, pod.Name))

		klog.Infof("Successfully deleted pod %s/%s", pod.Namespace, pod.Name)
	}

	return nil
}

// processBatch handles the actual pod deletion logic based on batch requirements
func (r *ReconcileNativeDaemonSet) processBatch(daemon *appsv1.DaemonSet, pods []*corev1.Pod, desiredReplicas int32) (reconcile.Result, error) {

	// Check if there are pods being deleted, if so, exit immediately
	if daemonsetutil.HasPodsBeingDeleted(pods) {
		klog.Infof("Pods are being deleted, requeue after %v", DefaultRetryDuration)
		return reconcile.Result{RequeueAfter: DefaultRetryDuration}, nil
	}

	// Analyze pods and determine what needs to be done
	podsToDelete, needToDelete, batchCompleted, err := r.analyzePods(pods, daemon.Annotations[util.DaemonSetBatchRevisionAnnotation], desiredReplicas, daemon)
	if err != nil {
		return reconcile.Result{}, err
	}

	// If batch is completed, don't requeue unless there are external changes
	if batchCompleted {
		klog.Infof("Batch is completed, not requeueing")
		return reconcile.Result{}, nil
	}

	// Execute pod deletion based on analysis
	err = r.executePodDeletion(podsToDelete, needToDelete, daemon)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Only requeue if we actually did some work (deleted pods)
	if needToDelete > 0 {
		klog.Infof("Deleted %d pods, requeue after %v to check progress", needToDelete, DefaultRetryDuration)
		return reconcile.Result{RequeueAfter: DefaultRetryDuration}, nil
	}

	return reconcile.Result{}, nil
}
