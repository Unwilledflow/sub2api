package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

const openAIPool5xxRecoveryLockPrefix = "openai_pool_5xx:recovery_probe:account:"

type OpenAIPool5xxRecoveryRepository interface {
	ListOpenAIPool5xxRecoveryCandidates(ctx context.Context, limit int) ([]Account, error)
	ClearOpenAIPool5xxTempUnschedulableIfMatch(ctx context.Context, id int64, expectedUntil time.Time, expectedReason string, expectedUpdatedAt time.Time) (bool, error)
}

type openAIPool5xxRecoveryProber interface {
	ProbeOpenAIPool5xxRecovery(ctx context.Context, account *Account, model string) (int, error)
}

type openAIPool5xxRecoveryProbeConfig struct {
	enabled        bool
	scanInterval   time.Duration
	probeInterval  time.Duration
	requestTimeout time.Duration
	maxPerScan     int
	concurrency    int
}

func defaultOpenAIPool5xxRecoveryProbeConfig() openAIPool5xxRecoveryProbeConfig {
	return openAIPool5xxRecoveryProbeConfig{
		enabled:        true,
		scanInterval:   10 * time.Second,
		probeInterval:  15 * time.Second,
		requestTimeout: 8 * time.Second,
		maxPerScan:     32,
		concurrency:    4,
	}
}

type OpenAIPool5xxRecoveryProbeService struct {
	repo           OpenAIPool5xxRecoveryRepository
	prober         openAIPool5xxRecoveryProber
	tempCache      TempUnschedCache
	counterCache   OpenAIPool5xxCounterCache
	runtimeBlocker AccountRuntimeBlocker
	lockCache      LeaderLockCache
	config         openAIPool5xxRecoveryProbeConfig
	instanceID     string

	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	started    bool
	stopped    bool
	cycleMu    sync.Mutex
	localLease map[int64]time.Time
}

func newOpenAIPool5xxRecoveryProbeService(
	repo OpenAIPool5xxRecoveryRepository,
	prober openAIPool5xxRecoveryProber,
	tempCache TempUnschedCache,
	counterCache OpenAIPool5xxCounterCache,
	runtimeBlocker AccountRuntimeBlocker,
	lockCache LeaderLockCache,
	config openAIPool5xxRecoveryProbeConfig,
) *OpenAIPool5xxRecoveryProbeService {
	ctx, cancel := context.WithCancel(context.Background())
	return &OpenAIPool5xxRecoveryProbeService{
		repo:           repo,
		prober:         prober,
		tempCache:      tempCache,
		counterCache:   counterCache,
		runtimeBlocker: runtimeBlocker,
		lockCache:      lockCache,
		config:         config,
		instanceID:     uuid.NewString(),
		ctx:            ctx,
		cancel:         cancel,
		localLease:     make(map[int64]time.Time),
	}
}

func ProvideOpenAIPool5xxRecoveryProbeService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	tempCache TempUnschedCache,
	counterCache OpenAIPool5xxCounterCache,
	openAIGatewayService *OpenAIGatewayService,
	lockCache LeaderLockCache,
	cfg *config.Config,
) *OpenAIPool5xxRecoveryProbeService {
	recoveryRepo, ok := accountRepo.(OpenAIPool5xxRecoveryRepository)
	probeConfig := defaultOpenAIPool5xxRecoveryProbeConfig()
	probeConfig.enabled = ok && cfg != nil && cfg.Gateway.OpenAIPool5xxRecoveryProbeEnabled
	if cfg != nil {
		probeConfig.scanInterval = time.Duration(cfg.Gateway.OpenAIPool5xxRecoveryScanSeconds) * time.Second
		probeConfig.probeInterval = time.Duration(cfg.Gateway.OpenAIPool5xxRecoveryProbeSeconds) * time.Second
		probeConfig.requestTimeout = time.Duration(cfg.Gateway.OpenAIPool5xxRecoveryTimeoutSeconds) * time.Second
		probeConfig.concurrency = cfg.Gateway.OpenAIPool5xxRecoveryConcurrency
		probeConfig.maxPerScan = cfg.Gateway.OpenAIPool5xxRecoveryBatchSize
	}
	svc := newOpenAIPool5xxRecoveryProbeService(
		recoveryRepo, accountTestService, tempCache, counterCache, openAIGatewayService, lockCache, probeConfig,
	)
	if svc.config.enabled {
		svc.Start()
	}
	return svc
}

func (s *OpenAIPool5xxRecoveryProbeService) Start() {
	if s == nil || !s.config.enabled || s.repo == nil || s.prober == nil {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go s.runLoop()
}

func (s *OpenAIPool5xxRecoveryProbeService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *OpenAIPool5xxRecoveryProbeService) runLoop() {
	defer s.wg.Done()
	if err := s.RunOnce(s.ctx); err != nil && s.ctx.Err() == nil {
		slog.Warn("openai_pool_5xx_recovery_scan_failed", "error", err)
	}
	interval := s.config.scanInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.RunOnce(s.ctx); err != nil && s.ctx.Err() == nil {
				slog.Warn("openai_pool_5xx_recovery_scan_failed", "error", err)
			}
		}
	}
}

func (s *OpenAIPool5xxRecoveryProbeService) RunOnce(ctx context.Context) error {
	if s == nil || s.repo == nil || s.prober == nil {
		return nil
	}
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()

	limit := s.config.maxPerScan
	if limit <= 0 {
		limit = 32
	}
	accounts, err := s.repo.ListOpenAIPool5xxRecoveryCandidates(ctx, limit)
	if err != nil {
		return fmt.Errorf("list openai pool 5xx recovery candidates: %w", err)
	}
	concurrency := s.config.concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range accounts {
		account := accounts[i]
		if _, ok := openAIPool5xxRecoveryModel(&account); !ok {
			continue
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()
			s.probeCandidate(ctx, &account)
		}()
	}
	wg.Wait()
	return nil
}

func openAIPool5xxRecoveryModel(account *Account) (string, bool) {
	if account == nil || account.ID <= 0 || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey ||
		account.Status != StatusActive || !account.Schedulable || !account.IsPoolMode() || account.TempUnschedulableUntil == nil ||
		!account.TempUnschedulableUntil.After(time.Now()) {
		return "", false
	}
	var state TempUnschedState
	if err := json.Unmarshal([]byte(account.TempUnschedulableReason), &state); err != nil || state.MatchedKeyword != "openai_pool_5xx" {
		return "", false
	}
	model := strings.TrimSpace(state.Model)
	if model == "" {
		model = selectResponsesProbeModel(account)
	}
	return model, model != ""
}

func (s *OpenAIPool5xxRecoveryProbeService) acquireProbeLease(ctx context.Context, accountID int64) bool {
	lease := s.config.probeInterval
	if minimum := s.config.requestTimeout + 2*time.Second; lease < minimum {
		lease = minimum
	}
	if lease <= 0 {
		lease = 15 * time.Second
	}
	if s.lockCache != nil {
		lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		acquired, err := s.lockCache.TryAcquireLeaderLock(
			lockCtx, fmt.Sprintf("%s%d", openAIPool5xxRecoveryLockPrefix, accountID), s.instanceID, lease,
		)
		if err != nil {
			slog.Warn("openai_pool_5xx_recovery_lock_failed", "account_id", accountID, "error", err)
			return false
		}
		return acquired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if until := s.localLease[accountID]; until.After(now) {
		return false
	}
	s.localLease[accountID] = now.Add(lease)
	return true
}

func (s *OpenAIPool5xxRecoveryProbeService) probeCandidate(ctx context.Context, account *Account) {
	model, ok := openAIPool5xxRecoveryModel(account)
	if !ok || !s.acquireProbeLease(ctx, account.ID) {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.config.requestTimeout)
	defer cancel()
	statusCode, err := s.prober.ProbeOpenAIPool5xxRecovery(probeCtx, account, model)
	if err != nil {
		slog.Debug("openai_pool_5xx_recovery_probe_failed", "account_id", account.ID, "model", model, "error", err)
		return
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		slog.Debug("openai_pool_5xx_recovery_probe_unhealthy", "account_id", account.ID, "model", model, "status_code", statusCode)
		return
	}
	if account.TempUnschedulableUntil == nil {
		return
	}
	cleared, err := s.repo.ClearOpenAIPool5xxTempUnschedulableIfMatch(
		ctx, account.ID, *account.TempUnschedulableUntil, account.TempUnschedulableReason, account.UpdatedAt,
	)
	if err != nil {
		slog.Warn("openai_pool_5xx_recovery_clear_failed", "account_id", account.ID, "model", model, "error", err)
		return
	}
	if !cleared {
		slog.Debug("openai_pool_5xx_recovery_probe_stale", "account_id", account.ID, "model", model)
		return
	}
	if s.tempCache != nil {
		if err := s.tempCache.DeleteTempUnsched(ctx, account.ID); err != nil {
			slog.Warn("openai_pool_5xx_recovery_temp_cache_clear_failed", "account_id", account.ID, "error", err)
		}
	}
	if s.counterCache != nil {
		if err := s.counterCache.ClearOpenAIPool5xxState(ctx, account.ID); err != nil {
			slog.Warn("openai_pool_5xx_recovery_counter_clear_failed", "account_id", account.ID, "error", err)
		}
	}
	if s.runtimeBlocker != nil {
		s.runtimeBlocker.ClearAccountSchedulingBlock(account.ID)
	}
	slog.Info("openai_pool_5xx_probe_recovered", "account_id", account.ID, "model", model, "status_code", statusCode, "group_ids", account.GroupIDs)
}
