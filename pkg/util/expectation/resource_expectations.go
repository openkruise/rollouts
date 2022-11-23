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

package expectations

import (
	"flag"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
)

// Action is the action, like create and delete.
type Action string

const (
	// Create action
	Create Action = "create"
	// Delete action
	Delete Action = "delete"
)

var (
	ExpectationTimeout   time.Duration
	ResourceExpectations = NewResourceExpectations()
)

func init() {
	flag.DurationVar(&ExpectationTimeout, "expectation-timeout", time.Minute*5, "The expectation timeout. Defaults 5min")
}

// Expectations is an interface that allows users to set and wait on expectations of resource creation and deletion.
type Expectations interface {
	Expect(controllerKey string, action Action, name string)
	Observe(controllerKey string, action Action, name string)
	SatisfiedExpectations(controllerKey string) (bool, time.Duration, map[Action][]string)
	DeleteExpectations(controllerKey string)
	GetExpectations(controllerKey string) map[Action]sets.String
}

// NewResourceExpectations returns a common Expectations.
func NewResourceExpectations() Expectations {
	return &realResourceExpectations{
		controllerCache: make(map[string]*realControllerResourceExpectations),
	}
}

type realResourceExpectations struct {
	sync.Mutex
	// key: parent key, workload namespace/name
	controllerCache map[string]*realControllerResourceExpectations
}

type realControllerResourceExpectations struct {
	// item: name for this object
	objsCache                 map[Action]sets.String
	firstUnsatisfiedTimestamp time.Time
}

func (r *realResourceExpectations) GetExpectations(controllerKey string) map[Action]sets.String {
	r.Lock()
	defer r.Unlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		return nil
	}

	res := make(map[Action]sets.String, len(expectations.objsCache))
	for k, v := range expectations.objsCache {
		res[k] = sets.NewString(v.List()...)
	}

	return res
}

func (r *realResourceExpectations) Expect(controllerKey string, action Action, name string) {
	r.Lock()
	defer r.Unlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		expectations = &realControllerResourceExpectations{
			objsCache: make(map[Action]sets.String),
		}
		r.controllerCache[controllerKey] = expectations
	}

	if s := expectations.objsCache[action]; s != nil {
		s.Insert(name)
	} else {
		expectations.objsCache[action] = sets.NewString(name)
	}
}

func (r *realResourceExpectations) Observe(controllerKey string, action Action, name string) {
	r.Lock()
	defer r.Unlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		return
	}

	s := expectations.objsCache[action]
	if s == nil {
		return
	}
	s.Delete(name)

	for _, s := range expectations.objsCache {
		if s.Len() > 0 {
			return
		}
	}
	delete(r.controllerCache, controllerKey)
}

func (r *realResourceExpectations) SatisfiedExpectations(controllerKey string) (bool, time.Duration, map[Action][]string) {
	r.Lock()
	defer r.Unlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		return true, 0, nil
	}

	for a, s := range expectations.objsCache {
		if s.Len() > 0 {
			if expectations.firstUnsatisfiedTimestamp.IsZero() {
				expectations.firstUnsatisfiedTimestamp = time.Now()
			}
			return false, time.Since(expectations.firstUnsatisfiedTimestamp), map[Action][]string{a: s.List()}
		}
	}

	delete(r.controllerCache, controllerKey)
	return true, 0, nil
}

func (r *realResourceExpectations) DeleteExpectations(controllerKey string) {
	r.Lock()
	defer r.Unlock()
	delete(r.controllerCache, controllerKey)
}
