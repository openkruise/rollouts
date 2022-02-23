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
	"sort"

	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// deploymentController is the place to hold fields needed for handle Deployment type of workloads
type deploymentController struct {
	workloadController
	releaseKey           types.NamespacedName
	stableNamespacedName types.NamespacedName
	canaryNamespacedName types.NamespacedName
}

// add the parent controller to the owner of the deployment, unpause it and initialize the size
// before kicking start the update and start from every pod in the old version
func (c *deploymentController) claimDeployment(stableDeploy, canaryDeploy *apps.Deployment) (*apps.Deployment, error) {
	var controlled bool
	if controlInfo, ok := stableDeploy.Annotations[util.BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == c.parentController.UID {
			klog.V(3).Infof("Deployment(%v) has been controlled by this BatchRelease(%v), no need to claim again",
				c.stableNamespacedName, c.releaseKey)
			controlled = true
		} else {
			klog.Errorf("Failed to parse controller info from Deployment(%v) annotation, error: %v, controller info: %+v",
				c.stableNamespacedName, err, *ref)
		}
	}

	// patch control info to stable deployments if it needs
	if !controlled {
		controlInfo, _ := json.Marshal(metav1.NewControllerRef(c.parentController, c.parentController.GetObjectKind().GroupVersionKind()))
		patchedInfo := map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]string{
					util.BatchReleaseControlAnnotation: string(controlInfo),
				},
			},
		}
		patchedBody, _ := json.Marshal(patchedInfo)
		if err := c.client.Patch(context.TODO(), stableDeploy, client.RawPatch(types.StrategicMergePatchType, patchedBody)); err != nil {
			klog.Errorf("Failed to patch controller info annotations to stable deployment(%v), error: %v", client.ObjectKeyFromObject(canaryDeploy), err)
			return canaryDeploy, err
		}
	}

	// create canary deployment if it needs
	if canaryDeploy == nil || !util.EqualIgnoreHash(&stableDeploy.Spec.Template, &canaryDeploy.Spec.Template) {
		var err error
		var collisionCount int32
		if c.releaseStatus.CollisionCount != nil {
			collisionCount = *c.releaseStatus.CollisionCount
		}

		for {
			canaryDeploy, err = c.createCanaryDeployment(stableDeploy, &collisionCount)
			if errors.IsAlreadyExists(err) {
				collisionCount++
				continue
			} else if err != nil {
				return nil, err
			}
			break
		}

		if collisionCount > 0 {
			c.releaseStatus.CollisionCount = pointer.Int32Ptr(collisionCount)
		}
	}

	return canaryDeploy, nil
}

func (c *deploymentController) createCanaryDeployment(stableDeploy *apps.Deployment, collisionCount *int32) (*apps.Deployment, error) {
	// TODO: find a better way to generate canary deployment name
	suffix := util.ShortRandomStr(collisionCount)
	canaryDeploy := &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("%v-%v", c.canaryNamespacedName.Name, suffix),
			Namespace:   c.stableNamespacedName.Namespace,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
	}
	for k, v := range stableDeploy.Labels {
		canaryDeploy.Labels[k] = v
	}
	for k, v := range stableDeploy.Annotations {
		canaryDeploy.Annotations[k] = v
	}
	for _, f := range stableDeploy.Finalizers {
		canaryDeploy.Finalizers = append(canaryDeploy.Finalizers, f)
	}
	for _, o := range stableDeploy.OwnerReferences {
		canaryDeploy.OwnerReferences = append(canaryDeploy.OwnerReferences, *o.DeepCopy())
	}

	canaryDeploy.Finalizers = append(canaryDeploy.Finalizers, util.CanaryDeploymentFinalizer)
	canaryDeploy.OwnerReferences = append(canaryDeploy.OwnerReferences, *metav1.NewControllerRef(
		c.parentController, c.parentController.GroupVersionKind()))

	// set extra labels & annotations
	canaryDeploy.Labels[util.CanaryDeploymentLabelKey] = c.stableNamespacedName.Name
	owner := metav1.NewControllerRef(c.parentController, c.parentController.GroupVersionKind())
	if owner != nil {
		ownerInfo, _ := json.Marshal(owner)
		canaryDeploy.Annotations[util.BatchReleaseControlAnnotation] = string(ownerInfo)
	}

	// copy spec
	canaryDeploy.Spec = *stableDeploy.Spec.DeepCopy()
	canaryDeploy.Spec.Replicas = pointer.Int32Ptr(0)
	canaryDeploy.Spec.Paused = false

	canaryKey := client.ObjectKeyFromObject(canaryDeploy)
	// create canary Deployment
	err := c.client.Create(context.TODO(), canaryDeploy)
	if err != nil {
		klog.Errorf("Failed to create canary Deployment(%v), error: %v", canaryKey, err)
		return nil, err
	}

	canaryDeployInfo, _ := json.Marshal(canaryDeploy)
	klog.V(3).Infof("Create canary Deployment(%v) successfully, details: %v", canaryKey, string(canaryDeployInfo))

	// fetch the canary Deployment
	var fetchedCanary *apps.Deployment
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fetchedCanary = &apps.Deployment{}
		return c.client.Get(context.TODO(), canaryKey, fetchedCanary)
	})

	return fetchedCanary, err
}

func (c *deploymentController) releaseDeployment(stableDeploy *apps.Deployment, pause *bool, cleanup bool) (bool, error) {
	var patchErr, deleteErr error

	// clean up control info for stable deployment if it needs
	if stableDeploy != nil && (len(stableDeploy.Annotations[util.BatchReleaseControlAnnotation]) > 0 || (pause != nil && stableDeploy.Spec.Paused != *pause)) {
		var patchByte []byte
		if pause == nil {
			patchByte = []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%v":null}}}`, util.BatchReleaseControlAnnotation))
		} else {
			patchByte = []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%v":null}},"spec":{"paused":%v}}`, util.BatchReleaseControlAnnotation, *pause))
		}

		patchErr = c.client.Patch(context.TODO(), stableDeploy, client.RawPatch(types.StrategicMergePatchType, patchByte))
		if patchErr != nil {
			klog.Errorf("Error occurred when patching Deployment(%v), error: %v", c.stableNamespacedName, patchErr)
			return false, patchErr
		}
	}

	// clean up canary deployment if it needs
	if cleanup {
		ds, err := c.listCanaryDeployment(client.InNamespace(c.stableNamespacedName.Namespace))
		if err != nil {
			return false, err
		}

		// must make sure the older is deleted firstly
		sort.Slice(ds, func(i, j int) bool {
			return ds[i].CreationTimestamp.Before(&ds[j].CreationTimestamp)
		})

		// delete all the canary deployments
		for _, d := range ds {
			// clean up finalizers first
			if controllerutil.ContainsFinalizer(d, util.CanaryDeploymentFinalizer) {
				finalizers := sets.NewString(d.Finalizers...).Delete(util.CanaryDeploymentFinalizer).List()
				patchErr = util.PatchFinalizer(c.client, d, finalizers)
				if patchErr != nil && !errors.IsNotFound(patchErr) {
					klog.Error("Error occurred when patching Deployment(%v), error: %v", client.ObjectKeyFromObject(d), patchErr)
					return false, patchErr
				}
				return false, nil
			}

			// delete the deployment
			deleteErr = c.client.Delete(context.TODO(), d)
			if deleteErr != nil && !errors.IsNotFound(deleteErr) {
				klog.Errorf("Error occurred when deleting Deployment(%v), error: %v", client.ObjectKeyFromObject(d), deleteErr)
				return false, deleteErr
			}
		}
	}

	klog.V(3).Infof("Release Deployment(%v) Successfully", c.stableNamespacedName)
	return true, nil
}

// scale the deployment
func (c *deploymentController) patchCanaryReplicas(canaryDeploy *apps.Deployment, replicas int32) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": pointer.Int32Ptr(replicas),
		},
	}

	patchByte, _ := json.Marshal(patch)
	if err := c.client.Patch(context.TODO(), canaryDeploy, client.RawPatch(types.MergePatchType, patchByte)); err != nil {
		c.recorder.Eventf(c.parentController, v1.EventTypeWarning, "PatchPartitionFailed",
			"Failed to update the canary Deployment to the correct canary replicas %d, error: %v", replicas, err)
		return err
	}

	klog.InfoS("Submitted modified partition quest for canary Deployment", "Deployment",
		client.ObjectKeyFromObject(canaryDeploy), "target canary replicas size", replicas, "batch", c.releaseStatus.CanaryStatus.CurrentBatch)
	return nil
}

func (c *deploymentController) listCanaryDeployment(options ...client.ListOption) ([]*apps.Deployment, error) {
	dList := &apps.DeploymentList{}
	if err := c.client.List(context.TODO(), dList, options...); err != nil {
		return nil, err
	}

	var ds []*apps.Deployment
	for i := range dList.Items {
		d := &dList.Items[i]
		o := metav1.GetControllerOf(d)
		if o == nil || o.UID != c.parentController.UID {
			continue
		}
		ds = append(ds, d)
	}

	return ds, nil
}
