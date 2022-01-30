package matchers

import (
	"fmt"
<<<<<<< HEAD
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
=======
	"net/http"
	"net/http/httptest"
>>>>>>> 33cbc1d (add batchrelease controller)

	"github.com/onsi/gomega/format"
)

type HaveHTTPStatusMatcher struct {
<<<<<<< HEAD
	Expected []interface{}
=======
	Expected interface{}
>>>>>>> 33cbc1d (add batchrelease controller)
}

func (matcher *HaveHTTPStatusMatcher) Match(actual interface{}) (success bool, err error) {
	var resp *http.Response
	switch a := actual.(type) {
	case *http.Response:
		resp = a
	case *httptest.ResponseRecorder:
		resp = a.Result()
	default:
		return false, fmt.Errorf("HaveHTTPStatus matcher expects *http.Response or *httptest.ResponseRecorder. Got:\n%s", format.Object(actual, 1))
	}

<<<<<<< HEAD
	if len(matcher.Expected) == 0 {
		return false, fmt.Errorf("HaveHTTPStatus matcher must be passed an int or a string. Got nothing")
	}

	for _, expected := range matcher.Expected {
		switch e := expected.(type) {
		case int:
			if resp.StatusCode == e {
				return true, nil
			}
		case string:
			if resp.Status == e {
				return true, nil
			}
		default:
			return false, fmt.Errorf("HaveHTTPStatus matcher must be passed int or string types. Got:\n%s", format.Object(expected, 1))
		}
	}

	return false, nil
}

func (matcher *HaveHTTPStatusMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n%s\n%s\n%s", formatHttpResponse(actual), "to have HTTP status", matcher.expectedString())
}

func (matcher *HaveHTTPStatusMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n%s\n%s\n%s", formatHttpResponse(actual), "not to have HTTP status", matcher.expectedString())
}

func (matcher *HaveHTTPStatusMatcher) expectedString() string {
	var lines []string
	for _, expected := range matcher.Expected {
		lines = append(lines, format.Object(expected, 1))
	}
	return strings.Join(lines, "\n")
}

func formatHttpResponse(input interface{}) string {
	var resp *http.Response
	switch r := input.(type) {
	case *http.Response:
		resp = r
	case *httptest.ResponseRecorder:
		resp = r.Result()
	default:
		return "cannot format invalid HTTP response"
	}

	body := "<nil>"
	if resp.Body != nil {
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			data = []byte("<error reading body>")
		}
		body = format.Object(string(data), 0)
	}

	var s strings.Builder
	s.WriteString(fmt.Sprintf("%s<%s>: {\n", format.Indent, reflect.TypeOf(input)))
	s.WriteString(fmt.Sprintf("%s%sStatus:     %s\n", format.Indent, format.Indent, format.Object(resp.Status, 0)))
	s.WriteString(fmt.Sprintf("%s%sStatusCode: %s\n", format.Indent, format.Indent, format.Object(resp.StatusCode, 0)))
	s.WriteString(fmt.Sprintf("%s%sBody:       %s\n", format.Indent, format.Indent, body))
	s.WriteString(fmt.Sprintf("%s}", format.Indent))

	return s.String()
=======
	switch e := matcher.Expected.(type) {
	case int:
		return resp.StatusCode == e, nil
	case string:
		return resp.Status == e, nil
	}

	return false, fmt.Errorf("HaveHTTPStatus matcher must be passed an int or a string. Got:\n%s", format.Object(matcher.Expected, 1))
}

func (matcher *HaveHTTPStatusMatcher) FailureMessage(actual interface{}) (message string) {
	return format.Message(actual, "to have HTTP status", matcher.Expected)
}

func (matcher *HaveHTTPStatusMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return format.Message(actual, "not to have HTTP status", matcher.Expected)
>>>>>>> 33cbc1d (add batchrelease controller)
}
