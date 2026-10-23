package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBuildOpenAIAccountLoadPlanMovesModelTTFTOutlierToOverflowWithoutPerformanceClass(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 3
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.TTFT = 8
	stats := newOpenAIAccountRuntimeStats()
	fast := 3000
	stable := 4200
	slow := 20_000
	stats.reportForModelClass(1001, "gpt-5.6-sol", "", true, &fast)
	stats.reportForModelClass(1002, "gpt-5.6-sol", "", true, &stable)
	stats.reportForModelClass(1003, "gpt-5.6-sol", "", true, &slow)
	scheduler := &defaultOpenAIAccountScheduler{
		service: &OpenAIGatewayService{cfg: cfg},
		stats:   stats,
	}
	accounts := []*Account{{ID: 1001}, {ID: 1002}, {ID: 1003}}

	plan := scheduler.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-5.6-sol",
	}, accounts, map[int64]*AccountLoadInfo{})

	require.True(t, plan.includeTTFTOverflowFallback)
	require.False(t, plan.candidates[0].ttftOutlier)
	require.False(t, plan.candidates[1].ttftOutlier)
	require.True(t, plan.candidates[2].ttftOutlier)
	require.Len(t, plan.selectionOrder, 3)
	require.Equal(t, int64(1003), plan.selectionOrder[2].account.ID)
	for _, candidate := range plan.selectionOrder[:2] {
		require.NotEqual(t, int64(1003), candidate.account.ID)
	}
}

func TestReportOpenAIAccountScheduleResult_UsesFailoverFirstTokenMs(t *testing.T) {
	svc := &OpenAIGatewayService{openaiAccountStats: newOpenAIAccountRuntimeStats()}
	err := &UpstreamFailoverError{StatusCode: 504, FirstTokenMs: 60_000}

	svc.ReportOpenAIAccountScheduleResult(&Account{ID: 2175}, "gpt-5.6-sol", false, nil, err)

	_, ttft, hasTTFT := svc.openaiAccountStats.snapshotForModelClass(2175, "gpt-5.6-sol", "")
	require.True(t, hasTTFT)
	require.InDelta(t, 60_000.0, ttft, 1e-9)
	errorRate, _, _ := svc.openaiAccountStats.snapshot(2175)
	require.GreaterOrEqual(t, errorRate, openAIFirstOutputStallErrorFloor)
}

func TestReportOpenAIAccountScheduleResult_OneFirstOutputStallEscapesStickyAccount(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10601)
	accounts := []Account{
		{ID: 26101, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}},
		{ID: 26102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_first_output_stall": 26101}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                cfg,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{26102: true}}),
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
	}

	svc.ReportOpenAIAccountScheduleResult(&accounts[0], "gpt-6-sol", false, nil, &UpstreamFailoverError{StatusCode: 504, FirstTokenMs: 45_000})

	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_first_output_stall", "gpt-6-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(26102), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, int64(26101), cache.sessionBindings["openai:session_hash_first_output_stall"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestBuildOpenAIAccountLoadPlanKeepsLeastStalledAccountsWhenWholePoolStalls(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 2
	stats := newOpenAIAccountRuntimeStats()
	first := 45_000
	second := 50_000
	third := 60_000
	stats.reportForModelClass(3001, "gpt-6-sol", "", false, &first)
	stats.reportForModelClass(3002, "gpt-6-sol", "", false, &second)
	stats.reportForModelClass(3003, "gpt-6-sol", "", false, &third)
	scheduler := &defaultOpenAIAccountScheduler{
		service: &OpenAIGatewayService{cfg: cfg},
		stats:   stats,
	}

	plan := scheduler.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-6-sol",
	}, []*Account{{ID: 3001}, {ID: 3002}, {ID: 3003}}, map[int64]*AccountLoadInfo{})

	require.GreaterOrEqual(t, len(plan.selectionOrder), 2)
	require.Equal(t, int64(3001), plan.selectionOrder[0].account.ID)
	require.Equal(t, int64(3002), plan.selectionOrder[1].account.ID)
}

func TestBuildOpenAIAccountLoadPlanMovesHighErrorRateAccountsToOverflow(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 2
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.ErrorRate = 8
	stats := newOpenAIAccountRuntimeStats()
	good := true
	bad := false
	stats.reportForModelClass(2001, "gpt-5.6-sol", "", good, nil)
	stats.reportForModelClass(2002, "gpt-5.6-sol", "", good, nil)
	for i := 0; i < 4; i++ {
		stats.reportForModelClass(2003, "gpt-5.6-sol", "", bad, nil)
	}
	scheduler := &defaultOpenAIAccountScheduler{
		service: &OpenAIGatewayService{cfg: cfg},
		stats:   stats,
	}
	accounts := []*Account{{ID: 2001}, {ID: 2002}, {ID: 2003}}

	plan := scheduler.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-5.6-sol",
	}, accounts, map[int64]*AccountLoadInfo{})

	require.True(t, plan.includeErrorRateOverflowFallback)
	require.Len(t, plan.selectionOrder, 3)
	require.Equal(t, int64(2003), plan.selectionOrder[2].account.ID)
}
