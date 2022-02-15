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

package batchrelease

import (
	"context"
	"encoding/json"
	"fmt"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
)

const (
	// rollouts.kruise.io
	BatchReleaseOwnerRefAnnotation = "rollouts.kruise.io/owner-ref"

	CanaryDeploymentLabelKey  = "rollouts.kruise.io/canary-deployment"
	CanaryDeploymentFinalizer = "finalizer.rollouts.kruise.io/batch-release"
)

type innerBatchController struct {
	client.Client

	rollout *appsv1alpha1.Rollout

	batchName string
}

func NewInnerBatchController(c client.Client, rollout *appsv1alpha1.Rollout) BatchController {
	r := &innerBatchController{
		Client:    c,
		rollout:   rollout,
		batchName: rolloutBatchName(rollout),
	}

	return r
}

func (r *innerBatchController) VerifyBatchInitial() (bool, string, error) {
	batch := &appsv1alpha1.BatchRelease{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch)
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.batchName, err.Error())
		return false, "", err
	} else if errors.IsNotFound(err) {
		// create new BatchRelease Crd
		br := createBatchRelease(r.rollout, r.batchName)
		if err = r.Create(context.TODO(), br); err != nil && !errors.IsAlreadyExists(err) {
			klog.Errorf("rollout(%s/%s) create BatchRelease(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.batchName, err.Error())
			return false, "", err
		}
		klog.Infof("rollout(%s/%s) create BatchRelease(%s) success", r.rollout.Namespace, r.rollout.Name, r.batchName)
		return false, "", nil
	}

	// verify batchRelease status
	if batch.Status.ObservedGeneration == batch.Generation {
		klog.Infof("rollout(%s/%s) batchRelease(%s) initialize done", r.rollout.Namespace, r.rollout.Name, r.batchName)
		return true, "", nil
	}
	klog.Infof("rollout(%s/%s) batchRelease(%s) is initialing, and wait a moment ", r.rollout.Namespace, r.rollout.Name, r.batchName)
	return false, "", nil
}

func (r *innerBatchController) BatchReleaseState() (*BatchReleaseState, error) {
	batch := &appsv1alpha1.BatchRelease{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch)
	if err != nil {
		klog.Errorf("rollout(%s/%s) fetch batch(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.batchName, err.Error())
		return nil, err
	}

	state := &BatchReleaseState{
		UpdateRevision:       batch.Status.UpdateRevision,
		StableRevision:       batch.Status.StableRevision,
		CurrentBatch:         batch.Status.CanaryStatus.CurrentBatch,
		UpdatedReplicas:      batch.Status.CanaryStatus.UpdatedReplicas,
		UpdatedReadyReplicas: batch.Status.CanaryStatus.UpdatedReadyReplicas,
		Paused:               batch.Spec.ReleasePlan.Paused,
	}
	if batch.Status.CanaryStatus.ReleasingBatchState == appsv1alpha1.ReadyBatchState {
		state.State = BatchReadyState
	} else {
		state.State = BatchInRollingState
	}
	return state, nil
}

func (r *innerBatchController) PromoteBatch(index int32) error {
	batch := &appsv1alpha1.BatchRelease{}
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch); err != nil {
			klog.Errorf("error getting updated rollout(%s/%s) from client", batch.Namespace, batch.Name)
			return err
		}
		if !batch.Spec.ReleasePlan.Paused && *batch.Spec.ReleasePlan.BatchPartition == index {
			return nil
		}
		batch.Spec.ReleasePlan.BatchPartition = utilpointer.Int32Ptr(index)
		batch.Spec.ReleasePlan.Paused = false
		if err := r.Client.Update(context.TODO(), batch); err != nil {
			return err
		}
		klog.Infof("rollout(%s/%s) promote batchRelease(%s) BatchPartition(%d) success", r.rollout.Namespace, r.rollout.Name, r.batchName, index)
		return nil
	}); err != nil {
		klog.Errorf("rollout(%s/%s) promote batchRelease(%s) BatchPartition(%d) failed: %s", r.rollout.Namespace, r.rollout.Name, r.batchName, index, err.Error())
		return err
	}
	return nil
}

func (r *innerBatchController) PromoteStableWorkload() (bool, error) {
	// if cloneSet, do nothing
	if r.rollout.Spec.ObjectRef.WorkloadRef.Kind == util.ControllerKruiseKindCS.Kind {
		return true, nil
	}

	obj := &apps.Deployment{}
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.rollout.Namespace, Name: r.rollout.Spec.ObjectRef.WorkloadRef.Name}, obj); err != nil {
			return err
		}
		if obj.Spec.Paused == true {
			return nil
		}
		obj.Spec.Paused = false
		return r.Update(context.TODO(), obj)
	})
	if err != nil {
		klog.Errorf("update rollout(%s/%s) stable deployment failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}
	maxUnavailable, _ := intstr.GetValueFromIntOrPercent(obj.Spec.Strategy.RollingUpdate.MaxUnavailable, int(*obj.Spec.Replicas), true)
	if obj.Status.ObservedGeneration != obj.Generation || obj.Status.UpdatedReplicas != *obj.Spec.Replicas &&
		*obj.Spec.Replicas-obj.Status.AvailableReplicas > int32(maxUnavailable) {
		klog.Infof("rollout(%s/%s) stable deployment AvailableReplicas(%d), and wait a moment", r.rollout.Namespace, r.rollout.Name, obj.Status.AvailableReplicas)
		return false, nil
	}
	klog.Infof("promote rollout(%s/%s) stable deployment AvailableReplicas(%d) success", r.rollout.Namespace, r.rollout.Name, obj.Status.AvailableReplicas)
	return true, nil
}

func (r *innerBatchController) Finalize() (bool, error) {
	batch := &appsv1alpha1.BatchRelease{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch)
	if err != nil && errors.IsNotFound(err) {
		klog.Infof("rollout(%s/%s) delete batch(%s) success", r.rollout.Namespace, r.rollout.Name, r.batchName)
		return true, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch batch failed: %s", r.rollout.Namespace, r.rollout.Name, r.batchName)
		return false, err
	}
	by,_ := json.Marshal(batch)
	if !batch.DeletionTimestamp.IsZero() {
		klog.Infof("rollout(%s/%s) batch(%s) is terminating, and wait a moment", r.rollout.Namespace, r.rollout.Name, string(by))
		return false, nil
	}

	//delete batchRelease
	err = r.Delete(context.TODO(), batch)
	if err != nil {
		klog.Errorf("rollout(%s/%s) delete batch(%s) failed: %s", r.rollout.Namespace, r.rollout.Name, r.batchName, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) delete batch(%s), and wait a moment", r.rollout.Namespace, r.rollout.Name, r.batchName)
	return false, nil
}

func (r *innerBatchController) deleteCanaryDeployment(batch *appsv1alpha1.BatchRelease) error {
	dList := &apps.DeploymentList{}
	if err := r.List(context.TODO(), dList, client.InNamespace(batch.Namespace),
		client.MatchingLabels(map[string]string{CanaryDeploymentLabelKey: string(batch.UID)})); err != nil {
		return err
	} else if len(dList.Items) == 0 {
		return nil
	}

	// delete all the canary deployments
	for i := range dList.Items {
		obj := &dList.Items[i]
		// clean up finalizers first
		if controllerutil.ContainsFinalizer(obj, CanaryDeploymentFinalizer) {
			newObj := obj.DeepCopy()
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				if err := r.Get(context.TODO(), types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, newObj); err != nil {
					return err
				}
				controllerutil.RemoveFinalizer(newObj, CanaryDeploymentFinalizer)
				return r.Update(context.TODO(), newObj)
			})
			if err != nil {
				klog.Errorf("remove rollout(%s/%s) canary deployment finalizer failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
				return err
			}
			klog.Infof("remove rollout(%s/%s) canary deployment(%s) finalizer success", r.rollout.Namespace, r.rollout.Name, obj.Name)
		}

		// delete deployment
		if err := r.Delete(context.TODO(), obj); err != nil {
			return err
		}
		klog.Infof("delete rollout(%s/%s) canary deployment(%s) success", r.rollout.Namespace, r.rollout.Name, obj.Name)
	}
	return nil
}

func createBatchRelease(rollout *appsv1alpha1.Rollout, batchName string) *appsv1alpha1.BatchRelease {
	var batches []appsv1alpha1.ReleaseBatch
	var lastBatch appsv1alpha1.ReleaseBatch
	for _, step := range rollout.Spec.Strategy.CanaryPlan.Steps {
		batches = append(batches, appsv1alpha1.ReleaseBatch{CanaryReplicas: intstr.FromString(strconv.Itoa(int(step.Weight)) + "%")})
		lastBatch = appsv1alpha1.ReleaseBatch{CanaryReplicas: intstr.FromString(strconv.Itoa(int(step.Weight)) + "%")}
	}
	batches = append(batches, lastBatch)

	br := &appsv1alpha1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace,
			Name:      batchName,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(rollout, schema.GroupVersionKind{
					Group:   appsv1alpha1.SchemeGroupVersion.Group,
					Version: appsv1alpha1.SchemeGroupVersion.Version,
					Kind:    "Rollout",
				}),
			},
			Annotations: map[string]string{
				BatchReleaseOwnerRefAnnotation: rollout.Name,
			},
		},
		Spec: appsv1alpha1.BatchReleaseSpec{
			TargetRef: appsv1alpha1.ObjectRef{
				Type: appsv1alpha1.WorkloadRefType,
				WorkloadRef: &appsv1alpha1.WorkloadRef{
					APIVersion: rollout.Spec.ObjectRef.WorkloadRef.APIVersion,
					Kind:       rollout.Spec.ObjectRef.WorkloadRef.Kind,
					Name:       rollout.Spec.ObjectRef.WorkloadRef.Name,
				},
			},
			ReleasePlan: appsv1alpha1.ReleasePlan{
				Batches:        batches,
				BatchPartition: utilpointer.Int32Ptr(0),
				Paused:         true,
			},
		},
	}
	return br
}

// {workload.name}-batch
func rolloutBatchName(rollout *appsv1alpha1.Rollout) string {
	return fmt.Sprintf("%s-batch", rollout.Spec.ObjectRef.WorkloadRef.Name)
}
