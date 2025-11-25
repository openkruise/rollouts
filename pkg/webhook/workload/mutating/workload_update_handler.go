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
	"strings"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
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
	"k8s.io/apimachinery/pkg/util/intstr"
	admission2 "k8s.io/apiserver/pkg/admission"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
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

	meetingRules, err := h.checkWorkloadRules(ctx, req)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if !meetingRules {
		return admission.Allowed("")
	}

	// Because kruise Rollout is a bypassed approach, needs to be determined in the webhook if the workload meet to enter the rollout progressing:
	// 1. Traffic Routing, all the following conditions must be met
	//   a. PodTemplateSpec is changed
	//   b. Workload must only contain one version of Pods
	// 2. No Traffic Routing, Only Release in batches
	//   a. No RolloutId
	//    - PodTemplateSpec is changed
	//   b. Configure RolloutId
	//    - RolloutId and PodTemplateSpec change, enter the rollout progressing.
	//    - RolloutId changes and PodTemplateSpec no change, enter the rollout progressing
	//    - RolloutId no change and PodTemplateSpec change, do not enter the rollout progressing

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
			newObjClone := newObj.DeepCopy()
			oldObj := &kruiseappsv1alpha1.CloneSet{}
			if err := h.Decoder.Decode(
				admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
				oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			changed, err := h.handleCloneSet(newObjClone, oldObj)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if !changed {
				return admission.Allowed("")
			}
			marshalled, err := json.Marshal(newObjClone)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			original, err := json.Marshal(newObj)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			return admission.PatchResponseFromRaw(original, marshalled)
		case util.ControllerKruiseKindDS.Kind:
			// check daemonset
			newObj := &kruiseappsv1alpha1.DaemonSet{}
			if err := h.Decoder.Decode(req, newObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			newObjClone := newObj.DeepCopy()
			oldObj := &kruiseappsv1alpha1.DaemonSet{}
			if err := h.Decoder.Decode(
				admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
				oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			changed, err := h.handleDaemonSet(newObjClone, oldObj)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if !changed {
				return admission.Allowed("")
			}
			marshalled, err := json.Marshal(newObjClone)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			original, err := json.Marshal(newObj)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			return admission.PatchResponseFromRaw(original, marshalled)
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
			newObjClone := newObj.DeepCopy()
			oldObj := &apps.Deployment{}
			if err := h.Decoder.Decode(
				admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
				oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			changed, err := h.handleDeployment(newObjClone, oldObj)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if !changed {
				return admission.Allowed("")
			}
			marshalled, err := json.Marshal(newObjClone)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			original, err := json.Marshal(newObj)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			return admission.PatchResponseFromRaw(original, marshalled)
		}
	}

	return admission.Allowed("")
}

func (h *WorkloadHandler) handleDeployment(newObj, oldObj *apps.Deployment) (bool, error) {
	// if the resources is updated by CD tools(like OCM,Karmada), the uid of new object is empty
	if len(newObj.GetUID()) == 0 {
		newObj.SetUID(oldObj.GetUID())
	}

	// make sure matched Rollout CR always exists
	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.IsEmptyRelease() {
		return false, nil
	}

	// in rollout progressing
	if newObj.Annotations[util.InRolloutProgressingAnnotation] != "" {
		modified := false
		strategy := util.GetDeploymentStrategy(newObj)
		// partition
		if strings.EqualFold(string(strategy.RollingStyle), string(appsv1alpha1.PartitionRollingStyle)) {
			if !newObj.Spec.Paused {
				modified = true
				newObj.Spec.Paused = true
			}
			// Make sure it is always Recreate to disable native controller
			if newObj.Spec.Strategy.Type == apps.RollingUpdateDeploymentStrategyType {
				modified = true
				newObj.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
			}
			if newObj.Spec.Strategy.RollingUpdate != nil {
				modified = true
				// Allow to modify RollingUpdate config during rolling
				strategy.RollingUpdate = newObj.Spec.Strategy.RollingUpdate
				newObj.Spec.Strategy.RollingUpdate = nil
			}
			if isEffectiveDeploymentRevisionChange(oldObj, newObj) {
				modified = true
				strategy.Paused = true
			}
			appsv1alpha1.SetDefaultDeploymentStrategy(&strategy)
			setDeploymentStrategyAnnotation(strategy, newObj)
			// bluegreenStyle
		} else if len(newObj.GetAnnotations()[appsv1beta1.OriginalDeploymentStrategyAnnotation]) > 0 {
			if isEffectiveDeploymentRevisionChange(oldObj, newObj) {
				newObj.Spec.Paused, modified = true, true
				// disallow continuous release, allow rollback
				klog.Warningf("rollback or continuous release detected in Deployment webhook, while only rollback is allowed for bluegreen release for now")
			}
			// not allow to modify Strategy.Type to Recreate
			if newObj.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
				modified = true
				newObj.Spec.Strategy.Type = oldObj.Spec.Strategy.Type
				klog.Warningf("Not allow to modify Strategy.Type to Recreate")
			}
		} else { // default
			if !newObj.Spec.Paused {
				modified = true
				newObj.Spec.Paused = true
			}
			// Do not allow to modify strategy as Recreate during rolling
			if newObj.Spec.Strategy.Type == apps.RecreateDeploymentStrategyType {
				modified = true
				newObj.Spec.Strategy = oldObj.Spec.Strategy
				klog.Warningf("")
			}
		}
		return modified, nil
	}

	// indicate whether the workload can enter the rollout process
	// replicas > 0
	if newObj.Spec.Replicas != nil && *newObj.Spec.Replicas == 0 {
		return false, nil
	}
	if !isEffectiveDeploymentRevisionChange(oldObj, newObj) {
		return false, nil
	}

	rss, err := h.Finder.GetReplicaSetsForDeployment(newObj)
	if err != nil || len(rss) == 0 {
		klog.Warningf("Cannot find any activate replicaset for deployment %s/%s, no need to rolling", newObj.Namespace, newObj.Name)
		return false, nil
	}
	// if traffic routing, workload must only be one version of Pods
	if rollout.Spec.Strategy.HasTrafficRoutings() {
		if len(rss) != 1 {
			klog.Warningf("Because deployment(%s/%s) have multiple versions of Pods, so can not enter rollout progressing", newObj.Namespace, newObj.Name)
			return false, nil
		}
	}

	// label the stable version replicaset
	_, stableRS := util.FindCanaryAndStableReplicaSet(rss, newObj)
	if stableRS == nil {
		klog.Warningf("Cannot find any stable replicaset for deployment %s/%s", newObj.Namespace, newObj.Name)
	} else {
		if newObj.Labels == nil {
			newObj.Labels = map[string]string{}
		}
		// blueGreen also need the stable revision label
		newObj.Labels[appsv1alpha1.DeploymentStableRevisionLabel] = stableRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
	}

	// need set workload paused = true
	newObj.Spec.Paused = true
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	if newObj.Annotations == nil {
		newObj.Annotations = map[string]string{}
	}
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	klog.Infof("Deployment(%s/%s) will be released incrementally based on Rollout(%s)", newObj.Namespace, newObj.Name, rollout.Name)
	return true, nil
}

func (h *WorkloadHandler) handleCloneSet(newObj, oldObj *kruiseappsv1alpha1.CloneSet) (bool, error) {
	// indicate whether the workload can enter the rollout process
	// when cloneSet don't contain any pods, no need to enter rollout progressing
	if newObj.Spec.Replicas != nil && *newObj.Spec.Replicas == 0 {
		return false, nil
	}
	if newObj.Annotations[appsv1beta1.RolloutIDLabel] != "" &&
		oldObj.Annotations[appsv1beta1.RolloutIDLabel] == newObj.Annotations[appsv1beta1.RolloutIDLabel] {
		return false, nil
	} else if newObj.Annotations[appsv1beta1.RolloutIDLabel] == "" && util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return false, nil
	}

	// if the resources is updated by CD tools(like OCM,Karmada), the uid of new object is empty
	if len(newObj.GetUID()) == 0 {
		newObj.SetUID(oldObj.GetUID())
	}

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.IsEmptyRelease() {
		return false, nil
	}

	// if traffic routing, there must only be one version of Pods
	if rollout.Spec.Strategy.HasTrafficRoutings() && newObj.Status.Replicas != newObj.Status.UpdatedReplicas {
		klog.Warningf("Because cloneSet(%s/%s) have multiple versions of Pods, so can not enter rollout progressing", newObj.Namespace, newObj.Name)
		return false, nil
	}

	newObj.Spec.UpdateStrategy.Partition = &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	if newObj.Annotations == nil {
		newObj.Annotations = map[string]string{}
	}
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	klog.Infof("CloneSet(%s/%s) will be released incrementally based on Rollout(%s)", newObj.Namespace, newObj.Name, rollout.Name)
	return true, nil
}

func (h *WorkloadHandler) handleDaemonSet(newObj, oldObj *kruiseappsv1alpha1.DaemonSet) (bool, error) {
	// indicate whether the workload can enter the rollout process

	if newObj.Annotations[appsv1beta1.RolloutIDLabel] != "" &&
		oldObj.Annotations[appsv1beta1.RolloutIDLabel] == newObj.Annotations[appsv1beta1.RolloutIDLabel] {
		return false, nil
	} else if newObj.Annotations[appsv1beta1.RolloutIDLabel] == "" && util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return false, nil
	}

	// if the resources is updated by CD tools(like OCM,Karmada), the uid of new object is empty
	if len(newObj.GetUID()) == 0 {
		newObj.SetUID(oldObj.GetUID())
	}

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.IsEmptyRelease() {
		return false, nil
	}

	newObj.Spec.UpdateStrategy.RollingUpdate.Partition = pointer.Int32(math.MaxInt16)
	state := &util.RolloutState{RolloutName: rollout.Name}
	by, _ := json.Marshal(state)
	if newObj.Annotations == nil {
		newObj.Annotations = map[string]string{}
	}
	newObj.Annotations[util.InRolloutProgressingAnnotation] = string(by)
	klog.Infof("DaemonSet(%s/%s) will be released incrementally based on Rollout(%s)", newObj.Namespace, newObj.Name, rollout.Name)
	return true, nil
}

func (h *WorkloadHandler) fetchMatchedRollout(obj client.Object) (*appsv1beta1.Rollout, error) {
	oGv := obj.GetObjectKind().GroupVersionKind()
	rolloutList := &appsv1beta1.RolloutList{}
	if err := h.Client.List(context.TODO(), rolloutList, utilclient.DisableDeepCopy,
		&client.ListOptions{Namespace: obj.GetNamespace()}); err != nil {
		klog.Errorf("WorkloadHandler List rollout failed: %s", err.Error())
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

func isEffectiveDeploymentRevisionChange(oldObj, newObj *apps.Deployment) bool {
	if newObj.Annotations[appsv1beta1.RolloutIDLabel] != "" &&
		oldObj.Annotations[appsv1beta1.RolloutIDLabel] == newObj.Annotations[appsv1beta1.RolloutIDLabel] {
		return false
	} else if newObj.Annotations[appsv1beta1.RolloutIDLabel] == "" &&
		util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return false
	}
	return true
}

func setDeploymentStrategyAnnotation(strategy appsv1alpha1.DeploymentStrategy, d *apps.Deployment) {
	strategyAnno, _ := json.Marshal(&strategy)
	d.Annotations[appsv1alpha1.DeploymentStrategyAnnotation] = string(strategyAnno)
}

func (h *WorkloadHandler) checkWorkloadRules(ctx context.Context, req admission.Request) (bool, error) {
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

func constructAttr(req admission.Request) (admission2.Attributes, error) {
	obj := unstructured.Unstructured{}
	if err := json.Unmarshal(req.Object.Raw, &obj); err != nil {
		return nil, err
	}

	return admission2.NewAttributesRecord(&obj, nil, obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName(),
		schema.GroupVersionResource{
			Group:    req.Resource.Group,
			Version:  req.Resource.Version,
			Resource: req.Resource.Resource,
		}, req.SubResource, admission2.Operation(req.Operation), nil, *req.DryRun, nil), nil
}
