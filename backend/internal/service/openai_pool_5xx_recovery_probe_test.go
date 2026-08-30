package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type openAIPool5xxRecoveryRepoStub struct {
	mu         sync.Mutex
	accounts   []Account
	clear      bool
	clearCalls int
}

func (r *openAIPool5xxRecoveryRepoStub) ListOpenAIPool5xxRecoveryCandidates(context.Context, int) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Account(nil), r.accounts...), nil
}

func (r *openAIPool5xxRecoveryRepoStub) ClearOpenAIPool5xxTempUnschedulableIfMatch(context.Context, int64, time.Time, string, time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearCalls++
	return r.clear, nil
}

type openAIPool5xxRecoveryProberStub struct {
	mu         sync.Mutex
	statusCode int
	err        error
	calls      int
	models     []string
}

type openAIPool5xxRecoveryConcurrencyProber struct {
	mu      sync.Mutex
	current int
	maximum int
}

func (p *openAIPool5xxRecoveryConcurrencyProber) ProbeOpenAIPool5xxRecovery(context.Context, *Account, string) (int, error) {
	p.mu.Lock()
	p.current++
	if p.current > p.maximum {
		p.maximum = p.current
	}
	p.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	p.mu.Lock()
	p.current--
	p.mu.Unlock()
	return http.StatusServiceUnavailable, nil
}

func (p *openAIPool5xxRecoveryProberStub) ProbeOpenAIPool5xxRecovery(_ context.Context, _ *Account, model string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.models = append(p.models, model)
	return p.statusCode, p.err
}

type openAIPool5xxRecoveryCacheStub struct {
	mu          sync.Mutex
	tempDeletes int
	stateClears int
}

func (*openAIPool5xxRecoveryCacheStub) SetTempUnsched(context.Context, int64, *TempUnschedState) error {
	return nil
}
func (*openAIPool5xxRecoveryCacheStub) GetTempUnsched(context.Context, int64) (*TempUnschedState, error) {
	return nil, nil
}
func (c *openAIPool5xxRecoveryCacheStub) DeleteTempUnsched(context.Context, int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tempDeletes++
	return nil
}
func (*openAIPool5xxRecoveryCacheStub) ObserveOpenAIPool5xxFailure(context.Context, int64, int, int) (int64, bool, error) {
	return 0, false, nil
}
func (*openAIPool5xxRecoveryCacheStub) ResetOpenAIPool5xxCount(context.Context, int64) error {
	return nil
}
func (c *openAIPool5xxRecoveryCacheStub) ClearOpenAIPool5xxState(context.Context, int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stateClears++
	return nil
}

type openAIPool5xxRecoveryBlockerStub struct {
	mu     sync.Mutex
	clears int
}

func (*openAIPool5xxRecoveryBlockerStub) BlockAccountScheduling(*Account, time.Time, string) {}
func (b *openAIPool5xxRecoveryBlockerStub) ClearAccountSchedulingBlock(int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clears++
}

type openAIPool5xxRecoveryLockStub struct {
	mu       sync.Mutex
	acquired map[string]bool
}

func (l *openAIPool5xxRecoveryLockStub) TryAcquireLeaderLock(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.acquired == nil {
		l.acquired = make(map[string]bool)
	}
	if l.acquired[key] {
		return false, nil
	}
	l.acquired[key] = true
	return true, nil
}

func (*openAIPool5xxRecoveryLockStub) ReleaseLeaderLock(context.Context, string, string) error {
	return nil
}

func newOpenAIPool5xxRecoveryCandidate() Account {
	now := time.Now().UTC()
	until := now.Add(5 * time.Minute)
	return Account{
		ID:                      42,
		Platform:                PlatformOpenAI,
		Type:                    AccountTypeAPIKey,
		Status:                  StatusActive,
		Schedulable:             true,
		Credentials:             map[string]any{"api_key": "key", "pool_mode": true},
		TempUnschedulableUntil:  &until,
		TempUnschedulableReason: `{"matched_keyword":"openai_pool_5xx","model":"gpt-5.5"}`,
		UpdatedAt:               now,
	}
}

func TestOpenAIPool5xxRecoveryProbeService_SuccessClearsAllState(t *testing.T) {
	repo := &openAIPool5xxRecoveryRepoStub{accounts: []Account{newOpenAIPool5xxRecoveryCandidate()}, clear: true}
	prober := &openAIPool5xxRecoveryProberStub{statusCode: http.StatusOK}
	cache := &openAIPool5xxRecoveryCacheStub{}
	blocker := &openAIPool5xxRecoveryBlockerStub{}
	svc := newOpenAIPool5xxRecoveryProbeService(repo, prober, cache, cache, blocker, &openAIPool5xxRecoveryLockStub{}, defaultOpenAIPool5xxRecoveryProbeConfig())

	require.NoError(t, svc.RunOnce(context.Background()))
	require.Equal(t, 1, prober.calls)
	require.Equal(t, []string{"gpt-5.5"}, prober.models)
	require.Equal(t, 1, repo.clearCalls)
	require.Equal(t, 1, cache.tempDeletes)
	require.Equal(t, 1, cache.stateClears)
	require.Equal(t, 1, blocker.clears)
}

func TestOpenAIPool5xxRecoveryProbeService_SkipsNonPoolAPIKey(t *testing.T) {
	account := newOpenAIPool5xxRecoveryCandidate()
	delete(account.Credentials, "pool_mode")
	repo := &openAIPool5xxRecoveryRepoStub{accounts: []Account{account}, clear: true}
	prober := &openAIPool5xxRecoveryProberStub{statusCode: http.StatusOK}
	cache := &openAIPool5xxRecoveryCacheStub{}
	blocker := &openAIPool5xxRecoveryBlockerStub{}
	svc := newOpenAIPool5xxRecoveryProbeService(repo, prober, cache, cache, blocker, &openAIPool5xxRecoveryLockStub{}, defaultOpenAIPool5xxRecoveryProbeConfig())

	require.NoError(t, svc.RunOnce(context.Background()))
	require.Zero(t, prober.calls)
	require.Zero(t, repo.clearCalls)
	require.Zero(t, cache.tempDeletes)
	require.Zero(t, cache.stateClears)
	require.Zero(t, blocker.clears)
}

func TestOpenAIPool5xxRecoveryProbeService_StaleSuccessDoesNotClearCaches(t *testing.T) {
	repo := &openAIPool5xxRecoveryRepoStub{accounts: []Account{newOpenAIPool5xxRecoveryCandidate()}, clear: false}
	prober := &openAIPool5xxRecoveryProberStub{statusCode: http.StatusOK}
	cache := &openAIPool5xxRecoveryCacheStub{}
	blocker := &openAIPool5xxRecoveryBlockerStub{}
	svc := newOpenAIPool5xxRecoveryProbeService(repo, prober, cache, cache, blocker, &openAIPool5xxRecoveryLockStub{}, defaultOpenAIPool5xxRecoveryProbeConfig())

	require.NoError(t, svc.RunOnce(context.Background()))
	require.Equal(t, 1, repo.clearCalls)
	require.Zero(t, cache.tempDeletes)
	require.Zero(t, cache.stateClears)
	require.Zero(t, blocker.clears)
}

func TestOpenAIPool5xxRecoveryProbeService_FailureKeepsCooldown(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    error
	}{
		{name: "upstream_503", status: http.StatusServiceUnavailable},
		{name: "transport_error", err: errors.New("timeout")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &openAIPool5xxRecoveryRepoStub{accounts: []Account{newOpenAIPool5xxRecoveryCandidate()}, clear: true}
			prober := &openAIPool5xxRecoveryProberStub{statusCode: tt.status, err: tt.err}
			cache := &openAIPool5xxRecoveryCacheStub{}
			svc := newOpenAIPool5xxRecoveryProbeService(repo, prober, cache, cache, &openAIPool5xxRecoveryBlockerStub{}, &openAIPool5xxRecoveryLockStub{}, defaultOpenAIPool5xxRecoveryProbeConfig())

			require.NoError(t, svc.RunOnce(context.Background()))
			require.Zero(t, repo.clearCalls)
			require.Zero(t, cache.tempDeletes)
			require.Zero(t, cache.stateClears)
		})
	}
}

func TestOpenAIPool5xxRecoveryProbeService_DistributedLeaseAllowsOneProbe(t *testing.T) {
	account := newOpenAIPool5xxRecoveryCandidate()
	lock := &openAIPool5xxRecoveryLockStub{}
	prober := &openAIPool5xxRecoveryProberStub{statusCode: http.StatusServiceUnavailable}
	repoA := &openAIPool5xxRecoveryRepoStub{accounts: []Account{account}}
	repoB := &openAIPool5xxRecoveryRepoStub{accounts: []Account{account}}
	config := defaultOpenAIPool5xxRecoveryProbeConfig()
	svcA := newOpenAIPool5xxRecoveryProbeService(repoA, prober, nil, nil, nil, lock, config)
	svcB := newOpenAIPool5xxRecoveryProbeService(repoB, prober, nil, nil, nil, lock, config)

	var wg sync.WaitGroup
	for _, svc := range []*OpenAIPool5xxRecoveryProbeService{svcA, svcB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, svc.RunOnce(context.Background()))
		}()
	}
	wg.Wait()
	require.Equal(t, 1, prober.calls)
}

func TestOpenAIPool5xxRecoveryProbeService_BoundsProbeConcurrency(t *testing.T) {
	accounts := make([]Account, 12)
	for i := range accounts {
		accounts[i] = newOpenAIPool5xxRecoveryCandidate()
		accounts[i].ID = int64(i + 1)
	}
	repo := &openAIPool5xxRecoveryRepoStub{accounts: accounts}
	prober := &openAIPool5xxRecoveryConcurrencyProber{}
	config := defaultOpenAIPool5xxRecoveryProbeConfig()
	config.concurrency = 3
	svc := newOpenAIPool5xxRecoveryProbeService(repo, prober, nil, nil, nil, nil, config)

	require.NoError(t, svc.RunOnce(context.Background()))
	require.Equal(t, 3, prober.maximum)
}
