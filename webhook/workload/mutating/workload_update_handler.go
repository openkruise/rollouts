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
	"reflect"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	admissionv1 "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
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
		if req.Kind.Kind != util.ControllerKruiseKindCS.Kind {
			return admission.Allowed("")
		}
		// check deployment
		newObj := &kruiseappsv1alpha1.CloneSet{}
		if err := h.Decoder.Decode(req, newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		copy := newObj.DeepCopy()
		oldObj := &kruiseappsv1alpha1.CloneSet{}
		if err := h.Decoder.Decode(
			admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
			oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if err := h.handlerCloneSet(newObj, oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if reflect.DeepEqual(newObj, copy) {
			return admission.Allowed("")
		}
		marshalled, err := json.Marshal(newObj)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		return admission.PatchResponseFromRaw(req.AdmissionRequest.Object.Raw, marshalled)
	// native k8s deloyment
	case apps.SchemeGroupVersion.Group:
		if req.Kind.Kind != util.ControllerKindDep.Kind {
			return admission.Allowed("")
		}
		// check deployment
		newObj := &apps.Deployment{}
		if err := h.Decoder.Decode(req, newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		copy := newObj.DeepCopy()
		oldObj := &apps.Deployment{}
		if err := h.Decoder.Decode(
			admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
			oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if err := h.handlerDeployment(newObj, oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if reflect.DeepEqual(newObj, copy) {
			return admission.Allowed("")
		}
		marshalled, err := json.Marshal(newObj)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		return admission.PatchResponseFromRaw(req.AdmissionRequest.Object.Raw, marshalled)
	}
	return admission.Allowed("")
}

func (h *WorkloadHandler) handlerDeployment(newObj, oldObj *apps.Deployment) error {
	// in rollout progressing
	if state, _ := util.GetRolloutState(newObj.Annotations); state != nil {
		// deployment paused=false is not allowed until the rollout is completed
		if newObj.Spec.Paused == false {
			newObj.Spec.Paused = true
			klog.Warningf("deployment(%s/%s) is in rollout(%s) progressing, and set paused=true", newObj.Namespace, newObj.Name, state.RolloutName)
		}
		return nil
	}

	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	if newObj.Spec.Replicas != nil && *newObj.Spec.Replicas == 0 {
		return nil
	}
	// 2. deployment.spec.strategy.type must be RollingUpdate
	if newObj.Spec.Strategy.Type == apps.RecreateDeploymentStrategyType {
		klog.Warningf("deployment(%s/%s) strategy type is 'Recreate', rollout will not work on it", newObj.Namespace, newObj.Name)
		return nil
	}
	// 3. deployment.spec.PodTemplate not change
	if util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return nil
	}
	// 4. the deployment must be in a stable version (only one version of rs)
	stableRs, err := h.Finder.GetDeploymentStableRs(newObj)
	if err != nil {
		return err
	} else if stableRs == nil {
		return nil
	}
	// 5. have matched rollout crd
	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return err
	} else if rollout == nil {
		return nil
	}
	klog.Infof("deployment(%s/%s) will be in rollout progressing, and set paused=true", newObj.Namespace, newObj.Name)
	// need set workload paused = true
	newObj.Spec.Paused = true
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	return nil
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
		if !rollout.DeletionTimestamp.IsZero() || rollout.Spec.ObjectRef.Type == appsv1alpha1.RevisionRefType ||
			rollout.Spec.ObjectRef.WorkloadRef == nil {
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

func (h *WorkloadHandler) handlerCloneSet(newObj, oldObj *kruiseappsv1alpha1.CloneSet) error {
	// in rollout progressing
	if state, _ := util.GetRolloutState(newObj.Annotations); state != nil {
		if newObj.Spec.UpdateStrategy.Paused == false {
			newObj.Spec.UpdateStrategy.Paused = true
			klog.Warningf("cloneSet(%s/%s) is in rollout(%s) progressing, and set paused=true", newObj.Namespace, newObj.Name, state.RolloutName)
		}
		return nil
	}

	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	if newObj.Spec.Replicas != nil && *newObj.Spec.Replicas == 0 {
		return nil
	}
	// 2. cloneSet.spec.PodTemplate is changed
	if util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return nil
	}
	// 3. the cloneSet must be in a stable version (only one version of pods)
	if newObj.Status.UpdatedReplicas != newObj.Status.Replicas {
		return nil
	}
	// 4. have matched rollout crd
	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return err
	} else if rollout == nil {
		return nil
	}
	klog.Infof("cloneSet(%s/%s) will be in rollout progressing, and paused", newObj.Namespace, newObj.Name)
	// need set workload paused = true
	newObj.Spec.UpdateStrategy.Paused = true
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	return nil
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
