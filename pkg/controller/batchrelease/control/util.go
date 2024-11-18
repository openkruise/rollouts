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

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CalculateBatchReplicas return the planned updated replicas of current batch.
func CalculateBatchReplicas(release *v1beta1.BatchRelease, workloadReplicas, currentBatch int) int {
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
func IsControlledByBatchRelease(release *v1beta1.BatchRelease, object client.Object) bool {
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

// only when IsReadyForBlueGreenRelease returns true, can we go on to the next batch
func ValidateReadyForBlueGreenRelease(object client.Object) error {
	// check the annotation
	if object.GetAnnotations()[util.BatchReleaseControlAnnotation] == "" {
		return fmt.Errorf("workload has no control info annotation")
	}
	switch o := object.(type) {
	case *apps.Deployment:
		// must be RollingUpdate
		if len(o.Spec.Strategy.Type) > 0 && o.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
			return fmt.Errorf("deployment strategy type is not RollingUpdate")
		}
		if o.Spec.Strategy.RollingUpdate == nil {
			return fmt.Errorf("deployment strategy rollingUpdate is nil")
		}
		// MinReadySeconds and ProgressDeadlineSeconds must be set
		if o.Spec.MinReadySeconds != v1beta1.MaxReadySeconds || o.Spec.ProgressDeadlineSeconds == nil || *o.Spec.ProgressDeadlineSeconds != v1beta1.MaxProgressSeconds {
			return fmt.Errorf("deployment strategy minReadySeconds or progressDeadlineSeconds is not MaxReadySeconds or MaxProgressSeconds")
		}

	case *appsv1alpha1.CloneSet:
		// must be ReCreate
		if len(o.Spec.UpdateStrategy.Type) > 0 && o.Spec.UpdateStrategy.Type != appsv1alpha1.RecreateCloneSetUpdateStrategyType {
			return fmt.Errorf("cloneSet strategy type is not ReCreate")
		}
		// MinReadySeconds and ProgressDeadlineSeconds must be set
		if o.Spec.MinReadySeconds != v1beta1.MaxReadySeconds {
			return fmt.Errorf("cloneSet strategy minReadySeconds is not MaxReadySeconds")
		}

	default:
		panic("unsupported workload type to ValidateReadyForBlueGreenRelease function")
	}
	return nil
}

// BuildReleaseControlInfo return a NewControllerRef of release with escaped `"`.
func BuildReleaseControlInfo(release *v1beta1.BatchRelease) string {
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
func ShouldWaitResume(release *v1beta1.BatchRelease) bool {
	return release.Spec.ReleasePlan.FinalizingPolicy == v1beta1.WaitResumeFinalizingPolicyType
}

// IsCurrentMoreThanOrEqualToDesired return true if current >= desired
func IsCurrentMoreThanOrEqualToDesired(current, desired intstr.IntOrString) bool {
	currentNum, _ := intstr.GetScaledValueFromIntOrPercent(&current, 10000000, true)
	desiredNum, _ := intstr.GetScaledValueFromIntOrPercent(&desired, 10000000, true)
	return currentNum >= desiredNum
}

// GetDeploymentStrategy decode the strategy object for advanced deployment
// from the annotation "rollouts.kruise.io/original-deployment-strategy"
func GetOriginalSetting(object client.Object) (OriginalDeploymentStrategy, error) {
	setting := OriginalDeploymentStrategy{}
	settingStr := object.GetAnnotations()[v1beta1.OriginalDeploymentStrategyAnnotation]
	if settingStr == "" {
		return setting, nil
	}
	err := json.Unmarshal([]byte(settingStr), &setting)
	return setting, err
}

// InitOriginalSetting will update the original setting based on the workload object
// note: update the maxSurge and maxUnavailable only when MaxSurge and MaxUnavailable are nil,
// which means they should keep unchanged in continuous release (though continuous release isn't supported for now)
func InitOriginalSetting(setting *OriginalDeploymentStrategy, object client.Object) {
	var changeLogs []string
	switch o := object.(type) {
	case *apps.Deployment:
		if setting.MaxSurge == nil {
			setting.MaxSurge = getMaxSurgeFromDeployment(o.Spec.Strategy.RollingUpdate)
			changeLogs = append(changeLogs, fmt.Sprintf("maxSurge changed from nil to %s", setting.MaxSurge.String()))
		}
		if setting.MaxUnavailable == nil {
			setting.MaxUnavailable = getMaxUnavailableFromDeployment(o.Spec.Strategy.RollingUpdate)
			changeLogs = append(changeLogs, fmt.Sprintf("maxUnavailable changed from nil to %s", setting.MaxUnavailable.String()))
		}
		if setting.ProgressDeadlineSeconds == nil {
			setting.ProgressDeadlineSeconds = getIntPtrOrDefault(o.Spec.ProgressDeadlineSeconds, 600)
			changeLogs = append(changeLogs, fmt.Sprintf("progressDeadlineSeconds changed from nil to %d", *setting.ProgressDeadlineSeconds))
		}
		if setting.MinReadySeconds == 0 {
			setting.MinReadySeconds = o.Spec.MinReadySeconds
			changeLogs = append(changeLogs, fmt.Sprintf("minReadySeconds changed from 0 to %d", setting.MinReadySeconds))
		}
	case *appsv1alpha1.CloneSet:
		if setting.MaxSurge == nil {
			setting.MaxSurge = getMaxSurgeFromCloneset(o.Spec.UpdateStrategy)
			changeLogs = append(changeLogs, fmt.Sprintf("maxSurge changed from nil to %s", setting.MaxSurge.String()))
		}
		if setting.MaxUnavailable == nil {
			setting.MaxUnavailable = getMaxUnavailableFromCloneset(o.Spec.UpdateStrategy)
			changeLogs = append(changeLogs, fmt.Sprintf("maxUnavailable changed from nil to %s", setting.MaxUnavailable.String()))
		}
		if setting.ProgressDeadlineSeconds == nil {
			// cloneset is planned to support progressDeadlineSeconds field
		}
		if setting.MinReadySeconds == 0 {
			setting.MinReadySeconds = o.Spec.MinReadySeconds
			changeLogs = append(changeLogs, fmt.Sprintf("minReadySeconds changed from 0 to %d", setting.MinReadySeconds))
		}
	default:
		panic(fmt.Errorf("unsupported object type %T", o))
	}
	if len(changeLogs) == 0 {
		klog.InfoS("InitOriginalSetting: original setting unchanged", "object", object.GetName())
		return
	}
	klog.InfoS("InitOriginalSetting: original setting updated", "object", object.GetName(), "changes", strings.Join(changeLogs, ";"))
}

func getMaxSurgeFromDeployment(ru *apps.RollingUpdateDeployment) *intstr.IntOrString {
	defaultMaxSurge := intstr.FromString("25%")
	if ru == nil || ru.MaxSurge == nil {
		return &defaultMaxSurge
	}
	return ru.MaxSurge
}
func getMaxUnavailableFromDeployment(ru *apps.RollingUpdateDeployment) *intstr.IntOrString {
	defaultMaxAnavailale := intstr.FromString("25%")
	if ru == nil || ru.MaxUnavailable == nil {
		return &defaultMaxAnavailale
	}
	return ru.MaxUnavailable
}

func getMaxSurgeFromCloneset(us appsv1alpha1.CloneSetUpdateStrategy) *intstr.IntOrString {
	defaultMaxSurge := intstr.FromString("0%")
	if us.MaxSurge == nil {
		return &defaultMaxSurge
	}
	return us.MaxSurge
}
func getMaxUnavailableFromCloneset(us appsv1alpha1.CloneSetUpdateStrategy) *intstr.IntOrString {
	defaultMaxUnavailable := intstr.FromString("20%")
	if us.MaxUnavailable == nil {
		return &defaultMaxUnavailable
	}
	return us.MaxUnavailable
}

func getIntPtrOrDefault(ptr *int32, defaultVal int32) *int32 {
	if ptr == nil {
		return &defaultVal
	}
	return ptr
}
