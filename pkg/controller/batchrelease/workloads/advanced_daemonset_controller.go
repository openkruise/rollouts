package workloads

import (
	"context"
	"encoding/json"

	"k8s.io/apimachinery/pkg/types"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// advancedDaemonSetController is the place to hold fields needed for handle Advanced DaemonSet type of workloads
type advancedDaemonSetController struct {
	workloadController
	releasePlanKey       types.NamespacedName
	targetNamespacedName types.NamespacedName
}

// add the parent controller to the owner of the AdvancedDaemonSet, unpause it and initialize the size
// before kicking start the update and start from every pod in the old version
func (c *advancedDaemonSetController) claimAdvancedDaemonSet(daemonSet *kruiseappsv1alpha1.DaemonSet) (bool, error) {
	var controlled bool
	if controlInfo, ok := daemonSet.Annotations[util.BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == c.release.UID {
			controlled = true
			klog.V(3).Infof("AdvancedDaemonSet(%v) has been controlled by this BatchRelease(%v), no need to claim again",
				c.targetNamespacedName, c.releasePlanKey)
		} else {
			klog.Errorf("Failed to parse controller info from AdvancedDaemonSet(%v) annotation, error: %v, controller info: %+v",
				c.targetNamespacedName, err, *ref)
		}
	}

	patch := map[string]interface{}{}
	switch {
	// if the cloneSet has been claimed by this release
	case controlled:
		// make sure paused=false
		if *daemonSet.Spec.UpdateStrategy.RollingUpdate.Paused {
			patch = map[string]interface{}{
				"spec": map[string]interface{}{
					"updateStrategy": map[string]interface{}{
						"rollingUpdate": map[string]interface{}{
							"paused": false,
						},
					},
				},
			}
		}

	default:
		patch = map[string]interface{}{
			"spec": map[string]interface{}{
				"updateStrategy": map[string]interface{}{
					"rollingUpdate": map[string]interface{}{
						"partition": &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
						"paused":    false,
					},
				},
			},
		}

		controlInfo := metav1.NewControllerRef(c.release, c.release.GetObjectKind().GroupVersionKind())
		controlByte, _ := json.Marshal(controlInfo)
		patch["metadata"] = map[string]interface{}{
			"annotations": map[string]string{
				util.BatchReleaseControlAnnotation: string(controlByte),
			},
		}
	}

	if len(patch) > 0 {
		cloneObj := daemonSet.DeepCopy()
		patchByte, _ := json.Marshal(patch)
		if err := c.client.Patch(context.TODO(), cloneObj, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
			c.recorder.Eventf(c.release, v1.EventTypeWarning, "ClaimAdvancedDaemonSetFailed", err.Error())
			return false, err
		}
	}

	klog.V(3).Infof("Claim AdvancedDaemonSet(%v) Successfully", c.targetNamespacedName)
	return true, nil
}
