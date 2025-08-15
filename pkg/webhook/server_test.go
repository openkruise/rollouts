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

package webhook

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openkruise/rollouts/pkg/webhook/types"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func resetGlobals() {
	HandlerMap = map[string]types.HandlerGetter{}
	handlerGates = map[string]GateFunc{}
	initialize = initializeImpl
}

type mockWebhookServer struct {
	registered map[string]http.Handler
}

func (m *mockWebhookServer) Register(path string, handler http.Handler) {
	if m.registered == nil {
		m.registered = make(map[string]http.Handler)
	}
	m.registered[path] = handler
}
func (m *mockWebhookServer) Start(context.Context) error { return nil }
func (m *mockWebhookServer) NeedLeaderElection() bool    { return false }
func (m *mockWebhookServer) StartedChecker() healthz.Checker {
	return func(req *http.Request) error { return nil }
}
func (m *mockWebhookServer) WebhookMux() *http.ServeMux { return nil }

type mockManager struct {
	manager.Manager
	server webhook.Server
	config *rest.Config
	scheme *runtime.Scheme
}

func (m *mockManager) GetWebhookServer() webhook.Server { return m.server }
func (m *mockManager) GetConfig() *rest.Config          { return m.config }
func (m *mockManager) GetScheme() *runtime.Scheme       { return m.scheme }

func TestAddHandlers(t *testing.T) {
	resetGlobals()
	h1Getter := func(mgr manager.Manager) admission.Handler { return nil }
	gateFunc := func() bool { return true }

	t.Run("addHandlers should add handlers to the map", func(t *testing.T) {
		addHandlers(map[string]types.HandlerGetter{"/path1": h1Getter})
		assert.Len(t, HandlerMap, 1)
	})

	t.Run("addHandlersWithGate should add handlers and gates", func(t *testing.T) {
		resetGlobals()
		addHandlersWithGate(map[string]types.HandlerGetter{"/path2": h1Getter}, gateFunc)
		assert.Len(t, HandlerMap, 1)
		assert.Len(t, handlerGates, 1)
	})
}

func TestFilterActiveHandlers(t *testing.T) {
	resetGlobals()
	HandlerMap = map[string]types.HandlerGetter{
		"/active-handler":   func(mgr manager.Manager) admission.Handler { return nil },
		"/inactive-handler": func(mgr manager.Manager) admission.Handler { return nil },
	}
	handlerGates = map[string]GateFunc{
		"/active-handler":   func() bool { return true },
		"/inactive-handler": func() bool { return false },
	}

	filterActiveHandlers()

	assert.Len(t, HandlerMap, 1)
	assert.Contains(t, HandlerMap, "/active-handler")
	assert.NotContains(t, HandlerMap, "/inactive-handler")
}

func TestSetupWithManager(t *testing.T) {
	t.Run("should register active handlers and filter inactive ones", func(t *testing.T) {
		// Arrange
		resetGlobals()
		initialize = func(ctx context.Context, cfg *rest.Config) error {
			return nil
		}
		addHandlersWithGate(map[string]types.HandlerGetter{"/active-gated": func(mgr manager.Manager) admission.Handler { return &admission.Webhook{} }}, func() bool { return true })
		addHandlersWithGate(map[string]types.HandlerGetter{"/inactive-gated": func(mgr manager.Manager) admission.Handler { return &admission.Webhook{} }}, func() bool { return false })
		mockServer := &mockWebhookServer{}
		mockMgr := &mockManager{
			server: mockServer,
			config: &rest.Config{},
			scheme: runtime.NewScheme(),
		}

		// Act
		err := SetupWithManager(mockMgr)

		// Assert
		assert.NoError(t, err)
		registeredHandlers := mockServer.registered
		assert.Len(t, registeredHandlers, 2)
		assert.Contains(t, registeredHandlers, "/active-gated")
		assert.Contains(t, registeredHandlers, "/convert")
		assert.NotContains(t, registeredHandlers, "/inactive-gated")
	})

	t.Run("should return error if webhook controller fails to initialize", func(t *testing.T) {
		// Arrange
		resetGlobals()
		expectedErr := errors.New("initialization failed")
		initialize = func(ctx context.Context, cfg *rest.Config) error {
			return expectedErr
		}
		mockMgr := &mockManager{
			server: &mockWebhookServer{},
			config: nil,
			scheme: runtime.NewScheme(),
		}

		// Act
		err := SetupWithManager(mockMgr)

		// Assert
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})
}
