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

package batchrelease

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// rollouts.kruise.io
	BatchReleaseOwnerRefLabel = "rollouts.kruise.io/owner-ref"
)

type innerBatchRelease struct {
	client.Client

	rollout *rolloutv1alpha1.Rollout

	batchName string
	rolloutID string
}

func NewInnerBatchController(c client.Client, rollout *rolloutv1alpha1.Rollout, rolloutID string) BatchRelease {
	r := &innerBatchRelease{
		Client:    c,
		rollout:   rollout,
		rolloutID: rolloutID,
		batchName: rolloutBatchName(rollout),
	}

	return r
}

func (r *innerBatchRelease) SyncRolloutID(currentID string) error {
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		batch := &rolloutv1alpha1.BatchRelease{}
		if err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch); err != nil {
			if errors.IsNotFound(err) {
				return nil // just return nil if batchRelease not exist
			}
			return err
		}
		if batch.Spec.ReleasePlan.RolloutID == currentID {
			return nil
		}
		batch.Spec.ReleasePlan.RolloutID = r.rolloutID
		if err := r.Client.Update(context.TODO(), batch); err != nil {
			return err
		}
		return nil
	}); err != nil {
		klog.Errorf("rollout(%s/%s) update batchRelease rolloutID %s failed: %s", r.rollout.Namespace, r.rollout.Name, currentID, err.Error())
		return err
	}
	return nil
}

func (r *innerBatchRelease) Verify(index int32) (bool, error) {
	index = index - 1
	batch := &rolloutv1alpha1.BatchRelease{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch)
	if errors.IsNotFound(err) {
		// create new BatchRelease Crd
		br := createBatchRelease(r.rollout, r.batchName, r.rolloutID)
		if err = r.Create(context.TODO(), br); err != nil && !errors.IsAlreadyExists(err) {
			klog.Errorf("rollout(%s/%s) create BatchRelease failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
			return false, err
		}
		data := util.DumpJSON(br)
		klog.Infof("rollout(%s/%s) create BatchRelease(%s) success", r.rollout.Namespace, r.rollout.Name, data)
		return false, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}

	// check whether batchRelease configuration is the latest
	newBr := createBatchRelease(r.rollout, r.batchName, r.rolloutID)
	if batchPlanDeepEqual(batch, newBr, index) {
		klog.Infof("rollout(%s/%s) batchRelease(generation:%d) configuration is the latest", r.rollout.Namespace, r.rollout.Name, batch.Generation)
		return true, nil
	}

	// update batchRelease to the latest version
	if err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err = r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch); err != nil {
			klog.Errorf("error getting updated BatchRelease(%s/%s) from client", batch.Namespace, batch.Name)
			return err
		}
		batch.Spec.ReleasePlan.RolloutID = r.rolloutID
		batch.Spec.ReleasePlan.Batches = newBr.Spec.ReleasePlan.Batches
		batch.Spec.ReleasePlan.BatchPartition = utilpointer.Int32Ptr(index)
		if err = r.Client.Update(context.TODO(), batch); err != nil {
			return err
		}
		return nil
	}); err != nil {
		klog.Errorf("rollout(%s/%s) update batchRelease configuration failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}
	data := util.DumpJSON(batch)
	klog.Infof("rollout(%s/%s) update batchRelease configuration(%s) to the latest", r.rollout.Namespace, r.rollout.Name, data)
	return false, nil
}

func (r *innerBatchRelease) FetchBatchRelease() (*rolloutv1alpha1.BatchRelease, error) {
	batch := &rolloutv1alpha1.BatchRelease{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch)
	if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return nil, err
	}
	return batch, nil
}

func (r *innerBatchRelease) Promote(index int32, isRollback, checkReady bool) (bool, error) {
	// Promote will resume stable workload if the last batch(index=-1) is finished
	if index == -1 {
		return r.resumeStableWorkload(checkReady)
	}

	// batch release workload's pods
	index = index - 1
	batch := &rolloutv1alpha1.BatchRelease{}
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch); err != nil {
			klog.Errorf("error getting updated BatchRelease(%s/%s) from client", batch.Namespace, batch.Name)
			return err
		}
		if IsPromoted(r.rollout, batch, isRollback) {
			return nil
		}
		if isRollback && len(r.rollout.Spec.Strategy.Canary.TrafficRoutings) == 0 {
			if batch.Annotations == nil {
				batch.Annotations = map[string]string{}
			}
			// only rollback case should update this rollout id for BatchRelease.
			batch.Spec.ReleasePlan.RolloutID = r.rolloutID
			batch.Annotations[util.RollbackInBatchAnnotation] = r.rollout.Annotations[util.RollbackInBatchAnnotation]
		}

		batch.Spec.Paused = false
		if batch.Labels == nil {
			batch.Labels = map[string]string{}
		}
		batch.Spec.ReleasePlan.BatchPartition = utilpointer.Int32Ptr(index)
		if err := r.Client.Update(context.TODO(), batch); err != nil {
			return err
		}
		klog.Infof("rollout(%s/%s) promote batchRelease BatchPartition(%d) success", r.rollout.Namespace, r.rollout.Name, index)
		return nil
	}); err != nil {
		klog.Errorf("rollout(%s/%s) promote batchRelease BatchPartition(%d) failed: %s", r.rollout.Namespace, r.rollout.Name, index, err.Error())
		return false, err
	}
	return false, nil
}

func (r *innerBatchRelease) resumeStableWorkload(checkReady bool) (bool, error) {
	// cloneSet
	switch r.rollout.Spec.ObjectRef.WorkloadRef.Kind {
	case util.ControllerKruiseKindCS.Kind:
		dName := r.rollout.Spec.ObjectRef.WorkloadRef.Name
		obj := &appsv1alpha1.CloneSet{}
		err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.rollout.Namespace, Name: dName}, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Warningf("rollout(%s/%s) cloneSet(%s) not found, and return true", r.rollout.Namespace, r.rollout.Name, dName)
				return true, nil
			}
			return false, err
		}
		// default partition.IntVal=0
		if !obj.Spec.UpdateStrategy.Paused && obj.Spec.UpdateStrategy.Partition.IntVal == 0 && obj.Spec.UpdateStrategy.Partition.Type == intstr.Int {
			return true, nil
		}

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err = r.Get(context.TODO(), types.NamespacedName{Namespace: r.rollout.Namespace, Name: dName}, obj); err != nil {
				return err
			}
			obj.Spec.UpdateStrategy.Paused = false
			obj.Spec.UpdateStrategy.Partition = nil
			return r.Update(context.TODO(), obj)
		})
		if err != nil {
			klog.Errorf("update rollout(%s/%s) cloneSet failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
			return false, err
		}
		klog.Infof("resume rollout(%s/%s) cloneSet(paused=false,partition=nil) success", r.rollout.Namespace, r.rollout.Name)
		return true, nil

	case util.ControllerKindDep.Kind:
		// deployment
		dName := r.rollout.Spec.ObjectRef.WorkloadRef.Name
		obj := &apps.Deployment{}
		err := r.Get(context.TODO(), types.NamespacedName{Namespace: r.rollout.Namespace, Name: dName}, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Warningf("rollout(%s/%s) stable deployment(%s) not found, and return true", r.rollout.Namespace, r.rollout.Name, dName)
				return true, nil
			}
			return false, err
		}
		// set deployment paused=false
		if obj.Spec.Paused {
			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err = r.Get(context.TODO(), types.NamespacedName{Namespace: r.rollout.Namespace, Name: dName}, obj); err != nil {
					return err
				}
				obj.Spec.Paused = false
				return r.Update(context.TODO(), obj)
			})
			if err != nil {
				klog.Errorf("update rollout(%s/%s) stable deployment failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
				return false, err
			}
			klog.Infof("resume rollout(%s/%s) stable deployment(paused=false) success", r.rollout.Namespace, r.rollout.Name)
		}

		// Whether to wait for pods are ready
		if !checkReady {
			return true, nil
		}
		data := util.DumpJSON(obj.Status)
		// wait for all pods are ready
		maxUnavailable, _ := intstr.GetScaledValueFromIntOrPercent(obj.Spec.Strategy.RollingUpdate.MaxUnavailable, int(*obj.Spec.Replicas), true)
		if obj.Status.ObservedGeneration != obj.Generation || obj.Status.UpdatedReplicas != *obj.Spec.Replicas ||
			obj.Status.Replicas != *obj.Spec.Replicas || *obj.Spec.Replicas-obj.Status.AvailableReplicas > int32(maxUnavailable) {
			klog.Infof("rollout(%s/%s) stable deployment status(%s), and wait a moment", r.rollout.Namespace, r.rollout.Name, data)
			return false, nil
		}
		klog.Infof("resume rollout(%s/%s) stable deployment(paused=false) status(%s) success", r.rollout.Namespace, r.rollout.Name, data)
		return true, nil

	default:
		// statefulset-like workloads
		workloadRef := r.rollout.Spec.ObjectRef.WorkloadRef
		workloadNsn := types.NamespacedName{Namespace: r.rollout.Namespace, Name: workloadRef.Name}
		workloadGVK := schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(workloadGVK)
		err := r.Get(context.TODO(), workloadNsn, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Warningf("rollout(%s/%s) statefulset(%s) not found, and return true", r.rollout.Namespace, r.rollout.Name, workloadNsn.Name)
				return true, nil
			}
			return false, err
		}

		if util.GetStatefulSetPartition(obj) == 0 {
			return true, nil
		}

		cloneObj := obj.DeepCopy()
		body := fmt.Sprintf(`{"spec":{"updateStrategy":{"rollingUpdate":{"partition":0}}}}`)
		err = r.Patch(context.TODO(), cloneObj, client.RawPatch(types.MergePatchType, []byte(body)))
		if err != nil {
			klog.Errorf("patch rollout(%s/%s) statefulset failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
			return false, err
		}
		klog.Infof("resume rollout(%s/%s) statefulset(partition=0) success", r.rollout.Namespace, r.rollout.Name)
		return true, nil
	}
}

func (r *innerBatchRelease) Finalize() (bool, error) {
	batch := &rolloutv1alpha1.BatchRelease{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: r.rollout.Namespace, Name: r.batchName}, batch)
	if err != nil && errors.IsNotFound(err) {
		klog.Infof("rollout(%s/%s) delete BatchRelease success", r.rollout.Namespace, r.rollout.Name)
		return true, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", r.rollout.Namespace, r.rollout.Name)
		return false, err
	}
	if !batch.DeletionTimestamp.IsZero() {
		klog.Infof("rollout(%s/%s) BatchRelease is terminating, and wait a moment", r.rollout.Namespace, r.rollout.Name)
		return false, nil
	}

	//delete batchRelease
	err = r.Delete(context.TODO(), batch)
	if err != nil {
		klog.Errorf("rollout(%s/%s) delete BatchRelease failed: %s", r.rollout.Namespace, r.rollout.Name, err.Error())
		return false, err
	}
	klog.Infof("rollout(%s/%s) delete BatchRelease, and wait a moment", r.rollout.Namespace, r.rollout.Name)
	return false, nil
}

func createBatchRelease(rollout *rolloutv1alpha1.Rollout, batchName, rolloutID string) *rolloutv1alpha1.BatchRelease {
	var batches []rolloutv1alpha1.ReleaseBatch
	for _, step := range rollout.Spec.Strategy.Canary.Steps {
		if step.Replicas == nil {
			batches = append(batches, rolloutv1alpha1.ReleaseBatch{CanaryReplicas: intstr.FromString(strconv.Itoa(int(*step.Weight)) + "%")})
		} else {
			batches = append(batches, rolloutv1alpha1.ReleaseBatch{CanaryReplicas: *step.Replicas})
		}
	}

	br := &rolloutv1alpha1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace,
			Name:      batchName,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(rollout, schema.GroupVersionKind{
					Group:   rolloutv1alpha1.SchemeGroupVersion.Group,
					Version: rolloutv1alpha1.SchemeGroupVersion.Version,
					Kind:    "Rollout",
				}),
			},
			Labels: map[string]string{
				BatchReleaseOwnerRefLabel: rollout.Name,
			},
		},
		Spec: rolloutv1alpha1.BatchReleaseSpec{
			TargetRef: rolloutv1alpha1.ObjectRef{
				WorkloadRef: &rolloutv1alpha1.WorkloadRef{
					APIVersion: rollout.Spec.ObjectRef.WorkloadRef.APIVersion,
					Kind:       rollout.Spec.ObjectRef.WorkloadRef.Kind,
					Name:       rollout.Spec.ObjectRef.WorkloadRef.Name,
				},
			},
			ReleasePlan: rolloutv1alpha1.ReleasePlan{
				Batches:          batches,
				RolloutID:        rolloutID,
				BatchPartition:   utilpointer.Int32Ptr(0),
				FailureThreshold: rollout.Spec.Strategy.Canary.FailureThreshold,
			},
		},
	}
	return br
}

// {workload.name}-batch
func rolloutBatchName(rollout *rolloutv1alpha1.Rollout) string {
	return rollout.Name
}

// IsPromoted return true if the current batch has been promoted:
// - 1. BatchRelease BatchPartition == Rollout currentStepIndex-1;
// - 2. Rollback annotation has been patched to BatchRelease when rolling back.
func IsPromoted(rollout *rolloutv1alpha1.Rollout, batch *rolloutv1alpha1.BatchRelease, isRollback bool) bool {
	currentBatch := int32(0)
	if rollout.Status.CanaryStatus != nil {
		currentBatch = rollout.Status.CanaryStatus.CurrentStepIndex - 1
	}

	if batch.Spec.ReleasePlan.BatchPartition == nil || *batch.Spec.ReleasePlan.BatchPartition != currentBatch {
		return false
	}

	if isRollback && batch.Annotations[util.RollbackInBatchAnnotation] != rollout.Annotations[util.RollbackInBatchAnnotation] {
		return false
	}
	return true
}

func batchPlanDeepEqual(old, new *rolloutv1alpha1.BatchRelease, currentBatch int32) bool {
	if old.Spec.ReleasePlan.BatchPartition == nil || *old.Spec.ReleasePlan.BatchPartition != currentBatch {
		return false
	}
	if old.Spec.ReleasePlan.RolloutID != new.Spec.ReleasePlan.RolloutID {
		return false
	}
	return reflect.DeepEqual(old.Spec.ReleasePlan.Batches, new.Spec.ReleasePlan.Batches)
}
