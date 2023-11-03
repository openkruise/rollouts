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

package control

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestParseIntegerAsPercentage(t *testing.T) {
	RegisterFailHandler(Fail)

	supposeUpper := 10000
	for allReplicas := 1; allReplicas <= supposeUpper; allReplicas++ {
		for percent := 0; percent <= 100; percent++ {
			canaryPercent := intstr.FromString(fmt.Sprintf("%v%%", percent))
			canaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&canaryPercent, allReplicas, true)
			partition := ParseIntegerAsPercentageIfPossible(int32(allReplicas-canaryReplicas), int32(allReplicas), &canaryPercent)
			stableReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&partition, allReplicas, true)
			if percent == 0 {
				Expect(stableReplicas).Should(BeNumerically("==", allReplicas))
			} else if percent == 100 {
				Expect(stableReplicas).Should(BeNumerically("==", 0))
			} else if percent > 0 {
				Expect(allReplicas - stableReplicas).To(BeNumerically(">", 0))
			}
			Expect(stableReplicas).Should(BeNumerically("<=", allReplicas))
			Expect(math.Abs(float64((allReplicas - canaryReplicas) - stableReplicas))).Should(BeNumerically("<", float64(allReplicas)*0.01))
		}
	}
}

func TestCalculateBatchReplicas(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := map[string]struct {
		batchReplicas    intstr.IntOrString
		workloadReplicas int32
		expectedReplicas int32
	}{
		"batch: 5, replicas: 10": {
			batchReplicas:    intstr.FromInt(5),
			workloadReplicas: 10,
			expectedReplicas: 5,
		},
		"batch: 20%, replicas: 10": {
			batchReplicas:    intstr.FromString("20%"),
			workloadReplicas: 10,
			expectedReplicas: 2,
		},
		"batch: 100%, replicas: 10": {
			batchReplicas:    intstr.FromString("100%"),
			workloadReplicas: 10,
			expectedReplicas: 10,
		},
		"batch: 200%, replicas: 10": {
			batchReplicas:    intstr.FromString("200%"),
			workloadReplicas: 10,
			expectedReplicas: 10,
		},
		"batch: 200, replicas: 10": {
			batchReplicas:    intstr.FromInt(200),
			workloadReplicas: 10,
			expectedReplicas: 10,
		},
		"batch: 0, replicas: 10": {
			batchReplicas:    intstr.FromInt(0),
			workloadReplicas: 10,
			expectedReplicas: 0,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			release := &v1beta1.BatchRelease{
				Spec: v1beta1.BatchReleaseSpec{
					ReleasePlan: v1beta1.ReleasePlan{
						Batches: []v1beta1.ReleaseBatch{
							{
								CanaryReplicas: cs.batchReplicas,
							},
						},
					},
				},
			}
			got := CalculateBatchReplicas(release, int(cs.workloadReplicas), 0)
			Expect(got).Should(BeNumerically("==", cs.expectedReplicas))
		})
	}
}

func TestIsControlledByBatchRelease(t *testing.T) {
	RegisterFailHandler(Fail)

	release := &v1beta1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rollouts.kruise.io/v1alpha1",
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			UID:       "test",
			Name:      "test",
			Namespace: "test",
		},
	}

	controlInfo, _ := json.Marshal(metav1.NewControllerRef(release, release.GroupVersionKind()))

	cases := map[string]struct {
		object *apps.Deployment
		result bool
	}{
		"ownerRef": {
			object: &apps.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(release, release.GroupVersionKind()),
					},
				},
			},
			result: true,
		},
		"annoRef": {
			object: &apps.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						util.BatchReleaseControlAnnotation: string(controlInfo),
					},
				},
			},
			result: true,
		},
		"notRef": {
			object: &apps.Deployment{},
			result: false,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			got := IsControlledByBatchRelease(release, cs.object)
			Expect(got == cs.result).To(BeTrue())
		})
	}
}

func TestIsCurrentMoreThanOrEqualToDesired(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := map[string]struct {
		current intstr.IntOrString
		desired intstr.IntOrString
		result  bool
	}{
		"current=2,desired=1": {
			current: intstr.FromInt(2),
			desired: intstr.FromInt(1),
			result:  true,
		},
		"current=2,desired=2": {
			current: intstr.FromInt(2),
			desired: intstr.FromInt(2),
			result:  true,
		},
		"current=2,desired=3": {
			current: intstr.FromInt(2),
			desired: intstr.FromInt(3),
			result:  false,
		},
		"current=80%,desired=79%": {
			current: intstr.FromString("80%"),
			desired: intstr.FromString("79%"),
			result:  true,
		},
		"current=80%,desired=80%": {
			current: intstr.FromString("80%"),
			desired: intstr.FromString("80%"),
			result:  true,
		},
		"current=90%,desired=91%": {
			current: intstr.FromString("90%"),
			desired: intstr.FromString("91%"),
			result:  false,
		},
	}
	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			got := IsCurrentMoreThanOrEqualToDesired(cs.current, cs.desired)
			Expect(got == cs.result).Should(BeTrue())
		})
	}
}
