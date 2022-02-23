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

package workloads

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	releaseDeploy = &v1alpha1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: "application",
			UID:       uuid.NewUUID(),
		},
		Spec: v1alpha1.BatchReleaseSpec{
			TargetRef: v1alpha1.ObjectRef{
				WorkloadRef: &v1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "sample",
				},
			},
			ReleasePlan: v1alpha1.ReleasePlan{
				Batches: []v1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
						PauseSeconds:   100,
					},
					{
						CanaryReplicas: intstr.FromString("50%"),
						PauseSeconds:   100,
					},
					{
						CanaryReplicas: intstr.FromString("80%"),
						PauseSeconds:   100,
					},
				},
			},
		},
	}

	stableDeploy = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apps.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sample",
			Namespace:  "application",
			UID:        types.UID("87076677"),
			Generation: 2,
			Labels: map[string]string{
				"app":                                "busybox",
				apps.DefaultDeploymentUniqueLabelKey: "update-pod-hash",
			},
		},
		Spec: apps.DeploymentSpec{
			Replicas: pointer.Int32Ptr(100),
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(2)},
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: containers("v2"),
				},
			},
		},
		Status: apps.DeploymentStatus{
			Replicas:          100,
			ReadyReplicas:     100,
			UpdatedReplicas:   0,
			AvailableReplicas: 100,
		},
	}
)

func TestDeploymentController(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name    string
		Paused  bool
		Cleanup bool
	}{
		{
			Name:    "paused=true, cleanup=true",
			Paused:  true,
			Cleanup: true,
		},
		{
			Name:    "paused=true, cleanup=false",
			Paused:  true,
			Cleanup: false,
		},
		{
			Name:    "paused=false cleanup=true",
			Paused:  false,
			Cleanup: true,
		},
		{
			Name:    "paused=false , cleanup=false",
			Paused:  false,
			Cleanup: false,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseDeploy.DeepCopy(), stableDeploy.DeepCopy()).Build()
			rec := record.NewFakeRecorder(100)
			c := deploymentController{
				workloadController: workloadController{
					client:           cli,
					recorder:         rec,
					parentController: releaseDeploy,
					releasePlan:      &releaseDeploy.Spec.ReleasePlan,
					releaseStatus:    &releaseDeploy.Status,
				},
				stableNamespacedName: client.ObjectKeyFromObject(stableDeploy),
				canaryNamespacedName: client.ObjectKeyFromObject(stableDeploy),
			}
			oldObject := &apps.Deployment{}
			Expect(cli.Get(context.TODO(), c.stableNamespacedName, oldObject)).NotTo(HaveOccurred())
			canary, err := c.claimDeployment(oldObject.DeepCopy(), nil)
			Expect(canary).ShouldNot(BeNil())
			Expect(err).NotTo(HaveOccurred())

			newObject := &apps.Deployment{}
			Expect(cli.Get(context.TODO(), c.stableNamespacedName, newObject)).NotTo(HaveOccurred())
			_, err = c.releaseDeployment(newObject.DeepCopy(), &cs.Paused, cs.Cleanup)
			Expect(err).NotTo(HaveOccurred())

			newObject = &apps.Deployment{}
			Expect(cli.Get(context.TODO(), c.stableNamespacedName, newObject)).NotTo(HaveOccurred())
			newObject.Spec.Paused = oldObject.Spec.Paused
			Expect(reflect.DeepEqual(oldObject.Spec, newObject.Spec)).Should(BeTrue())
			Expect(reflect.DeepEqual(oldObject.Labels, newObject.Labels)).Should(BeTrue())
			Expect(reflect.DeepEqual(oldObject.Finalizers, newObject.Finalizers)).Should(BeTrue())
			Expect(reflect.DeepEqual(oldObject.Annotations, newObject.Annotations)).Should(BeTrue())
		})
	}
}
