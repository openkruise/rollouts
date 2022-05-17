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

package mutating

import (
	"context"
	"encoding/json"
	"net/http"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	admissionv1 "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// WorkloadHandler handles Pod
type WorkloadHandler struct {
	// To use the client, you need to do the following:
	// - uncomment it
	// - import sigs.k8s.io/controller-runtime/pkg/client
	// - uncomment the InjectClient method at the bottom of this file.
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
	Finder  *util.ControllerFinder
}

var _ admission.Handler = &WorkloadHandler{}

// Handle handles admission requests.
//  TODO
//  Currently there is an implicit condition for rollout: the workload must be currently in a stable version (only one version of Pods),
//  if not, it will not enter the rollout process. There is an additional problem here, the user may not be aware of this.
//  when user does a release and thinks it enters the rollout process, but due to the implicit condition above,
//  it actually goes through the normal release process. No good idea to solve this problem has been found yet.
func (h *WorkloadHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// if subResources, then ignore
	if req.Operation != admissionv1.Update || req.SubResource != "" {
		return admission.Allowed("")
	}

	switch req.Kind.Group {
	// kruise cloneSet
	case kruiseappsv1alpha1.GroupVersion.Group:
		switch req.Kind.Kind {
		case util.ControllerKruiseKindCS.Kind:
			// check cloneset
			newObj := &kruiseappsv1alpha1.CloneSet{}
			if err := h.Decoder.Decode(req, newObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			oldObj := &kruiseappsv1alpha1.CloneSet{}
			if err := h.Decoder.Decode(
				admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
				oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			changed, err := h.handleCloneSet(newObj, oldObj)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if !changed {
				return admission.Allowed("")
			}
			marshalled, err := json.Marshal(newObj)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			return admission.PatchResponseFromRaw(req.AdmissionRequest.Object.Raw, marshalled)
		}
	// native k8s deloyment
	case apps.SchemeGroupVersion.Group:
		switch req.Kind.Kind {
		case util.ControllerKindDep.Kind:
			// check deployment
			newObj := &apps.Deployment{}
			if err := h.Decoder.Decode(req, newObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			oldObj := &apps.Deployment{}
			if err := h.Decoder.Decode(
				admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
				oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			changed, err := h.handleDeployment(newObj, oldObj)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if !changed {
				return admission.Allowed("")
			}
			marshalled, err := json.Marshal(newObj)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			return admission.PatchResponseFromRaw(req.AdmissionRequest.Object.Raw, marshalled)
		}
	}

	newObj := &unstructured.Unstructured{}
	newObj.SetGroupVersionKind(schema.GroupVersionKind{Group: req.Kind.Group, Version: req.Kind.Version, Kind: req.Kind.Kind})
	if err := h.Decoder.Decode(req, newObj); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	oldObj := &unstructured.Unstructured{}
	oldObj.SetGroupVersionKind(schema.GroupVersionKind{Group: req.Kind.Group, Version: req.Kind.Version, Kind: req.Kind.Kind})
	if err := h.Decoder.Decode(
		admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
		oldObj); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	changed, err := h.handleStatefulSetLikeWorkload(newObj, oldObj)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if !changed {
		return admission.Allowed("")
	}
	marshalled, err := json.Marshal(newObj.Object)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.AdmissionRequest.Object.Raw, marshalled)
}

func (h *WorkloadHandler) handleStatefulSetLikeWorkload(newObj, oldObj *unstructured.Unstructured) (changed bool, err error) {
	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	replicas := util.ParseReplicasFrom(newObj)
	if replicas == 0 {
		return false, nil
	}
	oldTemplate := util.ParsePodTemplate(oldObj)
	if oldTemplate == nil {
		return false, nil
	}
	newTemplate := util.ParsePodTemplate(newObj)
	if newTemplate == nil {
		return false, nil
	}

	// 2. statefulset.spec.PodTemplate is changed
	if util.EqualIgnoreHash(oldTemplate, newTemplate) {
		return
	}
	// 3. have matched rollout crd
	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return
	} else if rollout == nil {
		return
	}

	klog.Infof("StatefulSet-Like Workload(%s/%s) will be in rollout progressing, and paused", newObj.GetNamespace(), newObj.GetName())
	if !util.IsRollingUpdateStrategy(newObj) {
		return
	}

	changed = true
	util.SetStatefulSetPartition(newObj, replicas)
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	annotation := newObj.GetAnnotations()
	if annotation == nil {
		annotation = map[string]string{}
	}
	annotation[util.InRolloutProgressingAnnotation] = string(by)
	newObj.SetAnnotations(annotation)
	return
}

func (h *WorkloadHandler) handleDeployment(newObj, oldObj *apps.Deployment) (changed bool, err error) {
	// in rollout progressing
	if state, _ := util.GetRolloutState(newObj.Annotations); state != nil {
		// deployment paused=false is not allowed until the rollout is completed
		if newObj.Spec.Paused == false {
			changed = true
			newObj.Spec.Paused = true
			klog.Warningf("deployment(%s/%s) is in rollout(%s) progressing, and set paused=true", newObj.Namespace, newObj.Name, state.RolloutName)
		}
		return
	}

	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	if newObj.Spec.Replicas != nil && *newObj.Spec.Replicas == 0 {
		return
	}
	// 2. deployment.spec.strategy.type must be RollingUpdate
	if newObj.Spec.Strategy.Type == apps.RecreateDeploymentStrategyType {
		klog.Warningf("deployment(%s/%s) strategy type is 'Recreate', rollout will not work on it", newObj.Namespace, newObj.Name)
		return
	}
	// 3. deployment.spec.PodTemplate not change
	if util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return
	}
	// 4. the deployment must be in a stable version (only one version of rs)
	rss, err := h.Finder.GetReplicaSetsForDeployment(newObj)
	if err != nil {
		return
	} else if len(rss) != 1 {
		klog.Warningf("deployment(%s/%s) contains len(%d) replicaSet, can't in rollout progressing", newObj.Namespace, newObj.Name, len(rss))
		return
	}
	// 5. have matched rollout crd
	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return
	} else if rollout == nil {
		return
	}
	klog.Infof("deployment(%s/%s) will be in rollout progressing, and set paused=true", newObj.Namespace, newObj.Name)

	changed = true
	// need set workload paused = true
	newObj.Spec.Paused = true
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	if newObj.Annotations == nil {
		newObj.Annotations = map[string]string{}
	}
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	return
}

func (h *WorkloadHandler) handleCloneSet(newObj, oldObj *kruiseappsv1alpha1.CloneSet) (changed bool, err error) {
	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	if newObj.Spec.Replicas != nil && *newObj.Spec.Replicas == 0 {
		return
	}
	// 2. cloneSet.spec.PodTemplate is changed
	if util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return
	}
	// 3. have matched rollout crd
	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return
	} else if rollout == nil {
		return
	}

	klog.Infof("cloneSet(%s/%s) will be in rollout progressing, and paused", newObj.Namespace, newObj.Name)
	changed = true
	// need set workload paused = true
	newObj.Spec.UpdateStrategy.Paused = true
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	if newObj.Annotations == nil {
		newObj.Annotations = map[string]string{}
	}
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	return
}

func (h *WorkloadHandler) fetchMatchedRollout(obj client.Object) (*appsv1alpha1.Rollout, error) {
	oGv := obj.GetObjectKind().GroupVersionKind()
	rolloutList := &appsv1alpha1.RolloutList{}
	if err := h.Client.List(context.TODO(), rolloutList, &client.ListOptions{Namespace: obj.GetNamespace()}); err != nil {
		klog.Errorf("WorkloadHandler List rollout failed: %s", err.Error())
		return nil, err
	}
	for i := range rolloutList.Items {
		rollout := &rolloutList.Items[i]
		if !rollout.DeletionTimestamp.IsZero() || rollout.Spec.ObjectRef.WorkloadRef == nil {
			continue
		}
		ref := rollout.Spec.ObjectRef.WorkloadRef
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			klog.Warningf("ParseGroupVersion rollout(%s/%s) ref failed: %s", rollout.Namespace, rollout.Name, err.Error())
			continue
		}
		if oGv.Group == gv.Group && oGv.Kind == ref.Kind && obj.GetName() == ref.Name {
			return rollout, nil
		}
	}
	return nil, nil
}

var _ inject.Client = &WorkloadHandler{}

// InjectClient injects the client into the WorkloadHandler
func (h *WorkloadHandler) InjectClient(c client.Client) error {
	h.Client = c
	h.Finder = util.NewControllerFinder(c)
	return nil
}

var _ admission.DecoderInjector = &WorkloadHandler{}

// InjectDecoder injects the decoder into the WorkloadHandler
func (h *WorkloadHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}
