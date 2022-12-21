/*
Copyright 2015 The Kubernetes Authors.

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

// Package deployment contains all the logic for handling Kubernetes Deployments.
// It implements a set of strategies (rolling, recreate) for deploying an application,
// the means to rollback to previous versions, proportional scaling for mitigating
// risk, cleanup policy, and other useful features of Deployments.
package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	deploymentutil "github.com/openkruise/rollouts/pkg/controller/deployment/util"
)

const (
	// maxRetries is the number of times a deployment will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a deployment is going to be requeued:
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
)

// controllerKind contains the schema.GroupVersionKind for this controller type.
var controllerKind = apps.SchemeGroupVersion.WithKind("Deployment")

// DeploymentController is responsible for synchronizing Deployment objects stored
// in the system with actual running replica sets and pods.
type DeploymentController struct {
	client clientset.Interface

	eventBroadcaster record.EventBroadcaster
	eventRecorder    record.EventRecorder

	// dLister can list/get deployments from the shared informer's store
	dLister appslisters.DeploymentLister
	// rsLister can list/get replica sets from the shared informer's store
	rsLister appslisters.ReplicaSetLister
	// podLister can list/get pods from the shared informer's store
	podLister corelisters.PodLister

	// we will use this strategy to replace spec.strategy of deployment
	strategy rolloutsv1alpha1.DeploymentStrategy
}

// getReplicaSetsForDeployment uses ControllerRefManager to reconcile
// ControllerRef by adopting and orphaning.
// It returns the list of ReplicaSets that this Deployment should manage.
func (dc *DeploymentController) getReplicaSetsForDeployment(ctx context.Context, d *apps.Deployment) ([]*apps.ReplicaSet, error) {
	deploymentSelector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("deployment %s/%s has invalid label selector: %v", d.Namespace, d.Name, err)
	}
	// List all ReplicaSets to find those we own but that no longer match our
	// selector. They will be orphaned by ClaimReplicaSets().
	return dc.rsLister.ReplicaSets(d.Namespace).List(deploymentSelector)
}

// syncDeployment will sync the deployment with the given key.
// This function is not meant to be invoked concurrently with the same key.
func (dc *DeploymentController) syncDeployment(ctx context.Context, deployment *apps.Deployment) (err error) {
	startTime := time.Now()
	klog.V(4).InfoS("Started syncing deployment", "deployment", klog.KObj(deployment), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing deployment", "deployment", klog.KObj(deployment), "duration", time.Since(startTime))
	}()

	// Deep-copy otherwise we are mutating our cache.
	// TODO: Deep-copy only when needed.
	d := deployment.DeepCopy()

	everything := metav1.LabelSelector{}
	if reflect.DeepEqual(d.Spec.Selector, &everything) {
		dc.eventRecorder.Eventf(d, v1.EventTypeWarning, "SelectingAll", "This deployment is selecting all pods. A non-empty selector is required.")
		if d.Status.ObservedGeneration < d.Generation {
			d.Status.ObservedGeneration = d.Generation
			dc.client.AppsV1().Deployments(d.Namespace).UpdateStatus(ctx, d, metav1.UpdateOptions{})
		}
		return
	}

	// List ReplicaSets owned by this Deployment, while reconciling ControllerRef
	// through adoption/orphaning.
	rsList, err := dc.getReplicaSetsForDeployment(ctx, d)
	if err != nil {
		return
	}

	if d.DeletionTimestamp != nil {
		return dc.syncStatusOnly(ctx, d, rsList)
	}

	defer func() {
		err = dc.updateExtraStatus(deployment, rsList)
	}()

	// Update deployment conditions with an Unknown condition when pausing/resuming
	// a deployment. In this way, we can be sure that we won't timeout when a user
	// resumes a Deployment with a set progressDeadlineSeconds.
	if err = dc.checkPausedConditions(ctx, d); err != nil {
		return
	}

	if d.Spec.Paused {
		err = dc.sync(ctx, d, rsList)
		return
	}

	scalingEvent, err := dc.isScalingEvent(ctx, d, rsList)
	if err != nil {
		return
	}

	if scalingEvent {
		err = dc.sync(ctx, d, rsList)
		return
	}

	err = dc.rolloutRolling(ctx, d, rsList)
	return
}

// updateExtraStatus will update extra status for advancedStatus
func (dc *DeploymentController) updateExtraStatus(deployment *apps.Deployment, rsList []*apps.ReplicaSet) error {
	newRS, _, err := dc.getAllReplicaSetsAndSyncRevision(context.TODO(), deployment, rsList, false)
	if err != nil {
		return err
	}

	updatedReadyReplicas := int32(0)
	if newRS != nil {
		updatedReadyReplicas = newRS.Status.ReadyReplicas
	}

	extraStatus := &rolloutsv1alpha1.DeploymentExtraStatus{
		ObservedGeneration:      deployment.Generation,
		UpdatedReadyReplicas:    updatedReadyReplicas,
		ExpectedUpdatedReplicas: deploymentutil.NewRSReplicasLimit(dc.strategy.Partition, deployment),
	}

	extraStatusByte, err := json.Marshal(extraStatus)
	if err != nil {
		klog.Errorf("Failed to marshal extra status for Deployment %v, err: %v", klog.KObj(deployment), err)
		return nil // no need to retry
	}

	extraStatusAnno := string(extraStatusByte)
	if deployment.Annotations[rolloutsv1alpha1.DeploymentExtraStatusAnnotation] == extraStatusAnno {
		return nil // no need to update
	}

	extraStatusAnno = strings.Replace(extraStatusAnno, `"`, `\"`, -1)
	body := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, rolloutsv1alpha1.DeploymentExtraStatusAnnotation, extraStatusAnno))
	_, err = dc.client.AppsV1().Deployments(deployment.Namespace).Patch(context.TODO(), deployment.Name, types.MergePatchType, body, metav1.PatchOptions{})
	return err
}
