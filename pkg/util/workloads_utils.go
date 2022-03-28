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
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"

	"github.com/davecgh/go-spew/spew"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// workload pod revision label
	RsPodRevisionLabelKey         = "pod-template-hash"
	CanaryDeploymentLabel         = "rollouts.kruise.io/canary-deployment"
	BatchReleaseControlAnnotation = "batchrelease.rollouts.kruise.io/control-info"
	StashCloneSetPartition        = "batchrelease.rollouts.kruise.io/stash-partition"
	CanaryDeploymentLabelKey      = "rollouts.kruise.io/canary-deployment"
	CanaryDeploymentFinalizer     = "finalizer.rollouts.kruise.io/batch-release"

	// We omit vowels from the set of available characters to reduce the chances
	// of "bad words" being formed.
	alphanums = "bcdfghjklmnpqrstvwxz2456789"
)

var (
	CloneSetGVK = appsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
)

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

func IsControlledBy(object, owner metav1.Object) bool {
	controlInfo, controlled := object.GetAnnotations()[BatchReleaseControlAnnotation]
	if !controlled {
		return false
	}

	o := &metav1.OwnerReference{}
	if err := json.Unmarshal([]byte(controlInfo), o); err != nil {
		return false
	}

	return o.UID == owner.GetUID()
}

func CalculateNewBatchTarget(rolloutSpec *v1alpha1.ReleasePlan, workloadReplicas, currentBatch int) int {
	batchSize, _ := intstr.GetValueFromIntOrPercent(&rolloutSpec.Batches[currentBatch].CanaryReplicas, workloadReplicas, true)
	if batchSize > workloadReplicas {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is more than workload.replicas %v", currentBatch, batchSize, workloadReplicas)
		batchSize = workloadReplicas
	} else if batchSize < 0 {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is less than 0 %v", currentBatch, batchSize)
		batchSize = 0
	}

	klog.V(3).InfoS("calculated the number of new pod size", "current batch", currentBatch,
		"new pod target", batchSize)
	return batchSize
}

func EqualIgnoreHash(template1, template2 *v1.PodTemplateSpec) bool {
	t1Copy := template1.DeepCopy()
	t2Copy := template2.DeepCopy()
	// Remove hash labels from template.Labels before comparing
	delete(t1Copy.Labels, apps.DefaultDeploymentUniqueLabelKey)
	delete(t2Copy.Labels, apps.DefaultDeploymentUniqueLabelKey)
	return apiequality.Semantic.DeepEqual(t1Copy, t2Copy)
}

type FinalizerOpType string

const (
	AddFinalizerOpType    FinalizerOpType = "Add"
	RemoveFinalizerOpType FinalizerOpType = "Remove"
)

func UpdateFinalizer(c client.Client, object client.Object, op FinalizerOpType, finalizer string) error {
	switch op {
	case AddFinalizerOpType, RemoveFinalizerOpType:
	default:
		panic(fmt.Sprintf("UpdateFinalizer Func 'op' parameter must be 'Add' or 'Remove'"))
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

func FilterActiveDeployment(ds []*apps.Deployment) []*apps.Deployment {
	var activeDs []*apps.Deployment
	for i := range ds {
		if ds[i].DeletionTimestamp == nil {
			activeDs = append(activeDs, ds[i])
		}
	}
	return activeDs
}

func GenRandomStr(length int) string {
	randStr := rand.String(length)
	return rand.SafeEncodeString(randStr)
}
