package matchers

import (
	"fmt"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/matchers/support/goraph/bipartitegraph"
)

type ContainElementsMatcher struct {
	Elements        []interface{}
	missingElements []interface{}
}

func (matcher *ContainElementsMatcher) Match(actual interface{}) (success bool, err error) {
	if !isArrayOrSlice(actual) && !isMap(actual) {
		return false, fmt.Errorf("ContainElements matcher expects an array/slice/map.  Got:\n%s", format.Object(actual, 1))
	}

	matchers := matchers(matcher.Elements)
	bipartiteGraph, err := bipartitegraph.NewBipartiteGraph(valuesOf(actual), matchers, neighbours)
	if err != nil {
		return false, err
	}

	edges := bipartiteGraph.LargestMatching()
	if len(edges) == len(matchers) {
		return true, nil
	}

	_, missingMatchers := bipartiteGraph.FreeLeftRight(edges)
	matcher.missingElements = equalMatchersToElements(missingMatchers)

	return false, nil
}

func (matcher *ContainElementsMatcher) FailureMessage(actual interface{}) (message string) {
<<<<<<< HEAD
	message = format.Message(actual, "to contain elements", presentable(matcher.Elements))
=======
	message = format.Message(actual, "to contain elements", matcher.Elements)
>>>>>>> 33cbc1d (add batchrelease controller)
	return appendMissingElements(message, matcher.missingElements)
}

func (matcher *ContainElementsMatcher) NegatedFailureMessage(actual interface{}) (message string) {
<<<<<<< HEAD
	return format.Message(actual, "not to contain elements", presentable(matcher.Elements))
=======
	return format.Message(actual, "not to contain elements", matcher.Elements)
>>>>>>> 33cbc1d (add batchrelease controller)
}
