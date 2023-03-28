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

package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CalculateBatchReplicas return the planned updated replicas of current batch.
func CalculateBatchReplicas(release *v1alpha1.BatchRelease, workloadReplicas, currentBatch int) int {
	batchSize, _ := intstr.GetScaledValueFromIntOrPercent(&release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas, workloadReplicas, true)
	if batchSize > workloadReplicas {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is more than workload.replicas %v", currentBatch, batchSize, workloadReplicas)
		batchSize = workloadReplicas
	} else if batchSize < 0 {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is less than 0 %v", currentBatch, batchSize)
		batchSize = 0
	}

	klog.V(3).InfoS("calculated the number of new pod size", "current batch", currentBatch, "new pod target", batchSize)
	return batchSize
}

// IsControlledByBatchRelease return true if
// * object ownerReference has referred release;
// * object has batchRelease control info annotation about release.
func IsControlledByBatchRelease(release *v1alpha1.BatchRelease, object client.Object) bool {
	if owner := metav1.GetControllerOfNoCopy(object); owner != nil && owner.UID == release.UID {
		return true
	}
	if controlInfo, ok := object.GetAnnotations()[util.BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == release.UID {
			return true
		}
	}
	return false
}

// BuildReleaseControlInfo return a NewControllerRef of release with escaped `"`.
func BuildReleaseControlInfo(release *v1alpha1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return strings.Replace(string(owner), `"`, `\"`, -1)
}

// ParseIntegerAsPercentageIfPossible will return a percentage type IntOrString, such as "20%", "33%", but "33.3%" is illegal.
// Given A, B, return P that should try best to satisfy ⌈P * B⌉ == A, and we ensure that the error is less than 1%.
// For examples:
// * Given stableReplicas 1,  allReplicas 3,   return "33%";
// * Given stableReplicas 98, allReplicas 99,  return "97%";
// * Given stableReplicas 1,  allReplicas 101, return "1%";
func ParseIntegerAsPercentageIfPossible(stableReplicas, allReplicas int32, canaryReplicas *intstr.IntOrString) intstr.IntOrString {
	if stableReplicas >= allReplicas {
		return intstr.FromString("100%")
	}

	if stableReplicas <= 0 {
		return intstr.FromString("0%")
	}

	pValue := stableReplicas * 100 / allReplicas
	percent := intstr.FromString(fmt.Sprintf("%v%%", pValue))
	restoredStableReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(allReplicas), true)
	// restoredStableReplicas == 0 is un-tolerated if user-defined canaryReplicas is not 100%.
	// we must make sure that at least one canary pod is created.
	if restoredStableReplicas <= 0 && canaryReplicas.StrVal != "100%" {
		return intstr.FromString("1%")
	}

	return percent
}

// GenerateNotFoundError return a not found error
func GenerateNotFoundError(name, resource string) error {
	return errors.NewNotFound(schema.GroupResource{Group: "apps", Resource: resource}, name)
}

// ShouldWaitResume return true if FinalizingPolicy is "waitResume".
func ShouldWaitResume(release *v1alpha1.BatchRelease) bool {
	return release.Spec.ReleasePlan.FinalizingPolicy == v1alpha1.WaitResumeFinalizingPolicyType
}

// IsCurrentMoreThanOrEqualToDesired return true if current >= desired
func IsCurrentMoreThanOrEqualToDesired(current, desired intstr.IntOrString) bool {
	currentNum, _ := intstr.GetScaledValueFromIntOrPercent(&current, 10000000, true)
	desiredNum, _ := intstr.GetScaledValueFromIntOrPercent(&desired, 10000000, true)
	return currentNum >= desiredNum
}
