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

	nativeDaemonSetParse = appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "unit-test",
			Name:       "native-daemonset-demo",
			Generation: 10,
			UID:        uuid.NewUUID(),
			Annotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
				DaemonSetRevisionAnnotation:         `{"canary-revision":"canary-revision-hash","stable-revision":"stable-revision-hash"}`,
			},
			Labels: map[string]string{
				"rollouts.kruise.io/unit-test-label": "true",
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "demo",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
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
			},
		},
		Status: appsv1.DaemonSetStatus{
			ObservedGeneration:     int64(10),
			DesiredNumberScheduled: 5,
			NumberReady:            4,
			UpdatedNumberScheduled: 3,
			NumberAvailable:        4,
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
		{
			name: "native daemonset parse",
			Get: func() client.Object {
				return nativeDaemonSetParse.DeepCopy()
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
			case *appsv1.DaemonSet:
				// Test GetReplicas for DaemonSet (should return DesiredNumberScheduled)
				Expect(GetReplicas(object)).Should(BeNumerically("==", o.Status.DesiredNumberScheduled))

				// Test getSelector for DaemonSet
				selector, err := metav1.LabelSelectorAsSelector(o.Spec.Selector)
				Expect(err).NotTo(HaveOccurred())
				parsedSelector, err := getSelector(object)
				Expect(err).NotTo(HaveOccurred())
				Expect(reflect.DeepEqual(parsedSelector, selector)).Should(BeTrue())

				// Test ParseWorkload for DaemonSet
				daemonsetInfo := ParseWorkload(object)
				{
					Expect(reflect.DeepEqual(daemonsetInfo.ObjectMeta, o.ObjectMeta)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Generation, o.Generation)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Replicas, o.Status.DesiredNumberScheduled)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Status.Replicas, o.Status.DesiredNumberScheduled)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Status.ReadyReplicas, o.Status.NumberReady)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Status.AvailableReplicas, o.Status.NumberAvailable)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Status.UpdatedReplicas, o.Status.UpdatedNumberScheduled)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Status.ObservedGeneration, o.Status.ObservedGeneration)).Should(BeTrue())
					canaryRevision, stableRevision := ParseDaemonSetRevision(o.Annotations)
					Expect(reflect.DeepEqual(daemonsetInfo.Status.StableRevision, stableRevision)).Should(BeTrue())
					Expect(reflect.DeepEqual(daemonsetInfo.Status.UpdateRevision, canaryRevision)).Should(BeTrue())
					Expect(daemonsetInfo.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 0))
				}

				// Test GetMetadata for DaemonSet
				metadata := GetMetadata(object)
				Expect(reflect.DeepEqual(*metadata, o.ObjectMeta)).Should(BeTrue())

				// Test GetTypeMeta for DaemonSet
				typeMeta := GetTypeMeta(object)
				Expect(reflect.DeepEqual(*typeMeta, o.TypeMeta)).Should(BeTrue())
			}
		})
	}
}
func TestNativeDaemonSetParse(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name              string
		getDaemonSet      func() *appsv1.DaemonSet
		expectedReplicas  int32
		expectedStable    string
		expectedCanary    string
		expectedObserved  int64
		expectedReady     int32
		expectedAvailable int32
		expectedUpdated   int32
	}{
		{
			name: "native daemonset with revision annotations",
			getDaemonSet: func() *appsv1.DaemonSet {
				return nativeDaemonSetParse.DeepCopy()
			},
			expectedReplicas:  5,
			expectedStable:    "stable-revision-hash",
			expectedCanary:    "canary-revision-hash",
			expectedObserved:  10,
			expectedReady:     4,
			expectedAvailable: 4,
			expectedUpdated:   3,
		},
		{
			name: "native daemonset without revision annotations",
			getDaemonSet: func() *appsv1.DaemonSet {
				ds := nativeDaemonSetParse.DeepCopy()
				delete(ds.Annotations, DaemonSetRevisionAnnotation)
				return ds
			},
			expectedReplicas:  5,
			expectedStable:    "",
			expectedCanary:    "",
			expectedObserved:  10,
			expectedReady:     4,
			expectedAvailable: 4,
			expectedUpdated:   3,
		},
		{
			name: "native daemonset with different status values",
			getDaemonSet: func() *appsv1.DaemonSet {
				ds := nativeDaemonSetParse.DeepCopy()
				ds.Status.DesiredNumberScheduled = 10
				ds.Status.NumberReady = 8
				ds.Status.NumberAvailable = 9
				ds.Status.UpdatedNumberScheduled = 7
				ds.Status.ObservedGeneration = 15
				SetDaemonSetRevision(ds.Annotations, "new-canary-hash", "new-stable-hash")
				return ds
			},
			expectedReplicas:  10,
			expectedStable:    "new-stable-hash",
			expectedCanary:    "new-canary-hash",
			expectedObserved:  15,
			expectedReady:     8,
			expectedAvailable: 9,
			expectedUpdated:   7,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			daemonSet := cs.getDaemonSet()

			// Test GetReplicas
			replicas := GetReplicas(daemonSet)
			Expect(replicas).Should(BeNumerically("==", cs.expectedReplicas))

			// Test ParseWorkloadStatus
			status := ParseWorkloadStatus(daemonSet)
			Expect(status.Replicas).Should(BeNumerically("==", cs.expectedReplicas))
			Expect(status.ReadyReplicas).Should(BeNumerically("==", cs.expectedReady))
			Expect(status.AvailableReplicas).Should(BeNumerically("==", cs.expectedAvailable))
			Expect(status.UpdatedReplicas).Should(BeNumerically("==", cs.expectedUpdated))
			Expect(status.ObservedGeneration).Should(BeNumerically("==", cs.expectedObserved))
			Expect(status.StableRevision).Should(Equal(cs.expectedStable))
			Expect(status.UpdateRevision).Should(Equal(cs.expectedCanary))
			Expect(status.UpdatedReadyReplicas).Should(BeNumerically("==", 0)) // DaemonSet doesn't have UpdatedReadyReplicas

			// Test getSelector
			expectedSelector, err := metav1.LabelSelectorAsSelector(daemonSet.Spec.Selector)
			Expect(err).NotTo(HaveOccurred())
			actualSelector, err := getSelector(daemonSet)
			Expect(err).NotTo(HaveOccurred())
			Expect(reflect.DeepEqual(actualSelector, expectedSelector)).Should(BeTrue())

			// Test GetMetadata
			metadata := GetMetadata(daemonSet)
			Expect(reflect.DeepEqual(*metadata, daemonSet.ObjectMeta)).Should(BeTrue())

			// Test GetTypeMeta
			typeMeta := GetTypeMeta(daemonSet)
			Expect(reflect.DeepEqual(*typeMeta, daemonSet.TypeMeta)).Should(BeTrue())

			// Test ParseWorkload
			workloadInfo := ParseWorkload(daemonSet)
			Expect(workloadInfo).NotTo(BeNil())
			Expect(workloadInfo.Replicas).Should(BeNumerically("==", cs.expectedReplicas))
			Expect(reflect.DeepEqual(workloadInfo.ObjectMeta, daemonSet.ObjectMeta)).Should(BeTrue())
			Expect(reflect.DeepEqual(workloadInfo.TypeMeta, daemonSet.TypeMeta)).Should(BeTrue())
			Expect(workloadInfo.Status.Replicas).Should(BeNumerically("==", cs.expectedReplicas))
			Expect(workloadInfo.Status.ReadyReplicas).Should(BeNumerically("==", cs.expectedReady))
			Expect(workloadInfo.Status.AvailableReplicas).Should(BeNumerically("==", cs.expectedAvailable))
			Expect(workloadInfo.Status.UpdatedReplicas).Should(BeNumerically("==", cs.expectedUpdated))
			Expect(workloadInfo.Status.ObservedGeneration).Should(BeNumerically("==", cs.expectedObserved))
			Expect(workloadInfo.Status.StableRevision).Should(Equal(cs.expectedStable))
			Expect(workloadInfo.Status.UpdateRevision).Should(Equal(cs.expectedCanary))
		})
	}
}

func TestNativeDaemonSetUnstructuredParse(t *testing.T) {
	RegisterFailHandler(Fail)

	t.Run("native daemonset unstructured parse", func(t *testing.T) {
		// Convert native DaemonSet to unstructured
		ds := nativeDaemonSetParse.DeepCopy()
		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ds)
		Expect(err).NotTo(HaveOccurred())
		unstructuredDS := &unstructured.Unstructured{Object: object}

		// Test getSelector with unstructured
		expectedSelector, err := metav1.LabelSelectorAsSelector(ds.Spec.Selector)
		Expect(err).NotTo(HaveOccurred())
		actualSelector, err := getSelector(unstructuredDS)
		Expect(err).NotTo(HaveOccurred())
		Expect(actualSelector.String()).Should(Equal(expectedSelector.String()))

		// Test GetMetadata with unstructured
		metadata := GetMetadata(unstructuredDS)
		Expect(metadata.Name).Should(Equal(ds.Name))
		Expect(metadata.Namespace).Should(Equal(ds.Namespace))
		Expect(metadata.Generation).Should(Equal(ds.Generation))

		// Test GetTypeMeta with unstructured
		typeMeta := GetTypeMeta(unstructuredDS)
		Expect(typeMeta.APIVersion).Should(Equal(ds.TypeMeta.APIVersion))
		Expect(typeMeta.Kind).Should(Equal(ds.TypeMeta.Kind))

		// Note: GetReplicas and ParseWorkload with unstructured DaemonSet will use the generic
		// unstructured parsing which looks for spec.replicas (which DaemonSet doesn't have).
		// This is expected behavior - in practice, unstructured parsing is used for workloads
		// that follow the standard spec.replicas pattern, not DaemonSets.

		// Test GetReplicas with unstructured (will return default value of 1 since DaemonSet has no spec.replicas)
		replicas := GetReplicas(unstructuredDS)
		Expect(replicas).Should(BeNumerically("==", 1)) // Default value when spec.replicas is not found

		// Test ParseWorkload with unstructured
		workloadInfo := ParseWorkload(unstructuredDS)
		Expect(workloadInfo).NotTo(BeNil())
		Expect(workloadInfo.Replicas).Should(BeNumerically("==", 1)) // Default value
		// Status parsing should work correctly from unstructured
		Expect(workloadInfo.Status.ObservedGeneration).Should(BeNumerically("==", ds.Status.ObservedGeneration))
	})
}
