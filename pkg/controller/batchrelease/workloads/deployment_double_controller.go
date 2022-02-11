package workloads

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sort"
	"time"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// deploymentController is the place to hold fields needed for handle Deployment type of workloads
type deploymentController struct {
	workloadController
	stableNamespacedName types.NamespacedName
	canaryNamespacedName types.NamespacedName
}

// add the parent controller to the owner of the deployment, unpause it and initialize the size
// before kicking start the update and start from every pod in the old version
func (c *deploymentController) claimDeployment(stableDeploy, canaryDeploy *apps.Deployment) (*apps.Deployment, error) {
	var controlled bool
	if controlInfo, ok := stableDeploy.Annotations[BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == c.parentController.UID {
			klog.V(3).Info("CloneSet has been controlled by this BatchRelease, no need to claim again")
			controlled = true
		} else {
			klog.Error("Failed to parse controller info from cloneset annotation, error: %v, controller info: %+v", err, *ref)
		}
	}

	if !controlled {
		controlInfo, _ := json.Marshal(metav1.NewControllerRef(c.parentController, c.parentController.GetObjectKind().GroupVersionKind()))
		patchedInfo := map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]string{
					BatchReleaseControlAnnotation: string(controlInfo),
				},
			},
		}
		patchedBody, _ := json.Marshal(patchedInfo)
		if err := c.client.Patch(context.TODO(), stableDeploy, client.RawPatch(types.StrategicMergePatchType, patchedBody)); err != nil {
			klog.Error("Failed to patch controller info annotations to stable deployment(%v), error: %v", client.ObjectKeyFromObject(canaryDeploy), err)
			return canaryDeploy, err
		}
	}

	if canaryDeploy == nil || !EqualIgnoreHash(&stableDeploy.Spec.Template, &canaryDeploy.Spec.Template) {
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
	suffix := ComputeHash(&stableDeploy.Spec.Template, collisionCount)
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

	canaryDeploy.Finalizers = append(canaryDeploy.Finalizers, CanaryDeploymentFinalizer)
	canaryDeploy.OwnerReferences = append(canaryDeploy.OwnerReferences, *metav1.NewControllerRef(
		stableDeploy, stableDeploy.GroupVersionKind()))

	// set labels & annotations
	canaryDeploy.Labels[CanaryDeploymentLabelKey] = string(stableDeploy.UID)
	owner := metav1.NewControllerRef(c.parentController, c.parentController.GroupVersionKind())
	if owner != nil {
		ownerInfo, _ := json.Marshal(owner)
		canaryDeploy.Annotations[BatchReleaseControlAnnotation] = string(ownerInfo)
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
	klog.V(3).Infof("Create canary deployment successfully, details: %+v", canaryDeployInfo)

	// fetch the canary Deployment
	var fetchedCanary *apps.Deployment
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fetchedCanary = &apps.Deployment{}
		return c.client.Get(context.TODO(), canaryKey, fetchedCanary)
	})

	return fetchedCanary, err
}

func (c *deploymentController) releaseDeployment(stableDeploy *apps.Deployment, pause, cleanup bool) (bool, error) {
	if stableDeploy == nil {
		return true, nil
	}

	var patchErr, deleteErr error
	{
		patchByte := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%v":null}},"spec":{"paused":%v}}`, BatchReleaseControlAnnotation, pause))
		patchErr = c.client.Patch(context.TODO(), stableDeploy, client.RawPatch(types.StrategicMergePatchType, patchByte))
		if patchErr != nil {
			klog.Error("Error occurred when patching Deployment, error: %v", patchErr)
			return false, patchErr
		}
	}

	if cleanup {
		ds, err := c.listCanaryDeployment(client.InNamespace(stableDeploy.Namespace),
			client.MatchingLabels(map[string]string{CanaryDeploymentLabelKey: string(stableDeploy.UID)}))
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
			if controllerutil.ContainsFinalizer(d, CanaryDeploymentFinalizer) {
				finalizers := sets.NewString(d.Finalizers...).Delete(CanaryDeploymentFinalizer).List()
				patchErr = PatchFinalizer(c.client, d, finalizers)
				if patchErr != nil && !errors.IsNotFound(patchErr) {
					klog.Error("Error occurred when patching Deployment, error: %v", patchErr)
					return false, patchErr
				}
				time.Sleep(time.Second)
			}
			// delete the deployment
			deleteErr = c.client.Delete(context.TODO(), d)
			if deleteErr != nil && !errors.IsNotFound(deleteErr) {
				klog.Error("Error occurred when deleting Deployment, error: %v", deleteErr)
				return false, deleteErr
			}
			time.Sleep(time.Second)
		}

		// make sure that all canary deployments has been deleted
		ds, err = c.listCanaryDeployment(client.InNamespace(stableDeploy.Namespace),
			client.MatchingLabels(map[string]string{CanaryDeploymentLabelKey: string(stableDeploy.UID)}))
		if err != nil {
			return false, err
		}

		return len(ds) == 0, nil
	}

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
		canaryDeploy.GetName(), "target canary replicas size", replicas, "batch", c.releaseStatus.CanaryStatus.CurrentBatch)
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
		if d.DeletionTimestamp != nil {
			continue
		}
		ds = append(ds, d)
	}

	return ds, nil
}
