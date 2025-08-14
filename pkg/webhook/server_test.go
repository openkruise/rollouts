package webhook

import (
	"testing"

	"github.com/openkruise/rollouts/pkg/webhook/types"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func resetGlobals() {
	HandlerMap = map[string]types.HandlerGetter{}
	handlerGates = map[string]GateFunc{}
}

func TestAddHandlers(t *testing.T) {
	resetGlobals()

	// We don't need a real handler implementation for these unit tests.
	h1Getter := func(mgr manager.Manager) admission.Handler { return nil }
	h2Getter := func(mgr manager.Manager) admission.Handler { return nil }

	gateFunc := func() bool { return true }

	t.Run("addHandlers should add handlers to the map", func(t *testing.T) {
		addHandlers(map[string]types.HandlerGetter{"/path1": h1Getter})
		assert.Len(t, HandlerMap, 1)
		assert.Contains(t, HandlerMap, "/path1")
		assert.Len(t, handlerGates, 0, "addHandlers should not add gates")
	})

	t.Run("addHandlersWithGate should add handlers and gates", func(t *testing.T) {
		resetGlobals() // reset for this specific test
		addHandlersWithGate(map[string]types.HandlerGetter{"/path2": h2Getter}, gateFunc)
		assert.Len(t, HandlerMap, 1)
		assert.Contains(t, HandlerMap, "/path2")
		assert.Len(t, handlerGates, 1)
		assert.Contains(t, handlerGates, "/path2")
	})

	t.Run("should add a leading slash to paths if missing", func(t *testing.T) {
		resetGlobals()
		addHandlers(map[string]types.HandlerGetter{"path3": h1Getter})
		assert.Len(t, HandlerMap, 1)
		assert.Contains(t, HandlerMap, "/path3")
		assert.NotContains(t, HandlerMap, "path3")
	})

	t.Run("should skip handlers with an empty path", func(t *testing.T) {
		resetGlobals()
		addHandlers(map[string]types.HandlerGetter{"": h1Getter})
		assert.Empty(t, HandlerMap)
	})
}

func TestFilterActiveHandlers(t *testing.T) {
	resetGlobals()

	HandlerMap = map[string]types.HandlerGetter{
		"/active-handler":   func(mgr manager.Manager) admission.Handler { return nil },
		"/inactive-handler": func(mgr manager.Manager) admission.Handler { return nil },
		"/no-gate-handler":  func(mgr manager.Manager) admission.Handler { return nil },
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
