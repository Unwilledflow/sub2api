package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdaptivePlanKeepsDegradedAsFailoverTail(t *testing.T) {
	healthy := []AdaptiveRouteCandidate{
		{LeafGroupID: 85, FrozenRateMultiplier: 0.06, HealthKnown: true, Healthy: true, MemberSortOrder: 10},
	}
	degraded := []AdaptiveRouteCandidate{
		{LeafGroupID: 17, FrozenRateMultiplier: 0.085, HealthKnown: true, Healthy: false, MemberSortOrder: 20},
		{LeafGroupID: 14, FrozenRateMultiplier: 0.11, HealthKnown: true, Healthy: false, MemberSortOrder: 30},
	}
	sortAdaptiveRouteCandidates(healthy, AdaptiveRouteModePrice, false)
	sortAdaptiveRouteCandidates(degraded, AdaptiveRouteModePrice, false)
	merged := append(append([]AdaptiveRouteCandidate{}, healthy...), degraded...)

	require.Len(t, merged, 3)
	require.Equal(t, int64(85), merged[0].LeafGroupID)
	require.Equal(t, int64(17), merged[1].LeafGroupID)
	require.Equal(t, int64(14), merged[2].LeafGroupID)
}

func TestSortAdaptiveRouteCandidatesPricePrecedesMapping(t *testing.T) {
	cands := []AdaptiveRouteCandidate{
		{LeafGroupID: 17, FrozenRateMultiplier: 0.05, MappedAccountCount: 0, MemberSortOrder: 1},
		{LeafGroupID: 85, FrozenRateMultiplier: 0.08, MappedAccountCount: 3, MemberSortOrder: 2},
	}
	sortAdaptiveRouteCandidates(cands, AdaptiveRouteModePrice, false)
	require.Equal(t, int64(17), cands[0].LeafGroupID)
	require.Equal(t, int64(85), cands[1].LeafGroupID)
}

func TestSortAdaptiveRouteCandidatesMappingBreaksEqualPriceTie(t *testing.T) {
	cands := []AdaptiveRouteCandidate{
		{LeafGroupID: 17, FrozenRateMultiplier: 0.05, MappedAccountCount: 0, MemberSortOrder: 1},
		{LeafGroupID: 85, FrozenRateMultiplier: 0.05, MappedAccountCount: 3, MemberSortOrder: 2},
	}
	sortAdaptiveRouteCandidates(cands, AdaptiveRouteModePrice, false)
	require.Equal(t, int64(85), cands[0].LeafGroupID)
}

func TestAdaptiveMessagesPlanDropsOpenAILeafWhenAnthropicCanServe(t *testing.T) {
	cheapOpenAI := testAdaptiveLeafGroup(17, PlatformOpenAI, true, false)
	cheapOpenAI.RateMultiplier = 0.05
	native := testAdaptiveLeafGroup(118, PlatformAnthropic, true, false)
	native.RateMultiplier = 1
	openaiAccount := testAdaptiveLeafAccount(201, PlatformOpenAI)
	openaiAccount.Type = AccountTypeOAuth
	openaiAccount.Credentials = map[string]any{}
	anthropicAccount := testAdaptiveLeafAccount(202, PlatformAnthropic)

	planner := NewAdaptiveRoutePlanner(
		stubAdaptivePoolRepo{snapshot: &AdaptivePoolSnapshot{
			ParentGroupID: 108, Platform: PlatformAdaptive, Enabled: true, ConfigGeneration: 1,
			Members: []AdaptiveLeafRef{
				{LeafGroupID: 17, Enabled: true, SortOrder: 1},
				{LeafGroupID: 118, Enabled: true, SortOrder: 2},
			},
		}},
		stubAdaptiveAccountRepo{byGroup: map[int64][]Account{17: {openaiAccount}, 118: {anthropicAccount}}},
		stubAdaptiveGroupRepo{groups: map[int64]*Group{17: cheapOpenAI, 118: native}},
		stubAdaptiveChannelRepo{},
		nil,
	)

	plan, err := planner.Plan(context.Background(), AdaptiveRouteRequest{
		ParentGroupID: 108, Platform: PlatformAnthropic, RequestedModel: "claude-sonnet-4",
		Mode: AdaptiveRouteModePrice, Protocol: AdaptiveRouteProtocolAnthropicMessages,
	})
	require.NoError(t, err)
	cands := plan.Candidates()
	require.Len(t, cands, 1)
	require.Equal(t, int64(118), cands[0].LeafGroupID)
	require.Equal(t, PlatformAnthropic, cands[0].Platform)
}

func TestAdaptiveRouteNativeLeafRejectsResponsesOnlyRequirements(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Status: "active", Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-test": "claude-test"}},
	}
	group := &Group{ID: 35, Platform: PlatformAnthropic}
	for _, req := range []AdaptiveRouteRequest{
		{RequireCompact: true},
		{RequiredOpenAICapability: OpenAIEndpointCapabilityResponses},
	} {
		require.False(t, adaptiveRouteAccountCompatible(context.Background(), account, group, PlatformAnthropic, "claude-test", req))
	}
	require.True(t, adaptiveRouteAccountCompatible(context.Background(), account, group, PlatformAnthropic, "claude-test", AdaptiveRouteRequest{}))
}

func TestAntiStallShouldFailHardEmptyReserveNoMoreSwitches(t *testing.T) {
	s := NewAntiStallSession(AntiStallProSettings{
		Enabled: true, BufferTokens: 8, UpstreamMaxRetry: 1,
		LowBufferTokens: 0, MaxLeafSwitches: 1, MaxDripSeconds: 30,
		DripTokensPerSecond: 1,
	})
	s.RecordLeafSwitch()
	s.BeginRecovery()
	require.True(t, s.ShouldFailHard())
}
