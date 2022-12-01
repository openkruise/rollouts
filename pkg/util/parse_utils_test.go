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
	"reflect"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1pub "github.com/openkruise/kruise-api/apps/pub"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "unit-test",
			Name:      "pod-demo",
			Labels: map[string]string{
				"app": "demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "busybox:1.32",
				},
			},
		},
	}
	nativeStatefulSet = appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "unit-test",
			Name:       "native-statefulset-demo",
			Generation: 10,
			UID:        uuid.NewUUID(),
			Annotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
			},
			Labels: map[string]string{
				"rollouts.kruise.io/unit-test-label": "true",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: pointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "demo",
				},
			},
			Template: template,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
					Partition: pointer.Int32(5),
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: int64(10),
			Replicas:           9,
			ReadyReplicas:      8,
			UpdatedReplicas:    5,
			CurrentReplicas:    4,
			AvailableReplicas:  7,
			CurrentRevision:    "sts-version1",
			UpdateRevision:     "sts-version2",
		},
	}

	advancedStatefulSet = appsv1beta1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1beta1.SchemeGroupVersion.String(),
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "unit-test",
			Name:       "advanced-statefulset-demo",
			Generation: 10,
			UID:        uuid.NewUUID(),
			Annotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
			},
			Labels: map[string]string{
				"rollouts.kruise.io/unit-test-label": "true",
			},
		},
		Spec: appsv1beta1.StatefulSetSpec{
			Replicas: pointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "demo",
				},
			},
			Template: template,
			UpdateStrategy: appsv1beta1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1beta1.RollingUpdateStatefulSetStrategy{
					Partition:      pointer.Int32(5),
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"},
					UnorderedUpdate: &appsv1beta1.UnorderedUpdateStrategy{
						PriorityStrategy: &appsv1pub.UpdatePriorityStrategy{
							OrderPriority: []appsv1pub.UpdatePriorityOrderTerm{
								{
									OrderedKey: "order-key",
								},
							},
						},
					},
				},
			},
		},
		Status: appsv1beta1.StatefulSetStatus{
			ObservedGeneration: int64(10),
			Replicas:           9,
			ReadyReplicas:      8,
			UpdatedReplicas:    5,
			AvailableReplicas:  7,
			CurrentRevision:    "sts-version1",
			UpdateRevision:     "sts-version2",
		},
	}

	cloneset = appsv1alpha1.CloneSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CloneSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "unit-test",
			Name:       "cloneset-demo",
			Generation: 10,
			UID:        uuid.NewUUID(),
			Annotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
			},
			Labels: map[string]string{
				"rollouts.kruise.io/unit-test-label": "true",
			},
		},
		Spec: appsv1alpha1.CloneSetSpec{
			Replicas: pointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "demo",
				},
			},
			Template: template,
			UpdateStrategy: appsv1alpha1.CloneSetUpdateStrategy{
				Type:           appsv1alpha1.InPlaceIfPossibleCloneSetUpdateStrategyType,
				Partition:      &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
				MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"},
				PriorityStrategy: &appsv1pub.UpdatePriorityStrategy{
					OrderPriority: []appsv1pub.UpdatePriorityOrderTerm{
						{
							OrderedKey: "order-key",
						},
					},
				},
			},
		},
		Status: appsv1alpha1.CloneSetStatus{
			ObservedGeneration:   int64(10),
			Replicas:             9,
			ReadyReplicas:        8,
			UpdatedReplicas:      5,
			UpdatedReadyReplicas: 4,
			AvailableReplicas:    7,
			CurrentRevision:      "sts-version1",
			UpdateRevision:       "sts-version2",
		},
	}

	deployment = appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "unit-test",
			Name:       "deployment-demo",
			Generation: 10,
			UID:        uuid.NewUUID(),
			Annotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
			},
			Labels: map[string]string{
				"rollouts.kruise.io/unit-test-label": "true",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "demo",
				},
			},
			Template: template,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: int64(10),
			Replicas:           9,
			ReadyReplicas:      8,
			UpdatedReplicas:    5,
			AvailableReplicas:  7,
		},
	}
)

func TestStatefulSetParse(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name string
		Get  func() *unstructured.Unstructured
	}{
		{
			name: "native statefulset parse without unorderedUpdate",
			Get: func() *unstructured.Unstructured {
				sts := nativeStatefulSet.DeepCopy()
				object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sts)
				Expect(err).NotTo(HaveOccurred())
				return &unstructured.Unstructured{Object: object}
			},
		},
		{
			name: "advanced statefulset parse with unorderedUpdate",
			Get: func() *unstructured.Unstructured {
				sts := advancedStatefulSet.DeepCopy()
				object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sts)
				Expect(err).NotTo(HaveOccurred())
				return &unstructured.Unstructured{Object: object}
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			object := cs.Get()
			Expect(IsStatefulSetRollingUpdate(object)).Should(BeTrue())
			if strings.Contains(cs.name, "native") {
				Expect(IsStatefulSetUnorderedUpdate(object)).Should(BeFalse())
				Expect(getStatefulSetMaxUnavailable(object)).Should(BeNil())
			} else {
				Expect(IsStatefulSetUnorderedUpdate(object)).Should(BeTrue())
				Expect(reflect.DeepEqual(getStatefulSetMaxUnavailable(object), &intstr.IntOrString{Type: intstr.String, StrVal: "10%"})).Should(BeTrue())
			}
			Expect(GetStatefulSetPartition(object)).Should(BeNumerically("==", 5))
			SetStatefulSetPartition(object, 7)
			Expect(GetStatefulSetPartition(object)).Should(BeNumerically("==", 7))
		})
	}
}

func TestWorkloadParse(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name string
		Get  func() client.Object
	}{
		{
			name: "native statefulset parse",
			Get: func() client.Object {
				return nativeStatefulSet.DeepCopy()
			},
		},
		{
			name: "advanced statefulset parse",
			Get: func() client.Object {
				return advancedStatefulSet.DeepCopy()
			},
		},
		{
			name: "cloneset parse",
			Get: func() client.Object {
				return cloneset.DeepCopy()
			},
		},
		{
			name: "deployment parse",
			Get: func() client.Object {
				return deployment.DeepCopy()
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			object := cs.Get()
			switch o := object.(type) {
			case *appsv1.Deployment:
				Expect(GetReplicas(object)).Should(BeNumerically("==", *o.Spec.Replicas))
				selector, err := metav1.LabelSelectorAsSelector(o.Spec.Selector)
				Expect(err).NotTo(HaveOccurred())
				parsedSelector, err := getSelector(object)
				Expect(err).NotTo(HaveOccurred())
				Expect(reflect.DeepEqual(parsedSelector, selector)).Should(BeTrue())
			case *appsv1alpha1.CloneSet:
				Expect(GetReplicas(object)).Should(BeNumerically("==", *o.Spec.Replicas))
				selector, err := metav1.LabelSelectorAsSelector(o.Spec.Selector)
				Expect(err).NotTo(HaveOccurred())
				parsedSelector, err := getSelector(object)
				Expect(err).NotTo(HaveOccurred())
				Expect(reflect.DeepEqual(parsedSelector, selector)).Should(BeTrue())
			case *appsv1.StatefulSet:
				uo, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
				Expect(err).NotTo(HaveOccurred())
				uobject := &unstructured.Unstructured{Object: uo}
				Expect(reflect.DeepEqual(GetTemplate(uobject), &o.Spec.Template)).Should(BeTrue())
				statefulsetInfo := ParseWorkload(uobject)
				{
					Expect(reflect.DeepEqual(statefulsetInfo.ObjectMeta, o.ObjectMeta)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Generation, o.Generation)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Replicas, *o.Spec.Replicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.Replicas, o.Status.Replicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.ReadyReplicas, o.Status.ReadyReplicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.AvailableReplicas, o.Status.AvailableReplicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.UpdatedReplicas, o.Status.UpdatedReplicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.ObservedGeneration, o.Status.ObservedGeneration)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.StableRevision, o.Status.CurrentRevision)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.UpdateRevision, o.Status.UpdateRevision)).Should(BeTrue())
					Expect(statefulsetInfo.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 0))
				}
			case *appsv1beta1.StatefulSet:
				uo, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
				Expect(err).NotTo(HaveOccurred())
				uobject := &unstructured.Unstructured{Object: uo}
				Expect(reflect.DeepEqual(GetTemplate(uobject), &o.Spec.Template)).Should(BeTrue())
				statefulsetInfo := ParseWorkload(uobject)
				{
					Expect(reflect.DeepEqual(statefulsetInfo.ObjectMeta, o.ObjectMeta)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Generation, o.Generation)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Replicas, *o.Spec.Replicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.Replicas, o.Status.Replicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.ReadyReplicas, o.Status.ReadyReplicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.AvailableReplicas, o.Status.AvailableReplicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.UpdatedReplicas, o.Status.UpdatedReplicas)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.ObservedGeneration, o.Status.ObservedGeneration)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.StableRevision, o.Status.CurrentRevision)).Should(BeTrue())
					Expect(reflect.DeepEqual(statefulsetInfo.Status.UpdateRevision, o.Status.UpdateRevision)).Should(BeTrue())
					Expect(statefulsetInfo.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 0))
				}
			}
		})
	}
}
