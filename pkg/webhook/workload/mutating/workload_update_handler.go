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
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	admissionv1 "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
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
		case util.ControllerKruiseKindDS.Kind:
			// check daemonset
			newObj := &kruiseappsv1alpha1.DaemonSet{}
			if err := h.Decoder.Decode(req, newObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			oldObj := &kruiseappsv1alpha1.DaemonSet{}
			if err := h.Decoder.Decode(
				admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Object: req.AdmissionRequest.OldObject}},
				oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			changed, err := h.handleDaemonSet(newObj, oldObj)
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

func (h *WorkloadHandler) handleStatefulSetLikeWorkload(newObj, oldObj *unstructured.Unstructured) (bool, error) {
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
	if newMetadata.Annotations[appsv1alpha1.RolloutIDLabel] != "" &&
		oldMetadata.Annotations[appsv1alpha1.RolloutIDLabel] == newMetadata.Annotations[appsv1alpha1.RolloutIDLabel] {
		return false, nil
	} else if newMetadata.Annotations[appsv1alpha1.RolloutIDLabel] == "" && util.EqualIgnoreHash(oldTemplate, newTemplate) {
		return false, nil
	}

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.Canary == nil {
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

func (h *WorkloadHandler) handleDeployment(newObj, oldObj *apps.Deployment) (bool, error) {
	// in rollout progressing
	if newObj.Annotations[util.InRolloutProgressingAnnotation] != "" {
		modified := false
		if !newObj.Spec.Paused {
			modified = true
			newObj.Spec.Paused = true
		}
		strategy := util.GetDeploymentStrategy(newObj)
		switch strings.ToLower(string(strategy.RollingStyle)) {
		case strings.ToLower(string(appsv1alpha1.PartitionRollingStyle)):
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
			setDeploymentStrategyAnnotation(strategy, newObj)
		default:
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

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.Canary == nil {
		return false, nil
	}
	rss, err := h.Finder.GetReplicaSetsForDeployment(newObj)
	if err != nil || len(rss) == 0 {
		klog.Warningf("Cannot find any activate replicaset for deployment %s/%s, no need to rolling", newObj.Namespace, newObj.Name)
		return false, nil
	}
	// if traffic routing, workload must only be one version of Pods
	if len(rollout.Spec.Strategy.Canary.TrafficRoutings) > 0 {
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
	if newObj.Annotations[appsv1alpha1.RolloutIDLabel] != "" &&
		oldObj.Annotations[appsv1alpha1.RolloutIDLabel] == newObj.Annotations[appsv1alpha1.RolloutIDLabel] {
		return false, nil
	} else if newObj.Annotations[appsv1alpha1.RolloutIDLabel] == "" && util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return false, nil
	}

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.Canary == nil {
		return false, nil
	}
	// if traffic routing, there must only be one version of Pods
	if len(rollout.Spec.Strategy.Canary.TrafficRoutings) > 0 && newObj.Status.Replicas != newObj.Status.UpdatedReplicas {
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

	if newObj.Annotations[appsv1alpha1.RolloutIDLabel] != "" &&
		oldObj.Annotations[appsv1alpha1.RolloutIDLabel] == newObj.Annotations[appsv1alpha1.RolloutIDLabel] {
		return false, nil
	} else if newObj.Annotations[appsv1alpha1.RolloutIDLabel] == "" && util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return false, nil
	}

	rollout, err := h.fetchMatchedRollout(newObj)
	if err != nil {
		return false, err
	} else if rollout == nil || rollout.Spec.Strategy.Canary == nil {
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

func (h *WorkloadHandler) fetchMatchedRollout(obj client.Object) (*appsv1alpha1.Rollout, error) {
	oGv := obj.GetObjectKind().GroupVersionKind()
	rolloutList := &appsv1alpha1.RolloutList{}
	if err := h.Client.List(context.TODO(), rolloutList, utilclient.DisableDeepCopy,
		&client.ListOptions{Namespace: obj.GetNamespace()}); err != nil {
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

func isEffectiveDeploymentRevisionChange(oldObj, newObj *apps.Deployment) bool {
	if newObj.Annotations[appsv1alpha1.RolloutIDLabel] != "" &&
		oldObj.Annotations[appsv1alpha1.RolloutIDLabel] == newObj.Annotations[appsv1alpha1.RolloutIDLabel] {
		return false
	} else if newObj.Annotations[appsv1alpha1.RolloutIDLabel] == "" &&
		util.EqualIgnoreHash(&oldObj.Spec.Template, &newObj.Spec.Template) {
		return false
	}
	return true
}

func setDeploymentStrategyAnnotation(strategy appsv1alpha1.DeploymentStrategy, d *apps.Deployment) {
	strategyAnno, _ := json.Marshal(&strategy)
	d.Annotations[appsv1alpha1.DeploymentStrategyAnnotation] = string(strategyAnno)
}
