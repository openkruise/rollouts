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

package rollout

import (
	kruisev1aplphal "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	scheme *runtime.Scheme

	rolloutDemo = &rolloutv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
		},
		Spec: rolloutv1alpha1.RolloutSpec{
			ObjectRef: rolloutv1alpha1.ObjectRef{
				Type: rolloutv1alpha1.WorkloadRefType,
				WorkloadRef: &rolloutv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "echoserver",
				},
			},
		},
	}

	deploymentDemo = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "echoserver",
			Labels: map[string]string{},
		},
	}
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = kruisev1aplphal.AddToScheme(scheme)
	_ = rolloutv1alpha1.AddToScheme(scheme)
}
