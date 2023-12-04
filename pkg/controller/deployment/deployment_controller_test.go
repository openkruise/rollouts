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

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

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
			fakeClient := fake.NewSimpleClientset()
			fakeRecord := record.NewFakeRecorder(10)
			informers := informers.NewSharedInformerFactory(fakeClient, 0)
			rsInformer := informers.Apps().V1().ReplicaSets().Informer()
			dInformer := informers.Apps().V1().Deployments().Informer()

			var deployment apps.Deployment
			var newRS apps.ReplicaSet
			{
				deployment = generateDeployment("busybox")
				deployment.Spec.Replicas = pointer.Int32(test.dReplicas)
				deployment.Status.ReadyReplicas = test.newRSReplicas
				availableReplicas := test.newRSAvailable
				for _, available := range test.oldRSsAvailable {
					availableReplicas += available
				}
				deployment.Status.UpdatedReplicas = test.newRSReplicas
				deployment.Status.Replicas = availableReplicas
				deployment.Status.AvailableReplicas = availableReplicas
				dInformer.GetIndexer().Add(&deployment)
				_, err := fakeClient.AppsV1().Deployments(deployment.Namespace).Create(context.TODO(), &deployment, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("got unexpected error: %v", err)
				}
			}
			{
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
					rsInformer.GetIndexer().Add(&rs)
					_, err := fakeClient.AppsV1().ReplicaSets(rs.Namespace).Create(context.TODO(), &rs, metav1.CreateOptions{})
					if err != nil {
						t.Fatalf("got unexpected error: %v", err)
					}
				}
			}
			{
				newRS = generateRS(deployment)
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
				rsInformer.GetIndexer().Add(&newRS)
				_, err := fakeClient.AppsV1().ReplicaSets(newRS.Namespace).Create(context.TODO(), &newRS, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("got unexpected error: %v", err)
				}
			}

			dc := &DeploymentController{
				client:        fakeClient,
				eventRecorder: fakeRecord,
				dLister:       appslisters.NewDeploymentLister(dInformer.GetIndexer()),
				rsLister:      appslisters.NewReplicaSetLister(rsInformer.GetIndexer()),
				strategy: rolloutsv1alpha1.DeploymentStrategy{
					RollingUpdate: &apps.RollingUpdateDeployment{
						MaxSurge:       &test.maxSurge,
						MaxUnavailable: &test.maxUnavailable,
					},
					Partition: test.partition,
				},
			}

			err := dc.syncDeployment(context.TODO(), &deployment)
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			rss, err := dc.client.AppsV1().ReplicaSets(deployment.Namespace).List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			resultOld := int32(0)
			resultNew := int32(0)
			for _, rs := range rss.Items {
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
