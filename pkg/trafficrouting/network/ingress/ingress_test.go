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

package ingress

import (
	"github.com/openkruise/rollouts/api/v1alpha1"
	a6v2 "github.com/openkruise/rollouts/pkg/apis/apisix/v2"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	scheme       *runtime.Scheme
	apisixScheme *runtime.Scheme
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	apisixScheme = runtime.NewScheme()
	_ = a6v2.AddToScheme(apisixScheme)
	_ = clientgoscheme.AddToScheme(apisixScheme)
	_ = v1alpha1.AddToScheme(apisixScheme)

}
