package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIPool5xxRepoStub struct {
	AccountRepository
	tempCalls   int
	setErrCalls int
	lastID      int64
	lastUntil   time.Time
	lastReason  string
}

func (r *openAIPool5xxRepoStub) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrCalls++
	return nil
}

func (r *openAIPool5xxRepoStub) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls++
	r.lastID = id
	r.lastUntil = until
	r.lastReason = reason
	return nil
}

type openAIPool5xxCounterStub struct {
	count          int64
	observations   int
	resets         int
	window         int
	sampleInterval int
	sampled        bool
}

func (c *openAIPool5xxCounterStub) ObserveOpenAIPool5xxFailure(_ context.Context, _ int64, windowSeconds, sampleIntervalSeconds int) (int64, bool, error) {
	c.observations++
	c.window = windowSeconds
	c.sampleInterval = sampleIntervalSeconds
	if c.sampled {
		c.count++
	}
	return c.count, c.sampled, nil
}

func (c *openAIPool5xxCounterStub) ResetOpenAIPool5xxCount(_ context.Context, _ int64) error {
	c.resets++
	c.count = 0
	return nil
}

func (c *openAIPool5xxCounterStub) ClearOpenAIPool5xxState(_ context.Context, _ int64) error {
	c.resets++
	c.count = 0
	return nil
}

type openAIPool5xxTempCacheStub struct {
	TempUnschedCache
	sets  int
	state *TempUnschedState
}

func (c *openAIPool5xxTempCacheStub) SetTempUnsched(_ context.Context, _ int64, state *TempUnschedState) error {
	c.sets++
	c.state = state
	return nil
}

type openAIPool5xxRuntimeBlockerStub struct {
	blocks int
	id     int64
	until  time.Time
	reason string
}

func (b *openAIPool5xxRuntimeBlockerStub) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	b.blocks++
	b.id = account.ID
	b.until = until
	b.reason = reason
}

func (*openAIPool5xxRuntimeBlockerStub) ClearAccountSchedulingBlock(int64) {}

func newOpenAIPool5xxTestService(repo AccountRepository, cache TempUnschedCache, counter OpenAIPool5xxCounterCache) *RateLimitService {
	cfg := &config.Config{Gateway: config.GatewayConfig{
		OpenAIPool5xxFailureThreshold:      3,
		OpenAIPool5xxFailureWindowSeconds:  60,
		OpenAIPool5xxSampleIntervalSeconds: 10,
		OpenAIPool5xxCooldownSeconds:       300,
	}}
	svc := NewRateLimitService(repo, nil, cfg, nil, cache)
	svc.SetOpenAIPool5xxCounterCache(counter)
	return svc
}

func openAIPoolAccount(id int64) *Account {
	return &Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
}

func TestHandleUpstreamError_OpenAIPool5xxRequiresThreshold(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{sampled: true}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)
	account := openAIPoolAccount(625)

	for range 2 {
		require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"Service temporarily unavailable"}}`)))
	}

	require.Equal(t, 2, counter.observations)
	require.Equal(t, 60, counter.window)
	require.Equal(t, 10, counter.sampleInterval)
	require.Zero(t, repo.tempCalls)
}

func TestHandleUpstreamError_OpenAIPool5xxCoalescedBurstDoesNotCooldown(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{count: 1, sampled: false}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)
	account := openAIPoolAccount(623)

	for range 100 {
		require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, nil))
	}

	require.Equal(t, 100, counter.observations)
	require.Zero(t, repo.tempCalls)
}

func TestHandleUpstreamError_OpenAIPool5xxThresholdAppliesGlobalCooldown(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	cache := &openAIPool5xxTempCacheStub{}
	counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
	blocker := &openAIPool5xxRuntimeBlockerStub{}
	svc := newOpenAIPool5xxTestService(repo, cache, counter)
	svc.SetAccountRuntimeBlocker(blocker)
	account := openAIPoolAccount(625)
	before := time.Now()

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"Service temporarily unavailable"}}`))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, int64(625), repo.lastID)
	require.WithinDuration(t, before.Add(5*time.Minute), repo.lastUntil, 2*time.Second)
	require.Contains(t, repo.lastReason, "503")
	require.Equal(t, 1, cache.sets)
	require.NotNil(t, cache.state)
	require.Equal(t, http.StatusServiceUnavailable, cache.state.StatusCode)
	require.Equal(t, 1, blocker.blocks)
	require.Equal(t, int64(625), blocker.id)
	require.Equal(t, "openai_pool_5xx", blocker.reason)
	require.Equal(t, 1, counter.resets)
}

func TestHandleUpstreamError_OpenAIPool507ThresholdAppliesGlobalCooldown(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)
	account := openAIPoolAccount(2150)

	shouldDisable := svc.HandleUpstreamError(
		context.Background(), account, http.StatusInsufficientStorage, http.Header{},
		[]byte(`{"error":{"message":"insufficient storage"}}`), "gpt-5.6-sol",
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, counter.observations)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastReason, `"status_code":507`)
	require.Contains(t, repo.lastReason, `"model":"gpt-5.6-sol"`)
}

func TestHandleUpstreamError_OpenAIPool5xxPersistsCanonicalModelForRecoveryProbe(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)

	require.True(t, svc.HandleUpstreamError(
		context.Background(), openAIPoolAccount(627), http.StatusBadGateway, http.Header{},
		[]byte(`{"error":{"message":"temporary"}}`), "gpt-5.5",
	))

	require.Contains(t, repo.lastReason, `"model":"gpt-5.5"`)
}

func TestHandleUpstreamError_OpenAIPool5xxIgnoresSingleAndNonTransientErrors(t *testing.T) {
	for _, statusCode := range []int{http.StatusInternalServerError, http.StatusNotImplemented, 529} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			repo := &openAIPool5xxRepoStub{}
			counter := &openAIPool5xxCounterStub{sampled: true}
			svc := newOpenAIPool5xxTestService(repo, nil, counter)

			require.False(t, svc.HandleUpstreamError(context.Background(), openAIPoolAccount(700+int64(statusCode)), statusCode, http.Header{}, nil))
			require.Zero(t, counter.observations)
			require.Zero(t, repo.tempCalls)
		})
	}
}

func TestHandleUpstreamError_OpenAIPool5xxKeepsExplicitPolicyPriority(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
	}{
		{name: "pool", account: openAIPoolAccount(626)},
		{name: "non-pool", account: &Account{ID: 627, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &openAIPool5xxRepoStub{}
			counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
			svc := newOpenAIPool5xxTestService(repo, nil, counter)
			tt.account.Credentials["custom_error_codes_enabled"] = true
			tt.account.Credentials["custom_error_codes"] = []any{float64(http.StatusServiceUnavailable)}

			require.True(t, svc.HandleUpstreamError(context.Background(), tt.account, http.StatusServiceUnavailable, http.Header{}, []byte("custom failure")))
			require.Equal(t, 1, repo.setErrCalls)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, counter.observations)
		})
	}
}

func TestHandleUpstreamError_OpenAINonPool5xxSkipsBreakerWhenExplicitPolicyDoesNotMatch(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)
	account := &Account{
		ID:       628,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusServiceUnavailable)},
		},
	}

	require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, nil))
	require.Zero(t, repo.setErrCalls)
	require.Zero(t, repo.tempCalls)
	require.Zero(t, counter.observations)
}

func TestHandleUpstreamError_OpenAINonPool5xxKeepsTempUnschedulableRulePriority(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)
	account := &Account{
		ID:       629,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusServiceUnavailable),
					"keywords":         []any{"overloaded"},
					"duration_minutes": float64(10),
				},
			},
		},
	}

	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte("upstream overloaded")))
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastReason, `"matched_keyword":"overloaded"`)
	require.Zero(t, counter.observations)
}

func TestHandleUpstreamError_OpenAINonPoolAPIKey5xxThresholdAppliesCooldown(t *testing.T) {
	repo := &openAIPool5xxRepoStub{}
	counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
	svc := newOpenAIPool5xxTestService(repo, nil, counter)
	account := &Account{ID: 800, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, nil))
	require.Equal(t, 1, counter.observations)
	require.Equal(t, 1, repo.tempCalls)
}

func TestHandleUpstreamError_OpenAIPool5xxDoesNotAffectOAuthOrOtherPlatforms(t *testing.T) {
	accounts := []*Account{
		{ID: 801, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"pool_mode": true}},
		{ID: 802, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}},
	}
	for _, account := range accounts {
		repo := &openAIPool5xxRepoStub{}
		counter := &openAIPool5xxCounterStub{count: 2, sampled: true}
		svc := newOpenAIPool5xxTestService(repo, nil, counter)

		svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, nil)
		require.Zero(t, counter.observations)
		require.Zero(t, repo.tempCalls)
	}
}
