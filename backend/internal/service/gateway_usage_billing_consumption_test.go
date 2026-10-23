//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGatewayUsageBillingConsumesFinalEffortAndCacheTTL(t *testing.T) {
	for _, gateway := range []string{"gateway", "openai"} {
		for _, effort := range []string{"max", "high"} {
			t.Run(gateway+"/"+effort, func(t *testing.T) {
				const model = "claude-fable-5-1"
				const groupID int64 = 100
				pricing := ChannelModelPricing{Platform: PlatformAnthropic, Models: []string{model}, BillingMode: BillingModeToken,
					InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(4e-6),
					CacheWritePrice: testPtrFloat64(3e-6), CacheWrite1hPrice: testPtrFloat64(9e-6),
					ReasoningEffortMultipliers: map[string]float64{"max": 1.5},
				}
				channel := &Channel{ID: 1, Status: StatusActive, ModelPricing: []ChannelModelPricing{pricing},
					AccountStatsPricingRules: []AccountStatsPricingRule{{GroupIDs: []int64{groupID}, Pricing: []ChannelModelPricing{pricing}}},
				}
				channel.GroupIDs = []int64{groupID}
				cs := NewChannelService(&mockChannelRepository{
					listAllFn: func(context.Context) ([]Channel, error) { return []Channel{*channel}, nil },
					getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
						return map[int64]string{groupID: PlatformAnthropic}, nil
					},
				}, nil, nil, nil)
				logs := &openAIRecordUsageLogRepoStub{inserted: true}
				users := &openAIRecordUsageUserRepoStub{}
				key := &APIKey{ID: 20, GroupID: i64p(groupID), Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 0.8}}
				at := time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC)
				base := 1000*2e-6 + 200*4e-6
				if gateway == "gateway" {
					svc := newGatewayRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{})
					svc.channelService = cs
					svc.resolver = NewModelPricingResolver(cs, svc.billingService)
					base += 300*3e-6 + 700*9e-6
					require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
						Result: &ForwardResult{RequestID: "consume-" + gateway + effort, Model: model, UpstreamModel: model, ReasoningEffort: &effort,
							Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 200, CacheCreationInputTokens: 1000, CacheCreation5mTokens: 300, CacheCreation1hTokens: 700}},
						APIKey: key, User: &User{ID: 10}, Account: &Account{ID: 30, Platform: PlatformAnthropic}, PricingAt: at,
					}))
					require.Equal(t, 300, logs.lastLog.CacheCreation5mTokens)
					require.Equal(t, 700, logs.lastLog.CacheCreation1hTokens)
				} else {
					svc := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
					svc.channelService = cs
					svc.resolver = NewModelPricingResolver(cs, svc.billingService)
					require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: &OpenAIForwardResult{RequestID: "consume-" + gateway + effort, Model: model, UpstreamModel: model, ReasoningEffort: &effort,
							Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 200}},
						APIKey: key, User: &User{ID: 10}, Account: &Account{ID: 30, Platform: PlatformOpenAI}, PricingAt: at,
					}))
				}
				if effort == "max" {
					base *= 1.5
				}
				require.NotNil(t, logs.lastLog)
				require.Equal(t, effort, *logs.lastLog.ReasoningEffort)
				require.InDelta(t, base, logs.lastLog.TotalCost, 1e-12)
				require.InDelta(t, base*0.8, logs.lastLog.ActualCost, 1e-12)
				require.Equal(t, 1, users.deductCalls)
				require.InDelta(t, base*0.8, users.lastAmount, 1e-12)
				require.NotNil(t, logs.lastLog.AccountStatsCost)
				require.InDelta(t, base, *logs.lastLog.AccountStatsCost, 1e-12)
			})
		}
	}
}

func TestOpenAIUsageBillingWithoutResolverConsumesRequestPricingAt(t *testing.T) {
	for _, slot := range []struct {
		name       string
		at         time.Time
		multiplier float64
	}{
		{"peak", time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC), 2},
		{"off_peak", time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC), 1},
	} {
		t.Run(slot.name, func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			users := &openAIRecordUsageUserRepoStub{}
			svc := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
			svc.billingService = NewBillingService(&config.Config{}, nil)
			svc.resolver = nil
			const groupID int64 = 100
			svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, groupID, PlatformDeepseek)
			require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "no-resolver-" + slot.name, Model: "deepseek-v4-flash", UpstreamModel: "deepseek-v4-flash", Usage: OpenAIUsage{InputTokens: 2000, OutputTokens: 500, CacheReadInputTokens: 1000}},
				APIKey: &APIKey{ID: 20, GroupID: i64p(groupID), Group: &Group{ID: groupID, Platform: PlatformDeepseek, RateMultiplier: 0.8}},
				User:   &User{ID: 10}, Account: &Account{ID: 30, Platform: PlatformDeepseek}, PricingAt: slot.at,
			}))
			want := (1000*2.2e-7 + 500*6.6e-7 + 1000*7e-9) * slot.multiplier
			require.NotNil(t, logs.lastLog)
			require.False(t, logs.lastLog.CreatedAt.Equal(slot.at))
			require.InDelta(t, want, logs.lastLog.TotalCost, 1e-12)
			require.InDelta(t, want*0.8, users.lastAmount, 1e-12)
			require.NotNil(t, logs.lastLog.AccountStatsCost)
			require.InDelta(t, want, *logs.lastLog.AccountStatsCost, 1e-12)
		})
	}
}
