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
	"sort"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/feature"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
		&ControllerKruiseKindDS,
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
	metav1.TypeMeta
	metav1.ObjectMeta
	LogKey   string
	Replicas int32
	Status   WorkloadStatus
}

// IsStable return ture if observed generation >= generation
func (w *WorkloadInfo) IsStable() bool {
	return w.Status.ObservedGeneration >= w.Generation
}

// IsPromoted return true if replicas == updatedReplicas
func (w *WorkloadInfo) IsPromoted() bool {
	return w.Status.Replicas == w.Status.UpdatedReplicas
}

// IsScaling return true if observed replicas != replicas
func (w *WorkloadInfo) IsScaling(observed int32) bool {
	if observed == -1 {
		return false
	}
	return w.Replicas != observed
}

// IsRollback return true if workload stable revision equals to update revision.
// this function is edge-triggerred.
func (w *WorkloadInfo) IsRollback(observedStable, observedUpdate string) bool {
	if observedUpdate == "" {
		return false
	}
	// updateRevision == CurrentRevision means CloneSet is rolling back or newly-created.
	return w.Status.UpdateRevision == w.Status.StableRevision &&
		// stableRevision == UpdateRevision means CloneSet is rolling back instead of newly-created.
		observedStable == w.Status.UpdateRevision &&
		// StableRevision != observed UpdateRevision means the rollback event have not been observed.
		observedStable != observedUpdate
}

// IsRevisionNotEqual this function will return true if observed update revision != update revision.
func (w *WorkloadInfo) IsRevisionNotEqual(observed string) bool {
	if observed == "" {
		return false
	}
	return w.Status.UpdateRevision != observed
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
		r[i] = alphanums[int(b)%len(alphanums)]
	}
	return string(r)
}

func EqualIgnoreSpecifyMetadata(template1, template2 *v1.PodTemplateSpec, ignoreLabels, ignoreAnno []string) bool {
	t1Copy := template1.DeepCopy()
	t2Copy := template2.DeepCopy()
	if ignoreLabels == nil {
		ignoreLabels = make([]string, 0)
	}
	// default remove the hash label
	ignoreLabels = append(ignoreLabels, apps.DefaultDeploymentUniqueLabelKey)
	for _, k := range ignoreLabels {
		delete(t1Copy.Labels, k)
		delete(t2Copy.Labels, k)
	}
	for _, k := range ignoreAnno {
		delete(t1Copy.Annotations, k)
		delete(t2Copy.Annotations, k)
	}
	return apiequality.Semantic.DeepEqual(t1Copy, t2Copy)
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
	case ControllerKruiseKindDS:
		return &appsv1alpha1.DaemonSet{}
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

// DeploymentMaxUnavailable returns the maximum unavailable pods a rolling deployment can take.
func DeploymentMaxUnavailable(deployment *apps.Deployment) int32 {
	strategy := deployment.Spec.Strategy
	if strategy.Type != apps.RollingUpdateDeploymentStrategyType || *deployment.Spec.Replicas == 0 {
		return int32(0)
	}
	// Error caught by validation
	_, maxUnavailable, _ := resolveFenceposts(strategy.RollingUpdate.MaxSurge, strategy.RollingUpdate.MaxUnavailable, *deployment.Spec.Replicas)
	if maxUnavailable > *deployment.Spec.Replicas {
		return *deployment.Spec.Replicas
	}
	return maxUnavailable
}

// resolveFenceposts resolves both maxSurge and maxUnavailable. This needs to happen in one
// step. For example:
//
// 2 desired, max unavailable 1%, surge 0% - should scale old(-1), then new(+1), then old(-1), then new(+1)
// 1 desired, max unavailable 1%, surge 0% - should scale old(-1), then new(+1)
// 2 desired, max unavailable 25%, surge 1% - should scale new(+1), then old(-1), then new(+1), then old(-1)
// 1 desired, max unavailable 25%, surge 1% - should scale new(+1), then old(-1)
// 2 desired, max unavailable 0%, surge 1% - should scale new(+1), then old(-1), then new(+1), then old(-1)
// 1 desired, max unavailable 0%, surge 1% - should scale new(+1), then old(-1)
func resolveFenceposts(maxSurge, maxUnavailable *intstr.IntOrString, desired int32) (int32, int32, error) {
	surge, err := intstr.GetScaledValueFromIntOrPercent(intstr.ValueOrDefault(maxSurge, intstr.FromInt(0)), int(desired), true)
	if err != nil {
		return 0, 0, err
	}
	unavailable, err := intstr.GetScaledValueFromIntOrPercent(intstr.ValueOrDefault(maxUnavailable, intstr.FromInt(0)), int(desired), false)
	if err != nil {
		return 0, 0, err
	}

	if surge == 0 && unavailable == 0 {
		// Validation should never allow the user to explicitly use zero values for both maxSurge
		// maxUnavailable. Due to rounding down maxUnavailable though, it may resolve to zero.
		// If both fenceposts resolve to zero, then we should set maxUnavailable to 1 on the
		// theory that surge might not work due to quota.
		unavailable = 1
	}

	return int32(surge), int32(unavailable), nil
}

// GetEmptyObjectWithKey return an empty object with the same namespaced name
func GetEmptyObjectWithKey(object client.Object) client.Object {
	var empty client.Object
	switch object.(type) {
	case *v1.Pod:
		empty = &v1.Pod{}
	case *v1.Service:
		empty = &v1.Service{}
	case *netv1.Ingress:
		empty = &netv1.Ingress{}
	case *apps.Deployment:
		empty = &apps.Deployment{}
	case *apps.ReplicaSet:
		empty = &apps.ReplicaSet{}
	case *apps.StatefulSet:
		empty = &apps.StatefulSet{}
	case *appsv1alpha1.CloneSet:
		empty = &appsv1alpha1.CloneSet{}
	case *appsv1beta1.StatefulSet:
		empty = &appsv1beta1.StatefulSet{}
	case *appsv1alpha1.DaemonSet:
		empty = &appsv1alpha1.DaemonSet{}
	case *unstructured.Unstructured:
		unstructure := &unstructured.Unstructured{}
		unstructure.SetGroupVersionKind(object.GetObjectKind().GroupVersionKind())
		empty = unstructure
	}
	empty.SetName(object.GetName())
	empty.SetNamespace(object.GetNamespace())
	return empty
}

// GetDeploymentStrategy decode the strategy object for advanced deployment
// from the annotation rollouts.kruise.io/deployment-strategy
func GetDeploymentStrategy(deployment *apps.Deployment) v1alpha1.DeploymentStrategy {
	strategy := v1alpha1.DeploymentStrategy{}
	if deployment == nil {
		return strategy
	}
	strategyStr := deployment.Annotations[v1alpha1.DeploymentStrategyAnnotation]
	if strategyStr == "" {
		return strategy
	}
	_ = json.Unmarshal([]byte(strategyStr), &strategy)
	return strategy
}

// GetDeploymentExtraStatus decode the extra-status object for advanced deployment
// from the annotation rollouts.kruise.io/deployment-extra-status
func GetDeploymentExtraStatus(deployment *apps.Deployment) v1alpha1.DeploymentExtraStatus {
	extraStatus := v1alpha1.DeploymentExtraStatus{}
	if deployment == nil {
		return extraStatus
	}
	extraStatusStr := deployment.Annotations[v1alpha1.DeploymentExtraStatusAnnotation]
	if extraStatusStr == "" {
		return extraStatus
	}
	_ = json.Unmarshal([]byte(extraStatusStr), &extraStatus)
	return extraStatus
}

// FindCanaryAndStableReplicaSet find the canary and stable replicaset for the deployment
// - canary replicaset: the template equals to deployment's;
// - stable replicaset: an active replicaset(replicas>0) with the smallest revision.
func FindCanaryAndStableReplicaSet(rss []*apps.ReplicaSet, d *apps.Deployment) (*apps.ReplicaSet, *apps.ReplicaSet) {
	// sort replicas set by revision ordinals
	sort.Slice(rss, func(i, j int) bool {
		revision1, err1 := strconv.Atoi(rss[i].Annotations[DeploymentRevisionAnnotation])
		revision2, err2 := strconv.Atoi(rss[j].Annotations[DeploymentRevisionAnnotation])
		if err1 != nil || err2 != nil || revision1 == revision2 {
			return rss[i].CreationTimestamp.Before(&rss[j].CreationTimestamp)
		}
		return revision1 < revision2
	})

	var newRS *apps.ReplicaSet
	var oldRS *apps.ReplicaSet
	for _, rs := range rss {
		if EqualIgnoreHash(&rs.Spec.Template, &d.Spec.Template) {
			newRS = rs
		} else if oldRS == nil && *rs.Spec.Replicas > 0 {
			oldRS = rs
		}
	}
	return newRS, oldRS
}
