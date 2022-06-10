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
	"time"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruiseappsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/pkg/util/client"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	RolloutHashAnnotation = "rollouts.kruise.io/hash"
	// RolloutIDLabel is designed to distinguish each workload revision publications.
	// The value of RolloutIDLabel corresponds Rollout.Spec.RolloutID.
	RolloutIDLabel = "apps.kruise.io/rollout-id"
	// RolloutBatchIDLabel is the label key of batch id that will be patched to pods during rollout.
	// Only when RolloutIDLabel is set, RolloutBatchIDLabel will be patched.
	// Users can use RolloutIDLabel and RolloutBatchIDLabel to select the pods that are upgraded in some certain batch and release.
	RolloutBatchIDLabel = "apps.kruise.io/rollout-batch-id"
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

func AddWorkloadWatcher(c controller.Controller, handler handler.EventHandler) error {
	if DiscoverGVK(ControllerKruiseKindCS) {
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

	// Watch changes to Advanced StatefulSet, use unstructured informer
	if DiscoverGVK(ControllerKruiseKindSts) {
		objectType := &unstructured.Unstructured{}
		objectType.SetGroupVersionKind(kruiseappsv1beta1.SchemeGroupVersion.WithKind("StatefulSet"))
		err = c.Watch(&source.Kind{Type: objectType}, handler)
		if err != nil {
			return err
		}
	}

	// Watch changes to Native StatefulSet, use unstructured informer
	objectType := &unstructured.Unstructured{}
	objectType.SetGroupVersionKind(apps.SchemeGroupVersion.WithKind("StatefulSet"))
	err = c.Watch(&source.Kind{Type: objectType}, handler)
	if err != nil {
		return err
	}
	return nil
}

func DiscoverGVK(gvk schema.GroupVersionKind) bool {
	genericClient := client.GetGenericClient()
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

func DumpJSON(o interface{}) string {
	by, _ := json.Marshal(o)
	return string(by)
}
