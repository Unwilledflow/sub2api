package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

// AccountFailureGuardErrorPrefix 是自动禁用账号时写入 error_message 的固定前缀，
// 恢复扫描据此识别「连续失败自动禁用」的账号，避免误伤管理员手动禁用的账号。
const AccountFailureGuardErrorPrefix = "auto-disabled: "

const (
	accountFailureGuardRecoveryLeaderLockKey = "account_failure_guard:recovery:leader"
)

// AccountFailureCounterCache 通用账号连续失败计数器（Redis 实现，跨实例共享）。
type AccountFailureCounterCache interface {
	// ObserveAccountFailure 在滑动窗口内记录一次失败，返回窗口内累计失败次数。
	// sampled=false 表示本次采样被抑制（采样间隔内只计一次），count 为累计值。
	ObserveAccountFailure(ctx context.Context, accountID int64, windowSeconds, sampleIntervalSeconds int) (count int64, sampled bool, err error)
	// ResetAccountFailureCount 清零失败计数（保留防抖倍率）。
	ResetAccountFailureCount(ctx context.Context, accountID int64) error
	// ClearAccountFailureState 清零失败计数与采样标记（保留防抖倍率）。
	ClearAccountFailureState(ctx context.Context, accountID int64) error
	// CurrentMultiplier 返回当前防抖倍率（无记录时返回 1）。
	CurrentMultiplier(ctx context.Context, accountID int64) (int, error)
	// BumpMultiplier 递增防抖倍率并刷新 debounce TTL，返回值被钳制在 maxMultiplier 以内。
	BumpMultiplier(ctx context.Context, accountID int64, debounceSeconds, maxMultiplier int) (int, error)
}

// AccountFailureGuardRepository 是连续失败守卫所需的账号仓库能力，
// 独立小接口避免改动 AccountRepository 大接口及其全部 mock。
// 禁用于「临时不可调度」（TempUnschedulableUntil）而非 StatusError：到期自动恢复可调度，
// 管理面板仅显示临时不可用，不与管理员手动禁用/认证失败禁用的语义混淆。
type AccountFailureGuardRepository interface {
	ListCooledDownAccounts(ctx context.Context, limit int) ([]Account, error)
	SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error
	ClearTempUnschedulable(ctx context.Context, id int64) error
}

type accountFailureGuardConfig struct {
	enabled             bool
	threshold           int
	windowSeconds       int
	sampleIntervalSecs  int
	cooldownSeconds     int
	recoveryInterval    time.Duration
	debounceSeconds     int
	maxMultiplier       int
	recoveryBatchSize   int
	probeTimeout        time.Duration
}

func defaultAccountFailureGuardConfig() accountFailureGuardConfig {
	return accountFailureGuardConfig{
		enabled:            true,
		threshold:          5,
		windowSeconds:      600,
		sampleIntervalSecs: 20,
		cooldownSeconds:    600,
		recoveryInterval:   5 * time.Minute,
		debounceSeconds:    1800,
		maxMultiplier:      8,
		recoveryBatchSize:  20,
		probeTimeout:       15 * time.Second,
	}
}

// accountFailureGuardProber 是恢复探测所需的健康探测能力，
// 由 AccountTestService 隐式实现（测试中以 stub 替代）。
type accountFailureGuardProber interface {
	SuggestProbeModel(ctx context.Context, accountID int64) (string, error)
	RunHealthProbe(ctx context.Context, accountID int64, modelID string) (*ScheduledTestResult, error)
}

// accountFailureGuardRecoverer 是恢复动作所需的冷却清除能力，
// 由 RateLimitService 隐式实现（ClearTempUnschedulable 同时清模型级限流与调度缓存）。
type accountFailureGuardRecoverer interface {
	ClearTempUnschedulable(ctx context.Context, accountID int64) error
}

// AccountFailureGuardService 跨平台实现「连续上游失败 → 临时冷却 → 定时探测 → 提前恢复」闭环。
// 失败计数持久化在 Redis（窗口滑动，跨实例共享）；冷却到期自动恢复可调度，
// 恢复探测复用 AccountTestService.RunHealthProbe，探测成功提前解除冷却。
type AccountFailureGuardService struct {
	repo         AccountFailureGuardRepository
	counter      AccountFailureCounterCache
	recoverer    accountFailureGuardRecoverer
	prober       accountFailureGuardProber
	lockCache    LeaderLockCache
	config       accountFailureGuardConfig
	instanceID   string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	started bool
	stopped bool
}

func newAccountFailureGuardService(
	repo AccountFailureGuardRepository,
	counter AccountFailureCounterCache,
	recoverer accountFailureGuardRecoverer,
	prober accountFailureGuardProber,
	lockCache LeaderLockCache,
	config accountFailureGuardConfig,
) *AccountFailureGuardService {
	ctx, cancel := context.WithCancel(context.Background())
	return &AccountFailureGuardService{
		repo:         repo,
		counter:      counter,
		recoverer:    recoverer,
		prober:       prober,
		lockCache:    lockCache,
		config:       config,
		instanceID:   uuid.NewString(),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// ProvideAccountFailureGuardService 装配连续失败守卫：注入各平台错误入口
// （RateLimitService 覆盖 OpenAI/Claude/Anthropic 等，GeminiMessagesCompatService 覆盖 Gemini），
// 并启动恢复探测循环。
func ProvideAccountFailureGuardService(
	accountRepo AccountRepository,
	counterCache AccountFailureCounterCache,
	rateLimitService *RateLimitService,
	accountTestService *AccountTestService,
	geminiCompatService *GeminiMessagesCompatService,
	lockCache LeaderLockCache,
	cfg *config.Config,
) *AccountFailureGuardService {
	guardRepo, ok := accountRepo.(AccountFailureGuardRepository)
	guardConfig := defaultAccountFailureGuardConfig()
	if cfg != nil {
		g := cfg.Gateway
		guardConfig.enabled = ok && g.AccountFailureGuardEnabled
		guardConfig.threshold = g.AccountFailureGuardThreshold
		guardConfig.windowSeconds = g.AccountFailureGuardWindowSeconds
		guardConfig.sampleIntervalSecs = g.AccountFailureGuardSampleIntervalSeconds
		guardConfig.cooldownSeconds = g.AccountFailureGuardCooldownSeconds
		guardConfig.recoveryInterval = time.Duration(g.AccountFailureGuardRecoveryIntervalSeconds) * time.Second
		guardConfig.debounceSeconds = g.AccountFailureGuardDebounceSeconds
		guardConfig.maxMultiplier = g.AccountFailureGuardMaxMultiplier
		guardConfig.recoveryBatchSize = g.AccountFailureGuardRecoveryBatchSize
		guardConfig.probeTimeout = time.Duration(g.AccountFailureGuardProbeTimeoutSeconds) * time.Second
	}
	svc := newAccountFailureGuardService(guardRepo, counterCache, rateLimitService, accountTestService, lockCache, guardConfig)
	if !guardConfig.enabled || guardRepo == nil || counterCache == nil {
		return svc
	}
	if rateLimitService != nil {
		rateLimitService.SetAccountFailureGuard(svc)
	}
	if geminiCompatService != nil {
		geminiCompatService.SetAccountFailureGuard(svc)
	}
	svc.StartRecoveryLoop()
	return svc
}

// isCountableUpstreamFailure 判断状态码是否计入连续失败：
// 限流与瞬时 5xx/网关错误可恢复，计入；认证类 401/403 已有独立的禁用逻辑，不计入；
// 业务 4xx（参数错误等）与账号健康无关，不计入。
func isCountableUpstreamFailure(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		520, 521, 522, 523, 524:
		return true
	}
	return false
}

// RecordFailure 记录一次上游失败；窗口内累计达到阈值（含防抖倍率）时把账号置为
// 临时不可调度（TempUnschedulableUntil=now+cooldown），冷却到期自动恢复可调度。
// 仅对可恢复错误计数；已处于冷却/非 active 的账号不再计数。幂等：多次并发调用最多触发一次冷却。
func (s *AccountFailureGuardService) RecordFailure(ctx context.Context, account *Account, statusCode int) {
	if s == nil || s.counter == nil || s.repo == nil || !s.config.enabled {
		return
	}
	if account == nil || account.ID <= 0 || account.Status != StatusActive || !isCountableUpstreamFailure(statusCode) {
		return
	}
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(time.Now()) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	count, _, err := s.counter.ObserveAccountFailure(ctx, account.ID, s.config.windowSeconds, s.config.sampleIntervalSecs)
	if err != nil {
		slog.Warn("account_failure_guard_observe_failed", "account_id", account.ID, "error", err)
		return
	}
	mult := 1
	if m, err := s.counter.CurrentMultiplier(ctx, account.ID); err == nil && m > 0 {
		mult = m
	}
	if s.config.maxMultiplier > 0 && mult > s.config.maxMultiplier {
		mult = s.config.maxMultiplier
	}
	effectiveThreshold := int64(s.config.threshold) * int64(mult)
	if count < effectiveThreshold {
		return
	}

	until := time.Now().Add(time.Duration(s.config.cooldownSeconds) * time.Second)
	reason := fmt.Sprintf("%s%d consecutive upstream failures (last status %d) within %ds",
		AccountFailureGuardErrorPrefix, count, statusCode, s.config.windowSeconds)
	if err := s.repo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		slog.Warn("account_failure_guard_cooldown_failed", "account_id", account.ID, "error", err)
		return
	}
	// 冷却写完后清计数，避免每个在途请求重复触发；防抖倍率保留。
	if err := s.counter.ResetAccountFailureCount(ctx, account.ID); err != nil {
		slog.Warn("account_failure_guard_reset_failed", "account_id", account.ID, "error", err)
	}
	if _, err := s.counter.BumpMultiplier(ctx, account.ID, s.config.debounceSeconds, s.config.maxMultiplier); err != nil {
		slog.Warn("account_failure_guard_bump_multiplier_failed", "account_id", account.ID, "error", err)
	}
	slog.Warn("account_failure_guard_cooled_down",
		"account_id", account.ID,
		"account_name", account.Name,
		"platform", account.Platform,
		"failures", count,
		"multiplier", mult,
		"status_code", statusCode,
		"until", until,
	)
}

// StartRecoveryLoop 启动恢复探测后台循环。
func (s *AccountFailureGuardService) StartRecoveryLoop() {
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
	go s.runRecoveryLoop()
}

// Stop stops the recovery loop.
func (s *AccountFailureGuardService) Stop() {
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

func (s *AccountFailureGuardService) runRecoveryLoop() {
	defer s.wg.Done()
	interval := s.config.recoveryInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if err := s.RecoverOnce(s.ctx); err != nil && s.ctx.Err() == nil {
		slog.Warn("account_failure_guard_recovery_scan_failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.RecoverOnce(s.ctx); err != nil && s.ctx.Err() == nil {
				slog.Warn("account_failure_guard_recovery_scan_failed", "error", err)
			}
		}
	}
}

// RecoverOnce 扫描处于「连续失败冷却」中的账号并对每个执行健康探测；探测成功即提前解除冷却。
// 冷却到期会自动恢复可调度，本循环仅用于提前恢复。多实例下通过 leader lock 保证同一时刻只有一个实例执行扫描。
func (s *AccountFailureGuardService) RecoverOnce(ctx context.Context) error {
	if s == nil || !s.config.enabled || s.repo == nil || s.prober == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ttl := s.config.recoveryInterval
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if s.lockCache != nil {
		acquired, err := s.lockCache.TryAcquireLeaderLock(ctx, accountFailureGuardRecoveryLeaderLockKey, s.instanceID, ttl)
		if err == nil && !acquired {
			return nil
		}
		// 锁获取失败时继续执行：单实例部署或 Redis 抖动不应饿死恢复流程。
		defer func() {
			_ = s.lockCache.ReleaseLeaderLock(context.Background(), accountFailureGuardRecoveryLeaderLockKey, s.instanceID)
		}()
	}

	limit := s.config.recoveryBatchSize
	if limit <= 0 {
		limit = 20
	}
	accounts, err := s.repo.ListCooledDownAccounts(ctx, limit)
	if err != nil {
		return fmt.Errorf("list cooled-down accounts: %w", err)
	}
	for i := range accounts {
		account := accounts[i]
		s.recoverCandidate(ctx, &account)
	}
	return nil
}

func (s *AccountFailureGuardService) recoverCandidate(ctx context.Context, account *Account) {
	if account == nil || account.ID <= 0 {
		return
	}
	model := ""
	if s.prober != nil {
		if suggested, err := s.prober.SuggestProbeModel(ctx, account.ID); err == nil {
			model = suggested
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.config.probeTimeout)
	defer cancel()
	result, err := s.prober.RunHealthProbe(probeCtx, account.ID, model)
	if err != nil || result == nil || result.Status != "success" {
		slog.Debug("account_failure_guard_recovery_probe_unhealthy", "account_id", account.ID, "error", err)
		return
	}
	if s.recoverer == nil {
		return
	}
	if err := s.recoverer.ClearTempUnschedulable(ctx, account.ID); err != nil {
		slog.Warn("account_failure_guard_recover_failed", "account_id", account.ID, "error", err)
		return
	}
	// 提前恢复成功后清失败计数，冷却期内已累积的失败不再带入下一轮。
	if err := s.counter.ClearAccountFailureState(ctx, account.ID); err != nil {
		slog.Warn("account_failure_guard_clear_state_failed", "account_id", account.ID, "error", err)
	}
	slog.Info("account_failure_guard_recovered",
		"account_id", account.ID,
		"account_name", account.Name,
		"platform", account.Platform,
		"model", model,
	)
}
