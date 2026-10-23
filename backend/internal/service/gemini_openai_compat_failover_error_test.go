//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiOpenAICompatFailoverErrorRequestScope(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       int
		body         string
		wantFailover bool
	}{
		{"unsupported parameter on 404", http.StatusNotFound, `{"error":{"message":"unsupported parameter: temperature"}}`, false},
		{"unsupported parameter on 503", http.StatusServiceUnavailable, `{"error":{"message":"unsupported parameter: temperature"}}`, false},
		{"dispatch policy", http.StatusForbidden, `{"error":{"message":"This group does not allow /v1/messages dispatch"}}`, false},
		{"credential forbidden", http.StatusForbidden, `{"error":{"message":"API key invalid"}}`, true},
		{"policy echoed in metadata", http.StatusForbidden, `{"error":{"message":"API key invalid"},"metadata":"This group does not allow /v1/messages dispatch"}`, true},
		{"model missing", http.StatusNotFound, `{"error":{"message":"model not found","code":"model_not_found"}}`, true},
		{"route missing", http.StatusNotFound, `<html><h1>404 Not Found</h1></html>`, true},
		{"capacity", http.StatusServiceUnavailable, `{"error":{"message":"service unavailable"}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &GeminiMessagesCompatService{}
			err := svc.geminiOpenAICompatFailoverError(nil, &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey}, "gemini-2.5-flash", tt.status, []byte(tt.body), "req-1")
			if tt.wantFailover {
				require.NotNil(t, err)
				require.Equal(t, tt.status, err.StatusCode)
			} else {
				require.Nil(t, err)
			}
		})
	}
}
