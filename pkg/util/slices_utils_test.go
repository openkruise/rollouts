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

package util

import (
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/stretchr/testify/assert"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// stringPointer is a helper to get a pointer to a string literal.
func stringPointer(s string) *string {
	return &s
}

func TestFilterHttpRouteMatch(t *testing.T) {
	headerMatchType := gatewayv1beta1.HeaderMatchExact
	pathMatchType := gatewayv1beta1.PathMatchExact

	// Define a sample slice to test against, matching the REAL API structure
	matches := []v1beta1.HttpRouteMatch{
		{
			// Match 0: Canary Header
			Headers: []gatewayv1beta1.HTTPHeaderMatch{
				{Name: "canary", Type: &headerMatchType, Value: "true"},
			},
		},
		{
			// Match 1: Specific Path
			Path: &gatewayv1beta1.HTTPPathMatch{
				Type:  &pathMatchType,
				Value: stringPointer("/api/v1"),
			},
		},
		{
			// Match 2: Canary Header AND Specific Path
			Headers: []gatewayv1beta1.HTTPHeaderMatch{
				{Name: "canary", Type: &headerMatchType, Value: "true"},
			},
			Path: &gatewayv1beta1.HTTPPathMatch{
				Type:  &pathMatchType,
				Value: stringPointer("/api/v1"),
			},
		},
	}

	testCases := []struct {
		name           string
		inputSlice     []v1beta1.HttpRouteMatch
		filterFunc     func(v1beta1.HttpRouteMatch) bool
		expectedResult []v1beta1.HttpRouteMatch
	}{
		{
			name:       "Filter for 'canary' header",
			inputSlice: matches,
			filterFunc: func(m v1beta1.HttpRouteMatch) bool {
				for _, h := range m.Headers {
					if h.Name == "canary" {
						return true
					}
				}
				return false
			},
			expectedResult: []v1beta1.HttpRouteMatch{matches[0], matches[2]},
		},
		{
			name:       "Filter for specific path value",
			inputSlice: matches,
			filterFunc: func(m v1beta1.HttpRouteMatch) bool {
				// Correctly check for nil and dereference the pointer
				return m.Path != nil && m.Path.Value != nil && *m.Path.Value == "/api/v1"
			},
			expectedResult: []v1beta1.HttpRouteMatch{matches[1], matches[2]},
		},
		{
			name:       "Filter that matches nothing",
			inputSlice: matches,
			filterFunc: func(m v1beta1.HttpRouteMatch) bool {
				return false
			},
			expectedResult: []v1beta1.HttpRouteMatch{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := FilterHttpRouteMatch(tc.inputSlice, tc.filterFunc)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}
