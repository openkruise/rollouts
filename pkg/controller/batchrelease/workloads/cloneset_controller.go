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

package workloads

import (
	"context"
	"encoding/json"
	"fmt"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// cloneSetController is the place to hold fields needed for handle CloneSet type of workloads
type cloneSetController struct {
	workloadController
	releasePlanKey       types.NamespacedName
	targetNamespacedName types.NamespacedName
}

// add the parent controller to the owner of the deployment, unpause it and initialize the size
// before kicking start the update and start from every pod in the old version
func (c *cloneSetController) claimCloneSet(clone *kruiseappsv1alpha1.CloneSet) (bool, error) {
	var controlled bool
	if controlInfo, ok := clone.Annotations[util.BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == c.parentController.UID {
			controlled = true
			klog.V(3).Infof("CloneSet(%v) has been controlled by this BatchRelease(%v), no need to claim again",
				c.targetNamespacedName, c.releasePlanKey)
		} else {
			klog.Errorf("Failed to parse controller info from CloneSet(%v) annotation, error: %v, controller info: %+v",
				c.targetNamespacedName, err, *ref)
		}
	}

	patch := map[string]interface{}{}
	switch {
	// if the cloneSet has been claimed by this parentController
	case controlled:
		// make sure paused=false
		if clone.Spec.UpdateStrategy.Paused {
			patch = map[string]interface{}{
				"spec": map[string]interface{}{
					"updateStrategy": map[string]interface{}{
						"paused": false,
					},
				},
			}
		}

	default:
		patch = map[string]interface{}{
			"spec": map[string]interface{}{
				"updateStrategy": map[string]interface{}{
					"partition": &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					"paused":    false,
				},
			},
		}

		controlInfo := metav1.NewControllerRef(c.parentController, c.parentController.GetObjectKind().GroupVersionKind())
		controlByte, _ := json.Marshal(controlInfo)
		patch["metadata"] = map[string]interface{}{
			"annotations": map[string]string{
				util.BatchReleaseControlAnnotation: string(controlByte),
			},
		}
	}

	if len(patch) > 0 {
		cloneObj := clone.DeepCopy()
		patchByte, _ := json.Marshal(patch)
		if err := c.client.Patch(context.TODO(), cloneObj, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
			c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "ClaimCloneSetFailed", err.Error())
			return false, err
		}
	}

	klog.V(3).Infof("Claim CloneSet(%v) Successfully", c.targetNamespacedName)
	return true, nil
}

// remove the parent controller from the deployment's owner list
func (c *cloneSetController) releaseCloneSet(clone *kruiseappsv1alpha1.CloneSet, cleanup bool) (bool, error) {
	if clone == nil {
		return true, nil
	}

	var found bool
	var refByte string
	if refByte, found = clone.Annotations[util.BatchReleaseControlAnnotation]; found && refByte != "" {
		ref := &metav1.OwnerReference{}
		if err := json.Unmarshal([]byte(refByte), ref); err != nil {
			found = false
			klog.Errorf("failed to decode controller annotations of BatchRelease")
		} else if ref.UID != c.parentController.UID {
			found = false
		}
	}

	if !found {
		klog.V(3).Infof("the CloneSet(%v) is already released", c.targetNamespacedName)
		return true, nil
	}

	cloneObj := clone.DeepCopy()
	patchByte := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, util.BatchReleaseControlAnnotation))
	if err := c.client.Patch(context.TODO(), cloneObj, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "ReleaseCloneSetFailed", err.Error())
		return false, err
	}

	klog.V(3).Infof("Release CloneSet(%v) Successfully", c.targetNamespacedName)
	return true, nil
}

// scale the deployment
func (c *cloneSetController) patchCloneSetPartition(clone *kruiseappsv1alpha1.CloneSet, partition *intstr.IntOrString) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"updateStrategy": map[string]interface{}{
				"partition": partition,
				"paused":    false,
			},
		},
	}

	cloneObj := clone.DeepCopy()
	patchByte, _ := json.Marshal(patch)
	if err := c.client.Patch(context.TODO(), cloneObj, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "PatchPartitionFailed",
			"Failed to update the CloneSet(%v) to the correct target partition %d, error: %v",
			c.targetNamespacedName, partition, err)
		return err
	}

	klog.InfoS("Submitted modified partition quest for CloneSet", "CloneSet", c.targetNamespacedName,
		"target partition size", partition, "batch", c.releaseStatus.CanaryStatus.CurrentBatch)

	return nil
}

// the canary workload size for the current batch
func (c *cloneSetController) calculateCurrentCanary(totalSize int32) int32 {
	targetSize := int32(util.CalculateNewBatchTarget(c.releasePlan, int(totalSize), int(c.releaseStatus.CanaryStatus.CurrentBatch)))
	klog.InfoS("Calculated the number of pods in the target CloneSet after current batch",
		"CloneSet", c.targetNamespacedName, "BatchRelease", c.releasePlanKey,
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload updateRevision size", targetSize)
	return targetSize
}

// the source workload size for the current batch
func (c *cloneSetController) calculateCurrentStable(totalSize int32) int32 {
	sourceSize := totalSize - c.calculateCurrentCanary(totalSize)
	klog.InfoS("Calculated the number of pods in the source CloneSet after current batch",
		"CloneSet", c.targetNamespacedName, "BatchRelease", c.releasePlanKey,
		"current batch", c.releaseStatus.CanaryStatus.CurrentBatch, "workload stableRevision size", sourceSize)
	return sourceSize
}

// ParseIntegerAsPercentageIfPossible will return a percentage type IntOrString, such as "20%", "33%", but "33.3%" is illegal.
// Given A, B, return P that should try best to satisfy ⌈P * B⌉ == A, and we ensure that the error is less than 1%.
// For examples:
// * Given stableReplicas 1,  allReplicas 3,   return "33%";
// * Given stableReplicas 98, allReplicas 99,  return "97%";
// * Given stableReplicas 1,  allReplicas 101, return "1";
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

func CalculateRealCanaryReplicasGoal(expectedStableReplicas, allReplicas int32, canaryReplicas *intstr.IntOrString) int32 {
	if canaryReplicas.Type == intstr.Int {
		return allReplicas - expectedStableReplicas
	}
	partition := ParseIntegerAsPercentageIfPossible(expectedStableReplicas, allReplicas, canaryReplicas)
	realStableReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&partition, int(allReplicas), true)
	return allReplicas - int32(realStableReplicas)
}
