package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepositoryAdaptiveSearchHydratesInOneBatch(t *testing.T) {
	repo, mock := adaptiveMockRepository(t)
	mock.ExpectQuery(`SELECT .* FROM "api_keys"`).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "name"}).AddRow(2, 1, "first").AddRow(3, 1, "second"))
	mock.ExpectQuery(`SELECT id, adaptive_routing_preference, adaptive_max_rate_multiplier, adaptive_leaf_group_ids`).WithArgs("{2,3}").WillReturnRows(sqlmock.NewRows([]string{"id", "pref", "rate", "leaf"}).AddRow(2, "price", 1.5, "{17,85}").AddRow(3, "intelligence", nil, nil))
	keys, err := repo.SearchAPIKeys(context.Background(), 1, "", 10)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assertAdaptiveHydrated(t, &keys[0], false)
	require.Equal(t, "intelligence", keys[1].AdaptiveRoutingPreference)
	require.Nil(t, keys[1].AdaptiveMaxRateMultiplier)
	require.Empty(t, keys[1].AdaptiveLeafGroupIDs)
}

func TestAPIKeyRepositoryAdaptiveHydrationFailureKeepsExistingReadContract(t *testing.T) {
	repo, mock := adaptiveMockRepository(t)
	expectAdaptiveKeyRows(mock)
	mock.ExpectQuery(`SELECT .* FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, adaptive_routing_preference, adaptive_max_rate_multiplier, adaptive_leaf_group_ids`).WillReturnError(errors.New("temporary read error"))
	mock.ExpectQuery(`SELECT api_key_id, group_id`).WithArgs("{2}").WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "group_id"}))
	key, err := repo.GetByID(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, key)
	require.Equal(t, service.NormalizeAnthropicCacheTTLMode("1h"), key.AnthropicCacheTTLMode)
}
