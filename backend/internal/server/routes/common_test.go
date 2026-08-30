package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCommonRoutesOperationalProbes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router)

	for _, path := range []string{"/health", "/ready"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "path=%s", path)
		require.JSONEq(t, `{"status":"ok"}`, w.Body.String(), "path=%s", path)
	}
}

func TestCommonRoutesReadyWithDependencyChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutesWithOptions(router, CommonRouteOptions{
		ReadinessChecks: []ReadinessCheck{
			{
				Name: "database",
				Check: func(context.Context) error {
					return nil
				},
			},
			{
				Name: "redis",
				Check: func(context.Context) error {
					return nil
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"status":"ok","checks":{"database":{"status":"ok"},"redis":{"status":"ok"}}}`, w.Body.String())
}

func TestCommonRoutesReadyDegradedWithoutLeakingErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutesWithOptions(router, CommonRouteOptions{
		ReadinessChecks: []ReadinessCheck{
			{
				Name: "database",
				Check: func(context.Context) error {
					return errors.New("password=secret database timeout")
				},
			},
			{
				Name: "redis",
				Check: func(context.Context) error {
					return nil
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.JSONEq(t, `{"status":"degraded","checks":{"database":{"status":"error"},"redis":{"status":"ok"}}}`, w.Body.String())
	require.NotContains(t, w.Body.String(), "secret")
	require.NotContains(t, w.Body.String(), "timeout")
}

func TestCommonRoutesTelemetryAndSetupStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router)

	telemetryReq := httptest.NewRequest(http.MethodPost, "/api/event_logging/batch", nil)
	telemetryRecorder := httptest.NewRecorder()
	router.ServeHTTP(telemetryRecorder, telemetryReq)
	require.Equal(t, http.StatusOK, telemetryRecorder.Code)

	setupReq := httptest.NewRequest(http.MethodGet, "/setup/status", nil)
	setupRecorder := httptest.NewRecorder()
	router.ServeHTTP(setupRecorder, setupReq)
	require.Equal(t, http.StatusOK, setupRecorder.Code)
	require.JSONEq(t, `{"code":0,"data":{"needs_setup":false,"step":"completed"}}`, setupRecorder.Body.String())
}
