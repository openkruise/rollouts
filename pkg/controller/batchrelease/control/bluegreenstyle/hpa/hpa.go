package hpa

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	HPADisableSuffix = "-DisableByRollout"
)

func DisableHPA(cli client.Client, object client.Object) error {
	hpa := findHPAForWorkload(cli, object)
	if hpa == nil {
		return nil
	}
	targetRef, found, err := unstructured.NestedFieldCopy(hpa.Object, "spec", "scaleTargetRef")
	if err != nil || !found {
		return fmt.Errorf("get HPA targetRef for workload %v failed, because %s", klog.KObj(object), err.Error())
	}
	ref := targetRef.(map[string]interface{})
	name, version, kind := ref["name"].(string), ref["apiVersion"].(string), ref["kind"].(string)
	if !strings.HasSuffix(name, HPADisableSuffix) {
		body := fmt.Sprintf(`{"spec":{"scaleTargetRef":{"apiVersion": "%s", "kind": "%s", "name": "%s"}}}`, version, kind, addSuffix(name))
		if err = cli.Patch(context.TODO(), hpa, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
			return fmt.Errorf("failed to disable HPA %v for workload %v, because %s", klog.KObj(hpa), klog.KObj(object), err.Error())
		}
	}
	return nil
}

func RestoreHPA(cli client.Client, object client.Object) error {
	hpa := findHPAForWorkload(cli, object)
	if hpa == nil {
		return nil
	}
	targetRef, found, err := unstructured.NestedFieldCopy(hpa.Object, "spec", "scaleTargetRef")
	if err != nil || !found {
		return fmt.Errorf("get HPA targetRef for workload %v failed, because %s", klog.KObj(object), err.Error())
	}
	ref := targetRef.(map[string]interface{})
	name, version, kind := ref["name"].(string), ref["apiVersion"].(string), ref["kind"].(string)
	if strings.HasSuffix(name, HPADisableSuffix) {
		body := fmt.Sprintf(`{"spec":{"scaleTargetRef":{"apiVersion": "%s", "kind": "%s", "name": "%s"}}}`, version, kind, removeSuffix(name))
		if err = cli.Patch(context.TODO(), hpa, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
			return fmt.Errorf("failed to restore HPA %v for workload %v, because %s", klog.KObj(hpa), klog.KObj(object), err.Error())
		}
	}
	return nil
}

func findHPAForWorkload(cli client.Client, object client.Object) *unstructured.Unstructured {
	hpa := findHPA(cli, object, "v2")
	if hpa != nil {
		return hpa
	}
	return findHPA(cli, object, "v1")
}

func findHPA(cli client.Client, object client.Object, version string) *unstructured.Unstructured {
	unstructuredList := &unstructured.UnstructuredList{}
	hpaGvk := schema.GroupVersionKind{Group: "autoscaling", Kind: "HorizontalPodAutoscaler", Version: version}
	unstructuredList.SetGroupVersionKind(hpaGvk)
	if err := cli.List(context.TODO(), unstructuredList, &client.ListOptions{Namespace: object.GetNamespace()}); err != nil {
		klog.Warningf("Get HPA for workload %v failed, because %s", klog.KObj(object), err.Error())
		return nil
	}
	klog.Infof("Get %d HPA with %s in namespace %s in total", len(unstructuredList.Items), version, object.GetNamespace())
	for _, item := range unstructuredList.Items {
		scaleTargetRef, found, err := unstructured.NestedFieldCopy(item.Object, "spec", "scaleTargetRef")
		if err != nil || !found {
			continue
		}
		ref := scaleTargetRef.(map[string]interface{})
		name, version, kind := ref["name"].(string), ref["apiVersion"].(string), ref["kind"].(string)
		if version == object.GetObjectKind().GroupVersionKind().GroupVersion().String() &&
			kind == object.GetObjectKind().GroupVersionKind().Kind &&
			removeSuffix(name) == object.GetName() {
			return &item
		}
	}
	klog.Infof("No HPA found for workload %v", klog.KObj(object))
	return nil
}

func addSuffix(HPARefName string) string {
	if strings.HasSuffix(HPARefName, HPADisableSuffix) {
		return HPARefName
	}
	return HPARefName + HPADisableSuffix
}

func removeSuffix(HPARefName string) string {
	refName := HPARefName
	for strings.HasSuffix(refName, HPADisableSuffix) {
		refName = refName[:len(refName)-len(HPADisableSuffix)]
	}
	return refName
}
