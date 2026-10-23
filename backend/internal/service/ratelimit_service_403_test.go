//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type runtimeBlockRecorder struct {
	accounts   []*Account
	until      []time.Time
	reasons    []string
	clearedIDs []int64
}

func (r *runtimeBlockRecorder) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	r.accounts = append(r.accounts, account)
	r.until = append(r.until, until)
	r.reasons = append(r.reasons, reason)
}

func (r *runtimeBlockRecorder) ClearAccountSchedulingBlock(accountID int64) {
	r.clearedIDs = append(r.clearedIDs, accountID)
}

func TestRateLimitService_HandleUpstreamError_OpenAI403FirstHitTempUnschedulable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{1}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &Account{
		ID:       301,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"temporary edge rejection"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, "temporary edge rejection")
	require.Contains(t, repo.lastTempReason, "(1/3)")
	require.Len(t, blocker.accounts, 1)
	require.Equal(t, account.ID, blocker.accounts[0].ID)
	require.Equal(t, "openai_403_temp", blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestRateLimitService_HandleUpstreamError_OpenAI403ThresholdDisables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{3}}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	account := &Account{
		ID:       302,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"workspace forbidden by policy"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, repo.lastErrorMsg, "workspace forbidden by policy")
	require.Contains(t, repo.lastErrorMsg, "consecutive_403=3/3")
}

func TestMessagesDispatchPolicyError_IsClientOnlyAndSpecific(t *testing.T) {
	body := []byte(`{"error":{"type":"permission_error","message":"This group does not allow /v1/messages dispatch"}}`)
	class := ClassifyUpstreamFailure(http.StatusForbidden, nil, body, nil)
	require.Equal(t, UpstreamFailureClient, class.Kind)
	require.False(t, class.Failover)
	require.False(t, class.PunishAccount)
	require.False(t, isMessagesDispatchPolicyError(http.StatusForbidden, []byte(`{"error":{"message":"invalid api key; this group does not allow /v1/messages dispatch"}}`)))
	require.True(t, isMessagesDispatchPolicyError(http.StatusForbidden, body))
	require.False(t, isMessagesDispatchPolicyError(http.StatusUnauthorized, body))
}

func TestMessagesDispatchPolicyError_RepeatedDenialsNeverMutateAccount(t *testing.T) {
	body := []byte(`{"error":{"type":"permission_error","message":"This group does not allow /v1/messages dispatch"}}`)
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGrok} {
		for _, policy := range []string{"default", "custom", "temp", "pool"} {
			for _, cacheMode := range []string{"threshold", "missing", "failure"} {
				t.Run(platform+"/"+policy+"/"+cacheMode, func(t *testing.T) {
					repo := &rateLimitAccountRepoStub{}
					blocker := &runtimeBlockRecorder{}
					svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
					svc.SetAccountRuntimeBlocker(blocker)
					counter := &openAI403CounterCacheStub{counts: []int64{3}}
					if cacheMode == "failure" {
						counter.err = errors.New("counter unavailable")
					}
					if cacheMode != "missing" {
						svc.SetOpenAI403CounterCache(counter)
					}
					account := &Account{ID: 304, Platform: platform, Type: AccountTypeOAuth, Credentials: map[string]any{}}
					if policy == "custom" {
						account.Credentials["custom_error_codes_enabled"] = true
						account.Credentials["custom_error_codes"] = []any{float64(403)}
					}
					if policy == "temp" || policy == "pool" {
						account.Credentials["temp_unschedulable_enabled"] = true
						account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
							"error_code": float64(403), "keywords": []any{"dispatch"}, "duration_minutes": float64(10),
						}}
					}
					if policy == "pool" {
						account.Credentials["pool_mode"] = true
						account.Credentials["pool_mode_retry_status_codes"] = []any{float64(403)}
					}
					gateway := &OpenAIGatewayService{rateLimitService: svc}
					for range 6 {
						require.Equal(t, ErrorPolicySkipped, svc.CheckErrorPolicy(context.Background(), account, 403, body))
						require.False(t, svc.HandleTempUnschedulable(context.Background(), account, 403, body))
						require.False(t, svc.HandleUpstreamError(context.Background(), account, 403, nil, body))
						require.False(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, body))
						require.False(t, gateway.shouldFailoverOpenAIUpstreamResponse(account, 403, "", body))
						require.False(t, shouldFailoverOpenAIPassthroughResponse(account, 403, body))
						require.False(t, PoolModeSameAccountRetry(account, 403, nil, body))
						if platform == PlatformGrok {
							require.False(t, gateway.shouldFailoverGrokUpstreamError(403, body))
							gateway.handleGrokAccountUpstreamError(context.Background(), account, 403, nil, body)
						}
					}
					require.Zero(t, repo.setErrorCalls)
					require.Zero(t, repo.tempCalls)
					require.Empty(t, blocker.accounts)
					require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
					require.Len(t, counter.counts, 1)
				})
			}
		}
	}
}

func TestRateLimitService_HandleUpstreamError_MessagesDispatch403DoesNotDisable(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			counter := &openAI403CounterCacheStub{counts: []int64{3}}
			service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			service.SetOpenAI403CounterCache(counter)
			account := &Account{ID: 303, Platform: platform, Type: AccountTypeOAuth}

			shouldDisable := service.HandleUpstreamError(
				context.Background(),
				account,
				http.StatusForbidden,
				http.Header{},
				[]byte(`{"error":{"type":"permission_error","message":"This group does not allow /v1/messages dispatch"}}`),
			)

			require.False(t, shouldDisable)
			require.Equal(t, 0, repo.setErrorCalls)
			require.Equal(t, 0, repo.tempCalls)
			require.Len(t, counter.counts, 1)
		})
	}
}
