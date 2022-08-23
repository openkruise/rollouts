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

package util

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/pkg/feature"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	knownWorkloadGVKs = []*schema.GroupVersionKind{
		&ControllerKindRS,
		&ControllerKindDep,
		&ControllerKindSts,
		&ControllerKruiseKindCS,
		&ControllerKruiseKindSts,
		&ControllerKruiseOldKindSts,
	}
)

type WorkloadStatus struct {
	Replicas             int32
	ReadyReplicas        int32
	UpdatedReplicas      int32
	UpdatedReadyReplicas int32
	AvailableReplicas    int32
	ObservedGeneration   int64
	UpdateRevision       string
	StableRevision       string
}

type WorkloadInfo struct {
	metav1.ObjectMeta
	Paused         bool
	Replicas       *int32
	GVKWithName    string
	Selector       labels.Selector
	MaxUnavailable *intstr.IntOrString
	Status         *WorkloadStatus
}

func NewWorkloadInfo() *WorkloadInfo {
	return &WorkloadInfo{
		Status: &WorkloadStatus{},
	}
}

// DeepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func DeepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", objectToWrite)
}

// ComputeHash returns a hash value calculated from pod template and
// a collisionCount to avoid hash collision. The hash will be safe encoded to
// avoid bad words.
func ComputeHash(template *v1.PodTemplateSpec, collisionCount *int32) string {
	podTemplateSpecHasher := fnv.New32a()
	DeepHashObject(podTemplateSpecHasher, *template)

	// Add collisionCount in the hash if it exists.
	if collisionCount != nil {
		collisionCountBytes := make([]byte, 8)
		binary.LittleEndian.PutUint32(collisionCountBytes, uint32(*collisionCount))
		podTemplateSpecHasher.Write(collisionCountBytes)
	}

	return SafeEncodeString(fmt.Sprint(podTemplateSpecHasher.Sum32()))
}

// SafeEncodeString encodes s using the same characters as rand.String. This reduces the chances of bad words and
// ensures that strings generated from hash functions appear consistent throughout the API.
func SafeEncodeString(s string) string {
	r := make([]byte, len(s))
	for i, b := range []rune(s) {
		r[i] = alphanums[(int(b) % len(alphanums))]
	}
	return string(r)
}

// EqualIgnoreHash compare template without pod-template-hash label
func EqualIgnoreHash(template1, template2 *v1.PodTemplateSpec) bool {
	t1Copy := template1.DeepCopy()
	t2Copy := template2.DeepCopy()
	// Remove hash labels from template.Labels before comparing
	delete(t1Copy.Labels, apps.DefaultDeploymentUniqueLabelKey)
	delete(t2Copy.Labels, apps.DefaultDeploymentUniqueLabelKey)
	return apiequality.Semantic.DeepEqual(t1Copy, t2Copy)
}

// UpdateFinalizer add/remove a finalizer from a object
func UpdateFinalizer(c client.Client, object client.Object, op FinalizerOpType, finalizer string) error {
	switch op {
	case AddFinalizerOpType, RemoveFinalizerOpType:
	default:
		panic("UpdateFinalizer Func 'op' parameter must be 'Add' or 'Remove'")
	}

	key := client.ObjectKeyFromObject(object)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fetchedObject := object.DeepCopyObject().(client.Object)
		getErr := c.Get(context.TODO(), key, fetchedObject)
		if getErr != nil {
			return getErr
		}
		finalizers := fetchedObject.GetFinalizers()
		switch op {
		case AddFinalizerOpType:
			if controllerutil.ContainsFinalizer(fetchedObject, finalizer) {
				return nil
			}
			finalizers = append(finalizers, finalizer)
		case RemoveFinalizerOpType:
			finalizerSet := sets.NewString(finalizers...)
			if !finalizerSet.Has(finalizer) {
				return nil
			}
			finalizers = finalizerSet.Delete(finalizer).List()
		}
		fetchedObject.SetFinalizers(finalizers)
		return c.Update(context.TODO(), fetchedObject)
	})
}

// GetEmptyWorkloadObject return specific object based on the given gvk
func GetEmptyWorkloadObject(gvk schema.GroupVersionKind) client.Object {
	if !IsSupportedWorkload(gvk) {
		return nil
	}

	switch gvk {
	case ControllerKindRS:
		return &apps.ReplicaSet{}
	case ControllerKindDep:
		return &apps.Deployment{}
	case ControllerKruiseKindCS:
		return &appsv1alpha1.CloneSet{}
	case ControllerKindSts:
		return &apps.StatefulSet{}
	case ControllerKruiseKindSts, ControllerKruiseOldKindSts:
		return &appsv1beta1.StatefulSet{}
	default:
		unstructuredObject := &unstructured.Unstructured{}
		unstructuredObject.SetGroupVersionKind(gvk)
		return unstructuredObject
	}
}

// FilterActiveDeployment will filter out terminating deployment
func FilterActiveDeployment(ds []*apps.Deployment) []*apps.Deployment {
	var activeDs []*apps.Deployment
	for i := range ds {
		if ds[i].DeletionTimestamp == nil {
			activeDs = append(activeDs, ds[i])
		}
	}
	return activeDs
}

// GetOwnerWorkload return the top-level workload that is controlled by rollout,
// if the object has no owner, just return nil
func GetOwnerWorkload(r client.Reader, object client.Object) (client.Object, error) {
	if object == nil {
		return nil, nil
	}
	owner := metav1.GetControllerOf(object)
	// We just care about the top-level workload that is referred by rollout
	if owner == nil || len(object.GetAnnotations()[InRolloutProgressingAnnotation]) > 0 {
		return object, nil
	}

	ownerGvk := schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind)
	ownerKey := types.NamespacedName{Namespace: object.GetNamespace(), Name: owner.Name}
	ownerObj := GetEmptyWorkloadObject(ownerGvk)
	if ownerObj == nil {
		return nil, nil
	}
	err := r.Get(context.TODO(), ownerKey, ownerObj)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	return GetOwnerWorkload(r, ownerObj)
}

// IsOwnedBy will return true if the child is owned by parent directly or indirectly.
func IsOwnedBy(r client.Reader, child, parent client.Object) (bool, error) {
	if child == nil {
		return false, nil
	}
	owner := metav1.GetControllerOf(child)
	if owner == nil {
		return false, nil
	} else if owner.UID == parent.GetUID() {
		return true, nil
	}
	ownerGvk := schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind)
	ownerKey := types.NamespacedName{Namespace: child.GetNamespace(), Name: owner.Name}
	ownerObj := GetEmptyWorkloadObject(ownerGvk)
	if ownerObj == nil {
		return false, nil
	}
	err := r.Get(context.TODO(), ownerKey, ownerObj)
	if client.IgnoreNotFound(err) != nil {
		return false, err
	}
	return IsOwnedBy(r, ownerObj, parent)
}

// IsSupportedWorkload return true if the kind of workload can be processed by Rollout
func IsSupportedWorkload(gvk schema.GroupVersionKind) bool {
	if !feature.NeedFilterWorkloadType() {
		return true
	}
	for _, known := range knownWorkloadGVKs {
		if gvk.Group == known.Group && gvk.Kind == known.Kind {
			return true
		}
	}
	return false
}

// IsWorkloadType return true is object matches the workload type
func IsWorkloadType(object client.Object, t WorkloadType) bool {
	return WorkloadType(strings.ToLower(object.GetLabels()[WorkloadTypeLabel])) == t
}

// GenRandomStr returns a safe encoded string with a specific length
func GenRandomStr(length int) string {
	randStr := rand.String(length)
	return rand.SafeEncodeString(randStr)
}
