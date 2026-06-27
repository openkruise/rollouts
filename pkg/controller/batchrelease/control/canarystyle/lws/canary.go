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
	"encoding/json"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	expectations "github.com/openkruise/rollouts/pkg/util/expectation"
)

type realCanaryController struct {
	canaryInfo   *util.WorkloadInfo
	canaryObject *unstructured.Unstructured
	canaryClient client.Client
	objectKey    types.NamespacedName
	canaryPods   []*corev1.Pod
}

func newCanary(cli client.Client, key types.NamespacedName) realCanaryController {
	return realCanaryController{canaryClient: cli, objectKey: key}
}

func (r *realCanaryController) GetCanaryInfo() *util.WorkloadInfo {
	return r.canaryInfo
}

// Delete removes finalizers from canary LeaderWorkerSets
func (r *realCanaryController) Delete(release *v1beta1.BatchRelease) error {
	lwsList, err := r.listLeaderWorkerSet(release, client.InNamespace(r.objectKey.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return err
	}

	for _, lws := range lwsList {
		if !controllerutil.ContainsFinalizer(lws, util.CanaryDeploymentFinalizer) {
			continue
		}
		err = util.UpdateFinalizer(r.canaryClient, lws, util.RemoveFinalizerOpType, util.CanaryDeploymentFinalizer)
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		klog.Infof("Successfully remove finalizers for LeaderWorkerSet %v", klog.KObj(lws))
	}
	return nil
}

func (r *realCanaryController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	desired := ctx.DesiredUpdatedReplicas
	if r.canaryInfo.Replicas >= desired {
		return nil
	}

	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, desired)
	lws := &unstructured.Unstructured{}
	lws.SetGroupVersionKind(util.ControllerLWSKind)
	lws.SetName(r.canaryObject.GetName())
	lws.SetNamespace(r.canaryObject.GetNamespace())

	return r.canaryClient.Patch(context.TODO(), lws, client.RawPatch(types.StrategicMergePatchType, []byte(body)))
}

func (r *realCanaryController) Create(release *v1beta1.BatchRelease) error {
	if r.canaryObject != nil {
		return nil // Don't re-create if exists
	}

	// check expectation before creating canary LWS
	controllerKey := client.ObjectKeyFromObject(release).String()
	satisfied, timeoutDuration, rest := expectations.ResourceExpectations.SatisfiedExpectations(controllerKey)
	if !satisfied {
		if timeoutDuration >= expectations.ExpectationTimeout {
			klog.Warningf("Unsatisfied time of expectation exceeds %v, delete key and continue, key: %v, rest: %v",
				expectations.ExpectationTimeout, klog.KObj(release), rest)
			expectations.ResourceExpectations.DeleteExpectations(controllerKey)
		} else {
			return fmt.Errorf("expectation is not satisfied, key: %v, rest: %v", klog.KObj(release), rest)
		}
	}

	// fetch the stable LWS as template to create canary LWS
	stable := &unstructured.Unstructured{}
	stable.SetGroupVersionKind(util.ControllerLWSKind)
	if err := r.canaryClient.Get(context.TODO(), r.objectKey, stable); err != nil {
		return err
	}
	return r.create(release, stable)
}

func (r *realCanaryController) create(release *v1beta1.BatchRelease, template *unstructured.Unstructured) error {
	canary := template.DeepCopy()

	// Set metadata
	canary.SetGenerateName(fmt.Sprintf("%v-", r.objectKey.Name))
	canary.SetName("") // Clear name to use GenerateName
	canary.SetNamespace(r.objectKey.Namespace)
	canary.SetLabels(map[string]string{})
	canary.SetAnnotations(map[string]string{})

	// Add finalizer
	finalizers := []string{util.CanaryDeploymentFinalizer}
	canary.SetFinalizers(finalizers)

	// Set owner reference
	ownerRef := metav1.NewControllerRef(release, release.GroupVersionKind())
	canary.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})

	// Add labels
	labels := canary.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[util.CanaryDeploymentLabel] = template.GetName()
	canary.SetLabels(labels)

	// Add annotations
	ownerInfo, _ := json.Marshal(ownerRef)
	annotations := canary.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[util.BatchReleaseControlAnnotation] = string(ownerInfo)
	canary.SetAnnotations(annotations)

	// Copy spec and patch pod template metadata if needed
	if release.Spec.ReleasePlan.PatchPodTemplateMetadata != nil {
		patch := release.Spec.ReleasePlan.PatchPodTemplateMetadata
		templateObj, found, _ := unstructured.NestedMap(canary.Object, "spec", "template")
		if found {
			podTemplate := &corev1.PodTemplateSpec{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(templateObj, podTemplate); err == nil {
				// Apply label patches
				if podTemplate.Labels == nil {
					podTemplate.Labels = make(map[string]string)
				}
				for k, v := range patch.Labels {
					podTemplate.Labels[k] = v
				}
				// Apply annotation patches
				if podTemplate.Annotations == nil {
					podTemplate.Annotations = make(map[string]string)
				}
				for k, v := range patch.Annotations {
					podTemplate.Annotations[k] = v
				}
				// Convert back to unstructured
				updatedTemplate, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(podTemplate)
				unstructured.SetNestedMap(canary.Object, updatedTemplate, "spec", "template")
			}
		}
	}

	// Set initial replicas to 0
	unstructured.SetNestedField(canary.Object, int64(0), "spec", "replicas")

	// Remove paused if it exists
	unstructured.RemoveNestedField(canary.Object, "spec", "paused")

	if err := r.canaryClient.Create(context.TODO(), canary); err != nil {
		klog.Errorf("Failed to create canary LeaderWorkerSet(%v), error: %v", klog.KObj(canary), err)
		return err
	}

	// add expect to avoid to create repeatedly
	controllerKey := client.ObjectKeyFromObject(release).String()
	expectations.ResourceExpectations.Expect(controllerKey, expectations.Create, string(canary.GetUID()))

	canaryInfo, _ := json.Marshal(canary)
	klog.Infof("Create canary LeaderWorkerSet(%v) successfully, details: %s", klog.KObj(canary), string(canaryInfo))
	return fmt.Errorf("created canary LeaderWorkerSet %v succeeded, but waiting informer synced", klog.KObj(canary))
}

func (r *realCanaryController) listLeaderWorkerSet(release *v1beta1.BatchRelease, options ...client.ListOption) ([]*unstructured.Unstructured, error) {
	lwsList := &unstructured.UnstructuredList{}
	lwsList.SetGroupVersionKind(util.ControllerLWSKind.GroupVersion().WithKind("LeaderWorkerSetList"))

	if err := r.canaryClient.List(context.TODO(), lwsList, options...); err != nil {
		return nil, err
	}

	var lwsObjects []*unstructured.Unstructured
	for i := range lwsList.Items {
		lws := &lwsList.Items[i]
		// Only include LWS objects that match our GroupVersionKind
		if lws.GroupVersionKind() != util.ControllerLWSKind {
			continue
		}
		o := metav1.GetControllerOf(lws)
		if o == nil || o.UID != release.UID {
			continue
		}
		lwsObjects = append(lwsObjects, lws)
	}
	return lwsObjects, nil
}

// filterCanaryLWS returns the latest LeaderWorkerSet with matching template
func filterCanaryLWS(release *v1beta1.BatchRelease, lwsList []*unstructured.Unstructured, template *corev1.PodTemplateSpec) *unstructured.Unstructured {
	if len(lwsList) == 0 {
		return nil
	}

	sort.Slice(lwsList, func(i, j int) bool {
		return lwsList[i].GetCreationTimestamp().After(lwsList[j].GetCreationTimestamp().Time)
	})

	if template == nil {
		return lwsList[0]
	}

	var ignoreLabels, ignoreAnno []string
	if release.Spec.ReleasePlan.PatchPodTemplateMetadata != nil {
		patch := release.Spec.ReleasePlan.PatchPodTemplateMetadata
		for k := range patch.Labels {
			ignoreLabels = append(ignoreLabels, k)
		}
		for k := range patch.Annotations {
			ignoreAnno = append(ignoreAnno, k)
		}
	}

	for _, lws := range lwsList {
		templateObj, found, _ := unstructured.NestedMap(lws.Object, "spec", "template")
		if !found {
			continue
		}
		lwsTemplate := &corev1.PodTemplateSpec{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(templateObj, lwsTemplate); err != nil {
			continue
		}
		if util.EqualIgnoreSpecifyMetadata(template, lwsTemplate, ignoreLabels, ignoreAnno) {
			return lws
		}
	}
	return nil
}
