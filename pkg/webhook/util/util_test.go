package util

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test cases for functions that return a default value
var defaultTestCases = []struct {
	funcName     string
	envVarName   string
	getterFunc   func() string
	defaultValue string
	setValue     string
}{
	{
		funcName:     "GetNamespace",
		envVarName:   "POD_NAMESPACE",
		getterFunc:   GetNamespace,
		defaultValue: "kruise-rollout",
		setValue:     "my-namespace",
	},
	{
		funcName:     "GetSecretName",
		envVarName:   "SECRET_NAME",
		getterFunc:   GetSecretName,
		defaultValue: "kruise-rollout-webhook-certs",
		setValue:     "my-secret",
	},
	{
		funcName:     "GetServiceName",
		envVarName:   "SERVICE_NAME",
		getterFunc:   GetServiceName,
		defaultValue: "kruise-rollout-webhook-service",
		setValue:     "my-service",
	},
	{
		funcName:     "GetCertDir",
		envVarName:   "WEBHOOK_CERT_DIR",
		getterFunc:   GetCertDir,
		defaultValue: "/tmp/kruise-rollout-webhook-certs",
		setValue:     "/custom/cert/dir",
	},
}

func TestGettersWithEnvVarSet(t *testing.T) {
	for _, tc := range defaultTestCases {
		t.Run(tc.funcName, func(t *testing.T) {
			t.Setenv(tc.envVarName, tc.setValue)
			assert.Equal(t, tc.setValue, tc.getterFunc())
		})
	}
}

func TestGettersWithDefaultValue(t *testing.T) {
	for _, tc := range defaultTestCases {
		t.Run(tc.funcName, func(t *testing.T) {
			// Ensure the environment variable is not set for this test
			os.Unsetenv(tc.envVarName)
			assert.Equal(t, tc.defaultValue, tc.getterFunc())
		})
	}
}

func TestGetPort(t *testing.T) {
	t.Run("should return default port when env var is not set", func(t *testing.T) {
		os.Unsetenv("WEBHOOK_PORT")
		port := GetPort()
		assert.Equal(t, 9876, port)
	})

	t.Run("should return port from env var when set correctly", func(t *testing.T) {
		t.Setenv("WEBHOOK_PORT", "8080")
		port := GetPort()
		assert.Equal(t, 8080, port)
	})
}

// This test checks for code that calls klog.Fatalf, which exits the program.
// It works by re-running the test in a separate process.
func TestGetPort_Fatal(t *testing.T) {
	if os.Getenv("BE_CRASHER") == "1" {
		t.Setenv("WEBHOOK_PORT", "not-a-number")
		GetPort()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGetPort_Fatal$")
	cmd.Env = append(os.Environ(), "BE_CRASHER=1")
	err := cmd.Run()
	e, ok := err.(*exec.ExitError)
	assert.True(t, ok, "Expected command to exit with an error")
	assert.False(t, e.Success(), "Expected command to fail")
}

func TestSimpleGetters(t *testing.T) {
	t.Run("GetHost should return value from env var", func(t *testing.T) {
		expectedHost := "my-test-host"
		t.Setenv("WEBHOOK_HOST", expectedHost)
		assert.Equal(t, expectedHost, GetHost())
	})

	t.Run("GetHost should return empty string if not set", func(t *testing.T) {
		os.Unsetenv("WEBHOOK_HOST")
		assert.Empty(t, GetHost())
	})

	t.Run("GetCertWriter should return value from env var", func(t *testing.T) {
		t.Setenv("WEBHOOK_CERT_WRITER", "true")
		assert.Equal(t, "true", GetCertWriter())
	})
}
