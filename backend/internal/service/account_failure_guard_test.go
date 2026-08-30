package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type failureGuardRepoStub struct {
	cooldownIDs []Account
	setCooldownCalls int
	lastCooldownID int64
	lastCooldownUntil time.Time
	lastCooldownReason string
	clearCooldownCalls int
	lastClearCooldownID int64
}

func (r *failureGuardRepoStub) ListCooledDownAccounts(ctx context.Context, limit int) ([]Account, error) {
	if len(r.cooldownIDs) > limit {
		return r.cooldownIDs[:limit], nil
	}
	return r.cooldownIDs, nil
}

func (r *failureGuardRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.setCooldownCalls++
	r.lastCooldownID = id
	r.lastCooldownUntil = until
	r.lastCooldownReason = reason
	return nil
}

func (r *failureGuardRepoStub) ClearTempUnschedulable(ctx context.Context, id int64) error {
	r.clearCooldownCalls++
	r.lastClearCooldownID = id
	return nil
}

type failureGuardCounterStub struct {
	counts   map[int64]int64
	mult     map[int64]int
	obsCalls map[int64]int
	// observeErr 非 nil 时 ObserveAccountFailure 返回错误
	observeErr error
}

func newFailureGuardCounterStub() *failureGuardCounterStub {
	return &failureGuardCounterStub{
		counts:   make(map[int64]int64),
		mult:     make(map[int64]int),
		obsCalls: make(map[int64]int),
	}
}

func (c *failureGuardCounterStub) ObserveAccountFailure(ctx context.Context, accountID int64, windowSeconds, sampleIntervalSeconds int) (int64, bool, error) {
	if c.observeErr != nil {
		return 0, false, c.observeErr
	}
	c.obsCalls[accountID]++
	c.counts[accountID]++
	return c.counts[accountID], true, nil
}

func (c *failureGuardCounterStub) ResetAccountFailureCount(ctx context.Context, accountID int64) error {
	delete(c.counts, accountID)
	return nil
}

func (c *failureGuardCounterStub) ClearAccountFailureState(ctx context.Context, accountID int64) error {
	delete(c.counts, accountID)
	return nil
}

func (c *failureGuardCounterStub) CurrentMultiplier(ctx context.Context, accountID int64) (int, error) {
	if m, ok := c.mult[accountID]; ok {
		return m, nil
	}
	return 1, nil
}

func (c *failureGuardCounterStub) BumpMultiplier(ctx context.Context, accountID int64, debounceSeconds, maxMultiplier int) (int, error) {
	if _, ok := c.mult[accountID]; !ok {
		c.mult[accountID] = 1
	}
	c.mult[accountID]++
	if c.mult[accountID] > maxMultiplier {
		c.mult[accountID] = maxMultiplier
	}
	return c.mult[accountID], nil
}

type failureGuardProberStub struct {
	probeStatus string
	probeErr    error
	model       string
	probedIDs   []int64
}

func (p *failureGuardProberStub) SuggestProbeModel(ctx context.Context, accountID int64) (string, error) {
	return p.model, nil
}

func (p *failureGuardProberStub) RunHealthProbe(ctx context.Context, accountID int64, modelID string) (*ScheduledTestResult, error) {
	p.probedIDs = append(p.probedIDs, accountID)
	if p.probeErr != nil {
		return nil, p.probeErr
	}
	return &ScheduledTestResult{Status: p.probeStatus}, nil
}

type failureGuardRecovererStub struct {
	clearedIDs []int64
}

func (r *failureGuardRecovererStub) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	r.clearedIDs = append(r.clearedIDs, accountID)
	return nil
}

func newFailureGuardTestService(repo *failureGuardRepoStub, counter *failureGuardCounterStub) *AccountFailureGuardService {
	cfg := defaultAccountFailureGuardConfig()
	return newAccountFailureGuardService(repo, counter, &failureGuardRecovererStub{}, &failureGuardProberStub{probeStatus: "success"}, nil, cfg)
}

func TestAccountFailureGuard_CooldownAfterThreshold(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	svc := newFailureGuardTestService(repo, counter)
	account := &Account{ID: 7, Status: StatusActive}

	cfg := svc.config
	for i := 0; i < cfg.threshold-1; i++ {
		svc.RecordFailure(context.Background(), account, http.StatusServiceUnavailable)
	}
	require.Equal(t, 0, repo.setCooldownCalls, "below threshold must not cool down")

	before := time.Now()
	svc.RecordFailure(context.Background(), account, http.StatusServiceUnavailable)
	require.Equal(t, 1, repo.setCooldownCalls)
	require.Equal(t, int64(7), repo.lastCooldownID)
	require.Contains(t, repo.lastCooldownReason, AccountFailureGuardErrorPrefix)
	require.WithinDuration(t, before.Add(time.Duration(cfg.cooldownSeconds)*time.Second), repo.lastCooldownUntil, 2*time.Second)
	require.Equal(t, int64(0), counter.counts[7], "counter must reset after cooldown")
	require.Equal(t, 2, counter.mult[7], "multiplier must bump after first cooldown")
}

func TestAccountFailureGuard_IgnoresNonCountableStatuses(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	svc := newFailureGuardTestService(repo, counter)
	account := &Account{ID: 8, Status: StatusActive}

	for i := 0; i < 10; i++ {
		svc.RecordFailure(context.Background(), account, http.StatusUnauthorized)
		svc.RecordFailure(context.Background(), account, http.StatusForbidden)
		svc.RecordFailure(context.Background(), account, http.StatusBadRequest)
		svc.RecordFailure(context.Background(), account, http.StatusNotFound)
	}
	require.Equal(t, 0, counter.obsCalls[8])
	require.Equal(t, 0, repo.setCooldownCalls)
}

func TestAccountFailureGuard_IgnoresNonActiveAccounts(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	svc := newFailureGuardTestService(repo, counter)
	account := &Account{ID: 9, Status: StatusError}

	for i := 0; i < 20; i++ {
		svc.RecordFailure(context.Background(), account, http.StatusBadGateway)
	}
	require.Equal(t, 0, counter.obsCalls[9])
	require.Equal(t, 0, repo.setCooldownCalls)
}

func TestAccountFailureGuard_IgnoresAlreadyCooledAccounts(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	svc := newFailureGuardTestService(repo, counter)
	future := time.Now().Add(time.Minute)
	account := &Account{ID: 16, Status: StatusActive, TempUnschedulableUntil: &future}

	for i := 0; i < 30; i++ {
		svc.RecordFailure(context.Background(), account, http.StatusBadGateway)
	}
	require.Equal(t, 0, counter.obsCalls[16])
	require.Equal(t, 0, repo.setCooldownCalls)
}

func TestAccountFailureGuard_DebounceMultiplier(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	counter.mult[10] = 2
	svc := newFailureGuardTestService(repo, counter)
	account := &Account{ID: 10, Status: StatusActive}

	cfg := svc.config
	for i := 0; i < cfg.threshold*2-1; i++ {
		svc.RecordFailure(context.Background(), account, http.StatusBadGateway)
	}
	require.Equal(t, 0, repo.setCooldownCalls, "multiplier 2 doubles the threshold")

	svc.RecordFailure(context.Background(), account, http.StatusBadGateway)
	require.Equal(t, 1, repo.setCooldownCalls)
}

func TestAccountFailureGuard_CounterErrorDoesNotCooldown(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	counter.observeErr = errors.New("redis down")
	svc := newFailureGuardTestService(repo, counter)
	account := &Account{ID: 11, Status: StatusActive}

	for i := 0; i < 30; i++ {
		svc.RecordFailure(context.Background(), account, http.StatusInternalServerError)
	}
	require.Equal(t, 0, repo.setCooldownCalls)
}

func TestAccountFailureGuard_RecoverOnceRestoresAccount(t *testing.T) {
	repo := &failureGuardRepoStub{
		cooldownIDs: []Account{{ID: 12, Name: "bad-account", Status: StatusActive}},
	}
	counter := newFailureGuardCounterStub()
	counter.counts[12] = 3
	prober := &failureGuardProberStub{probeStatus: "success", model: "gemini-2.5-pro"}
	recoverer := &failureGuardRecovererStub{}
	cfg := defaultAccountFailureGuardConfig()
	svc := newAccountFailureGuardService(repo, counter, recoverer, prober, nil, cfg)

	require.NoError(t, svc.RecoverOnce(context.Background()))
	require.Equal(t, []int64{12}, prober.probedIDs)
	require.Equal(t, []int64{12}, recoverer.clearedIDs)
	require.Equal(t, int64(0), counter.counts[12], "counter state must clear after recovery")
}

func TestAccountFailureGuard_RecoverOnceSkipsUnhealthy(t *testing.T) {
	repo := &failureGuardRepoStub{
		cooldownIDs: []Account{{ID: 13, Name: "still-bad", Status: StatusActive}},
	}
	counter := newFailureGuardCounterStub()
	prober := &failureGuardProberStub{probeStatus: "failed"}
	recoverer := &failureGuardRecovererStub{}
	cfg := defaultAccountFailureGuardConfig()
	svc := newAccountFailureGuardService(repo, counter, recoverer, prober, nil, cfg)

	require.NoError(t, svc.RecoverOnce(context.Background()))
	require.Empty(t, recoverer.clearedIDs)
}

func TestAccountFailureGuard_RecoverOnceProbeError(t *testing.T) {
	repo := &failureGuardRepoStub{
		cooldownIDs: []Account{{ID: 14, Name: "err-account", Status: StatusActive}},
	}
	counter := newFailureGuardCounterStub()
	prober := &failureGuardProberStub{probeErr: errors.New("network failure")}
	recoverer := &failureGuardRecovererStub{}
	cfg := defaultAccountFailureGuardConfig()
	svc := newAccountFailureGuardService(repo, counter, recoverer, prober, nil, cfg)

	require.NoError(t, svc.RecoverOnce(context.Background()))
	require.Empty(t, recoverer.clearedIDs)
}

func TestAccountFailureGuard_RecordFailureWithDeadline(t *testing.T) {
	repo := &failureGuardRepoStub{}
	counter := newFailureGuardCounterStub()
	svc := newFailureGuardTestService(repo, counter)
	account := &Account{ID: 15, Status: StatusActive}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	svc.RecordFailure(ctx, account, http.StatusGatewayTimeout)
	require.Equal(t, 1, counter.obsCalls[15])
	require.Equal(t, 0, repo.setCooldownCalls)
}
