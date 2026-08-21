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

package lws

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/canarystyle"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
)

type realController struct {
	realStableController
	realCanaryController
}

func NewController(cli client.Client, key types.NamespacedName) canarystyle.Interface {
	return &realController{
		realStableController: newStable(cli, key),
		realCanaryController: newCanary(cli, key),
	}
}

func (rc *realController) BuildStableController() (canarystyle.StableInterface, error) {
	if rc.stableObject != nil {
		return rc, nil
	}

	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(util.ControllerLWSKind)
	err := rc.stableClient.Get(context.TODO(), rc.stableKey, object)
	if err != nil {
		return rc, err
	}
	rc.stableObject = object
	rc.stableInfo = parseWorkload(object)
	return rc, nil
}

func (rc *realController) BuildCanaryController(release *v1beta1.BatchRelease) (canarystyle.CanaryInterface, error) {
	if rc.canaryObject != nil {
		return rc, nil
	}

	lwsList, err := rc.listLeaderWorkerSet(release, client.InNamespace(rc.stableKey.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return rc, err
	}

	template, err := rc.getLatestTemplate()
	if client.IgnoreNotFound(err) != nil {
		return rc, err
	}
	rc.canaryObject = filterCanaryLWS(release, lwsList, template)
	if rc.canaryObject == nil {
		return rc, control.GenerateNotFoundError(fmt.Sprintf("%v-canary", rc.stableKey), "LeaderWorkerSet")
	}

	rc.canaryInfo = parseWorkload(rc.canaryObject)
	return rc, nil
}

func (rc *realController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	if rolloutID != "" {
		if _, err := rc.ListOwnedPods(); err != nil {
			return nil, err
		}
	}

	replicas, _, _ := unstructured.NestedInt64(rc.stableObject.Object, "spec", "replicas")
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredUpdate := int32(control.CalculateBatchReplicas(release, int(replicas), int(currentBatch)))

	updatedReplicas, _, _ := unstructured.NestedInt64(rc.canaryObject.Object, "status", "replicas")
	updatedReadyReplicas, _, _ := unstructured.NestedInt64(rc.canaryObject.Object, "status", "readyReplicas")

	return &batchcontext.BatchContext{
		Pods:                   rc.canaryPods,
		RolloutID:              rolloutID,
		Replicas:               int32(replicas),
		UpdateRevision:         release.Status.UpdateRevision,
		CurrentBatch:           currentBatch,
		DesiredUpdatedReplicas: desiredUpdate,
		FailureThreshold:       release.Spec.ReleasePlan.FailureThreshold,
		UpdatedReplicas:        int32(updatedReplicas),
		UpdatedReadyReplicas:   int32(updatedReadyReplicas),
	}, nil
}

func (rc *realController) getLatestTemplate() (*corev1.PodTemplateSpec, error) {
	_, err := rc.BuildStableController()
	if err != nil {
		return nil, err
	}

	templateObj, found, _ := unstructured.NestedMap(rc.stableObject.Object, "spec", "template")
	if !found {
		return nil, fmt.Errorf("spec.template not found in LeaderWorkerSet")
	}

	podTemplate := &corev1.PodTemplateSpec{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(templateObj, podTemplate); err != nil {
		return nil, fmt.Errorf("failed to convert template: %w", err)
	}

	return podTemplate, nil
}

func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.canaryPods != nil {
		return rc.canaryPods, nil
	}
	var err error
	rc.canaryPods, err = util.ListOwnedPods(rc.canaryClient, rc.canaryObject)
	return rc.canaryPods, err
}

// parseWorkload extracts WorkloadInfo from an unstructured LeaderWorkerSet
func parseWorkload(lws *unstructured.Unstructured) *util.WorkloadInfo {
	// Use the existing ParseWorkload utility if it supports unstructured objects
	// Otherwise construct manually
	replicas, _, _ := unstructured.NestedInt64(lws.Object, "spec", "replicas")
	statusReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "replicas")
	readyReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "readyReplicas")
	updatedReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "updatedReplicas")
	updatedReadyReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "updatedReadyReplicas")
	availableReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "availableReplicas")
	observedGen, _, _ := unstructured.NestedInt64(lws.Object, "status", "observedGeneration")

	// Extract pod template for hash calculation
	templateObj, found, _ := unstructured.NestedMap(lws.Object, "spec", "template")
	var updateRevision string
	if found {
		podTemplate := &corev1.PodTemplateSpec{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(templateObj, podTemplate); err == nil {
			updateRevision = util.ComputeHash(podTemplate, nil)
		}
	}

	stableRevision, _, _ := unstructured.NestedString(lws.Object, "status", "stableRevision")

	return &util.WorkloadInfo{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lws.GetAPIVersion(),
			Kind:       lws.GetKind(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        lws.GetName(),
			Namespace:   lws.GetNamespace(),
			UID:         lws.GetUID(),
			Generation:  lws.GetGeneration(),
			Labels:      lws.GetLabels(),
			Annotations: lws.GetAnnotations(),
		},
		LogKey:   fmt.Sprintf("%s/%s (%s)", lws.GetNamespace(), lws.GetName(), lws.GroupVersionKind()),
		Replicas: int32(replicas),
		Status: util.WorkloadStatus{
			Replicas:             int32(statusReplicas),
			ReadyReplicas:        int32(readyReplicas),
			UpdatedReplicas:      int32(updatedReplicas),
			UpdatedReadyReplicas: int32(updatedReadyReplicas),
			AvailableReplicas:    int32(availableReplicas),
			ObservedGeneration:   observedGen,
			UpdateRevision:       updateRevision,
			StableRevision:       stableRevision,
		},
	}
}
