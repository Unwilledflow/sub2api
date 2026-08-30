package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	healthSchedulerLeaderKey  = "account:health-scheduler:leader"
	healthSchedulerInterval   = 30 * time.Second
	healthSchedulerLockTTL    = 90 * time.Second
	SettingKeyHealthScheduler = "account_health_scheduler"
	guardianBaselineExtraKey  = "guardian_baseline"
)

// AccountHealthSchedulerPolicy 内置健康调度策略（DB 可调，面板只调参）。
// 移植自 guardian 引擎核心原则：被动信号（usage_logs/ops_error_logs）驱动，
// 429/限流只降权不摘除，凭据失效才熔断，分组保底永不断供，熔断带基线可恢复。
type AccountHealthSchedulerPolicy struct {
	Enabled                  bool    `json:"enabled"`
	ShortWindowMin           int     `json:"short_window_min"` // 统计窗口（分钟）
	FuseScore                float64 `json:"fuse_score"`       // 低于此分熔断
	RecoverScore             float64 `json:"recover_score"`    // 高于此分且无致命错误才回池
	SlowTTFTMs               int     `json:"slow_ttft_ms"`     // 首字慢阈值
	MinLoadFactor            int     `json:"min_load_factor"`
	MaxLoadFactor            int     `json:"max_load_factor"`
	MinAvailablePerGroup     int     `json:"min_available_per_group"` // 分组保底
	CooldownMinutes          int     `json:"cooldown_minutes"`        // 熔断冷却
	ExplorationSamples       int     `json:"exploration_samples"`     // 低于此样本数视为探索期
	ExplorationMinLoadFactor int     `json:"exploration_min_load_factor"` // 探索期负载下限

	// ---- 自适应调参（v20）：调度器根据全局池表现自动调整这些参数 ----
	// AutoTune 开启后：SlowTTFTMs / 冷却时长按池级 P50/P95 分位数自动校准，
	// 无需人工设定；策略里的数值仅作初值与安全边界。
	AutoTune                 bool    `json:"auto_tune"`
	AutoTuneSlowTTFTMultiple float64 `json:"auto_tune_slow_ttft_multiple"` // 慢阈值 = 池 P95 * multiple
	AutoTuneCooldownMin      int     `json:"auto_tune_cooldown_min"`       // 自适应冷却下限（分钟）
	AutoTuneCooldownMax      int     `json:"auto_tune_cooldown_max"`       // 自适应冷却上限（分钟）

	// ---- 写回通道（重建自调度器会话）：并发归一与优先级重映射 ----
	// ConcurrencyEnabled：无流量账号并发归一；健康且打满的账号扩容；闲置收缩。
	// PriorityEnabled：优先级按 latency_rate 语义重映射（首字系数 + 稳定性系数，
	// 样本不足惩罚），调度器自主判断，面板不再写这些字段。
	ConcurrencyEnabled       bool    `json:"concurrency_enabled"`
	ConcurrencyNormMin       int     `json:"concurrency_norm_min"`       // 归一目标下限（默认 5）
	ConcurrencyExpandCap     int     `json:"concurrency_expand_cap"`     // 单账号扩容上限（默认 600）
	PriorityEnabled          bool    `json:"priority_enabled"`
	PriorityFirstTokenCoef   float64 `json:"priority_first_token_coef"`   // 首字延迟系数（默认 10000）
	PriorityRateCoef         float64 `json:"priority_rate_coef"`         // 稳定性系数（默认 10000）
	PrioritySampleSize       int     `json:"priority_sample_size"`       // 样本不足阈值（默认 20）
	PriorityLookbackMin      int     `json:"priority_lookback_min"`      // 优先级评估回看窗口（默认 180）
	PriorityMissingPenaltyMs int     `json:"priority_missing_penalty_ms"` // 无样本账号虚拟首字惩罚（默认 2000）
}

func defaultHealthSchedulerPolicy() AccountHealthSchedulerPolicy {
	return AccountHealthSchedulerPolicy{
		Enabled: false, ShortWindowMin: 5,
		FuseScore: 30, RecoverScore: 70, SlowTTFTMs: 15000,
		MinLoadFactor: 20, MaxLoadFactor: 500,
		MinAvailablePerGroup: 1, CooldownMinutes: 5,
		ExplorationSamples: 20, ExplorationMinLoadFactor: 100,
		AutoTune:                 true,
		AutoTuneSlowTTFTMultiple: 1.2,
		AutoTuneCooldownMin:      3,
		AutoTuneCooldownMax:      30,
		ConcurrencyEnabled:       true,
		ConcurrencyNormMin:       5,
		ConcurrencyExpandCap:     600,
		PriorityEnabled:          true,
		PriorityFirstTokenCoef:   10000,
		PriorityRateCoef:         10000,
		PrioritySampleSize:       20,
		PriorityLookbackMin:      180,
		PriorityMissingPenaltyMs: 2000,
	}
}

type guardianBaseline struct {
	LoadFactor *int  `json:"load_factor,omitempty"`
	Priority   int   `json:"priority"`
	FusedAt    int64 `json:"fused_at"`
}

// AccountHealthScheduler 内置被动健康调度器：零额外上游探测，
// 直接聚合网关已产生的 usage_logs（成功+TTFB）与 ops_error_logs（失败分类）。
type AccountHealthScheduler struct {
	accountRepo AccountRepository
	db          *sql.DB
	lockCache   LeaderLockCache
	instanceID  string
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewAccountHealthScheduler(accountRepo AccountRepository, db *sql.DB, lockCache LeaderLockCache) *AccountHealthScheduler {
	return &AccountHealthScheduler{
		accountRepo: accountRepo, db: db, lockCache: lockCache,
		instanceID: uuid.NewString(), stopCh: make(chan struct{}),
	}
}

// ProvideAccountHealthScheduler 装配并自启调度循环（进程退出即终止）。
func ProvideAccountHealthScheduler(accountRepo AccountRepository, lockCache LeaderLockCache, db *sql.DB) *AccountHealthScheduler {
	s := NewAccountHealthScheduler(accountRepo, db, lockCache)
	s.Start()
	return s
}

func (s *AccountHealthScheduler) Start() {
	if s == nil || s.db == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(healthSchedulerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *AccountHealthScheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *AccountHealthScheduler) GetPolicy(ctx context.Context) AccountHealthSchedulerPolicy {
	pol := defaultHealthSchedulerPolicy()
	if s == nil || s.db == nil {
		return pol
	}
	raw, err := s.settingValue(ctx, SettingKeyHealthScheduler)
	if err != nil || raw == "" {
		return pol
	}
	_ = json.Unmarshal([]byte(raw), &pol)
	return pol
}

// UpdatePolicy 持久化策略（UPSERT settings 表）。
func (s *AccountHealthScheduler) UpdatePolicy(ctx context.Context, pol AccountHealthSchedulerPolicy) (AccountHealthSchedulerPolicy, error) {
	def := defaultHealthSchedulerPolicy()
	if pol.ShortWindowMin <= 0 {
		pol.ShortWindowMin = def.ShortWindowMin
	}
	if pol.FuseScore <= 0 {
		pol.FuseScore = def.FuseScore
	}
	if pol.RecoverScore <= 0 {
		pol.RecoverScore = def.RecoverScore
	}
	if pol.SlowTTFTMs <= 0 {
		pol.SlowTTFTMs = def.SlowTTFTMs
	}
	if pol.MinLoadFactor <= 0 {
		pol.MinLoadFactor = def.MinLoadFactor
	}
	if pol.MaxLoadFactor <= 0 {
		pol.MaxLoadFactor = def.MaxLoadFactor
	}
	if pol.MinAvailablePerGroup <= 0 {
		pol.MinAvailablePerGroup = def.MinAvailablePerGroup
	}
	if pol.CooldownMinutes <= 0 {
		pol.CooldownMinutes = def.CooldownMinutes
	}
	if pol.ExplorationSamples <= 0 {
		pol.ExplorationSamples = def.ExplorationSamples
	}
	if pol.ExplorationMinLoadFactor <= 0 {
		pol.ExplorationMinLoadFactor = def.ExplorationMinLoadFactor
	}
	if pol.AutoTuneSlowTTFTMultiple <= 0 {
		pol.AutoTuneSlowTTFTMultiple = def.AutoTuneSlowTTFTMultiple
	}
	if pol.AutoTuneCooldownMin <= 0 {
		pol.AutoTuneCooldownMin = def.AutoTuneCooldownMin
	}
	if pol.AutoTuneCooldownMax < pol.AutoTuneCooldownMin {
		pol.AutoTuneCooldownMax = def.AutoTuneCooldownMax
	}
	if pol.ConcurrencyNormMin <= 0 {
		pol.ConcurrencyNormMin = def.ConcurrencyNormMin
	}
	if pol.ConcurrencyExpandCap <= 0 {
		pol.ConcurrencyExpandCap = def.ConcurrencyExpandCap
	}
	if pol.PriorityFirstTokenCoef <= 0 {
		pol.PriorityFirstTokenCoef = def.PriorityFirstTokenCoef
	}
	if pol.PriorityRateCoef <= 0 {
		pol.PriorityRateCoef = def.PriorityRateCoef
	}
	if pol.PrioritySampleSize <= 0 {
		pol.PrioritySampleSize = def.PrioritySampleSize
	}
	if pol.PriorityLookbackMin <= 0 {
		pol.PriorityLookbackMin = def.PriorityLookbackMin
	}
	if pol.PriorityMissingPenaltyMs <= 0 {
		pol.PriorityMissingPenaltyMs = def.PriorityMissingPenaltyMs
	}
	b, err := json.Marshal(pol)
	if err != nil {
		return pol, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		SettingKeyHealthScheduler, string(b))
	return pol, err
}

func (s *AccountHealthScheduler) settingValue(ctx context.Context, key string) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

type healthAgg struct {
	accountID int64
	okCount   int64
	fatalCnt  int64 // 401/403/404
	softCnt   int64 // 429/5xx/网络
	slowCnt   int64
	ttftSum   float64
	ttftCnt   int64
	// 优先级评估用长窗口（lookback_min）聚合，与短窗口解耦。
	longOk      int64
	longErr     int64
	longFatal   int64 // 长窗口致命错误（401/403/404）——优先级只惩罚 fatal
	longTtftSum float64
	longTtftCnt int64
}

func (s *AccountHealthScheduler) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pol := s.GetPolicy(ctx)
	if !pol.Enabled {
		return
	}

	lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
	release, ok := tryAcquireSingletonLeaderLock(lockCtx, s.lockCache, s.db, healthSchedulerLeaderKey, s.instanceID, healthSchedulerLockTTL)
	lockCancel()
	if !ok {
		return
	}
	defer release()

	aggs, err := s.aggregate(ctx, pol)
	if err != nil {
		slog.Warn("[HealthScheduler] aggregate failed", "error", err)
		return
	}
	groupCounts, err := s.schedulablePerGroup(ctx)
	if err != nil {
		slog.Warn("[HealthScheduler] group count failed", "error", err)
		return
	}

	// 账号调度上下文（主分组/倍率/负载因子）：优先级组内对比与 auto-tune 都依赖。
	acctCtx := s.collectAccountContext(ctx, aggs)

	// 自适应调参：用分组级分位数动态校准策略参数（单次循环内生效，不写回 DB）。
	// TTFT 阈值按各分组各自的 P95 取最大偏离组校准（组特征不同，不能全局混比）。
	if pol.AutoTune {
		pol = autoTunePolicy(pol, aggs, groupCounts, acctCtx)
	}

	// 优先级重映射：分组内相对分档（同组账号才可比），组内按质量分排序 → 均匀分档 1-30。
	if pol.PriorityEnabled {
		s.remapPrioritiesByGroup(ctx, pol, aggs, acctCtx)
	}

	for _, a := range aggs {
		s.decide(ctx, pol, a, groupCounts)
	}
}

// remapPrioritiesByGroup 优先级分组内重映射：
//
// 质量分（越大越好）= 首字项 + 稳定性项 − 倍率惩罚（公式见 qualityScore）。
// 同一主分组内按质量分排序 → 均匀分档到 [1,30]（rank 0 = 最高优先 = 数字最小）。
// 组内不足 2 个可比账号时跳过（无对比意义）；并列分数共享同一档。
// 变化 >=2 才写库。
func (s *AccountHealthScheduler) remapPrioritiesByGroup(ctx context.Context, pol AccountHealthSchedulerPolicy, aggs []healthAgg, acctCtx map[int64]accountSchedContext) {
	// 按主分组建桶
	buckets := map[int64][]int64{} // groupID -> []accountID
	for _, a := range aggs {
		ac, ok := acctCtx[a.accountID]
		if !ok || ac.groupID <= 0 {
			continue
		}
		buckets[ac.groupID] = append(buckets[ac.groupID], a.accountID)
	}
	aggByID := map[int64]*healthAgg{}
	for i := range aggs {
		aggByID[aggs[i].accountID] = &aggs[i]
	}

	updates := map[int64]int{} // accountID -> new priority
	for gid, ids := range buckets {
		if len(ids) < 2 {
			continue
		}
		// 组内算质量分
		type scored struct {
			id    int64
			score float64
		}
		scoredIDs := make([]scored, 0, len(ids))
		for _, id := range ids {
			a := aggByID[id]
			ac := acctCtx[id]
			if a == nil {
				continue
			}
			sc := qualityScore(pol, *a, ac.rateMultiplier)
			scoredIDs = append(scoredIDs, scored{id: id, score: sc})
		}
		if len(scoredIDs) < 2 {
			continue
		}
		// 质量分降序（好 → 前）
		sort.SliceStable(scoredIDs, func(i, j int) bool { return scoredIDs[i].score > scoredIDs[j].score })
		// 均匀分档：rank 0 → 1（最高优先），最后 rank → 30
		n := len(scoredIDs)
		for rank, sc := range scoredIDs {
			desired := 1 + rank*(30-1)/max(n-1, 1)
			// 并列分数共享同一档
			if rank > 0 && sc.score == scoredIDs[rank-1].score {
				desired = updates[scoredIDs[rank-1].id]
			}
			updates[sc.id] = desired
		}
		slog.Debug("[HealthScheduler] priority group ranking", "group", gid, "accounts", n)
	}

	// 批量写库（迟滞：变化 >=2 才写）
	for id, desired := range updates {
		if a := aggByID[id]; a != nil {
			if account, err := s.accountRepo.GetByID(ctx, id); err == nil && account != nil && abs(desired-account.Priority) >= 2 {
				if _, err := s.accountRepo.BulkUpdate(ctx, []int64{id}, AccountBulkUpdate{Priority: &desired}); err != nil {
					slog.Warn("[HealthScheduler] priority remap failed", "account", id, "error", err)
				} else {
					slog.Info("[HealthScheduler] priority remapped", "account", id, "from", account.Priority, "to", desired, "group", acctCtx[id].groupID)
				}
			}
		}
	}
}

// qualityScore 账号质量分（越大越好，仅在同分组内比较有意义）：
//
//	15 × baselineTTFT / firstTokenMs + 15 / (1 + fatalRate×10) − rate×50
//
// 首字达到基准（missing_penalty_ms）得一半分，快一倍得满分；致命错误率
// 每 10% 衰减一半；倍率越小（越便宜）分越高。零样本两段各给保守一半分。
func qualityScore(pol AccountHealthSchedulerPolicy, a healthAgg, rateMultiplier float64) float64 {
	samples := a.longOk + a.longErr
	var firstToken float64
	if a.longTtftCnt > 0 {
		firstToken = a.longTtftSum / float64(a.longTtftCnt)
	}
	if samples < int64(pol.PrioritySampleSize) {
		missingRatio := 1 - float64(samples)/float64(pol.PrioritySampleSize)
		firstToken += float64(pol.PriorityMissingPenaltyMs) * missingRatio
	}
	fatalRate := 0.0
	if samples > 0 {
		fatalRate = float64(a.longFatal) / float64(samples)
	}
	rateWeight := 50.0

	var latencyScore float64
	stabilityScore := 15.0 / (1 + fatalRate*10)
	if a.longTtftCnt <= 0 {
		latencyScore = 7.5
		stabilityScore = min(stabilityScore, 7.5)
	} else {
		baseline := float64(max(pol.PriorityMissingPenaltyMs, 1))
		latencyScore = 15.0 * baseline / math.Max(firstToken, 1)
	}
	return latencyScore + stabilityScore - rateMultiplier*rateWeight
}

// autoTunePolicy 根据分组级表现自适应校准关键参数（调度器自主判断，预设仅作初值/边界）：
//  1. SlowTTFTMs：慢阈值 = 各分组 P95 中最大者 * multiple（分组特征不同——生图组天然
//     比 GPT 组慢，全局混比会把慢组全员误判劣化；取最大偏离组保证阈值对每组都不过严）。
//  2. CooldownMinutes：熔断冷却 = 基础值 * (0.5 + 池冗余度)。池子充裕时冷却更长
//     （不急着回池试探），池子紧张时缩短（尽快恢复供给）。
//  3. ExplorationMinLoadFactor：探索期负载下限 = 各分组成熟健康账号负载因子中位数的
//     最小值 * 80%（新渠道保底跟随其所在组的成熟水平）。
func autoTunePolicy(pol AccountHealthSchedulerPolicy, aggs []healthAgg, groupCounts map[int64]int64, acctCtx map[int64]accountSchedContext) AccountHealthSchedulerPolicy {
	if len(aggs) == 0 {
		return pol
	}

	// 分组内每账号平均 TTFT → 各组 P95 → 跨组取中位数
	// （取最大值会在组间流量波动时来回抖动；中位数对慢组公平且稳定）
	perGroupTTFT := map[int64][]float64{}
	for _, a := range aggs {
		if a.ttftCnt > 0 {
			if ac, ok := acctCtx[a.accountID]; ok && ac.groupID > 0 {
				perGroupTTFT[ac.groupID] = append(perGroupTTFT[ac.groupID], a.ttftSum/float64(a.ttftCnt))
			}
		}
	}
	groupP95s := make([]float64, 0, len(perGroupTTFT))
	for gid, vals := range perGroupTTFT {
		if len(vals) < 3 { // 组内样本太少不参与（单账号组无分位意义）
			continue
		}
		p95 := percentileFloat(vals, 0.95)
		groupP95s = append(groupP95s, p95)
		slog.Debug("[HealthScheduler] group ttft p95", "group", gid, "p95", int(p95), "accounts", len(vals))
	}
	if len(groupP95s) > 0 {
		medP95 := percentileFloat(groupP95s, 0.5)
		tuned := int(medP95 * pol.AutoTuneSlowTTFTMultiple)
		tuned = clampInt(tuned, 3000, 60000)
		// 只有偏离超过 30% 才调整，避免频繁抖动
		if abs(tuned-pol.SlowTTFTMs)*10 > pol.SlowTTFTMs*3 {
			slog.Info("[HealthScheduler] auto-tune slow_ttft", "from", pol.SlowTTFTMs, "to", tuned, "group_p95_median", int(medP95))
			pol.SlowTTFTMs = tuned
		}
	}

	// 池冗余度 → 冷却时长
	totalSched := int64(0)
	for _, c := range groupCounts {
		totalSched += c
	}
	if totalSched > 0 {
		fused := int64(0)
		for _, a := range aggs {
			if a.score(pol) < pol.FuseScore {
				fused++
			}
		}
		redundancy := float64(totalSched) / float64(totalSched+fused) // (0,1]
		base := float64(pol.CooldownMinutes)
		tuned := base * (0.5 + redundancy)
		tunedMin := clampInt(int(tuned), pol.AutoTuneCooldownMin, pol.AutoTuneCooldownMax)
		if abs(tunedMin-pol.CooldownMinutes) >= 2 {
			slog.Info("[HealthScheduler] auto-tune cooldown", "from", pol.CooldownMinutes, "to", tunedMin, "redundancy", redundancy)
			pol.CooldownMinutes = tunedMin
		}
	}

	// 探索期下限 → 各分组成熟健康账号负载因子中位数，取最小者 * 80%
	perGroupLF := map[int64][]float64{}
	for _, a := range aggs {
		ac, ok := acctCtx[a.accountID]
		if !ok || ac.groupID <= 0 {
			continue
		}
		if ac.loadFactor > 0 && a.score(pol) >= 70 && (a.okCount+a.fatalCnt+a.softCnt) >= int64(pol.ExplorationSamples) {
			perGroupLF[ac.groupID] = append(perGroupLF[ac.groupID], float64(ac.loadFactor))
		}
	}
	minMedian := 0
	for gid, vals := range perGroupLF {
		if len(vals) < 2 {
			continue
		}
		med := int(percentileFloat(vals, 0.5))
		if minMedian == 0 || med < minMedian {
			minMedian = med
		}
		slog.Debug("[HealthScheduler] group mature lf median", "group", gid, "median", med, "accounts", len(vals))
	}
	if minMedian > 0 {
		// 下限取中位数的 80%（让新渠道略低于成熟水平即可被调度到），clamp 到安全区间
		tuned := clampInt(minMedian*8/10, pol.MinLoadFactor, pol.MaxLoadFactor)
		if abs(tuned-pol.ExplorationMinLoadFactor)*10 > pol.ExplorationMinLoadFactor*3 {
			slog.Info("[HealthScheduler] auto-tune exploration floor", "from", pol.ExplorationMinLoadFactor, "to", tuned, "mature_min_median", minMedian)
			pol.ExplorationMinLoadFactor = tuned
		}
	}

	return pol
}

// collectAccountContext 批量取聚合账号的调度上下文（负载因子、倍率、主分组），
// 一次查询避免逐账号 GetByID。优先级组内对比与 auto-tune 分组分位都依赖它。
func (s *AccountHealthScheduler) collectAccountContext(ctx context.Context, aggs []healthAgg) map[int64]accountSchedContext {
	if len(aggs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(aggs))
	for _, a := range aggs {
		ids = append(ids, a.accountID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, COALESCE(a.load_factor, 100), COALESCE(a.rate_multiplier, 1),
			(SELECT ag.group_id FROM account_groups ag WHERE ag.account_id = a.id ORDER BY ag.priority, ag.created_at LIMIT 1)
		FROM accounts a WHERE a.id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[int64]accountSchedContext{}
	for rows.Next() {
		var id int64
		var ac accountSchedContext
		var gid sql.NullInt64
		if err := rows.Scan(&id, &ac.loadFactor, &ac.rateMultiplier, &gid); err != nil {
			continue
		}
		if gid.Valid {
			ac.groupID = gid.Int64
		}
		out[id] = ac
	}
	return out
}

// accountSchedContext 账号调度上下文：优先级组内对比所需的最小字段集。
type accountSchedContext struct {
	loadFactor    int
	rateMultiplier float64
	groupID       int64 // 主分组（account_groups 中最早的绑定）
}

// aggregate 聚合窗口内成功/失败/慢样本。成功与 TTFB 来自 usage_logs，
// 失败分类来自 ops_error_logs（429/限流归 soft，401/403/404 归 fatal）。
// 长窗口（优先级评估）单独聚合，避免短窗口的抖动直接影响优先级。
func (s *AccountHealthScheduler) aggregate(ctx context.Context, pol AccountHealthSchedulerPolicy) ([]healthAgg, error) {
	since := time.Now().Add(-time.Duration(pol.ShortWindowMin) * time.Minute)
	rows, err := s.db.QueryContext(ctx, `
		SELECT account_id,
			COALESCE(SUM(ok),0), COALESCE(SUM(fatal),0), COALESCE(SUM(soft),0),
			COALESCE(SUM(slow),0), COALESCE(SUM(ttft_sum),0), COALESCE(SUM(ttft_cnt),0)
		FROM (
			SELECT account_id, COUNT(*) AS ok, 0 AS fatal, 0 AS soft,
				COUNT(*) FILTER (WHERE COALESCE(first_token_ms,0) > $2) AS slow,
				SUM(COALESCE(first_token_ms,0)) FILTER (WHERE COALESCE(first_token_ms,0) > 0) AS ttft_sum,
				COUNT(*) FILTER (WHERE COALESCE(first_token_ms,0) > 0) AS ttft_cnt
			FROM usage_logs
			WHERE created_at >= $1 AND account_id IS NOT NULL
			GROUP BY account_id
			UNION ALL
			SELECT account_id, 0,
				COUNT(*) FILTER (WHERE status_code IN (401,403,404)),
				COUNT(*) FILTER (WHERE status_code = 429 OR status_code >= 500 OR status_code = 0),
				0, 0, 0
			FROM ops_error_logs
			WHERE created_at >= $1 AND account_id IS NOT NULL
			GROUP BY account_id
		) t
		GROUP BY account_id`, since, pol.SlowTTFTMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[int64]*healthAgg{}
	for rows.Next() {
		var a healthAgg
		if err := rows.Scan(&a.accountID, &a.okCount, &a.fatalCnt, &a.softCnt, &a.slowCnt, &a.ttftSum, &a.ttftCnt); err != nil {
			return nil, err
		}
		byID[a.accountID] = &a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 长窗口聚合（优先级评估）：仅当写回通道开启时才查询，省一次全表扫。
	if pol.PriorityEnabled {
		longSince := time.Now().Add(-time.Duration(pol.PriorityLookbackMin) * time.Minute)
		lrows, lerr := s.db.QueryContext(ctx, `
			SELECT account_id,
				COUNT(*),
				COUNT(*) FILTER (WHERE COALESCE(first_token_ms,0) > 0),
				COALESCE(SUM(COALESCE(first_token_ms,0)) FILTER (WHERE COALESCE(first_token_ms,0) > 0),0)
			FROM usage_logs
			WHERE created_at >= $1 AND account_id IS NOT NULL
			GROUP BY account_id`, longSince)
		if lerr != nil {
			return nil, lerr
		}
		for lrows.Next() {
			var id, okCnt, ttftCnt, ttftSum int64
			if err := lrows.Scan(&id, &okCnt, &ttftCnt, &ttftSum); err != nil {
				lrows.Close()
				return nil, err
			}
			a := byID[id]
			if a == nil {
				a = &healthAgg{accountID: id}
				byID[id] = a
			}
			a.longOk = okCnt
			a.longTtftCnt = ttftCnt
			a.longTtftSum = float64(ttftSum)
		}
		lerr = lrows.Err()
		lrows.Close()
		if lerr != nil {
			return nil, lerr
		}

		// 长窗口错误数（总错误 + 致命错误拆分：优先级只惩罚 fatal，软错误走健康分通道）
		lerows, leerr := s.db.QueryContext(ctx, `
			SELECT account_id,
				COUNT(*),
				COUNT(*) FILTER (WHERE status_code IN (401,403,404))
			FROM ops_error_logs
			WHERE created_at >= $1 AND account_id IS NOT NULL
			GROUP BY account_id`, longSince)
		if leerr != nil {
			return nil, leerr
		}
		for lerows.Next() {
			var id, cnt, fatal int64
			if err := lerows.Scan(&id, &cnt, &fatal); err != nil {
				lerows.Close()
				return nil, err
			}
			if a := byID[id]; a != nil {
				a.longErr = cnt
				a.longFatal = fatal
			}
		}
		leerr = lerows.Err()
		lerows.Close()
		if leerr != nil {
			return nil, leerr
		}
	}

	out := make([]healthAgg, 0, len(byID))
	for _, a := range byID {
		out = append(out, *a)
	}
	return out, nil
}

func (s *AccountHealthScheduler) schedulablePerGroup(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ag.group_id, COUNT(*)
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE a.schedulable AND a.status = 'active' AND a.deleted_at IS NULL
		GROUP BY ag.group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var gid, cnt int64
		if err := rows.Scan(&gid, &cnt); err != nil {
			return nil, err
		}
		out[gid] = cnt
	}
	return out, rows.Err()
}

// score 计算健康分：成功率主导，慢请求扣分，致命错误一票否决；
// 429/限流只通过成功率自然降分，不额外惩罚（限流是等一会就好的事）。
func (a healthAgg) score(pol AccountHealthSchedulerPolicy) float64 {
	total := a.okCount + a.fatalCnt + a.softCnt
	if total == 0 {
		return 100
	}
	sc := float64(a.okCount) / float64(total) * 100
	if a.ttftCnt > 0 {
		slowRatio := float64(a.slowCnt) / float64(a.ttftCnt)
		sc -= slowRatio * 20
	}
	if a.fatalCnt > 0 {
		sc = min(sc, 10)
	}
	return math.Max(0, math.Min(100, sc))
}

func (s *AccountHealthScheduler) decide(ctx context.Context, pol AccountHealthSchedulerPolicy, a healthAgg, groupCounts map[int64]int64) {
	account, err := s.accountRepo.GetByID(ctx, a.accountID)
	if err != nil || account == nil || !account.IsActive() {
		return
	}

	sc := a.score(pol)
	baseline := readGuardianBaseline(account)

	if baseline != nil {
		// 已熔断：冷却后健康分回升且无致命错误才回池，恢复基线值。
		if time.Now().Unix() < baseline.FusedAt+int64(pol.CooldownMinutes)*60 {
			return
		}
		if sc >= pol.RecoverScore && a.fatalCnt == 0 {
			updates := AccountBulkUpdate{Schedulable: boolPtr(true)}
			if baseline.LoadFactor != nil {
				updates.LoadFactor = baseline.LoadFactor
			}
			if baseline.Priority > 0 {
				updates.Priority = &baseline.Priority
			}
			if _, err := s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, updates); err != nil {
				slog.Warn("[HealthScheduler] recover failed", "account", account.ID, "error", err)
				return
			}
			s.clearBaseline(ctx, account)
			slog.Info("[HealthScheduler] account recovered", "account", account.ID, "score", sc)
		}
		return
	}

	if !account.Schedulable {
		return // 人工停用的账号不自动干预
	}

	// 探索期：样本不足的新渠道健康分乐观但无调用证据，
	// 给负载下限保证它被调度到，避免「好渠道永远不被调用」。
	samples := a.okCount + a.fatalCnt + a.softCnt
	if samples < int64(pol.ExplorationSamples) && a.fatalCnt == 0 {
		lf := pol.ExplorationMinLoadFactor
		if current := account.EffectiveLoadFactor(); current < lf {
			if _, err := s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{LoadFactor: &lf}); err != nil {
				slog.Warn("[HealthScheduler] exploration boost failed", "account", account.ID, "error", err)
			}
		}
		// 探索期账号并发归一：无流量证据时保证并发在合理区间
		if pol.ConcurrencyEnabled {
			s.normalizeConcurrency(ctx, pol, account)
		}
		return
	}

	// 调权：健康分映射到 load_factor，变化显著才写库。
	desired := int(sc / 100 * float64(pol.MaxLoadFactor))
	desired = max(pol.MinLoadFactor, min(pol.MaxLoadFactor, desired))
	current := account.EffectiveLoadFactor()
	if abs(desired-current) > max(10, current/10) {
		lf := desired
		if _, err := s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{LoadFactor: &lf}); err != nil {
			slog.Warn("[HealthScheduler] reweight failed", "account", account.ID, "error", err)
		}
	}

	// 并发写回：按账号利用率与池压力自主调整（扩容 / 收缩）。
	if pol.ConcurrencyEnabled {
		s.tuneConcurrency(ctx, pol, account, a)
	}

	// 熔断：仅致命错误主导（分极低）时摘除；分组保底优先。
	if sc >= pol.FuseScore {
		return
	}
	gid := s.primaryGroup(ctx, account.ID)
	if gid > 0 && groupCounts[gid] <= int64(pol.MinAvailablePerGroup) {
		// 保底：只降权到最低，不摘除，保证分组永不断供。
		lf := pol.MinLoadFactor
		_, _ = s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{LoadFactor: &lf})
		slog.Warn("[HealthScheduler] floor held, not fused", "account", account.ID, "group", gid, "score", sc)
		return
	}
	bl := guardianBaseline{Priority: account.Priority, FusedAt: time.Now().Unix()}
	if account.LoadFactor != nil {
		v := *account.LoadFactor
		bl.LoadFactor = &v
	}
	s.writeBaseline(ctx, account, bl)
	if _, err := s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{Schedulable: boolPtr(false)}); err != nil {
		slog.Warn("[HealthScheduler] fuse failed", "account", account.ID, "error", err)
		return
	}
	slog.Warn("[HealthScheduler] account fused", "account", account.ID, "score", sc)
}

func (s *AccountHealthScheduler) primaryGroup(ctx context.Context, accountID int64) int64 {
	var gid sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1 ORDER BY priority, created_at LIMIT 1`, accountID).Scan(&gid)
	if err != nil || !gid.Valid {
		return 0
	}
	return gid.Int64
}

// tuneConcurrency 并发写回通道（调度器自主判断）：
//   - 账号窗口内请求量长期低于当前并发的 10% → 收缩到 max(归一下限, 压力*4)，回收闲置额度；
//   - 账号把并发吃满（请求量 ≥ 并发的 80%）且健康分好（≥80）→ 扩容 20%，让优质账号接更多量。
//
// 写库带迟滞（变化 >20% 才写），避免每轮抖动。
func (s *AccountHealthScheduler) tuneConcurrency(ctx context.Context, pol AccountHealthSchedulerPolicy, account *Account, a healthAgg) {
	if account == nil || account.Concurrency <= 0 {
		return
	}
	cur := account.Concurrency
	pressure := int(a.okCount) + int(a.softCnt) // 窗口内请求量 ≈ 并发压力代理

	desired := cur
	switch {
	case pressure*10 < cur: // 闲置：压力不足并发 1/10
		desired = max(pol.ConcurrencyNormMin, pressure*4)
	case pressure*5 >= cur*4 && a.score(pol) >= 80: // 打满且健康：扩 20%
		desired = min(pol.ConcurrencyExpandCap, cur+cur/5)
	}
	// 迟滞：变化不足 20% 不写
	if desired == cur || abs(desired-cur)*5 < cur {
		return
	}
	if _, err := s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{Concurrency: &desired}); err != nil {
		slog.Warn("[HealthScheduler] concurrency tune failed", "account", account.ID, "error", err)
		return
	}
	slog.Info("[HealthScheduler] concurrency tuned", "account", account.ID, "from", cur, "to", desired, "pressure", pressure)
}

// normalizeConcurrency 探索期账号的并发归一：无流量证据时把并发拉回安全区间。
func (s *AccountHealthScheduler) normalizeConcurrency(ctx context.Context, pol AccountHealthSchedulerPolicy, account *Account) {
	if account == nil || account.Concurrency <= 0 {
		return
	}
	desired := account.Concurrency
	if account.Concurrency < pol.ConcurrencyNormMin {
		desired = pol.ConcurrencyNormMin
	} else if account.Concurrency > pol.ConcurrencyExpandCap {
		desired = pol.ConcurrencyExpandCap
	}
	if desired != account.Concurrency {
		if _, err := s.accountRepo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{Concurrency: &desired}); err != nil {
			slog.Warn("[HealthScheduler] concurrency normalize failed", "account", account.ID, "error", err)
		}
	}
}

func readGuardianBaseline(account *Account) *guardianBaseline {
	if account == nil || len(account.Extra) == 0 {
		return nil
	}
	raw, ok := account.Extra[guardianBaselineExtraKey]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var bl guardianBaseline
	if err := json.Unmarshal(b, &bl); err != nil || bl.FusedAt == 0 {
		return nil
	}
	return &bl
}

func (s *AccountHealthScheduler) writeBaseline(ctx context.Context, account *Account, bl guardianBaseline) {
	s.updateBaselineExtra(ctx, account, &bl)
}

func (s *AccountHealthScheduler) clearBaseline(ctx context.Context, account *Account) {
	s.updateBaselineExtra(ctx, account, nil)
}

func (s *AccountHealthScheduler) updateBaselineExtra(ctx context.Context, account *Account, bl *guardianBaseline) {
	updates := map[string]any{}
	if bl == nil {
		updates[guardianBaselineExtraKey] = nil
	} else {
		updates[guardianBaselineExtraKey] = bl
	}
	_ = s.accountRepo.UpdateExtra(ctx, account.ID, updates)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// percentileFloat 计算分位数（插入排序法，样本量小足够）。
func percentileFloat(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	idx := p * float64(len(sorted)-1)
	lo := int(idx)
	hi := min(lo+1, len(sorted)-1)
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
