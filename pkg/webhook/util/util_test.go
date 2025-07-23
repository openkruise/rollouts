package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGettersWithDefaults(t *testing.T) {
	testCases := []struct {
		testName     string
		funcName     string
		envVarName   string
		getterFunc   func() string
		defaultValue string
		setValue     string
	}{
		{
			testName:     "GetNamespace with env var set",
			funcName:     "GetNamespace",
			envVarName:   "POD_NAMESPACE",
			getterFunc:   GetNamespace,
			defaultValue: "kruise-rollout",
			setValue:     "my-namespace",
		},
		{
			testName:     "GetSecretName with env var set",
			funcName:     "GetSecretName",
			envVarName:   "SECRET_NAME",
			getterFunc:   GetSecretName,
			defaultValue: "kruise-rollout-webhook-certs",
			setValue:     "my-secret",
		},
		{
			testName:     "GetServiceName with env var set",
			funcName:     "GetServiceName",
			envVarName:   "SERVICE_NAME",
			getterFunc:   GetServiceName,
			defaultValue: "kruise-rollout-webhook-service",
			setValue:     "my-service",
		},
		{
			testName:     "GetCertDir with env var set",
			funcName:     "GetCertDir",
			envVarName:   "WEBHOOK_CERT_DIR",
			getterFunc:   GetCertDir,
			defaultValue: "/tmp/kruise-rollout-webhook-certs",
			setValue:     "/custom/cert/dir",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			t.Setenv(tc.envVarName, tc.setValue)
			assert.Equal(t, tc.setValue, tc.getterFunc())
		})

		defaultCaseName := tc.funcName + " with default value"
		t.Run(defaultCaseName, func(t *testing.T) {
			assert.Equal(t, tc.defaultValue, tc.getterFunc())
		})
	}
}

func TestGetPort(t *testing.T) {
	t.Run("should return default port when env var is not set", func(t *testing.T) {
		port := GetPort()
		assert.Equal(t, 9876, port)
	})

	t.Run("should return port from env var when set correctly", func(t *testing.T) {
		t.Setenv("WEBHOOK_PORT", "8080")
		port := GetPort()
		assert.Equal(t, 8080, port)
	})

}

func TestSimpleGetters(t *testing.T) {
	t.Run("GetHost should return value from env var", func(t *testing.T) {
		expectedHost := "my-test-host"
		t.Setenv("WEBHOOK_HOST", expectedHost)
		assert.Equal(t, expectedHost, GetHost())
	})

	t.Run("GetHost should return empty string if not set", func(t *testing.T) {
		assert.Empty(t, GetHost())
	})

	t.Run("GetCertWriter should return value from env var", func(t *testing.T) {
		t.Setenv("WEBHOOK_CERT_WRITER", "true")
		assert.Equal(t, "true", GetCertWriter())
	})
}
