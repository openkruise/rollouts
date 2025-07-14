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
	"time"

	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	deploymentutil "github.com/openkruise/rollouts/pkg/controller/deployment/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	eventBroadcaster record.EventBroadcaster
	eventRecorder    record.EventRecorder

	// we will use this strategy to replace spec.strategy of deployment
	strategy rolloutsv1alpha1.DeploymentStrategy

	runtimeClient client.Client
}

// getReplicaSetsForDeployment uses ControllerRefManager to reconcile
// ControllerRef by adopting and orphaning.
// It returns the list of ReplicaSets that this Deployment should manage.
func (dc *DeploymentController) getReplicaSetsForDeployment(ctx context.Context, d *apps.Deployment) ([]*apps.ReplicaSet, error) {
	deploymentSelector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("deployment %s/%s has invalid label selector: %v", d.Namespace, d.Name, err)
	}

	// List all ReplicaSets using runtimeClient
	rsList := &apps.ReplicaSetList{}
	err = dc.runtimeClient.List(ctx, rsList, client.InNamespace(d.Namespace), client.MatchingLabelsSelector{Selector: deploymentSelector}, utilclient.DisableDeepCopy)
	if err != nil {
		return nil, fmt.Errorf("list %s/%s rs failed:%v", d.Namespace, d.Name, err)
	}

	// select rs owner by current deployment
	ownedRSs := make([]*apps.ReplicaSet, 0)
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !rs.DeletionTimestamp.IsZero() {
			continue
		}

		if metav1.IsControlledBy(rs, d) {
			ownedRSs = append(ownedRSs, rs)
		}
	}
	return ownedRSs, nil
}

// syncDeployment will sync the deployment with the given key.
// This function is not meant to be invoked concurrently with the same key.
func (dc *DeploymentController) syncDeployment(ctx context.Context, deployment *apps.Deployment) error {
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
			err := dc.runtimeClient.Status().Update(ctx, d)
			if err != nil {
				klog.Errorf("Failed to update deployment status: %v", err)
			}
		}
		return nil
	}

	// List ReplicaSets owned by this Deployment, while reconciling ControllerRef
	// through adoption/orphaning.
	rsList, err := dc.getReplicaSetsForDeployment(ctx, d)
	if err != nil {
		return err
	}

	if d.DeletionTimestamp != nil {
		return dc.syncStatusOnly(ctx, d, rsList)
	}

	if dc.strategy.Paused {
		return dc.sync(ctx, d, rsList)
	}

	scalingEvent, err := dc.isScalingEvent(ctx, d, rsList)
	if err != nil {
		return err
	}

	if scalingEvent {
		return dc.sync(ctx, d, rsList)
	}

	return dc.rolloutRolling(ctx, d, rsList)
}

// patchExtraStatus will update extra status for advancedStatus
func (dc *DeploymentController) patchExtraStatus(deployment *apps.Deployment) error {
	// It is necessary to fetch the latest Deployment here because previous steps in the reconcile loop
	// may update the condition fields (such as lastTransitionTime and lastUpdateTime), causing the
	// resourceVersion to change. This can lead to patch failures if we do not use the latest object.
	// The deployment passed in here has an old resourceVersion, so we need to fetch the latest deployment
	// to ensure patch success.
	latestDeployment := &apps.Deployment{}
	err := dc.runtimeClient.Get(context.TODO(), client.ObjectKeyFromObject(deployment), latestDeployment)
	if err != nil {
		klog.Errorf("Failed to get deployment: %v", err)
		return err
	}
	rsList, err := dc.getReplicaSetsForDeployment(context.TODO(), latestDeployment)
	if err != nil {
		return err
	}

	updatedReadyReplicas := int32(0)
	newRS := deploymentutil.FindNewReplicaSet(latestDeployment, rsList)
	if newRS != nil {
		updatedReadyReplicas = newRS.Status.ReadyReplicas
	}

	extraStatus := &rolloutsv1alpha1.DeploymentExtraStatus{
		UpdatedReadyReplicas:    updatedReadyReplicas,
		ExpectedUpdatedReplicas: deploymentutil.NewRSReplicasLimit(dc.strategy.Partition, latestDeployment),
	}

	extraStatusByte, err := json.Marshal(extraStatus)
	if err != nil {
		klog.Errorf("Failed to marshal extra status for Deployment %v, err: %v", klog.KObj(latestDeployment), err)
		return nil // no need to retry
	}

	extraStatusAnno := string(extraStatusByte)
	if latestDeployment.Annotations[rolloutsv1alpha1.DeploymentExtraStatusAnnotation] == extraStatusAnno {
		return nil // no need to update
	}
	deploymentCopy := latestDeployment.DeepCopy()
	deploymentCopy.Annotations[rolloutsv1alpha1.DeploymentExtraStatusAnnotation] = extraStatusAnno

	patch := client.MergeFromWithOptions(latestDeployment, client.MergeFromWithOptimisticLock{})
	err = dc.runtimeClient.Patch(context.TODO(), deploymentCopy, patch)
	if err != nil {
		klog.Errorf("Failed to patch deployment extra status: %v", err)
		return err
	}
	return err
}
