package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Simulates the merge logic used after Plan() healthy/degraded split:
// degraded leaves must remain as failover tail when healthy leaves exist.
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
	// Degraded still present for group failover on 503
	require.Equal(t, int64(17), merged[1].LeafGroupID)
	require.Equal(t, int64(14), merged[2].LeafGroupID)
}

func TestAntiStallShouldFailHardEmptyReserveNoMoreSwitches(t *testing.T) {
	s := NewAntiStallSession(AntiStallProSettings{
		Enabled: true, BufferTokens: 8, UpstreamMaxRetry: 1,
		LowBufferTokens: 0, MaxLeafSwitches: 0, MaxDripSeconds: 30,
		DripTokensPerSecond: 1,
	})
	// MaxLeafSwitches normalized to at least 1 in Normalize — set after
	s.cfg.MaxLeafSwitches = 0 // no switches left if already at cap
	// Simulate: already used max switches by setting leafSwitches high
	s = NewAntiStallSession(AntiStallProSettings{
		Enabled: true, BufferTokens: 8, UpstreamMaxRetry: 1,
		LowBufferTokens: 0, MaxLeafSwitches: 1, MaxDripSeconds: 30,
		DripTokensPerSecond: 1,
	})
	s.RecordLeafSwitch() // leafSwitches=1 == max
	s.BeginRecovery()
	// empty reserve + fails → fail hard so client is not stuck dripping nothing
	require.True(t, s.ShouldFailHard())
}
