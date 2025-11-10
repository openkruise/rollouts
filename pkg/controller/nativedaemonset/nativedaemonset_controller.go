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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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

	daemonsetutil "github.com/openkruise/rollouts/pkg/controller/nativedaemonset/util"
	"github.com/openkruise/rollouts/pkg/util"
	clientutil "github.com/openkruise/rollouts/pkg/util/client"
	expectations "github.com/openkruise/rollouts/pkg/util/expectation"
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
		expectations:  expectations.NewResourceExpectations(),
	}, nil
}

// ReconcileNativeDaemonSet reconciles a Native DaemonSet object
type ReconcileNativeDaemonSet struct {
	// client interface
	client.Client
	eventRecorder record.EventRecorder
	// A TTLCache of pod creates/deletes each ds expects to see
	expectations expectations.Expectations
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	reconciler := r.(*ReconcileNativeDaemonSet)

	// Create a new controller
	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler: r, MaxConcurrentReconciles: concurrentReconciles})
	if err != nil {
		return err
	}

	// Watch for changes to DaemonSet partition annotation ONLY
	updateHandler := func(e event.UpdateEvent) bool {
		oldObject := e.ObjectOld.(*appsv1.DaemonSet)
		newObject := e.ObjectNew.(*appsv1.DaemonSet)

		oldPartition := oldObject.Annotations[util.DaemonSetAdvancedControlAnnotation]
		newPartition := newObject.Annotations[util.DaemonSetAdvancedControlAnnotation]

		// Only trigger reconcile when partition annotation changes
		if oldPartition != newPartition {
			klog.Infof("Observed updated partition for DaemonSet: %s/%s (partition: %s -> %s)",
				newObject.Namespace, newObject.Name, oldPartition, newPartition)
			return true
		}
		return false
	}

	// Filter DaemonSets on controller restart - only process those with partition annotation
	createHandler := func(e event.CreateEvent) bool {
		ds := e.Object.(*appsv1.DaemonSet)
		_, hasPartition := ds.Annotations[util.DaemonSetAdvancedControlAnnotation]
		if hasPartition {
			klog.Infof("Observed DaemonSet with partition annotation on controller restart: %s/%s", ds.Namespace, ds.Name)
			return true
		}
		return false
	}

	// Watch for changes to DaemonSet
	if err = c.Watch(source.Kind(mgr.GetCache(), &appsv1.DaemonSet{}),
		&handler.EnqueueRequestForObject{},
		predicate.Funcs{
			UpdateFunc: updateHandler,
			CreateFunc: createHandler,
		}); err != nil {
		return err
	}

	// Watch for pod deletions to update expectations only
	// Do NOT trigger reconcile on pod deletion
	podDeletePredicate := predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			pod := e.Object.(*corev1.Pod)
			for _, ownerRef := range pod.OwnerReferences {
				if ownerRef.Kind == "DaemonSet" &&
					ownerRef.APIVersion == "apps/v1" &&
					ownerRef.Controller != nil && *ownerRef.Controller {

					dsKey := fmt.Sprintf("%s/%s", pod.Namespace, ownerRef.Name)

					// Only observe deletions for DaemonSets we actually manage
					// This prevents interfering with other DaemonSet controllers
					dsExpectations := reconciler.expectations.GetExpectations(dsKey)
					if len(dsExpectations) > 0 {
						reconciler.expectations.Observe(dsKey, expectations.Delete, string(pod.UID))
						klog.Infof("Observed pod deletion for managed DaemonSet %s, pod: %s/%s", dsKey, pod.Namespace, pod.Name)
					}
					break // Found the controlling DaemonSet, no need to check other owners
				}
			}
			// Always return false to prevent enqueueing
			return false
		},
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}

	// Watch pods with a handler that never enqueues
	return c.Watch(source.Kind(mgr.GetCache(), &corev1.Pod{}),
		&handler.EnqueueRequestForObject{},
		podDeletePredicate)
}

// Reconcile reads that state of the cluster for a DaemonSet object and makes changes based on the annotations
// to implement progressive delivery by deleting pods according to batch requirements.
func (r *ReconcileNativeDaemonSet) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	dsKey := request.NamespacedName.String()

	daemon := new(appsv1.DaemonSet)
	err := r.Get(ctx, request.NamespacedName, daemon)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, clean up expectations and return.
			r.expectations.DeleteExpectations(dsKey)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Check for partition annotation
	partitionStr, _ := util.ParseDaemonSetAdvancedControl(daemon.Annotations)
	if partitionStr == "" {
		// No partition annotation, nothing to do
		return reconcile.Result{}, nil
	}

	// Check expectations first
	satisfied, timeoutDuration, rest := r.expectations.SatisfiedExpectations(dsKey)
	if !satisfied {
		if timeoutDuration >= expectations.ExpectationTimeout {
			klog.Warningf("Unsatisfied time of expectation exceeds %v, delete key and continue, key: %s, rest: %v",
				expectations.ExpectationTimeout, dsKey, rest)
			r.expectations.DeleteExpectations(dsKey)
		} else {
			klog.Infof("Expectations not satisfied, requeuing DaemonSet %s/%s, rest: %v, timeoutDuration: %v", daemon.Namespace, daemon.Name, rest, timeoutDuration)
			return reconcile.Result{RequeueAfter: DefaultRetryDuration}, nil
		}
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

	klog.Infof("Processing DaemonSet %s/%s with partition: %s, desired updated replicas: %d",
		daemon.Namespace, daemon.Name, partitionStr, desiredUpdatedReplicas)

	// Process the pods according to partition requirements
	result, err := r.processBatch(ctx, daemon, pods, desiredUpdatedReplicas)
	if err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

// analyzePods analyzes the current pod state and determines how many pods need to be deleted
// Returns: podsToDelete slice, needToDelete count, error
func (r *ReconcileNativeDaemonSet) analyzePods(pods []*corev1.Pod, updateRevision string, desiredUpdatedReplicas int32, daemon *appsv1.DaemonSet) ([]*corev1.Pod, int32, error) {
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
		klog.Infof("Batch completed: have %d updated pods >= %d desired", updatedPods, desiredUpdatedReplicas)
		return podsToDelete, 0, nil
	}

	// Calculate how many pods need to be deleted
	needToDelete := desiredUpdatedReplicas - updatedPods

	// Get maxUnavailable constraint
	maxUnavailable := daemonsetutil.GetMaxUnavailable(daemon)

	klog.Infof("Current status - updatedPods: %d, desired: %d, needToDelete: %d, available: %d",
		updatedPods, desiredUpdatedReplicas, needToDelete, len(podsToDelete))

	// Apply constraints
	needToDelete = daemonsetutil.ApplyDeletionConstraints(needToDelete, maxUnavailable, len(podsToDelete))

	return podsToDelete, needToDelete, nil
}

// executePodDeletion executes the actual pod deletion based on analysis using expectations mechanism
func (r *ReconcileNativeDaemonSet) executePodDeletion(ctx context.Context, podsToDelete []*corev1.Pod, needToDelete int32, daemon *appsv1.DaemonSet) error {
	dsKey := fmt.Sprintf("%s/%s", daemon.Namespace, daemon.Name)

	if needToDelete <= 0 {
		klog.Infof("No pods need to be deleted")
		return nil
	}

	// Sort pods by deletion priority (pending, not ready, high cost, newer pods first)
	daemonsetutil.SortPodsForDeletion(podsToDelete)

	// Limit the number of pods to delete based on needToDelete
	actualPodsToDelete := podsToDelete
	if int32(len(podsToDelete)) > needToDelete {
		actualPodsToDelete = podsToDelete[:needToDelete]
	}

	deleteDiff := len(actualPodsToDelete)

	// Set expectations for pod deletions BEFORE actually deleting
	for _, pod := range actualPodsToDelete {
		r.expectations.Expect(dsKey, expectations.Delete, string(pod.UID))
	}

	klog.Infof("Pods to delete for DaemonSet %s/%s, count: %d", daemon.Namespace, daemon.Name, deleteDiff)

	// Use SlowStartBatch for better performance and error handling
	successCount, err := daemonsetutil.SlowStartBatch(deleteDiff, 1, func(index int) error {
		pod := actualPodsToDelete[index]

		klog.Infof("About to delete pod %s/%s for DaemonSet %s/%s (currentRevision=%s)",
			pod.Namespace, pod.Name, daemon.Namespace, daemon.Name, pod.Labels[appsv1.ControllerRevisionHashLabelKey])

		if deleteErr := r.Delete(ctx, pod); deleteErr != nil {
			// On deletion failure, observe immediately to decrease expectation
			r.expectations.Observe(dsKey, expectations.Delete, string(pod.UID))
			if !errors.IsNotFound(deleteErr) {
				klog.Infof("Failed deletion, decremented expectations for DaemonSet %s/%s, pod: %s/%s", daemon.Namespace, daemon.Name, pod.Namespace, pod.Name)
				utilruntime.HandleError(deleteErr)
				return deleteErr
			} else {
				// NotFound means pod is already deleted, which is fine
				klog.Infof("Pod already deleted (NotFound): %s/%s", pod.Namespace, pod.Name)
			}
		} else {
			// On successful deletion, the expectation will be observed by the Pod Delete watch handler
			r.eventRecorder.Event(daemon, corev1.EventTypeNormal, "PodDeleted",
				fmt.Sprintf("Deleted pod %s/%s for batch update", pod.Namespace, pod.Name))
			klog.Infof("Successfully deleted pod %s/%s for DaemonSet %s/%s", pod.Namespace, pod.Name, daemon.Namespace, daemon.Name)
		}
		return nil
	})

	if err != nil {
		klog.Errorf("Failed to delete pods for DaemonSet %s/%s, successfully deleted: %d out of %d planned, error: %v",
			daemon.Namespace, daemon.Name, successCount, deleteDiff, err)
		return err
	}

	klog.Infof("Successfully deleted %d/%d pods for DaemonSet %s/%s", successCount, deleteDiff, daemon.Namespace, daemon.Name)

	return nil
}

// processBatch handles the actual pod deletion logic based on batch requirements
func (r *ReconcileNativeDaemonSet) processBatch(ctx context.Context, daemon *appsv1.DaemonSet, pods []*corev1.Pod, desiredReplicas int32) (reconcile.Result, error) {

	// Check if there are pods being deleted, if so, exit immediately
	if daemonsetutil.HasPodsBeingDeleted(pods) {
		klog.Infof("Pods are being deleted for DaemonSet %s/%s, requeue after %v", daemon.Namespace, daemon.Name, DefaultRetryDuration)
		return reconcile.Result{RequeueAfter: DefaultRetryDuration}, nil
	}

	// Analyze pods and determine what needs to be done
	_, batchRevision := util.ParseDaemonSetAdvancedControl(daemon.Annotations)
	podsToDelete, needToDelete, err := r.analyzePods(pods, batchRevision, desiredReplicas, daemon)
	if err != nil {
		return reconcile.Result{}, err
	}

	// If no pods need to be deleted, batch is completed
	if needToDelete == 0 {
		klog.Infof("Batch is completed for DaemonSet %s/%s, not requeueing", daemon.Namespace, daemon.Name)
		return reconcile.Result{}, nil
	}

	// Execute pod deletion based on analysis
	err = r.executePodDeletion(ctx, podsToDelete, needToDelete, daemon)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Only requeue if we actually did some work (deleted pods)
	if needToDelete > 0 {
		klog.Infof("Deleted %d pods for DaemonSet %s/%s, requeue after %v to check progress", needToDelete, daemon.Namespace, daemon.Name, DefaultRetryDuration)
		return reconcile.Result{RequeueAfter: DefaultRetryDuration}, nil
	}

	return reconcile.Result{}, nil
}
