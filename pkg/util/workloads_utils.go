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
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// cloneSet pod revision label
	CloneSetPodRevisionLabelKey = "controller-revision-hash"
	// replicaSet pod revision label
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
	CloneSetGVK = kruiseappsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
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
	batchSize, _ := intstr.GetScaledValueFromIntOrPercent(&rolloutSpec.Batches[currentBatch].CanaryReplicas, workloadReplicas, true)
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

func PatchFinalizer(c client.Client, object client.Object, finalizers []string) error {
	patchByte, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"finalizers": finalizers,
		},
	})
	return c.Patch(context.TODO(), object, client.RawPatch(types.MergePatchType, patchByte))
}

func IsControlledByRollout(release *v1alpha1.BatchRelease) bool {
	owner := metav1.GetControllerOf(release)
	if owner != nil && owner.APIVersion == v1alpha1.GroupVersion.String() && owner.Kind == "Rollout" {
		return true
	}
	return false
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

func ShortRandomStr(collisionCount *int32) string {
	randStr := rand.String(3)
	return rand.SafeEncodeString(randStr)
}
