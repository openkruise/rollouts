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
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	admissionv1 "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"net/http"
	"reflect"

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
func (h *WorkloadHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// if subResources, then ignore
	if req.Operation != admissionv1.Update || req.SubResource != "" {
		return admission.Allowed("")
	}

	switch req.Kind.Group {
	// kruise workload
	case kruiseappsv1alpha1.GroupVersion.Group:
		if req.Kind.Kind != util.ControllerKruiseKindCS.Kind {
			return admission.Allowed("")
		}
		// todo
	// native k8s deloyment
	case apps.SchemeGroupVersion.Group:
		if req.Kind.Kind != util.ControllerKindDep.Kind {
			return admission.Allowed("")
		}
		// only check deployment
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
		if !state.RolloutDone && newObj.Spec.Paused == false {
			newObj.Spec.Paused = true
			klog.Warningf("deployment(%s/%s) is in rollout(%s) progressing, and set paused=true", newObj.Namespace, newObj.Name, state.RolloutName)
		}
		return nil
	}

	rollout, err := h.isCanInRolloutProgressing(newObj, oldObj)
	if err != nil {
		return err
	} else if rollout == nil {
		return nil
	}
	klog.Infof("deployment(%s/%s) will be in rollout progressing, and paused", newObj.Namespace, newObj.Name)
	// need set workload paused = true
	newObj.Spec.Paused = true
	state := &util.RolloutState{RolloutDone: false, RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	return nil
}

func (h *WorkloadHandler) isCanInRolloutProgressing(new, old *apps.Deployment) (*appsv1alpha1.Rollout, error) {
	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	if new.Spec.Replicas != nil && *new.Spec.Replicas == 0 {
		return nil, nil
	}
	// 2. deployment.spec.PodTemplate is changed
	if util.EqualIgnoreHash(&old.Spec.Template, &new.Spec.Template) {
		return nil, nil
	}
	// 3. the deployment must be in a stable version (only one version of rs)
	stableRs, err := h.Finder.GetDeploymentStableRs(new)
	if err != nil || stableRs == nil {
		return nil, err
	}
	// 4. have matched rollout crd
	return h.fetchMatchedRollout(new)
}

func (h *WorkloadHandler) fetchMatchedRollout(obj *apps.Deployment) (*appsv1alpha1.Rollout, error) {
	oGv, err := schema.ParseGroupVersion(obj.APIVersion)
	if err != nil {
		klog.Warningf("ParseGroupVersion deployment(%s/%s) failed: %s", obj.Namespace, obj.Name, err.Error())
		return nil, nil
	}

	rolloutList := &appsv1alpha1.RolloutList{}
	if err := h.Client.List(context.TODO(), rolloutList, &client.ListOptions{Namespace: obj.Namespace}); err != nil {
		klog.Errorf("WorkloadHandler List rollout failed: %s", err.Error())
		return nil, err
	}
	for i := range rolloutList.Items {
		rollout := &rolloutList.Items[i]
		if rollout.Spec.ObjectRef.Type == appsv1alpha1.RevisionRefType || rollout.Spec.ObjectRef.WorkloadRef == nil {
			continue
		}
		ref := rollout.Spec.ObjectRef.WorkloadRef
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			klog.Warningf("ParseGroupVersion rollout(%s/%s) ref failed: %s", rollout.Namespace, rollout.Name, err.Error())
			continue
		}
		if oGv.Group == gv.Group && obj.Kind == ref.Kind && obj.Name == ref.Name {
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
