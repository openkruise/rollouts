package util

import (
	"encoding/json"
	"fmt"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ParseStatefulSetInfo(object client.Object, namespacedName types.NamespacedName) *WorkloadInfo {
	workloadGVKWithName := fmt.Sprintf("%v(%v)", object.GetObjectKind().GroupVersionKind(), namespacedName)
	selector, err := getSelector(object)
	if err != nil {
		klog.Errorf("Failed to parse selector for workload(%v)", workloadGVKWithName)
	}
	return &WorkloadInfo{
		ObjectMeta:     *getMetadata(object),
		MaxUnavailable: getStatefulSetMaxUnavailable(object),
		Replicas:       pointer.Int32(GetReplicas(object)),
		Status:         ParseWorkloadStatus(object),
		Selector:       selector,
		GVKWithName:    workloadGVKWithName,
	}
}

func IsStatefulSetRollingUpdate(object client.Object) bool {
	switch o := object.(type) {
	case *apps.StatefulSet:
		return o.Spec.UpdateStrategy.Type == "" || o.Spec.UpdateStrategy.Type == apps.RollingUpdateStatefulSetStrategyType
	case *appsv1beta1.StatefulSet:
		return o.Spec.UpdateStrategy.Type == "" || o.Spec.UpdateStrategy.Type == apps.RollingUpdateStatefulSetStrategyType
	case *unstructured.Unstructured:
		t, _, err := unstructured.NestedString(o.Object, "spec", "updateStrategy", "type")
		if err != nil {
			return false
		}
		return t == "" || t == string(apps.RollingUpdateStatefulSetStrategyType)
	default:
		panic("unsupported workload type to getStatefulSetMaxUnavailable function")
	}
}

func SetStatefulSetPartition(object client.Object, partition int32) {
	switch o := object.(type) {
	case *apps.StatefulSet:
		if o.Spec.UpdateStrategy.RollingUpdate == nil {
			o.Spec.UpdateStrategy.RollingUpdate = &apps.RollingUpdateStatefulSetStrategy{
				Partition: &partition,
			}
		} else {
			o.Spec.UpdateStrategy.RollingUpdate.Partition = &partition
		}
	case *appsv1beta1.StatefulSet:
		if o.Spec.UpdateStrategy.RollingUpdate == nil {
			o.Spec.UpdateStrategy.RollingUpdate = &appsv1beta1.RollingUpdateStatefulSetStrategy{
				Partition: &partition,
			}
		} else {
			o.Spec.UpdateStrategy.RollingUpdate.Partition = &partition
		}
	case *unstructured.Unstructured:
		spec, ok := o.Object["spec"].(map[string]interface{})
		if !ok {
			return
		}
		updateStrategy, ok := spec["updateStrategy"].(map[string]interface{})
		if !ok {
			spec["updateStrategy"] = map[string]interface{}{
				"type": apps.RollingUpdateStatefulSetStrategyType,
				"rollingUpdate": map[string]interface{}{
					"partition": int64(partition),
				},
			}
			return
		}
		rollingUpdate, ok := updateStrategy["rollingUpdate"].(map[string]interface{})
		if !ok {
			updateStrategy["rollingUpdate"] = map[string]interface{}{
				"partition": int64(partition),
			}
		} else {
			rollingUpdate["partition"] = int64(partition)
		}
	default:
		panic("unsupported workload type to getStatefulSetMaxUnavailable function")
	}
}

func GetStatefulSetPartition(object client.Object) int32 {
	partition := int32(0)
	switch o := object.(type) {
	case *apps.StatefulSet:
		if o.Spec.UpdateStrategy.RollingUpdate != nil && o.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			partition = *o.Spec.UpdateStrategy.RollingUpdate.Partition
		}
	case *appsv1beta1.StatefulSet:
		if o.Spec.UpdateStrategy.RollingUpdate != nil && o.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			partition = *o.Spec.UpdateStrategy.RollingUpdate.Partition
		}
	case *unstructured.Unstructured:
		field, found, err := unstructured.NestedInt64(o.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
		if err == nil && found {
			partition = int32(field)
		}
	default:
		panic("unsupported workload type to getStatefulSetMaxUnavailable function")
	}
	return partition
}

func IsStatefulSetUnorderedUpdate(object client.Object) bool {
	switch o := object.(type) {
	case *apps.StatefulSet:
		return false
	case *appsv1beta1.StatefulSet:
		return o.Spec.UpdateStrategy.RollingUpdate != nil && o.Spec.UpdateStrategy.RollingUpdate.UnorderedUpdate != nil
	case *unstructured.Unstructured:
		field, found, err := unstructured.NestedFieldNoCopy(o.Object, "spec", "updateStrategy", "rollingUpdate", "unorderedUpdate")
		if err != nil || !found {
			return false
		}
		return field != nil
	default:
		panic("unsupported workload type to getStatefulSetMaxUnavailable function")
	}
}

func getStatefulSetMaxUnavailable(object client.Object) *intstr.IntOrString {
	switch o := object.(type) {
	case *apps.StatefulSet:
		return nil
	case *appsv1beta1.StatefulSet:
		if o.Spec.UpdateStrategy.RollingUpdate != nil {
			return o.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable
		}
		return nil
	case *unstructured.Unstructured:
		m, found, err := unstructured.NestedFieldCopy(o.Object, "spec", "updateStrategy", "rollingUpdate", "maxUnavailable")
		if err == nil && found {
			return unmarshalIntStr(m)
		}
		return nil
	default:
		panic("unsupported workload type to getStatefulSetMaxUnavailable function")
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
			UpdateRevision:       o.Status.UpdateRevision,
			StableRevision:       o.Status.CurrentRevision,
		}

	case *apps.StatefulSet:
		return &WorkloadStatus{
			Replicas:           o.Status.Replicas,
			ReadyReplicas:      o.Status.ReadyReplicas,
			AvailableReplicas:  o.Status.AvailableReplicas,
			UpdatedReplicas:    o.Status.UpdatedReplicas,
			ObservedGeneration: o.Status.ObservedGeneration,
			UpdateRevision:     o.Status.UpdateRevision,
			StableRevision:     o.Status.CurrentRevision,
		}

	case *appsv1beta1.StatefulSet:
		return &WorkloadStatus{
			Replicas:           o.Status.Replicas,
			ReadyReplicas:      o.Status.ReadyReplicas,
			AvailableReplicas:  o.Status.AvailableReplicas,
			UpdatedReplicas:    o.Status.UpdatedReplicas,
			ObservedGeneration: o.Status.ObservedGeneration,
			UpdateRevision:     o.Status.UpdateRevision,
			StableRevision:     o.Status.CurrentRevision,
		}

	case *unstructured.Unstructured:
		return &WorkloadStatus{
			ObservedGeneration:   int64(parseStatusIntFromUnstructured(o, "observedGeneration")),
			Replicas:             int32(parseStatusIntFromUnstructured(o, "replicas")),
			ReadyReplicas:        int32(parseStatusIntFromUnstructured(o, "readyReplicas")),
			UpdatedReplicas:      int32(parseStatusIntFromUnstructured(o, "updatedReplicas")),
			AvailableReplicas:    int32(parseStatusIntFromUnstructured(o, "availableReplicas")),
			UpdatedReadyReplicas: int32(parseStatusIntFromUnstructured(o, "updatedReadyReplicas")),
			UpdateRevision:       parseStatusStringFromUnstructured(o, "updateRevision"),
			StableRevision:       parseStatusStringFromUnstructured(o, "currentRevision"),
		}

	default:
		panic("unsupported workload type to ParseWorkloadStatus function")
	}
}

// GetReplicas return replicas from client workload object
func GetReplicas(object client.Object) int32 {
	switch o := object.(type) {
	case *apps.Deployment:
		return *o.Spec.Replicas
	case *appsv1alpha1.CloneSet:
		return *o.Spec.Replicas
	case *apps.StatefulSet:
		return *o.Spec.Replicas
	case *appsv1beta1.StatefulSet:
		return *o.Spec.Replicas
	case *unstructured.Unstructured:
		return parseReplicasFromUnstructured(o)
	default:
		panic("unsupported workload type to ParseReplicasFrom function")
	}
}

// GetTemplate return pod template spec for client workload object
func GetTemplate(object client.Object) *corev1.PodTemplateSpec {
	switch o := object.(type) {
	case *apps.Deployment:
		return &o.Spec.Template
	case *appsv1alpha1.CloneSet:
		return &o.Spec.Template
	case *apps.StatefulSet:
		return &o.Spec.Template
	case *appsv1beta1.StatefulSet:
		return &o.Spec.Template
	case *unstructured.Unstructured:
		return parseTemplateFromUnstructured(o)
	default:
		panic("unsupported workload type to ParseTemplateFrom function")
	}
}

// getSelector can find labelSelector and return labels.Selector after parsed from it for client object
func getSelector(object client.Object) (labels.Selector, error) {
	switch o := object.(type) {
	case *apps.Deployment:
		return metav1.LabelSelectorAsSelector(o.Spec.Selector)
	case *appsv1alpha1.CloneSet:
		return metav1.LabelSelectorAsSelector(o.Spec.Selector)
	case *apps.StatefulSet:
		return metav1.LabelSelectorAsSelector(o.Spec.Selector)
	case *appsv1beta1.StatefulSet:
		return metav1.LabelSelectorAsSelector(o.Spec.Selector)
	case *unstructured.Unstructured:
		return parseSelectorFromUnstructured(o)
	default:
		panic("unsupported workload type to parseSelectorFrom function")
	}
}

// getMetadata can parse the whole metadata field from client workload object
func getMetadata(object client.Object) *metav1.ObjectMeta {
	switch o := object.(type) {
	case *apps.Deployment:
		return &o.ObjectMeta
	case *appsv1alpha1.CloneSet:
		return &o.ObjectMeta
	case *apps.StatefulSet:
		return &o.ObjectMeta
	case *appsv1beta1.StatefulSet:
		return &o.ObjectMeta
	case *unstructured.Unstructured:
		return parseMetadataFromUnstructured(o)
	default:
		panic("unsupported workload type to ParseSelector function")
	}
}

// parseReplicasFromUnstructured parses replicas from unstructured workload object
func parseReplicasFromUnstructured(object *unstructured.Unstructured) int32 {
	replicas := int32(1)
	field, found, err := unstructured.NestedInt64(object.Object, "spec", "replicas")
	if err == nil && found {
		replicas = int32(field)
	}
	return replicas
}

// ParseStatusIntFromUnstructured can parse some fields with int type from unstructured workload object status
func parseStatusIntFromUnstructured(object *unstructured.Unstructured, field string) int64 {
	value, found, err := unstructured.NestedInt64(object.Object, "status", field)
	if err == nil && found {
		return value
	}
	return 0
}

// ParseStatusStringFromUnstructured can parse some fields with string type from unstructured workload object status
func parseStatusStringFromUnstructured(object *unstructured.Unstructured, field string) string {
	value, found, err := unstructured.NestedFieldNoCopy(object.Object, "status", field)
	if err == nil && found {
		return value.(string)
	}
	return ""
}

// parseSelectorFromUnstructured can parse labelSelector as selector from unstructured workload object
func parseSelectorFromUnstructured(object *unstructured.Unstructured) (labels.Selector, error) {
	m, found, err := unstructured.NestedFieldNoCopy(object.Object, "spec", "selector")
	if err != nil || !found {
		return nil, err
	}
	byteInfo, _ := json.Marshal(m)
	labelSelector := &metav1.LabelSelector{}
	_ = json.Unmarshal(byteInfo, labelSelector)
	return metav1.LabelSelectorAsSelector(labelSelector)
}

// parseTemplateFromUnstructured can parse pod template from unstructured workload object
func parseTemplateFromUnstructured(object *unstructured.Unstructured) *corev1.PodTemplateSpec {
	t, found, err := unstructured.NestedFieldNoCopy(object.Object, "spec", "template")
	if err != nil || !found {
		return nil
	}
	template := &corev1.PodTemplateSpec{}
	templateByte, _ := json.Marshal(t)
	_ = json.Unmarshal(templateByte, template)
	return template
}

// parseMetadata can parse the whole metadata field from client workload object
func parseMetadataFromUnstructured(object *unstructured.Unstructured) *metav1.ObjectMeta {
	m, found, err := unstructured.NestedMap(object.Object, "metadata")
	if err != nil || !found {
		return nil
	}
	data, _ := json.Marshal(m)
	meta := &metav1.ObjectMeta{}
	_ = json.Unmarshal(data, meta)
	return meta
}

func unmarshalIntStr(m interface{}) *intstr.IntOrString {
	field := &intstr.IntOrString{}
	data, _ := json.Marshal(m)
	_ = json.Unmarshal(data, field)
	return field
}
