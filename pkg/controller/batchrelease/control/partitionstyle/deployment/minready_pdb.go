/*
Copyright 2026 The Kruise Authors.

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

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (mc *MinReadyControl) hasPDBCoveringDeployment() (bool, error) {
	pdbList := &policyv1.PodDisruptionBudgetList{}
	if err := mc.client.List(context.TODO(), pdbList, client.InNamespace(mc.object.Namespace)); err != nil {
		return false, fmt.Errorf("%s: list PDBs: %w", EventDegradedPDBIncompatible, err)
	}
	templateLabels := labels.Set(mc.object.Spec.Template.Labels)
	for i := range pdbList.Items {
		covered, err := pdbCoversLabels(&pdbList.Items[i], templateLabels)
		if err != nil || covered {
			return covered, err
		}
	}
	return false, nil
}

func pdbCoversLabels(pdb *policyv1.PodDisruptionBudget, templateLabels labels.Set) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
	if err != nil {
		return false, fmt.Errorf("%s: invalid PDB selector %s/%s: %w",
			EventDegradedPDBIncompatible, pdb.Namespace, pdb.Name, err)
	}
	return selector.Matches(templateLabels), nil
}
