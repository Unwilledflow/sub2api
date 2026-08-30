//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRateLimitService_BalanceErrorsUseTemporaryCooldown(t *testing.T) {
	tests := []struct {
		name       string
		platform   string
		statusCode int
		body       string
	}{
		{
			name:       "anthropic credit balance 400",
			platform:   PlatformAnthropic,
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"credit balance is too low"}}`,
		},
		{
			name:       "gemini insufficient balance 403",
			platform:   PlatformGemini,
			statusCode: http.StatusForbidden,
			body:       `{"error":{"message":"insufficient balance"}}`,
		},
		{
			name:       "generic payment required 402",
			platform:   PlatformOpenAI,
			statusCode: http.StatusPaymentRequired,
			body:       `{"error":{"message":"billing payment required"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &Account{
				ID:          2277,
				Platform:    tt.platform,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
			}

			shouldDisable := svc.HandleUpstreamError(
				context.Background(), account, tt.statusCode, http.Header{}, []byte(tt.body),
			)

			require.True(t, shouldDisable)
			require.Zero(t, repo.setErrorCalls, "balance failures must not become permanent account errors")
			require.Equal(t, 1, repo.tempCalls)
			require.Equal(t, account.ID, repo.lastTempID)
			require.Contains(t, repo.lastTempReason, "balance")
		})
	}
}
