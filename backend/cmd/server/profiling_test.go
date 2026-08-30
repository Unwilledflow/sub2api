package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfilingSettingsFromEnvDisabledByDefault(t *testing.T) {
	t.Setenv(profilingEnabledEnv, "")
	t.Setenv(profilingPortEnv, "")

	settings, err := profilingSettingsFromEnv()
	require.NoError(t, err)
	require.False(t, settings.enabled)
	require.Equal(t, defaultProfilingPort, settings.port)
}

func TestProfilingSettingsFromEnvEnabledOnLoopbackPort(t *testing.T) {
	t.Setenv(profilingEnabledEnv, "true")
	t.Setenv(profilingPortEnv, "16060")

	settings, err := profilingSettingsFromEnv()
	require.NoError(t, err)
	require.True(t, settings.enabled)
	require.Equal(t, 16060, settings.port)
	require.Equal(t, "127.0.0.1:16060", settings.address())
}

func TestProfilingSettingsFromEnvRejectsInvalidValues(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		t.Setenv(profilingEnabledEnv, "sometimes")
		_, err := profilingSettingsFromEnv()
		require.ErrorContains(t, err, profilingEnabledEnv)
	})

	t.Run("port", func(t *testing.T) {
		t.Setenv(profilingEnabledEnv, "true")
		t.Setenv(profilingPortEnv, "70000")
		_, err := profilingSettingsFromEnv()
		require.ErrorContains(t, err, profilingPortEnv)
	})
}

func TestNewProfilingHandlerServesPprofWithoutApplicationFallback(t *testing.T) {
	handler := newProfilingHandler()

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	require.Equal(t, http.StatusOK, index.Code)
	require.Contains(t, index.Body.String(), "Types of profiles available")

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusNotFound, missing.Code)
}
