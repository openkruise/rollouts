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

package deployment

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/deployment/util"
)

func TestSyncDeployment(t *testing.T) {
	tests := map[string]struct {
		oldRSsReplicas    []int32
		newRSReplicas     int32
		dReplicas         int32
		oldRSsAvailable   []int32
		newRSAvailable    int32
		dAvailable        int32
		partition         intstrutil.IntOrString
		maxSurge          intstrutil.IntOrString
		maxUnavailable    intstrutil.IntOrString
		expectOldReplicas int32
		expectNewReplicas int32
	}{
		"rolling new: surge new first, limited by maxSurge": {
			[]int32{6, 4}, 0, 10,
			[]int32{0, 0}, 0, 0,
			intstrutil.FromInt(4), intstrutil.FromInt(2), intstrutil.FromString("50%"),
			10, 2,
		},
		"rolling new: surge new first, limited by partition": {
			[]int32{6, 4}, 0, 10,
			[]int32{0, 0}, 0, 0,
			intstrutil.FromInt(4), intstrutil.FromInt(10), intstrutil.FromString("50%"),
			10, 4,
		},
		"rolling old: limited by maxUnavailable": {
			[]int32{6, 4}, 2, 10,
			[]int32{6, 4}, 0, 10,
			intstrutil.FromInt(4), intstrutil.FromInt(2), intstrutil.FromString("20%"),
			8, 2,
		},
		"rolling new: limited by partition": {
			[]int32{6, 2}, 2, 10,
			[]int32{6, 2}, 0, 8,
			intstrutil.FromInt(3), intstrutil.FromInt(5), intstrutil.FromString("40%"),
			8, 3,
		},
		"rolling new: scaling down old first, limited by partition": {
			[]int32{6, 4}, 0, 10,
			[]int32{6, 4}, 0, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(0), intstrutil.FromString("40%"),
			7, 0,
		},
		"rolling new: scaling down old first, limited by maxUnavailable": {
			[]int32{6, 4}, 0, 10,
			[]int32{6, 4}, 0, 10,
			intstrutil.FromInt(5), intstrutil.FromInt(0), intstrutil.FromString("20%"),
			8, 0,
		},
		"no op: partition satisfied, maxSurge>0, new pod unavailable": {
			[]int32{3, 4}, 3, 10,
			[]int32{3, 4}, 0, 7,
			intstrutil.FromInt(3), intstrutil.FromInt(2), intstrutil.FromString("30%"),
			7, 3,
		},
		"no op: partition satisfied, maxSurge>0, new pod available": {
			[]int32{3, 4}, 3, 10,
			[]int32{3, 4}, 3, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(2), intstrutil.FromString("30%"),
			7, 3,
		},
		"rolling old: scale down old to satisfied replicas": {
			[]int32{3}, 3, 5,
			[]int32{3}, 3, 6,
			intstrutil.FromInt(3), intstrutil.FromInt(2), intstrutil.FromString("25%"),
			2, 3,
		},
		"scale up: scale down old first": {
			[]int32{4}, 0, 5,
			[]int32{4}, 0, 4,
			intstrutil.FromInt(3), intstrutil.FromInt(0), intstrutil.FromInt(1),
			5, 0,
		},
		"scale up": {
			[]int32{5}, 5, 20,
			[]int32{5}, 5, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(0), intstrutil.FromString("30%"),
			10, 10,
		},
		"scale down": {
			[]int32{12}, 8, 10,
			[]int32{12}, 8, 20,
			intstrutil.FromInt(5), intstrutil.FromInt(0), intstrutil.FromString("30%"),
			6, 4,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			deployment := generateDeployment("busybox")
			deployment.Spec.Replicas = pointer.Int32(test.dReplicas)
			deployment.Status.ReadyReplicas = test.newRSReplicas
			availableReplicas := test.newRSAvailable
			for _, available := range test.oldRSsAvailable {
				availableReplicas += available
			}
			deployment.Status.UpdatedReplicas = test.newRSReplicas
			deployment.Status.Replicas = availableReplicas
			deployment.Status.AvailableReplicas = availableReplicas

			var allObjects []ctrlclient.Object
			allObjects = append(allObjects, &deployment)

			for index, replicas := range test.oldRSsReplicas {
				rs := generateRS(deployment)
				rs.SetName(fmt.Sprintf("rs-%d", index))
				rs.Spec.Replicas = pointer.Int32(replicas)
				rs.Status.Replicas = replicas
				if strings.HasPrefix(name, "scale") {
					rs.Annotations = map[string]string{
						util.ReplicasAnnotation:    strconv.Itoa(-1),
						util.MaxReplicasAnnotation: strconv.Itoa(int(test.dAvailable + test.maxSurge.IntVal)),
					}
				}
				rs.Spec.Template.Spec.Containers[0].Image = fmt.Sprintf("old-version-%d", index)
				rs.Status.ReadyReplicas = test.oldRSsAvailable[index]
				rs.Status.AvailableReplicas = test.oldRSsAvailable[index]
				allObjects = append(allObjects, &rs)
			}

			newRS := generateRS(deployment)
			newRS.SetName("rs-new")
			newRS.Spec.Replicas = pointer.Int32(test.newRSReplicas)
			if strings.HasPrefix(name, "scale") {
				newRS.Annotations = map[string]string{
					util.ReplicasAnnotation:    strconv.Itoa(-1),
					util.MaxReplicasAnnotation: strconv.Itoa(int(test.dAvailable + test.maxSurge.IntVal)),
				}
			}
			newRS.Status.Replicas = test.newRSReplicas
			newRS.Status.ReadyReplicas = test.newRSAvailable
			newRS.Status.AvailableReplicas = test.newRSAvailable
			allObjects = append(allObjects, &newRS)

			fakeCtrlClient := ctrlfake.NewClientBuilder().
				WithObjects(allObjects...).
				Build()

			// Create a mock event recorder
			fakeRecord := record.NewFakeRecorder(10)

			dc := &DeploymentController{
				eventRecorder: fakeRecord,
				runtimeClient: fakeCtrlClient,
				strategy: rolloutsv1alpha1.DeploymentStrategy{
					RollingUpdate: &apps.RollingUpdateDeployment{
						MaxSurge:       &test.maxSurge,
						MaxUnavailable: &test.maxUnavailable,
					},
					Partition: test.partition,
				},
			}

			// Retry syncDeployment to handle potential resource conflicts gracefully
			// This simulates the behavior of controller-runtime's reconcile loop
			var err error
			maxRetries := 10
			for i := 0; i < maxRetries; i++ {
				err = dc.syncDeployment(context.TODO(), &deployment)
				if err == nil {
					break
				}

				// Check if it's a conflict error (409)
				if errors.IsConflict(err) {
					if i < maxRetries-1 {
						// Wait a bit before retrying, simulating the reconcile delay
						time.Sleep(1 * time.Second)
						continue
					}
				}

				// For non-conflict errors or after max retries, break
				break
			}

			if err != nil {
				t.Fatalf("got unexpected error after retries: %v", err)
			}

			var rsList apps.ReplicaSetList
			if err := fakeCtrlClient.List(context.TODO(), &rsList); err != nil {
				t.Fatalf("list rs error: %v", err)
			}
			resultOld := int32(0)
			resultNew := int32(0)
			for _, rs := range rsList.Items {
				if rs.GetName() != "rs-new" {
					resultOld += *rs.Spec.Replicas
				} else {
					resultNew = *rs.Spec.Replicas
				}
			}
			if resultOld != test.expectOldReplicas || resultNew != test.expectNewReplicas {
				t.Fatalf("expect new %d, but got new %d; expect old %d, but got old %d ", test.expectNewReplicas, resultNew, test.expectOldReplicas, resultOld)
			}
		})
	}
}
