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

// Package advanceddeployment contains all the logic for handling Kubernetes Deployments.
// It implements a set of strategies (rolling, recreate) for deploying an application,
// the means to rollback to previous versions, proportional scaling for mitigating
// risk, cleanup policy, and other useful features of Deployments.
package advanceddeployment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/advanceddeployment/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// controllerKind contains the schema.GroupVersionKind for this DeploymentController type.
var controllerKind = apps.SchemeGroupVersion.WithKind("Deployment")

// DeploymentController is responsible for synchronizing Deployment objects stored
// in the system with actual running replica sets and pods.
type DeploymentController struct {
	client        client.Client
	eventRecorder record.EventRecorder
	strategy      v1alpha1.AdvancedDeploymentStrategy
}

func NewSyncer(cli client.Client, rec record.EventRecorder, strategy v1alpha1.AdvancedDeploymentStrategy) *DeploymentController {
	return &DeploymentController{
		client:        cli,
		eventRecorder: rec,
		strategy:      strategy,
	}
}

// getReplicaSetsForDeployment uses ControllerRefManager to reconcile
// ControllerRef by adopting and orphaning.
// It returns the list of ReplicaSets that this Deployment should manage.
func (dc *DeploymentController) getReplicaSetsForDeployment(d *apps.Deployment) ([]*apps.ReplicaSet, error) {
	// List all ReplicaSets to find those we own but that no longer match our
	// selector. They will be orphaned by ClaimReplicaSets().
	deploymentSelector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("deployment %s/%s has invalid label selector: %v", d.Namespace, d.Name, err)
	}
	rsLister := &apps.ReplicaSetList{}
	err = dc.client.List(context.TODO(), rsLister, &client.ListOptions{Namespace: d.Namespace, LabelSelector: deploymentSelector}, utilclient.DisableDeepCopy)
	if err != nil {
		return nil, err
	}
	rsList := make([]*apps.ReplicaSet, 0, len(rsLister.Items))
	for i := range rsLister.Items {
		rs := &rsLister.Items[i]
		if !rs.DeletionTimestamp.IsZero() {
			continue
		}
		if owner := metav1.GetControllerOfNoCopy(rs); owner == nil || owner.UID != d.UID {
			continue
		}
		rsList = append(rsList, rs)
	}
	return rsList, nil
}

// SyncDeployment will sync the deployment with the given key.
// This function is not meant to be invoked concurrently with the same key.
func (dc *DeploymentController) SyncDeployment(deployment *apps.Deployment) (bool, error) {
	startTime := time.Now()
	klog.V(4).InfoS("Started syncing deployment", "deployment", klog.KObj(deployment), "startTime", startTime)
	defer func() {
		klog.V(4).InfoS("Finished syncing deployment", "deployment", klog.KObj(deployment), "duration", time.Since(startTime))
	}()

	// Deep-copy otherwise we are mutating our cache.
	// TODO: Deep-copy only when needed.
	d := deployment.DeepCopy()

	// List ReplicaSets owned by this Deployment, while reconciling ControllerRef
	// through adoption/orphaning.
	rsList, err := dc.getReplicaSetsForDeployment(d)
	if err != nil {
		return false, err
	}

	if dc.strategy.Paused {
		return dc.sync(context.TODO(), d, rsList)
	}

	scalingEvent, err := dc.isScalingEvent(d, rsList)
	if err != nil {
		return false, err
	}
	if scalingEvent {
		return dc.sync(context.TODO(), d, rsList)
	}

	return dc.rolloutRolling(context.TODO(), d, rsList)
}

func (dc *DeploymentController) UpdateExtraStatus(deployment *apps.Deployment) error {
	rsList, err := dc.getReplicaSetsForDeployment(deployment)
	if err != nil {
		return err
	}
	newRS, _, err := dc.getAllReplicaSetsAndSyncRevision(deployment, rsList, false)
	if err != nil {
		return err
	}

	updatedReadyReplicas := int32(0)
	if newRS != nil {
		updatedReadyReplicas = newRS.Status.ReadyReplicas
	}

	extraStatus := &v1alpha1.AdvancedDeploymentStatus{
		ObservedGeneration:     deployment.Generation,
		UpdatedReadyReplicas:   updatedReadyReplicas,
		DesiredUpdatedReplicas: util.NewRSReplicasLimit(dc.strategy.Partition, deployment),
	}

	extraStatusByte, err := json.Marshal(extraStatus)
	if err != nil {
		klog.Errorf("Failed to marshal extra status for Deployment %v, err: %v", klog.KObj(deployment), err)
		return nil // no need to retry
	}

	extraStatusAnno := string(extraStatusByte)
	if deployment.Annotations[v1alpha1.AdvancedDeploymentStatusAnnotation] == extraStatusAnno {
		return nil
	}
	rev := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: deployment.Name, Namespace: deployment.Namespace}}
	extraStatusAnno = strings.Replace(extraStatusAnno, `"`, `\"`, -1)
	body := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`,
		v1alpha1.AdvancedDeploymentStatusAnnotation, extraStatusAnno))
	return dc.client.Patch(context.TODO(), rev, client.RawPatch(types.MergePatchType, body))
}
