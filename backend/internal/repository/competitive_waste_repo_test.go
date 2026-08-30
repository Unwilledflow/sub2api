package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryCreatesCompetitiveWasteLog(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	cost := 0.0042
	createdAt := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	mock.ExpectExec("INSERT INTO competitive_waste_logs").
		WithArgs(
			"winner-request", "loser-request", int64(601), int64(501), int64(97),
			int64(701), int64(702), 2, "claude-sonnet-4", "claude-sonnet-4-20250514",
			80, 2, 40, 5, 40, 0, 0, true, cost,
			string(service.CompetitiveWasteReasonCanceledLoser), int64(250), createdAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateCompetitiveWasteLogs(context.Background(), []service.CompetitiveWasteLog{
		{
			LogicalRequestID:      "winner-request",
			UpstreamRequestID:     "loser-request",
			UserID:                601,
			APIKeyID:              501,
			GroupID:               97,
			WinnerAccountID:       701,
			AccountID:             702,
			AttemptNo:             2,
			Model:                 "claude-sonnet-4",
			UpstreamModel:         "claude-sonnet-4-20250514",
			InputTokens:           80,
			OutputTokens:          2,
			CacheCreationTokens:   40,
			CacheReadTokens:       5,
			CacheCreation5mTokens: 40,
			UsageReported:         true,
			CompetitiveWasteCost:  &cost,
			Reason:                service.CompetitiveWasteReasonCanceledLoser,
			DurationMs:            250,
			CreatedAt:             createdAt,
		},
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompetitiveWasteMigrationDefinesInternalCostTable(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "199_competitive_waste_logs.sql"))
	require.NoError(t, err)
	sql := string(content)
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS competitive_waste_logs")
	require.Contains(t, sql, "competitive_waste_cost NUMERIC(20,10)")
	require.Contains(t, sql, "UNIQUE (logical_request_id, api_key_id, attempt_no)")
}
