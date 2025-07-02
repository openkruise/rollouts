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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/deployment/util"
)

// mockReplicaSetLister implements appslisters.ReplicaSetLister for testing
type mockReplicaSetLister struct {
	client ctrlclient.Client
}

func (m *mockReplicaSetLister) List(selector labels.Selector) ([]*apps.ReplicaSet, error) {
	var rsList apps.ReplicaSetList
	if err := m.client.List(context.TODO(), &rsList); err != nil {
		return nil, err
	}

	var result []*apps.ReplicaSet
	for i := range rsList.Items {
		if selector.Matches(labels.Set(rsList.Items[i].Labels)) {
			result = append(result, &rsList.Items[i])
		}
	}
	return result, nil
}

func (m *mockReplicaSetLister) ReplicaSets(namespace string) appslisters.ReplicaSetNamespaceLister {
	return &mockReplicaSetNamespaceLister{
		client:    m.client,
		namespace: namespace,
	}
}

func (m *mockReplicaSetLister) GetPodReplicaSets(pod *v1.Pod) ([]*apps.ReplicaSet, error) {
	// For testing purposes, return empty list
	return []*apps.ReplicaSet{}, nil
}

// mockDeploymentLister implements appslisters.DeploymentLister for testing
type mockDeploymentLister struct {
	client ctrlclient.Client
}

func (m *mockDeploymentLister) List(selector labels.Selector) ([]*apps.Deployment, error) {
	var deploymentList apps.DeploymentList
	if err := m.client.List(context.TODO(), &deploymentList); err != nil {
		return nil, err
	}

	var result []*apps.Deployment
	for i := range deploymentList.Items {
		if selector.Matches(labels.Set(deploymentList.Items[i].Labels)) {
			result = append(result, &deploymentList.Items[i])
		}
	}
	return result, nil
}

func (m *mockDeploymentLister) Deployments(namespace string) appslisters.DeploymentNamespaceLister {
	return &mockDeploymentNamespaceLister{
		client:    m.client,
		namespace: namespace,
	}
}

// mockDeploymentNamespaceLister implements appslisters.DeploymentNamespaceLister for testing
type mockDeploymentNamespaceLister struct {
	client    ctrlclient.Client
	namespace string
}

func (m *mockDeploymentNamespaceLister) List(selector labels.Selector) ([]*apps.Deployment, error) {
	var deploymentList apps.DeploymentList
	if err := m.client.List(context.TODO(), &deploymentList, ctrlclient.InNamespace(m.namespace)); err != nil {
		return nil, err
	}

	var result []*apps.Deployment
	for i := range deploymentList.Items {
		if selector.Matches(labels.Set(deploymentList.Items[i].Labels)) {
			result = append(result, &deploymentList.Items[i])
		}
	}
	return result, nil
}

func (m *mockDeploymentNamespaceLister) Get(name string) (*apps.Deployment, error) {
	var deployment apps.Deployment
	if err := m.client.Get(context.TODO(), ctrlclient.ObjectKey{Namespace: m.namespace, Name: name}, &deployment); err != nil {
		return nil, err
	}
	return &deployment, nil
}

// mockReplicaSetNamespaceLister implements appslisters.ReplicaSetNamespaceLister for testing
type mockReplicaSetNamespaceLister struct {
	client    ctrlclient.Client
	namespace string
}

func (m *mockReplicaSetNamespaceLister) List(selector labels.Selector) ([]*apps.ReplicaSet, error) {
	var rsList apps.ReplicaSetList
	if err := m.client.List(context.TODO(), &rsList, ctrlclient.InNamespace(m.namespace)); err != nil {
		return nil, err
	}

	var result []*apps.ReplicaSet
	for i := range rsList.Items {
		if selector.Matches(labels.Set(rsList.Items[i].Labels)) {
			result = append(result, &rsList.Items[i])
		}
	}
	return result, nil
}

func (m *mockReplicaSetNamespaceLister) Get(name string) (*apps.ReplicaSet, error) {
	var rs apps.ReplicaSet
	if err := m.client.Get(context.TODO(), ctrlclient.ObjectKey{Namespace: m.namespace, Name: name}, &rs); err != nil {
		return nil, err
	}
	return &rs, nil
}

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

			// Create a mock deployment lister
			mockDeploymentLister := &mockDeploymentLister{client: fakeCtrlClient}

			// Create a fake client with the same objects
			fakeClient := fake.NewSimpleClientset()
			_, err := fakeClient.AppsV1().Deployments(deployment.Namespace).Create(context.TODO(), &deployment, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to create deployment in fake client: %v", err)
			}

			for _, obj := range allObjects {
				if rs, ok := obj.(*apps.ReplicaSet); ok {
					_, err := fakeClient.AppsV1().ReplicaSets(rs.Namespace).Create(context.TODO(), rs, metav1.CreateOptions{})
					if err != nil {
						t.Fatalf("failed to create replicaset in fake client: %v", err)
					}
				}
			}

			dc := &DeploymentController{
				client:        fakeClient,
				eventRecorder: fakeRecord,
				dLister:       mockDeploymentLister,
				rsLister:      &mockReplicaSetLister{client: fakeCtrlClient},
				runtimeClient: fakeCtrlClient,
				strategy: rolloutsv1alpha1.DeploymentStrategy{
					RollingUpdate: &apps.RollingUpdateDeployment{
						MaxSurge:       &test.maxSurge,
						MaxUnavailable: &test.maxUnavailable,
					},
					Partition: test.partition,
				},
			}

			err = dc.syncDeployment(context.TODO(), &deployment)
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
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
