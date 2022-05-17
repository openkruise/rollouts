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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"

	"github.com/davecgh/go-spew/spew"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1alpha1"
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
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
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
	CloneSetGVK     = appsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
	AStatefulSetGVK = appsv1beta1.SchemeGroupVersion.WithKind("StatefulSet")
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
	Paused         bool
	Replicas       *int32
	GVKWithName    string
	Selector       labels.Selector
	MaxUnavailable *intstr.IntOrString
	Metadata       *metav1.ObjectMeta
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

func HashReleasePlanBatches(releasePlan *v1alpha1.ReleasePlan) string {
	by, _ := json.Marshal(releasePlan.Batches)
	md5Hash := sha256.Sum256(by)
	return hex.EncodeToString(md5Hash[:])
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

func PatchUpdateStrategy(c client.Client, object client.Object, updateStrategy map[string]interface{}) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"updateStrategy": updateStrategy,
		},
	}

	patchByte, _ := json.Marshal(patch)
	clone := object.DeepCopyObject().(client.Object)
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

func ReleaseWorkload(c client.Client, object client.Object) error {
	_, found := object.GetAnnotations()[BatchReleaseControlAnnotation]
	if !found {
		klog.V(3).Infof("Workload(%v) is already released", client.ObjectKeyFromObject(object))
		return nil
	}

	clone := object.DeepCopyObject().(client.Object)
	patchByte := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null,"%s":null}}}`,
		BatchReleaseControlAnnotation,
		WorkloadRollingUpdateAnnotation))
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

func ClaimWorkload(c client.Client, planController *v1alpha1.BatchRelease, object client.Object, patchUpdateStrategy map[string]interface{}) error {
	if controlInfo, ok := object.GetAnnotations()[BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == planController.UID {
			klog.V(3).Infof("Workload(%v) has been controlled by this BatchRelease(%v), no need to claim again",
				client.ObjectKeyFromObject(object), client.ObjectKeyFromObject(planController))
			return nil
		} else {
			klog.Errorf("Failed to parse controller info from Workload(%v) annotation, error: %v, controller info: %+v",
				client.ObjectKeyFromObject(object), err, *ref)
		}
	}

	controlInfo, _ := json.Marshal(metav1.NewControllerRef(planController, planController.GetObjectKind().GroupVersionKind()))
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				BatchReleaseControlAnnotation: string(controlInfo),
			},
		},
		"spec": map[string]interface{}{
			"updateStrategy": patchUpdateStrategy,
		},
	}

	patchByte, _ := json.Marshal(patch)
	clone := object.DeepCopyObject().(client.Object)
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

func IsRollingUpdateStrategy(object *unstructured.Unstructured) bool {
	t, _, err := unstructured.NestedString(object.Object, "spec", "updateStrategy", "type")
	if err != nil {
		return false
	}
	return t == "" || t == string(apps.RollingUpdateStatefulSetStrategyType)
}

func ParseReplicasFrom(object *unstructured.Unstructured) int32 {
	replicas := int32(1)
	field, found, err := unstructured.NestedInt64(object.Object, "spec", "replicas")
	if err == nil && found {
		replicas = int32(field)
	}
	return replicas
}

func ParseInt32PartitionFrom(object *unstructured.Unstructured) int32 {
	partition := int32(0)
	field, found, err := unstructured.NestedInt64(object.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
	if err == nil && found {
		partition = int32(field)
	}
	return partition
}

func ParseStatusIntFrom(object *unstructured.Unstructured, field string) int64 {
	value, found, err := unstructured.NestedInt64(object.Object, "status", field)
	if err == nil && found {
		return value
	}
	return 0
}

func ParseStatusStringFrom(object *unstructured.Unstructured, field string) string {
	value, found, err := unstructured.NestedFieldNoCopy(object.Object, "status", field)
	if err == nil && found {
		return value.(string)
	}
	return ""
}

func ParseMetadataFrom(object *unstructured.Unstructured) *metav1.ObjectMeta {
	m, found, err := unstructured.NestedMap(object.Object, "metadata")
	if err != nil || !found {
		return nil
	}
	data, _ := json.Marshal(m)
	meta := &metav1.ObjectMeta{}
	_ = json.Unmarshal(data, meta)
	return meta
}

func ParseMaxUnavailableFrom(object *unstructured.Unstructured) *intstr.IntOrString {
	// case 1: object is statefulset
	m, found, err := unstructured.NestedFieldCopy(object.Object, "spec", "updateStrategy", "rollingUpdate", "maxUnavailable")
	if err == nil && found {
		return parseIntStr(m)
	}

	// case2: object is cloneset
	m, found, err = unstructured.NestedFieldCopy(object.Object, "spec", "updateStrategy", "maxUnavailable")
	if err == nil && found {
		return parseIntStr(m)
	}

	// case3: object is deployment
	m, found, err = unstructured.NestedFieldCopy(object.Object, "spec", "strategy", "rollingUpdate", "maxUnavailable")
	if err == nil && found {
		return parseIntStr(m)
	}

	return nil
}

func ParseExpectedUpdatedReplicasFrom(object *unstructured.Unstructured) int32 {
	m, found, err := unstructured.NestedInt64(object.Object, "status", "expectedUpdatedReplicas")
	if err == nil && found {
		return int32(m)
	}

	return integer.Int32Max(ParseReplicasFrom(object)-ParsePartitionFrom(object), 0)
}

func ParseSelector(object *unstructured.Unstructured) (labels.Selector, error) {
	m, found, err := unstructured.NestedFieldNoCopy(object.Object, "spec", "selector")
	if err != nil || !found {
		return nil, err
	}
	byteInfo, _ := json.Marshal(m)
	labelSelector := &metav1.LabelSelector{}
	_ = json.Unmarshal(byteInfo, labelSelector)
	return metav1.LabelSelectorAsSelector(labelSelector)
}

func GetEmptyWorkloadObject(gvk schema.GroupVersionKind) client.Object {
	switch gvk.Kind {
	case ControllerKindDep.Kind:
		return &apps.Deployment{}
	case ControllerKruiseKindCS.Kind:
		return &appsv1alpha1.CloneSet{}
	default:
		unstructuredObject := &unstructured.Unstructured{}
		unstructuredObject.SetGroupVersionKind(gvk)
		return unstructuredObject
	}
}

func ParsePartitionFrom(object *unstructured.Unstructured) int32 {
	replicas := ParseReplicasFrom(object)

	// case 1: object is statefulset
	m, found, err := unstructured.NestedInt64(object.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
	if err == nil && found {
		return int32(m)
	}

	// case2: object is cloneset
	v, found, err := unstructured.NestedFieldCopy(object.Object, "spec", "updateStrategy", "partition")
	if err == nil && found {
		pValue, err := intstr.GetScaledValueFromIntOrPercent(parseIntStr(v), int(replicas), true)
		if err == nil {
			return int32(pValue)
		}
	}

	// case3: object is deployment
	v, found, err = unstructured.NestedFieldCopy(object.Object, "spec", "strategy", "rollingUpdate", "partition")
	if err == nil && found {
		pValue, err := intstr.GetScaledValueFromIntOrPercent(parseIntStr(v), int(replicas), true)
		if err == nil {
			return int32(pValue)
		}
	}

	return 0
}

func parseIntStr(m interface{}) *intstr.IntOrString {
	field := &intstr.IntOrString{}
	data, _ := json.Marshal(m)
	_ = json.Unmarshal(data, field)
	return field
}

func ParseWorkloadInfo(object *unstructured.Unstructured, namespacedName types.NamespacedName) *WorkloadInfo {
	workloadGVKWithName := fmt.Sprintf("%v(%v)", object.GroupVersionKind().String(), namespacedName)
	updateRevision := ParseStatusStringFrom(object, "updateRevision")
	if len(updateRevision) > 0 {
		updateRevision = updateRevision[len(object.GetName())+1:]
	}
	stableRevision := ParseStatusStringFrom(object, "currentRevision")
	if len(stableRevision) > 0 {
		stableRevision = stableRevision[len(object.GetName())+1:]
	}
	selector, err := ParseSelector(object)
	if err != nil {
		klog.Errorf("Failed to parse selector for workload(%v)", workloadGVKWithName)
	}
	return &WorkloadInfo{
		Metadata:       ParseMetadataFrom(object),
		MaxUnavailable: ParseMaxUnavailableFrom(object),
		Replicas:       pointer.Int32(ParseReplicasFrom(object)),
		GVKWithName:    workloadGVKWithName,
		Selector:       selector,
		Status: &WorkloadStatus{
			ObservedGeneration:   int64(ParseStatusIntFrom(object, "observedGeneration")),
			Replicas:             int32(ParseStatusIntFrom(object, "replicas")),
			ReadyReplicas:        int32(ParseStatusIntFrom(object, "readyReplicas")),
			UpdatedReplicas:      int32(ParseStatusIntFrom(object, "updatedReplicas")),
			AvailableReplicas:    int32(ParseStatusIntFrom(object, "availableReplicas")),
			UpdatedReadyReplicas: int32(ParseStatusIntFrom(object, "updatedReadyReplicas")),
			UpdateRevision:       updateRevision,
			StableRevision:       stableRevision,
		},
	}
}

func ParseUpdateStrategyType(object *unstructured.Unstructured) string {
	t, found, err := unstructured.NestedString(object.Object, "spec", "updateStrategy", "type")
	if err == nil && found {
		return t
	}

	t, found, err = unstructured.NestedString(object.Object, "spec", "strategy", "type")
	if err == nil && found {
		return t
	}
	return ""
}
