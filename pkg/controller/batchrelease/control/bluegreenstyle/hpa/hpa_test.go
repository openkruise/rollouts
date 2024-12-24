package hpa

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()
)

func TestHPAPackage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HPA Package Suite")
}

var _ = Describe("HPA Operations", func() {
	var (
		cli    client.Client
		object *unstructured.Unstructured
	)

	BeforeEach(func() {
		object = &unstructured.Unstructured{}
		object.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
		})
		object.SetNamespace("default")
		object.SetName("my-deployment")

		cli = fake.NewClientBuilder().WithScheme(scheme).WithObjects(object).Build()
	})

	Context("when disabling and restoring HPA", func() {
		It("should disable and restore HPA successfully", func() {
			// Create a fake HPA
			hpa := &unstructured.Unstructured{}
			hpa.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "autoscaling",
				Version: "v2",
				Kind:    "HorizontalPodAutoscaler",
			})
			hpa.SetNamespace("default")
			hpa.SetName("my-hpa")
			unstructured.SetNestedField(hpa.Object, map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       "my-deployment",
			}, "spec", "scaleTargetRef")

			Expect(cli.Create(context.TODO(), hpa)).To(Succeed())

			// Disable HPA
			DisableHPA(cli, object)

			fetchedHPA := &unstructured.Unstructured{}
			fetchedHPA.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "autoscaling",
				Version: "v2",
				Kind:    "HorizontalPodAutoscaler",
			})
			Expect(cli.Get(context.TODO(), types.NamespacedName{
				Namespace: "default",
				Name:      "my-hpa",
			}, fetchedHPA)).To(Succeed())

			targetRef, found, err := unstructured.NestedFieldCopy(fetchedHPA.Object, "spec", "scaleTargetRef")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())

			ref := targetRef.(map[string]interface{})
			Expect(ref["name"]).To(Equal("my-deployment" + HPADisableSuffix))

			// Restore HPA
			RestoreHPA(cli, object)

			Expect(cli.Get(context.TODO(), types.NamespacedName{
				Namespace: "default",
				Name:      "my-hpa",
			}, fetchedHPA)).To(Succeed())

			targetRef, found, err = unstructured.NestedFieldCopy(fetchedHPA.Object, "spec", "scaleTargetRef")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())

			ref = targetRef.(map[string]interface{})
			Expect(ref["name"]).To(Equal("my-deployment"))
		})
	})

	Context("when finding HPA for workload", func() {
		It("should find the correct HPA", func() {
			// Create a fake HPA v2
			hpaV2 := &unstructured.Unstructured{}
			hpaV2.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "autoscaling",
				Version: "v2",
				Kind:    "HorizontalPodAutoscaler",
			})
			hpaV2.SetNamespace("default")
			hpaV2.SetName("my-hpa-v2")
			unstructured.SetNestedField(hpaV2.Object, map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       "my-deployment",
			}, "spec", "scaleTargetRef")

			// Create a fake HPA v1
			hpaV1 := &unstructured.Unstructured{}
			hpaV1.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "autoscaling",
				Version: "v1",
				Kind:    "HorizontalPodAutoscaler",
			})
			hpaV1.SetNamespace("default")
			hpaV1.SetName("my-hpa-v1")
			unstructured.SetNestedField(hpaV1.Object, map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       "my-deployment",
			}, "spec", "scaleTargetRef")

			Expect(cli.Create(context.TODO(), hpaV2)).To(Succeed())
			Expect(cli.Create(context.TODO(), hpaV1)).To(Succeed())

			// Test finding HPA for workload
			foundHPA := findHPAForWorkload(cli, object)
			Expect(foundHPA).NotTo(BeNil())
			Expect(foundHPA.GetName()).To(Equal("my-hpa-v2"))

			// Delete v2 HPA and check if v1 is found
			Expect(cli.Delete(context.TODO(), hpaV2)).To(Succeed())
			foundHPA = findHPAForWorkload(cli, object)
			Expect(foundHPA).NotTo(BeNil())
			Expect(foundHPA.GetName()).To(Equal("my-hpa-v1"))
		})
	})
})
