package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSnapshotRoundTripPreservesFallbackGroupIDs(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, &config.Config{})
	groupID := int64(9)
	apiKey := &APIKey{
		ID:               1,
		UserID:           2,
		GroupID:          &groupID,
		Key:              "k-fallback-roundtrip",
		Name:             "Fallback Key",
		Status:           StatusActive,
		FallbackGroupIDs: []int64{11, 12},
		BlockOpenAIFast:  true,
		User: &User{
			ID:          2,
			Status:      StatusActive,
			Role:        RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &Group{
			ID:               groupID,
			Name:             "openai",
			Platform:         PlatformOpenAI,
			Status:           StatusActive,
			SubscriptionType: SubscriptionTypeStandard,
			RateMultiplier:   1,
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.snapshotToAPIKey(apiKey.Key, snapshot)

	require.Equal(t, []int64{11, 12}, snapshot.FallbackGroupIDs)
	require.Equal(t, []int64{11, 12}, roundTrip.FallbackGroupIDs)
	require.True(t, snapshot.BlockOpenAIFast)
	require.True(t, roundTrip.BlockOpenAIFast)
}
