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

package configuration

import (
	"fmt"
	"os"
	"testing"

	"github.com/openkruise/rollouts/pkg/util"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetTrafficRoutingIngressLuaScript(t *testing.T) {
	os.Setenv("POD_NAMESPACE", "kruise-rollout")
	defer os.Unsetenv("POD_NAMESPACE")

	cases := []struct {
		name           string
		ingressType    string
		existingCM     *corev1.ConfigMap
		expectedScript string
		expectErr      bool
	}{
		{
			name:           "ConfigMap not found",
			ingressType:    "nginx",
			existingCM:     nil,
			expectedScript: "",
			expectErr:      false,
		},
		{
			name:        "ConfigMap exists but key not found",
			ingressType: "nginx",
			existingCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: util.GetRolloutNamespace(),
					Name:      RolloutConfigurationName,
				},
				Data: map[string]string{
					"another-key": "another-value",
				},
			},
			expectedScript: "",
			expectErr:      false,
		},
		{
			name:        "Successfully retrieve Lua script",
			ingressType: "nginx",
			existingCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: util.GetRolloutNamespace(),
					Name:      RolloutConfigurationName,
				},
				Data: map[string]string{
					fmt.Sprintf("%s.nginx", LuaTrafficRoutingIngressTypePrefix): "return 'hello world'",
				},
			},
			expectedScript: "return 'hello world'",
			expectErr:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cli client.Client
			if tc.existingCM != nil {
				cli = fake.NewClientBuilder().WithRuntimeObjects(tc.existingCM).Build()
			} else {
				cli = fake.NewClientBuilder().Build()
			}

			script, err := GetTrafficRoutingIngressLuaScript(cli, tc.ingressType)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedScript, script)
			}
		})
	}
}

func TestGetRolloutConfiguration(t *testing.T) {
	os.Setenv("POD_NAMESPACE", "kruise-rollout")
	defer os.Unsetenv("POD_NAMESPACE")

	cases := []struct {
		name         string
		existingCM   *corev1.ConfigMap
		expectedData map[string]string
		expectErr    bool
	}{
		{
			name:         "ConfigMap not found",
			existingCM:   nil,
			expectedData: nil,
			expectErr:    false,
		},
		{
			name: "Successfully retrieve ConfigMap data",
			existingCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: util.GetRolloutNamespace(),
					Name:      RolloutConfigurationName,
				},
				Data: map[string]string{
					"key1": "value1",
				},
			},
			expectedData: map[string]string{
				"key1": "value1",
			},
			expectErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cli client.Client
			if tc.existingCM != nil {
				cli = fake.NewClientBuilder().WithRuntimeObjects(tc.existingCM).Build()
			} else {
				cli = fake.NewClientBuilder().Build()
			}
			data, err := getRolloutConfiguration(cli)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedData, data)
			}
		})
	}
}
