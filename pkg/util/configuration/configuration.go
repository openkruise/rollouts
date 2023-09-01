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
	"context"
	"fmt"

	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// kruise rollout configmap name
	RolloutConfigurationName = "kruise-rollout-configuration"

	LuaTrafficRoutingIngressTypePrefix = "lua.traffic.routing.ingress"
	LuaTrafficRoutingCustomTypePrefix  = "lua.traffic.routing"
)

func GetTrafficRoutingIngressLuaScript(client client.Client, iType string) (string, error) {
	data, err := getRolloutConfiguration(client)
	if err != nil {
		return "", err
	} else if len(data) == 0 {
		return "", nil
	}
	value, ok := data[fmt.Sprintf("%s.%s", LuaTrafficRoutingIngressTypePrefix, iType)]
	if !ok {
		return "", nil
	}
	return value, nil
}

func getRolloutConfiguration(c client.Client) (map[string]string, error) {
	cfg := &corev1.ConfigMap{}
	err := c.Get(context.TODO(), client.ObjectKey{Namespace: util.GetRolloutNamespace(), Name: RolloutConfigurationName}, cfg)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return cfg.Data, nil
}
