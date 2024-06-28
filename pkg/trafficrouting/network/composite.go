/*
Copyright 2024 The Kruise Authors.

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

package network

import (
	"context"

	"go.uber.org/multierr"

	"github.com/openkruise/rollouts/api/v1beta1"
)

var (
	_ NetworkProvider = (CompositeController)(nil)
)

// CompositeController is a set of NetworkProvider
type CompositeController []NetworkProvider

func (c CompositeController) Initialize(ctx context.Context) error {
	var err error
	for _, provider := range c {
		err = multierr.Append(err, provider.Initialize(ctx))
	}
	return err
}

func (c CompositeController) EnsureRoutes(ctx context.Context, strategy *v1beta1.TrafficRoutingStrategy) (bool, error) {
	done := true
	for _, provider := range c {
		innerDone, innerErr := provider.EnsureRoutes(ctx, strategy)
		if innerErr != nil {
			return false, innerErr
		} else if !innerDone {
			done = false
		}
	}
	return done, nil
}

func (c CompositeController) Finalise(ctx context.Context) error {
	var err error
	for _, provider := range c {
		err = multierr.Append(err, provider.Finalise(ctx))
	}
	return err
}
