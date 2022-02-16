/*
Copyright 2021.

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
	"fmt"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"time"
)

const (
	InRolloutProgressingAnnotation = "rollouts.kruise.io/in-rollout-progressing"
	KruiseRolloutFinalizer         = "finalizers.rollouts.kruise.io"
)

var (
	errKindNotFound = fmt.Errorf("kind not found in group version resources")
)

// annotation[InRolloutProgressingAnnotation] = rolloutState
type RolloutState struct {
	RolloutName string
	RolloutDone bool
}

func GetRolloutState(annotations map[string]string) (*RolloutState, error) {
	if value, ok := annotations[InRolloutProgressingAnnotation]; !ok || value == "" {
		return nil, nil
	} else {
		var obj *RolloutState
		err := json.Unmarshal([]byte(value), &obj)
		return obj, err
	}
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
		return errKindNotFound
	})

	if err != nil {
		if err == errKindNotFound {
			klog.Warningf("Not found kind %s in group version %s, waiting time %s", gvk.Kind, gvk.GroupVersion().String(), time.Since(startTime))
			return false
		}

		// This might be caused by abnormal apiserver or etcd, ignore it
		klog.Errorf("Failed to find resources in group version %s: %v, waiting time %s", gvk.GroupVersion().String(), err, time.Since(startTime))
	}

	return true
}
