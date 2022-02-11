package workloads

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
)

const BatchReleaseControlAnnotation = "batchrelease.rollouts.kruise.io/control-info"
const StashCloneSetPartition = "batchrelease.rollouts.kruise.io/stash-partition"

// cloneSetController is the place to hold fields needed for handle CloneSet type of workloads
type cloneSetController struct {
	workloadController
	targetNamespacedName types.NamespacedName
}

// add the parent controller to the owner of the deployment, unpause it and initialize the size
// before kicking start the update and start from every pod in the old version
func (c *cloneSetController) claimCloneSet(clone *kruiseappsv1alpha1.CloneSet) (bool, error) {
	var controlled bool
	if controlInfo, ok := clone.Annotations[BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == c.parentController.UID {
			controlled = true
			klog.V(3).Info("CloneSet has been controlled by this BatchRelease, no need to claim again")
		} else {
			klog.Error("Failed to parse controller info from cloneset annotation, error: %v, controller info: %+v", err, *ref)
		}
	}

	patch := map[string]interface{}{}
	switch {
	case controlled:
		patch = map[string]interface{}{
			"spec": map[string]interface{}{
				"updateStrategy": map[string]interface{}{
					"paused": false,
				},
			},
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
				BatchReleaseControlAnnotation: string(controlByte),
			},
		}

		if clone.Spec.UpdateStrategy.Partition != nil {
			partitionByte, _ := json.Marshal(clone.Spec.UpdateStrategy.Partition)
			metadata := patch["metadata"].(map[string]interface{})
			annotations := metadata["annotations"].(map[string]string)
			annotations[StashCloneSetPartition] = string(partitionByte)
			annotations[BatchReleaseControlAnnotation] = string(controlByte)
		}
	}

	patchByte, _ := json.Marshal(patch)
	if err := c.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "ClaimCloneSetFailed", err.Error())
		return false, err
	}

	klog.V(3).Info("Claim CloneSet Successfully")
	return true, nil
}

// remove the parent controller from the deployment's owner list
func (c *cloneSetController) releaseCloneSet(clone *kruiseappsv1alpha1.CloneSet, pause, cleanup bool) (bool, error) {
	if clone == nil {
		return true, nil
	}

	var found bool
	var refByte string
	if refByte, found = clone.Annotations[BatchReleaseControlAnnotation]; found && refByte != "" {
		ref := &metav1.OwnerReference{}
		if err := json.Unmarshal([]byte(refByte), ref); err != nil {
			found = false
			klog.Error("failed to decode controller annotations of BatchRelease")
		} else if ref.UID != c.parentController.UID {
			found = false
		}
	}

	if !found {
		klog.V(3).InfoS("the cloneset is already released", "CloneSet", clone.Name)
		return true, nil
	}

	patchSpec := map[string]interface{}{
		"updateStrategy": map[string]interface{}{
			"partition": nil,
			"paused":    pause,
		},
	}

	if len(clone.Annotations[StashCloneSetPartition]) > 0 {
		restoredPartition := &intstr.IntOrString{}
		if err := json.Unmarshal([]byte(clone.Annotations[StashCloneSetPartition]), restoredPartition); err == nil {
			updateStrategy := patchSpec["updateStrategy"].(map[string]interface{})
			updateStrategy["partition"] = restoredPartition
		}
	}

	patchSpecByte, _ := json.Marshal(patchSpec)
	patchByte := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null, "%s":null}},"spec":%s}`,
		BatchReleaseControlAnnotation, StashCloneSetPartition, string(patchSpecByte))

	if err := c.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(patchByte))); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "ReleaseCloneSetFailed", err.Error())
		return false, err
	}

	klog.V(3).Info("Release CloneSet Successfully")
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
			"Failed to update the CloneSet to the correct target partition %d, error: %v", partition, err)
		return err
	}

	klog.InfoS("Submitted modified partition quest for cloneset", "CloneSet",
		clone.GetName(), "target partition size", partition, "batch", c.releaseStatus.CanaryStatus.CurrentBatch)
	return nil
}
