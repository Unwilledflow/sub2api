package repository

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func adaptiveMockRepository(t *testing.T) (*apiKeyRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return &apiKeyRepository{client: client, sql: db}, mock
}

func expectAdaptiveKeyRows(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT .* FROM "api_keys"`).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "key", "name", "status", "anthropic_cache_ttl_mode"}).AddRow(2, 1, "sk-adaptive", "adaptive", "active", "1h"))
}

func expectAdaptiveHydration(mock sqlmock.Sqlmock, legacy bool) {
	if legacy {
		mock.ExpectQuery(`SELECT id, adaptive_routing_preference, adaptive_max_rate_multiplier, adaptive_leaf_group_ids`).WithArgs("{2}").WillReturnError(&pq.Error{Code: "42703"})
		mock.ExpectQuery(`SELECT id, adaptive_routing_preference, adaptive_max_rate_multiplier\s+FROM api_keys`).WithArgs("{2}").WillReturnRows(sqlmock.NewRows([]string{"id", "preference", "rate"}).AddRow(2, "price", 1.5))
	} else {
		mock.ExpectQuery(`SELECT id, adaptive_routing_preference, adaptive_max_rate_multiplier, adaptive_leaf_group_ids`).WithArgs("{2}").WillReturnRows(sqlmock.NewRows([]string{"id", "preference", "rate", "leaves"}).AddRow(2, "price", 1.5, "{85,17,85,0}"))
	}
}

func assertAdaptiveHydrated(t *testing.T, key *service.APIKey, legacy bool) {
	t.Helper()
	require.Equal(t, "price", key.AdaptiveRoutingPreference)
	require.NotNil(t, key.AdaptiveMaxRateMultiplier)
	require.Equal(t, 1.5, *key.AdaptiveMaxRateMultiplier)
	if legacy {
		require.Empty(t, key.AdaptiveLeafGroupIDs)
	} else {
		require.ElementsMatch(t, []int64{17, 85}, key.AdaptiveLeafGroupIDs)
	}
}

func TestAPIKeyRepositoryAdaptiveGetEntryPoints(t *testing.T) {
	for _, mode := range []string{"id", "key", "auth", "legacy-auth"} {
		t.Run(mode, func(t *testing.T) {
			repo, mock := adaptiveMockRepository(t)
			expectAdaptiveKeyRows(mock)
			mock.ExpectQuery(`SELECT .* FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			if mode != "id" {
				mock.ExpectQuery(`SELECT .* FROM "groups" JOIN "user_allowed_groups"`).WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id"}))
			}
			expectAdaptiveHydration(mock, mode == "legacy-auth")
			mock.ExpectQuery(`SELECT api_key_id, group_id`).WithArgs("{2}").WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "group_id"}).AddRow(2, 33))
			var key *service.APIKey
			var err error
			switch mode {
			case "id":
				key, err = repo.GetByID(context.Background(), 2)
			case "key":
				key, err = repo.GetByKey(context.Background(), "sk-adaptive")
			default:
				key, err = repo.GetByKeyForAuth(context.Background(), "sk-adaptive")
			}
			require.NoError(t, err)
			assertAdaptiveHydrated(t, key, mode == "legacy-auth")
			require.Equal(t, []int64{33}, key.RouteGroupIDs)
			require.Equal(t, "1h", key.AnthropicCacheTTLMode)
		})
	}
}

func TestAPIKeyRepositoryAdaptiveListEntryPoints(t *testing.T) {
	for _, mode := range []string{"user", "all-user", "group", "search"} {
		t.Run(mode, func(t *testing.T) {
			repo, mock := adaptiveMockRepository(t)
			if mode == "user" || mode == "group" {
				mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			}
			expectAdaptiveKeyRows(mock)
			if mode == "group" {
				mock.ExpectQuery(`SELECT .* FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			}
			expectAdaptiveHydration(mock, false)
			if mode == "user" || mode == "all-user" {
				mock.ExpectQuery(`SELECT api_key_id, group_id`).WithArgs("{2}").WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "group_id"}))
				mock.ExpectQuery(`SELECT requested.api_key_id, latest.ip_address`).WithArgs("{2}").WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "ip_address"}))
			}
			var keys []service.APIKey
			var err error
			params := pagination.PaginationParams{Page: 1, PageSize: 20}
			switch mode {
			case "user":
				keys, _, err = repo.ListByUserID(context.Background(), 1, params, service.APIKeyListFilters{})
			case "all-user":
				keys, err = repo.ListAllByUserID(context.Background(), 1, service.APIKeyListFilters{})
			case "group":
				keys, _, err = repo.ListByGroupID(context.Background(), 33, params)
			case "search":
				keys, err = repo.SearchAPIKeys(context.Background(), 1, "adaptive", 10)
			}
			require.NoError(t, err)
			require.Len(t, keys, 1)
			assertAdaptiveHydrated(t, &keys[0], false)
		})
	}
}

func TestAPIKeyRepositoryAdaptiveWrites(t *testing.T) {
	for _, mode := range []string{"create", "failed-create", "update", "adaptive-only-update", "clear-update", "legacy-update", "failed-update", "unrelated-update", "missing-update"} {
		t.Run(mode, func(t *testing.T) {
			repo, mock := adaptiveMockRepository(t)
			maxRate := 1.5
			key := &service.APIKey{ID: 2, UserID: 1, Key: "sk-adaptive", Name: "adaptive", Status: "active", AdaptiveRoutingPreference: " price ", AdaptiveMaxRateMultiplier: &maxRate, AdaptiveLeafGroupIDs: []int64{85, 17, 85, 0}}
			if mode == "create" || mode == "failed-create" {
				mock.ExpectQuery(`INSERT INTO "api_keys"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
			} else {
				affected := int64(1)
				if mode == "missing-update" {
					affected = 0
				}
				mock.ExpectExec(`UPDATE "api_keys"`).WillReturnResult(sqlmock.NewResult(0, affected))
			}
			if mode == "clear-update" {
				key.AdaptiveMaxRateMultiplier = nil
				key.AdaptiveLeafGroupIDs = nil
			}
			failure := errors.New("adaptive write failed")
			if mode != "unrelated-update" && mode != "missing-update" {
				exp := mock.ExpectExec(`UPDATE api_keys\s+SET adaptive_routing_preference`)
				if mode == "clear-update" {
					exp.WithArgs(int64(2), "price", nil, nil)
				} else {
					exp.WithArgs(int64(2), "price", 1.5, "{17,85}")
				}
				switch mode {
				case "legacy-update":
					exp.WillReturnError(&pq.Error{Code: "42703"})
					mock.ExpectExec(`UPDATE api_keys\s+SET adaptive_routing_preference`).WithArgs(int64(2), "price", 1.5).WillReturnResult(sqlmock.NewResult(0, 1))
				case "failed-update", "failed-create":
					exp.WillReturnError(failure)
				default:
					exp.WillReturnResult(sqlmock.NewResult(0, 1))
				}
			}
			var err error
			if mode == "create" || mode == "failed-create" {
				err = repo.Create(context.Background(), key)
			} else {
				err = repo.Update(context.Background(), key, service.APIKeyUpdateFields{Name: mode != "adaptive-only-update", AdaptiveRouting: mode != "unrelated-update"})
			}
			switch mode {
			case "failed-update", "failed-create":
				require.ErrorIs(t, err, failure)
			case "missing-update":
				require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
			default:
				require.NoError(t, err)
			}
		})
	}
}
