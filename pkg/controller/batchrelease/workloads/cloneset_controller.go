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

		if clone.Spec.UpdateStrategy.Partition != nil {
			partitionByte, _ := json.Marshal(clone.Spec.UpdateStrategy.Partition)
			metadata := patch["metadata"].(map[string]interface{})
			annotations := metadata["annotations"].(map[string]string)
			annotations[util.StashCloneSetPartition] = string(partitionByte)
			annotations[util.BatchReleaseControlAnnotation] = string(controlByte)
		}
	}

	if len(patch) > 0 {
		patchByte, _ := json.Marshal(patch)
		if err := c.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
			c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "ClaimCloneSetFailed", err.Error())
			return false, err
		}
	}

	klog.V(3).Infof("Claim CloneSet(%v) Successfully", c.targetNamespacedName)
	return true, nil
}

// remove the parent controller from the deployment's owner list
func (c *cloneSetController) releaseCloneSet(clone *kruiseappsv1alpha1.CloneSet, pause *bool, cleanup bool) (bool, error) {
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

	var patchByte string
	switch {
	case cleanup:
		patchSpec := map[string]interface{}{
			"updateStrategy": map[string]interface{}{
				"partition": nil,
			},
		}

		if len(clone.Annotations[util.StashCloneSetPartition]) > 0 {
			restoredPartition := &intstr.IntOrString{}
			if err := json.Unmarshal([]byte(clone.Annotations[util.StashCloneSetPartition]), restoredPartition); err == nil {
				updateStrategy := patchSpec["updateStrategy"].(map[string]interface{})
				updateStrategy["partition"] = restoredPartition
			}
		}

		patchSpecByte, _ := json.Marshal(patchSpec)
		patchByte = fmt.Sprintf(`{"metadata":{"annotations":{"%s":null, "%s":null}},"spec":%s}`,
			util.BatchReleaseControlAnnotation, util.StashCloneSetPartition, string(patchSpecByte))

	default:
		patchByte = fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, util.BatchReleaseControlAnnotation)
	}

	if err := c.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(patchByte))); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "ReleaseCloneSetFailed", err.Error())
		return false, err
	}

	klog.V(3).Infof("Release CloneSet(%v) Successfully", client.ObjectKeyFromObject(clone))
	return true, nil
}

// scale the deployment
func (c *cloneSetController) patchCloneSetPartition(clone *kruiseappsv1alpha1.CloneSet, partition int32) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"updateStrategy": map[string]interface{}{
				"partition": &intstr.IntOrString{Type: intstr.Int, IntVal: partition},
			},
		},
	}

	patchByte, _ := json.Marshal(patch)
	if err := c.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "PatchPartitionFailed",
			"Failed to update the CloneSet(%v) to the correct target partition %d, error: %v",
			c.targetNamespacedName, partition, err)
		return err
	}

	klog.InfoS("Submitted modified partition quest for CloneSet", "CloneSet", c.targetNamespacedName,
		"target partition size", partition, "batch", c.releaseStatus.CanaryStatus.CurrentBatch)

	return nil
}
