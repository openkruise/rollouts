package util

import (
	"encoding/json"
	"fmt"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
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
}

func GetStatefulSetPartition(object *unstructured.Unstructured) int32 {
	partition := int32(0)
	field, found, err := unstructured.NestedInt64(object.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
	if err == nil && found {
		partition = int32(field)
	}
	return partition
}

func IsStatefulSetUnorderedUpdate(object *unstructured.Unstructured) bool {
	_, found, err := unstructured.NestedFieldNoCopy(object.Object, "spec", "updateStrategy", "rollingUpdate", "unorderedUpdate")
	if err != nil || !found {
		return false
	}
	return true
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
		ObjectMeta:     *parseMetadataFrom(object),
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
		return &WorkloadStatus{
			ObservedGeneration:   int64(ParseStatusIntFrom(o, "observedGeneration")),
			Replicas:             int32(ParseStatusIntFrom(o, "replicas")),
			ReadyReplicas:        int32(ParseStatusIntFrom(o, "readyReplicas")),
			UpdatedReplicas:      int32(ParseStatusIntFrom(o, "updatedReplicas")),
			AvailableReplicas:    int32(ParseStatusIntFrom(o, "availableReplicas")),
			UpdatedReadyReplicas: int32(ParseStatusIntFrom(o, "updatedReadyReplicas")),
			UpdateRevision:       ParseStatusStringFrom(o, "updateRevision"),
			StableRevision:       ParseStatusStringFrom(o, "currentRevision"),
		}

	default:
		panic("unsupported workload type to ParseWorkloadStatus function")
	}
}

// ParseReplicasFrom parses replicas from client workload object
func ParseReplicasFrom(object client.Object) int32 {
	switch o := object.(type) {
	case *apps.Deployment:
		return *o.Spec.Replicas
	case *appsv1alpha1.CloneSet:
		return *o.Spec.Replicas
	case *unstructured.Unstructured:
		replicas := int32(1)
		field, found, err := unstructured.NestedInt64(o.Object, "spec", "replicas")
		if err == nil && found {
			replicas = int32(field)
		}
		return replicas
	default:
		panic("unsupported workload type to ParseReplicasFrom function")
	}
}

// ParseTemplateFrom parses template from unstructured workload object
func ParseTemplateFrom(object *unstructured.Unstructured) *corev1.PodTemplateSpec {
	t, found, err := unstructured.NestedFieldNoCopy(object.Object, "spec", "template")
	if err != nil || !found {
		return nil
	}
	template := &corev1.PodTemplateSpec{}
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
