package matchers

import (
	"fmt"
	"reflect"

<<<<<<< HEAD
=======
	"github.com/onsi/gomega/internal/oraclematcher"
>>>>>>> 33cbc1d (add batchrelease controller)
	"github.com/onsi/gomega/types"
)

type WithTransformMatcher struct {
	// input
<<<<<<< HEAD
	Transform interface{} // must be a function of one parameter that returns one value and an optional error
=======
	Transform interface{} // must be a function of one parameter that returns one value
>>>>>>> 33cbc1d (add batchrelease controller)
	Matcher   types.GomegaMatcher

	// cached value
	transformArgType reflect.Type

	// state
	transformedValue interface{}
}

<<<<<<< HEAD
// reflect.Type for error
var errorT = reflect.TypeOf((*error)(nil)).Elem()

=======
>>>>>>> 33cbc1d (add batchrelease controller)
func NewWithTransformMatcher(transform interface{}, matcher types.GomegaMatcher) *WithTransformMatcher {
	if transform == nil {
		panic("transform function cannot be nil")
	}
	txType := reflect.TypeOf(transform)
	if txType.NumIn() != 1 {
		panic("transform function must have 1 argument")
	}
<<<<<<< HEAD
	if numout := txType.NumOut(); numout != 1 {
		if numout != 2 || !txType.Out(1).AssignableTo(errorT) {
			panic("transform function must either have 1 return value, or 1 return value plus 1 error value")
		}
=======
	if txType.NumOut() != 1 {
		panic("transform function must have 1 return value")
>>>>>>> 33cbc1d (add batchrelease controller)
	}

	return &WithTransformMatcher{
		Transform:        transform,
		Matcher:          matcher,
		transformArgType: reflect.TypeOf(transform).In(0),
	}
}

func (m *WithTransformMatcher) Match(actual interface{}) (bool, error) {
<<<<<<< HEAD
	// prepare a parameter to pass to the Transform function
	var param reflect.Value
	if actual != nil && reflect.TypeOf(actual).AssignableTo(m.transformArgType) {
		// The dynamic type of actual is compatible with the transform argument.
		param = reflect.ValueOf(actual)

	} else if actual == nil && m.transformArgType.Kind() == reflect.Interface {
		// The dynamic type of actual is unknown, so there's no way to make its
		// reflect.Value. Create a nil of the transform argument, which is known.
		param = reflect.Zero(m.transformArgType)

	} else {
		return false, fmt.Errorf("Transform function expects '%s' but we have '%T'", m.transformArgType, actual)
=======
	// return error if actual's type is incompatible with Transform function's argument type
	actualType := reflect.TypeOf(actual)
	if !actualType.AssignableTo(m.transformArgType) {
		return false, fmt.Errorf("Transform function expects '%s' but we have '%s'", m.transformArgType, actualType)
>>>>>>> 33cbc1d (add batchrelease controller)
	}

	// call the Transform function with `actual`
	fn := reflect.ValueOf(m.Transform)
<<<<<<< HEAD
	result := fn.Call([]reflect.Value{param})
	if len(result) == 2 {
		if !result[1].IsNil() {
			return false, fmt.Errorf("Transform function failed: %s", result[1].Interface().(error).Error())
		}
	}
=======
	result := fn.Call([]reflect.Value{reflect.ValueOf(actual)})
>>>>>>> 33cbc1d (add batchrelease controller)
	m.transformedValue = result[0].Interface() // expect exactly one value

	return m.Matcher.Match(m.transformedValue)
}

func (m *WithTransformMatcher) FailureMessage(_ interface{}) (message string) {
	return m.Matcher.FailureMessage(m.transformedValue)
}

func (m *WithTransformMatcher) NegatedFailureMessage(_ interface{}) (message string) {
	return m.Matcher.NegatedFailureMessage(m.transformedValue)
}

func (m *WithTransformMatcher) MatchMayChangeInTheFuture(_ interface{}) bool {
	// TODO: Maybe this should always just return true? (Only an issue for non-deterministic transformers.)
	//
	// Querying the next matcher is fine if the transformer always will return the same value.
	// But if the transformer is non-deterministic and returns a different value each time, then there
	// is no point in querying the next matcher, since it can only comment on the last transformed value.
<<<<<<< HEAD
	return types.MatchMayChangeInTheFuture(m.Matcher, m.transformedValue)
=======
	return oraclematcher.MatchMayChangeInTheFuture(m.Matcher, m.transformedValue)
>>>>>>> 33cbc1d (add batchrelease controller)
}
