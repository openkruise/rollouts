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
	"strings"

	"github.com/davecgh/go-spew/spew"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
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
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// BatchReleaseControlAnnotation is controller info about batchRelease when rollout
	BatchReleaseControlAnnotation = "batchrelease.rollouts.kruise.io/control-info"
	// CanaryDeploymentLabel is to label canary deployment that is created by batchRelease controller
	CanaryDeploymentLabel = "rollouts.kruise.io/canary-deployment"
	// CanaryDeploymentFinalizer is a finalizer to resources patched by batchRelease controller
	CanaryDeploymentFinalizer = "finalizer.rollouts.kruise.io/batch-release"

	// We omit vowels from the set of available characters to reduce the chances
	// of "bad words" being formed.
	alphanums = "bcdfghjklmnpqrstvwxz2456789"
)

type WorkloadType string

const (
	StatefulSetType WorkloadType = "statefulset"
	DeploymentType  WorkloadType = "deployment"
	CloneSetType    WorkloadType = "cloneset"
)

var (
	knownWorkloadGVKs = []*schema.GroupVersionKind{
		&ControllerKindRS,
		&ControllerKindDep,
		&ControllerKindSts,
		&ControllerKruiseKindCS,
		&ControllerKruiseKindSts,
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
	by, _ := json.Marshal(releasePlan)
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

func PatchSpec(c client.Client, object client.Object, spec map[string]interface{}) error {
	patchByte, err := json.Marshal(map[string]interface{}{"spec": spec})
	if err != nil {
		return err
	}
	clone := object.DeepCopyObject().(client.Object)
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

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
	default:
		unstructuredObject := &unstructured.Unstructured{}
		unstructuredObject.SetGroupVersionKind(gvk)
		return unstructuredObject
	}
}

func ReleaseWorkload(c client.Client, object client.Object) error {
	_, found := object.GetAnnotations()[BatchReleaseControlAnnotation]
	if !found {
		klog.V(3).Infof("Workload(%v) is already released", client.ObjectKeyFromObject(object))
		return nil
	}

	clone := object.DeepCopyObject().(client.Object)
	patchByte := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, BatchReleaseControlAnnotation))
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

func IsStatefulSetRollingUpdate(object *unstructured.Unstructured) bool {
	t, _, err := unstructured.NestedString(object.Object, "spec", "updateStrategy", "type")
	if err != nil {
		return false
	}
	return t == "" || t == string(apps.RollingUpdateStatefulSetStrategyType)
}

func SetStatefulSetPartition(object *unstructured.Unstructured, partition int32) {
	o := object.Object
	spec, ok := o["spec"].(map[string]interface{})
	if !ok {
		return
	}
	updateStrategy, ok := spec["updateStrategy"].(map[string]interface{})
	if !ok {
		spec["updateStrategy"] = map[string]interface{}{
			"type": apps.RollingUpdateStatefulSetStrategyType,
			"rollingUpdate": map[string]interface{}{
				"partition": pointer.Int32(partition),
			},
		}
		return
	}
	rollingUpdate, ok := updateStrategy["rollingUpdate"].(map[string]interface{})
	if !ok {
		updateStrategy["rollingUpdate"] = map[string]interface{}{
			"partition": pointer.Int32(partition),
		}
	} else {
		rollingUpdate["partition"] = pointer.Int32(partition)
	}
}

func GetStatefulSetPartition(object *unstructured.Unstructured) int32 {
	partition := int32(0)
	field, found, err := unstructured.NestedInt64(object.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
	if err == nil && found {
		partition = int32(field)
	}
	return partition
}

func GetStatefulSetMaxUnavailable(object *unstructured.Unstructured) *intstr.IntOrString {
	m, found, err := unstructured.NestedFieldCopy(object.Object, "spec", "updateStrategy", "rollingUpdate", "maxUnavailable")
	if err == nil && found {
		return unmarshalIntStr(m)
	}
	return nil
}

func ParseStatefulSetInfo(object *unstructured.Unstructured, namespacedName types.NamespacedName) *WorkloadInfo {
	workloadGVKWithName := fmt.Sprintf("%v(%v)", object.GroupVersionKind().String(), namespacedName)
	selector, err := parseSelector(object)
	if err != nil {
		klog.Errorf("Failed to parse selector for workload(%v)", workloadGVKWithName)
	}
	return &WorkloadInfo{
		Metadata:       parseMetadataFrom(object),
		MaxUnavailable: GetStatefulSetMaxUnavailable(object),
		Replicas:       pointer.Int32(ParseReplicasFrom(object)),
		GVKWithName:    workloadGVKWithName,
		Selector:       selector,
		Status:         ParseWorkloadStatus(object),
	}
}

func ParseWorkloadStatus(object client.Object) *WorkloadStatus {
	switch o := object.(type) {
	case *apps.Deployment:
		return &WorkloadStatus{
			Replicas:           o.Status.Replicas,
			ReadyReplicas:      o.Status.ReadyReplicas,
			AvailableReplicas:  o.Status.AvailableReplicas,
			UpdatedReplicas:    o.Status.UpdatedReplicas,
			ObservedGeneration: o.Status.ObservedGeneration,
		}

	case *appsv1alpha1.CloneSet:
		return &WorkloadStatus{
			Replicas:             o.Status.Replicas,
			ReadyReplicas:        o.Status.ReadyReplicas,
			AvailableReplicas:    o.Status.AvailableReplicas,
			UpdatedReplicas:      o.Status.UpdatedReplicas,
			UpdatedReadyReplicas: o.Status.UpdatedReadyReplicas,
			ObservedGeneration:   o.Status.ObservedGeneration,
		}

	case *unstructured.Unstructured:
		updateRevision := ParseStatusStringFrom(o, "updateRevision")
		if len(updateRevision) > 0 {
			updateRevision = updateRevision[len(o.GetName())+1:]
		}
		stableRevision := ParseStatusStringFrom(o, "currentRevision")
		if len(stableRevision) > 0 {
			stableRevision = stableRevision[len(o.GetName())+1:]
		}
		return &WorkloadStatus{
			ObservedGeneration:   int64(ParseStatusIntFrom(o, "observedGeneration")),
			Replicas:             int32(ParseStatusIntFrom(o, "replicas")),
			ReadyReplicas:        int32(ParseStatusIntFrom(o, "readyReplicas")),
			UpdatedReplicas:      int32(ParseStatusIntFrom(o, "updatedReplicas")),
			AvailableReplicas:    int32(ParseStatusIntFrom(o, "availableReplicas")),
			UpdatedReadyReplicas: int32(ParseStatusIntFrom(o, "updatedReadyReplicas")),
			UpdateRevision:       updateRevision,
			StableRevision:       stableRevision,
		}

	default:
		panic("unsupported workload type to ParseWorkloadStatus function")
	}
}

// ParseReplicasFrom parses replicas from unstructured workload object
func ParseReplicasFrom(object *unstructured.Unstructured) int32 {
	replicas := int32(1)
	field, found, err := unstructured.NestedInt64(object.Object, "spec", "replicas")
	if err == nil && found {
		replicas = int32(field)
	}
	return replicas
}

// ParseTemplateFrom parses template from unstructured workload object
func ParseTemplateFrom(object *unstructured.Unstructured) *v1.PodTemplateSpec {
	t, found, err := unstructured.NestedFieldNoCopy(object.Object, "spec", "template")
	if err != nil || !found {
		return nil
	}
	template := &v1.PodTemplateSpec{}
	templateByte, _ := json.Marshal(t)
	_ = json.Unmarshal(templateByte, template)
	return template
}

// ParseStatusIntFrom can parse some fields with int type from unstructured workload object status
func ParseStatusIntFrom(object *unstructured.Unstructured, field string) int64 {
	value, found, err := unstructured.NestedInt64(object.Object, "status", field)
	if err == nil && found {
		return value
	}
	return 0
}

// ParseStatusStringFrom can parse some fields with string type from unstructured workload object status
func ParseStatusStringFrom(object *unstructured.Unstructured, field string) string {
	value, found, err := unstructured.NestedFieldNoCopy(object.Object, "status", field)
	if err == nil && found {
		return value.(string)
	}
	return ""
}

// parseMetadataFrom can parse the whole metadata field from unstructured workload object
func parseMetadataFrom(object *unstructured.Unstructured) *metav1.ObjectMeta {
	m, found, err := unstructured.NestedMap(object.Object, "metadata")
	if err != nil || !found {
		return nil
	}
	data, _ := json.Marshal(m)
	meta := &metav1.ObjectMeta{}
	_ = json.Unmarshal(data, meta)
	return meta
}

// parseSelector can find labelSelector and parse it as labels.Selector for client object
func parseSelector(object client.Object) (labels.Selector, error) {
	switch o := object.(type) {
	case *apps.Deployment:
		return metav1.LabelSelectorAsSelector(o.Spec.Selector)
	case *appsv1alpha1.CloneSet:
		return metav1.LabelSelectorAsSelector(o.Spec.Selector)
	case *unstructured.Unstructured:
		m, found, err := unstructured.NestedFieldNoCopy(o.Object, "spec", "selector")
		if err != nil || !found {
			return nil, err
		}
		byteInfo, _ := json.Marshal(m)
		labelSelector := &metav1.LabelSelector{}
		_ = json.Unmarshal(byteInfo, labelSelector)
		return metav1.LabelSelectorAsSelector(labelSelector)
	default:
		panic("unsupported workload type to ParseSelector function")
	}

}

func unmarshalIntStr(m interface{}) *intstr.IntOrString {
	field := &intstr.IntOrString{}
	data, _ := json.Marshal(m)
	_ = json.Unmarshal(data, field)
	return field
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

// GetOwnerWorkload return the top-level workload that is controlled by rollout,
// if the object has no owner, just return nil
func GetOwnerWorkload(r client.Reader, object client.Object) (client.Object, error) {
	if object == nil {
		return nil, nil
	}
	owner := metav1.GetControllerOf(object)
	// We just care about the top-level workload that is referred by rollout
	if owner == nil || len(object.GetAnnotations()[InRolloutProgressingAnnotation]) > 0 {
		return nil, nil
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

func IsWorkloadType(object client.Object, t WorkloadType) bool {
	return WorkloadType(strings.ToLower(object.GetLabels()[WorkloadTypeLabel])) == t
}
