package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type mockHandler struct {
	admission.Handler
}

func resetGlobals() {
	HandlerMap = map[string]admission.Handler{}
	handlerGates = map[string]GateFunc{}
}

func TestAddHandlers(t *testing.T) {
	resetGlobals()

	h1 := &mockHandler{}
	h2 := &mockHandler{}
	gateFunc := func() bool { return true }

	t.Run("addHandlers should add handlers to the map", func(t *testing.T) {
		addHandlers(map[string]admission.Handler{"/path1": h1})
		assert.Len(t, HandlerMap, 1)
		assert.Contains(t, HandlerMap, "/path1")
		assert.Len(t, handlerGates, 0, "addHandlers should not add gates")
	})

	t.Run("addHandlersWithGate should add handlers and gates", func(t *testing.T) {
		resetGlobals() // reset for this specific test
		addHandlersWithGate(map[string]admission.Handler{"/path2": h2}, gateFunc)
		assert.Len(t, HandlerMap, 1)
		assert.Contains(t, HandlerMap, "/path2")
		assert.Len(t, handlerGates, 1)
		assert.Contains(t, handlerGates, "/path2")
	})

	t.Run("should add a leading slash to paths if missing", func(t *testing.T) {
		resetGlobals()
		addHandlers(map[string]admission.Handler{"path3": h1})
		assert.Len(t, HandlerMap, 1)
		assert.Contains(t, HandlerMap, "/path3")
		assert.NotContains(t, HandlerMap, "path3")
	})

	t.Run("should skip handlers with an empty path", func(t *testing.T) {
		resetGlobals()
		addHandlers(map[string]admission.Handler{"": h1})
		assert.Empty(t, HandlerMap)
	})
}

func TestFilterActiveHandlers(t *testing.T) {
	resetGlobals()

	HandlerMap = map[string]admission.Handler{
		"/active-handler":   &mockHandler{},
		"/inactive-handler": &mockHandler{},
		"/no-gate-handler":  &mockHandler{},
	}

	handlerGates = map[string]GateFunc{
		"/active-handler":   func() bool { return true },
		"/inactive-handler": func() bool { return false },
	}

	filterActiveHandlers()

	assert.Len(t, HandlerMap, 2, "Expected one handler to be filtered out")
	assert.Contains(t, HandlerMap, "/active-handler")
	assert.Contains(t, HandlerMap, "/no-gate-handler")
	assert.NotContains(t, HandlerMap, "/inactive-handler", "Inactive handler should have been deleted")
}
