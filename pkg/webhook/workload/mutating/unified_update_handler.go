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
	"math"
	"net/http"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	util2 "github.com/openkruise/rollouts/pkg/webhook/util"
	"github.com/openkruise/rollouts/pkg/webhook/util/configuration"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/admissionregistration/v1"
	apps "k8s.io/api/apps/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	labels2 "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// UnifiedWorkloadHandler handles Pod
type UnifiedWorkloadHandler struct {
	// To use the client, you need to do the following:
	// - uncomment it
	// - import sigs.k8s.io/controller-runtime/pkg/client
	// - uncomment the InjectClient method at the bottom of this file.
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
	Finder  *util.ControllerFinder
}

var _ admission.Handler = &UnifiedWorkloadHandler{}

// Handle handles admission requests.
func (h *UnifiedWorkloadHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// if subResources, then ignore
	if req.Operation != admissionv1.Update || req.SubResource != "" {
		return admission.Allowed("")
	}

	meetingRules, err := h.checkWorkloadRules(ctx, req)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if !meetingRules {
		return admission.Allowed("")
	}

	switch req.Kind.Group {
	// kruise cloneSet
	case kruiseappsv1alpha1.GroupVersion.Group:
		switch req.Kind.Kind {
		case util.ControllerKruiseKindCS.Kind, util.ControllerKruiseKindDS.Kind:
			return admission.Allowed("")
		}
	// native k8s deloyment
	case apps.SchemeGroupVersion.Group:
		switch req.Kind.Kind {
		case util.ControllerKindDep.Kind:
			return admission.Allowed("")
		}
	}

	// handle other workload types, including native/advanced statefulset
	{
		newObj := &unstructured.Unstructured{}
		newObj.SetGroupVersionKind(schema.GroupVersionKind{Group: req.Kind.Group, Version: req.Kind.Version, Kind: req.Kind.Kind})
		if err := h.Decoder.Decode(req, newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if !util.IsWorkloadType(newObj, util.StatefulSetType) && req.Kind.Kind != util.ControllerKindSts.Kind {
			return admission.Allowed("")
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
}

func (h *UnifiedWorkloadHandler) handleStatefulSetLikeWorkload(newObj, oldObj *unstructured.Unstructured) (bool, error) {
	// indicate whether the workload can enter the rollout process
	// 1. replicas > 0
	if util.GetReplicas(newObj) == 0 || !util.IsStatefulSetRollingUpdate(newObj) {
		return false, nil
	}
	oldTemplate, newTemplate := util.GetTemplate(oldObj), util.GetTemplate(newObj)
	if oldTemplate == nil || newTemplate == nil {
		return false, nil
	}
	oldMetadata, newMetadata := util.GetMetadata(oldObj), util.GetMetadata(newObj)
	if newMetadata.Annotations[appsv1beta1.RolloutIDLabel] != "" &&
		oldMetadata.Annotations[appsv1beta1.RolloutIDLabel] == newMetadata.Annotations[appsv1beta1.RolloutIDLabel] {
		return false, nil
	} else if newMetadata.Annotations[appsv1beta1.RolloutIDLabel] == "" && util.EqualIgnoreHash(oldTemplate, newTemplate) {
		return false, nil
	}

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.IsEmptyRelease() {
		return false, nil
	}

	util.SetStatefulSetPartition(newObj, math.MaxInt16)
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	annotation := newObj.GetAnnotations()
	if annotation == nil {
		annotation = map[string]string{}
	}
	annotation[util.InRolloutProgressingAnnotation] = string(by)
	newObj.SetAnnotations(annotation)
	klog.Infof("StatefulSet(%s/%s) will be released incrementally based on Rollout(%s)", newMetadata.Namespace, newMetadata.Name, rollout.Name)
	return true, nil
}

func (h *UnifiedWorkloadHandler) fetchMatchedRollout(obj client.Object) (*appsv1beta1.Rollout, error) {
	oGv := obj.GetObjectKind().GroupVersionKind()
	rolloutList := &appsv1beta1.RolloutList{}
	if err := h.Client.List(context.TODO(), rolloutList, utilclient.DisableDeepCopy,
		&client.ListOptions{Namespace: obj.GetNamespace()}); err != nil {
		klog.Errorf("UnifiedWorkloadHandler List rollout failed: %s", err.Error())
		return nil, err
	}
	for i := range rolloutList.Items {
		rollout := &rolloutList.Items[i]
		if !rollout.DeletionTimestamp.IsZero() {
			continue
		}
		if rollout.Status.Phase == appsv1beta1.RolloutPhaseDisabled {
			klog.Infof("Disabled rollout(%s/%s) fetched when fetching matched rollout", rollout.Namespace, rollout.Name)
			continue
		}
		ref := rollout.Spec.WorkloadRef
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

var _ inject.Client = &UnifiedWorkloadHandler{}

// InjectClient injects the client into the UnifiedWorkloadHandler
func (h *UnifiedWorkloadHandler) InjectClient(c client.Client) error {
	h.Client = c
	h.Finder = util.NewControllerFinder(c)
	return nil
}

var _ admission.DecoderInjector = &UnifiedWorkloadHandler{}

// InjectDecoder injects the decoder into the UnifiedWorkloadHandler
func (h *UnifiedWorkloadHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}

func (h *UnifiedWorkloadHandler) checkWorkloadRules(ctx context.Context, req admission.Request) (bool, error) {
	webhook := &v1.MutatingWebhookConfiguration{}
	if err := h.Client.Get(ctx, types.NamespacedName{Name: configuration.MutatingWebhookConfigurationName}, webhook); err != nil {
		return false, err
	}

	newObject := unstructured.Unstructured{}
	if err := h.Decoder.Decode(req, &newObject); err != nil {
		return false, err
	}

	labels := newObject.GetLabels()

	attr, err := constructAttr(req)
	if err != nil {
		return false, err
	}

	for _, webhook := range webhook.Webhooks {
		for _, rule := range webhook.Rules {
			m := util2.Matcher{Rule: rule, Attr: attr}
			if m.Matches() {
				selector, err := v12.LabelSelectorAsSelector(webhook.ObjectSelector)
				if err != nil {
					return false, nil
				}
				if selector.Matches(labels2.Set(labels)) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
