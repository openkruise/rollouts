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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruiseappsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	rolloutv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
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

// RolloutState is annotation[rollouts.kruise.io/in-progressing] value
type RolloutState struct {
	RolloutName string `json:"rolloutName"`
}

func IsRollbackInBatchPolicy(rollout *rolloutv1beta1.Rollout, labels map[string]string) bool {
	// currently, only support the case of no traffic routing
	if rollout.Spec.Strategy.HasTrafficRoutings() {
		return false
	}
	workloadRef := rollout.Spec.WorkloadRef
	//currently, only CloneSet, StatefulSet support this policy
	if workloadRef.Kind == ControllerKindSts.Kind ||
		workloadRef.Kind == ControllerKruiseKindCS.Kind ||
		strings.EqualFold(labels[WorkloadTypeLabel], ControllerKindSts.Kind) {
		if rollout.Annotations[rolloutv1alpha1.RollbackInBatchAnnotation] == "true" {
			return true
		}
	}
	return false
}

func AddWorkloadWatcher(c controller.Controller, handler handler.EventHandler) error {
	// Watch changes to Deployment
	err := c.Watch(&source.Kind{Type: &apps.Deployment{}}, handler)
	if err != nil {
		return err
	}
	// Watch changes to Native StatefulSet, use unstructured informer
	err = c.Watch(&source.Kind{Type: &apps.StatefulSet{}}, handler)
	if err != nil {
		return err
	}
	// Watch changes to CloneSet if it has the CRD
	if DiscoverGVK(ControllerKruiseKindCS) {
		err := c.Watch(&source.Kind{Type: &kruiseappsv1alpha1.CloneSet{}}, handler)
		if err != nil {
			return err
		}
	}
	// Watch changes to DaemonSet if it has the CRD
	if DiscoverGVK(ControllerKruiseKindDS) {
		err := c.Watch(&source.Kind{Type: &kruiseappsv1alpha1.DaemonSet{}}, handler)
		if err != nil {
			return err
		}
	}
	// Watch changes to Advanced StatefulSet if it has the CRD
	if DiscoverGVK(ControllerKruiseKindSts) {
		err := c.Watch(&source.Kind{Type: &kruiseappsv1beta1.StatefulSet{}}, handler)
		if err != nil {
			return err
		}
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

func GetGVKFrom(workloadRef *rolloutv1beta1.ObjectRef) schema.GroupVersionKind {
	if workloadRef == nil {
		return schema.GroupVersionKind{}
	}
	return schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
}

func AddWatcherDynamically(c controller.Controller, h handler.EventHandler, gvk schema.GroupVersionKind) (bool, error) {
	if !DiscoverGVK(gvk) {
		klog.Errorf("Failed to find GVK(%v) in cluster", gvk.String())
		return false, nil
	}

	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	return true, c.Watch(&source.Kind{Type: object}, h)
}

func HashReleasePlanBatches(releasePlan *rolloutv1beta1.ReleasePlan) string {
	by, _ := json.Marshal(releasePlan)
	md5Hash := sha256.Sum256(by)
	return hex.EncodeToString(md5Hash[:])
}

func DumpJSON(o interface{}) string {
	by, _ := json.Marshal(o)
	return string(by)
}

// hash hashes `data` with sha256 and returns the hex string
func EncodeHash(data string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

// calculate the next batch index
func NextBatchIndex(rollout *rolloutv1beta1.Rollout, CurrentStepIndex int32) int32 {
	if rollout == nil {
		return -1
	}
	allSteps := int32(len(rollout.Spec.Strategy.GetSteps()))
	if CurrentStepIndex >= allSteps {
		return -1
	}
	return CurrentStepIndex + 1
}

// check if NextStepIndex is legal, if not, correct it
func CheckNextBatchIndexWithCorrect(rollout *rolloutv1beta1.Rollout) {
	if rollout == nil {
		return
	}
	nextStep := rollout.Status.GetSubStatus().NextStepIndex
	if nextStep <= 0 || nextStep > int32(len(rollout.Spec.Strategy.GetSteps())) {
		rollout.Status.GetSubStatus().NextStepIndex = NextBatchIndex(rollout, rollout.Status.GetSubStatus().CurrentStepIndex)
		if nextStep != rollout.Status.GetSubStatus().NextStepIndex {
			klog.Infof("rollout(%s/%s) invalid nextStepIndex(%d), reset to %d", rollout.Namespace, rollout.Name, nextStep, rollout.Status.GetSubStatus().NextStepIndex)
		}
	}
}
