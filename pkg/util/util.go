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

package util

import (
	"encoding/json"
	"strconv"
	"time"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	// InRolloutProgressingAnnotation marks workload as entering the rollout progressing process
	//and does not allow paused=false during this process
	InRolloutProgressingAnnotation = "rollouts.kruise.io/in-progressing"
	// finalizer
	KruiseRolloutFinalizer = "rollouts.kruise.io/rollout"
	// rollout spec hash
	RolloutHashAnnotation                  = "rollouts.kruise.io/hash"
	DisableQuicklyRollbackPolicyAnnotation = "rollouts.kruise.io/disable-quick-rollback"
	WorkloadRollingUpdateAnnotation        = "rollouts.kruise.io/rolling-update-info"
	RolloutBatchIDLabel                    = "apps.kruise.io/rollout-batch-id"
	RolloutIDLabel                         = "apps.kruise.io/rollout-id"
)

// RolloutState is annotation[rollouts.kruise.io/in-progressing] value
type RolloutState struct {
	RolloutName string `json:"rolloutName"`
}

func GetRolloutState(annotations map[string]string) (*RolloutState, error) {
	value, ok := annotations[InRolloutProgressingAnnotation]
	if !ok || value == "" {
		return nil, nil
	}
	var obj *RolloutState
	err := json.Unmarshal([]byte(value), &obj)
	return obj, err
}

func DisableQuicklyRollbackPolicy(workloadRef *rolloutv1alpha1.WorkloadRef, annotations map[string]string) bool {
	if workloadRef.Kind == ControllerKruiseKindCS.Kind {
		value, ok := annotations[DisableQuicklyRollbackPolicyAnnotation]
		if ok && value == "true" {
			return true
		}
	}
	return false
}

func BuildWorkloadWatcher(c controller.Controller, handler handler.EventHandler) error {
	if DiscoverGVK(CloneSetGVK) {
		// Watch changes to CloneSet
		err := c.Watch(&source.Kind{Type: &kruiseappsv1alpha1.CloneSet{}}, handler)
		if err != nil {
			return err
		}
	}

	// Watch changes to Deployment
	err := c.Watch(&source.Kind{Type: &apps.Deployment{}}, handler)
	if err != nil {
		return err
	}

	//// Watch changes to Advanced StatefulSet, use unstructured informer
	//if DiscoverGVK(AStatefulSetGVK) {
	//	objectType := &unstructured.Unstructured{}
	//	objectType.SetGroupVersionKind(kruiseappsv1beta1.SchemeGroupVersion.WithKind("StatefulSet"))
	//	err = c.Watch(&source.Kind{Type: objectType}, handler)
	//	if err != nil {
	//		return err
	//	}
	//}

	// Watch changes to Native StatefulSet, use unstructured informer
	objectType := &unstructured.Unstructured{}
	objectType.SetGroupVersionKind(apps.SchemeGroupVersion.WithKind("StatefulSet"))
	err = c.Watch(&source.Kind{Type: objectType}, handler)
	if err != nil {
		return err
	}
	return nil
}

func ReCalculateCanaryStepIndex(rollout *rolloutv1alpha1.Rollout, workloadReplicas, currentReplicas int) int32 {
	var stepIndex int32
	for i := range rollout.Spec.Strategy.Canary.Steps {
		step := rollout.Spec.Strategy.Canary.Steps[i]
		var desiredReplicas int
		if step.Replicas != nil {
			desiredReplicas, _ = intstr.GetScaledValueFromIntOrPercent(step.Replicas, workloadReplicas, true)
		} else {
			replicas := intstr.FromString(strconv.Itoa(int(step.Weight)) + "%")
			desiredReplicas, _ = intstr.GetScaledValueFromIntOrPercent(&replicas, workloadReplicas, true)
		}
		stepIndex = int32(i + 1)
		if currentReplicas <= desiredReplicas {
			break
		}
	}
	return stepIndex
}

func DiscoverGVK(gvk schema.GroupVersionKind) bool {
	genericClient := GetGenericClient()
	if genericClient == nil {
		return true
	}
	discoveryClient := genericClient.DiscoveryClient

	startTime := time.Now()
	err := retry.OnError(retry.DefaultRetry, func(err error) bool { return true }, func() error {
		resourceList, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
		if err != nil {
			return err
		}
		for _, r := range resourceList.APIResources {
			if r.Kind == gvk.Kind {
				return nil
			}
		}
		return errors.NewNotFound(schema.GroupResource{Group: gvk.GroupVersion().String(), Resource: gvk.Kind}, "")
	})

	if err != nil {
		if errors.IsNotFound(err) {
			klog.Warningf("Not found kind %s in group version %s, waiting time %s", gvk.Kind, gvk.GroupVersion().String(), time.Since(startTime))
			return false
		}

		// This might be caused by abnormal apiserver or etcd, ignore it
		klog.Errorf("Failed to find resources in group version %s: %v, waiting time %s", gvk.GroupVersion().String(), err, time.Since(startTime))
	}

	return true
}

func IsMatchRolloutID(pod *corev1.Pod, rolloutID string) bool {
	return pod.Labels[RolloutIDLabel] == rolloutID
}

func GetRolloutID(workloadLabels map[string]string) (string, bool) {
	rolloutID, exist := workloadLabels[RolloutIDLabel]
	return rolloutID, exist
}

func GetGVKFrom(workloadRef *rolloutv1alpha1.WorkloadRef) schema.GroupVersionKind {
	if workloadRef == nil {
		return schema.GroupVersionKind{}
	}
	return schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
}

func DynamicWatchCRD(c controller.Controller, h handler.EventHandler, WatchedSet sets.String, workloadRef *rolloutv1alpha1.WorkloadRef) (bool, error) {
	if workloadRef == nil {
		return false, nil
	}
	gvk := schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
	if WatchedSet.Has(gvk.String()) {
		return true, nil
	}
	if !DiscoverGVK(gvk) {
		klog.Errorf("Failed to find GVK(%v) in cluster", gvk.String())
		return false, nil
	}

	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	return true, c.Watch(&source.Kind{Type: object}, h)
}

func DumpJSON(o interface{}) string {
	by, _ := json.Marshal(o)
	return string(by)
}
