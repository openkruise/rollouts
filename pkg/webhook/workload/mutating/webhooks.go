/*
Copyright 2019 The Kruise Authors.

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

package mutating

import (
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mutate-apps-kruise-io-v1alpha1-cloneset,mutating=true,failurePolicy=Ignore,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups=apps.kruise.io,resources=clonesets,verbs=update,versions=v1alpha1,name=mcloneset.kb.io
// +kubebuilder:webhook:path=/mutate-apps-v1-deployment,mutating=true,failurePolicy=Ignore,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups=apps,resources=deployments,verbs=update,versions=v1,name=mdeployment.kb.io
// +kubebuilder:webhook:path=/mutate-apps-v1-statefulset,mutating=true,failurePolicy=Ignore,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups=apps,resources=statefulsets,verbs=update,versions=v1,name=mstatefulset.kb.io
// +kubebuilder:webhook:path=/mutate-apps-kruise-io-statefulset,mutating=true,failurePolicy=Ignore,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups=apps.kruise.io,resources=statefulsets,verbs=create;update,versions=v1alpha1;v1beta1,name=madvancedstatefulset.kb.io
// +kubebuilder:webhook:path=/mutate-unified-workload,mutating=true,failurePolicy=Ignore,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups=*,resources=*,verbs=create;update,versions=*,name=munifiedworload.kb.io

var (
	// HandlerMap contains admission webhook handlers
	HandlerMap = map[string]admission.Handler{
		"mutate-apps-kruise-io-v1alpha1-cloneset": &WorkloadHandler{},
		"mutate-apps-v1-deployment":               &WorkloadHandler{},
		"mutate-apps-v1-statefulset":              &WorkloadHandler{},
		"mutate-apps-kruise-io-statefulset":       &WorkloadHandler{},
		"mutate-unified-workload":                 &WorkloadHandler{},
	}
)
