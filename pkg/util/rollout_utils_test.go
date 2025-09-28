/*
Copyright 2025 The Kruise Authors.

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
)

func TestAnnotationsDeepEqual(t *testing.T) {
	tests := []struct {
		name         string
		a            map[string]string
		b            map[string]string
		keysToDelete []string
		expected     bool
	}{
		{
			name:     "both nil maps",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "one nil map",
			a:        nil,
			b:        map[string]string{},
			expected: true,
		},
		{
			name:     "both empty maps",
			a:        map[string]string{},
			b:        map[string]string{},
			expected: true,
		},
		{
			name:     "identical maps",
			a:        map[string]string{"key1": "value1", "key2": "value2"},
			b:        map[string]string{"key1": "value1", "key2": "value2"},
			expected: true,
		},
		{
			name:     "different values",
			a:        map[string]string{"key1": "value1", "key2": "value2"},
			b:        map[string]string{"key1": "value1", "key2": "different"},
			expected: false,
		},
		{
			name:     "different lengths",
			a:        map[string]string{"key1": "value1", "key2": "value2"},
			b:        map[string]string{"key1": "value1"},
			expected: false,
		},
		{
			name:         "delete keys from both - equal after deletion",
			a:            map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"},
			b:            map[string]string{"key1": "value1", "key2": "value2", "key4": "value4"},
			keysToDelete: []string{"key3", "key4"},
			expected:     true,
		},
		{
			name:         "delete keys from both - still not equal",
			a:            map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"},
			b:            map[string]string{"key1": "value1", "key2": "different", "key4": "value4"},
			keysToDelete: []string{"key3", "key4"},
			expected:     false,
		},
		{
			name:         "delete non-existent keys",
			a:            map[string]string{"key1": "value1"},
			b:            map[string]string{"key1": "value1"},
			keysToDelete: []string{"nonexistent"},
			expected:     true,
		},
		{
			name:         "delete all keys",
			a:            map[string]string{"key1": "value1", "key2": "value2"},
			b:            map[string]string{"key1": "value1", "key2": "value2"},
			keysToDelete: []string{"key1", "key2"},
			expected:     true,
		},
		{
			name:         "delete all keys from one side",
			a:            map[string]string{"key1": "value1", "key2": "value2"},
			b:            map[string]string{"key1": "value1"},
			keysToDelete: []string{"key2"},
			expected:     true,
		},
		{
			name:         "empty maps after deletion",
			a:            map[string]string{"key1": "value1"},
			b:            map[string]string{"key2": "value2"},
			keysToDelete: []string{"key1", "key2"},
			expected:     true,
		},
		{
			name:         "complex case with multiple deletions",
			a:            map[string]string{"key1": "value1", "key2": "value2", "key3": "value3", "key4": "value4"},
			b:            map[string]string{"key1": "value1", "key2": "value2", "key5": "value5"},
			keysToDelete: []string{"key3", "key4", "key5"},
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create copies to avoid modifying original data
			aCopy := make(map[string]string)
			for k, v := range tt.a {
				aCopy[k] = v
			}

			bCopy := make(map[string]string)
			for k, v := range tt.b {
				bCopy[k] = v
			}

			result := AnnotationsDeepEqual(aCopy, bCopy, tt.keysToDelete...)
			if result != tt.expected {
				t.Errorf("AnnotationsDeepEqual() = %v, want %v", result, tt.expected)
			}
		})
	}
}
