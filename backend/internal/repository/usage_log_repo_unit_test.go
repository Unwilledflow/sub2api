//go:build unit

package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSafeDateFormat(t *testing.T) {
	tests := []struct {
		name        string
		granularity string
		expected    string
	}{
		// 合法值
		{"hour", "hour", "YYYY-MM-DD HH24:00"},
		{"day", "day", "YYYY-MM-DD"},
		{"week", "week", "IYYY-IW"},
		{"month", "month", "YYYY-MM"},

		// 非法值回退到默认
		{"空字符串", "", "YYYY-MM-DD"},
		{"未知粒度 year", "year", "YYYY-MM-DD"},
		{"未知粒度 minute", "minute", "YYYY-MM-DD"},

		// 恶意字符串
		{"SQL 注入尝试", "'; DROP TABLE users; --", "YYYY-MM-DD"},
		{"带引号", "day'", "YYYY-MM-DD"},
		{"带括号", "day)", "YYYY-MM-DD"},
		{"Unicode", "日", "YYYY-MM-DD"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := safeDateFormat(tc.granularity)
			require.Equal(t, tc.expected, got, "safeDateFormat(%q)", tc.granularity)
		})
	}
}

func TestBuildUsageLogBatchInsertQuery_UpdatesOnlyUnsettledConflicts(t *testing.T) {
	log := &service.UsageLog{
		UserID:       1,
		APIKeyID:     2,
		AccountID:    3,
		RequestID:    "req-batch-no-update",
		Model:        "gpt-5",
		InputTokens:  10,
		OutputTokens: 5,
		TotalCost:    1.2,
		ActualCost:   1.2,
		CreatedAt:    time.Now().UTC(),
	}
	prepared := prepareUsageLogInsert(log)

	query, _ := buildUsageLogBatchInsertQuery([]string{usageLogBatchKey(log.RequestID, log.APIKeyID)}, map[string]usageLogInsertPrepared{
		usageLogBatchKey(log.RequestID, log.APIKeyID): prepared,
	})

	require.Contains(t, query, "ON CONFLICT (request_id, api_key_id) DO UPDATE")
	require.Contains(t, query, "usage_logs.actual_cost = 0")
	require.Contains(t, query, "EXCLUDED.actual_cost > 0")
}

func TestPreferSettledUsageLog(t *testing.T) {
	unsettled := usageLogInsertPrepared{actualCost: 0}
	settled := usageLogInsertPrepared{actualCost: 0.5}
	repriced := usageLogInsertPrepared{actualCost: 0.9}

	require.True(t, preferSettledUsageLog(unsettled, settled))
	require.False(t, preferSettledUsageLog(settled, unsettled))
	require.False(t, preferSettledUsageLog(settled, repriced))
}

func TestBestEffortRecentKeySeparatesUnsettledAndSettledWrites(t *testing.T) {
	repo := newUsageLogRepositoryWithSQL(nil, nil)
	unsettledKey, ok := repo.bestEffortRecentKey("req-settlement", 7, 0)
	require.True(t, ok)
	settledKey, ok := repo.bestEffortRecentKey("req-settlement", 7, 0.5)
	require.True(t, ok)
	require.NotEqual(t, unsettledKey, settledKey)

	repo.bestEffortRecent.SetDefault(unsettledKey, struct{}{})
	_, settledWasSkipped := repo.bestEffortRecent.Get(settledKey)
	require.False(t, settledWasSkipped, "a failed write must not suppress its later settled retry")
}

func TestBatchInsertUsageLogsReloadsConcurrentConflictState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	const requestID = "req-concurrent-conflict"
	const apiKeyID int64 = 7
	createdAt := time.Date(2026, time.August, 5, 3, 30, 0, 0, time.UTC)
	log := &service.UsageLog{
		APIKeyID:   apiKeyID,
		RequestID:  requestID,
		ActualCost: 0.5,
		CreatedAt:  createdAt,
	}
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(requestID, apiKeyID)

	// The first statement represents a conditional conflict that waited for a
	// concurrent insert and therefore returned a null identity in its snapshot.
	mock.ExpectQuery("WITH input").WillReturnRows(sqlmock.NewRows([]string{"json_agg"}).AddRow(
		`[{"request_id":"req-concurrent-conflict","api_key_id":7,"id":null,"created_at":null,"inserted":false}]`,
	))
	mock.ExpectQuery("SELECT id, created_at").WithArgs(requestID, apiKeyID).WillReturnRows(
		sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(42), createdAt),
	)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	inserted, states, safeFallback, err := repo.batchInsertUsageLogs(
		db,
		[]string{key},
		map[string]usageLogInsertPrepared{key: prepared},
	)
	require.NoError(t, err)
	require.False(t, safeFallback)
	require.False(t, inserted[key])
	require.Equal(t, usageLogBatchState{ID: 42, CreatedAt: createdAt}, states[key])
	require.NoError(t, mock.ExpectationsWereMet())
}
