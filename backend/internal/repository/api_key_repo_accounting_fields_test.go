package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestAPIKeyUpdateSQLExcludesAccountingFields(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAPIKeyRepositoryWithSQL(client, db)

	now := time.Now().UTC()
	mock.ExpectExec("api key metadata update").WillReturnResult(sqlmock.NewResult(0, 1))
	err = repo.Update(context.Background(), &service.APIKey{
		ID:              42,
		Name:            "metadata-only",
		Status:          service.StatusAPIKeyActive,
		Quota:           100,
		QuotaUsed:       91,
		RateLimit5h:     10,
		RateLimit1d:     20,
		RateLimit7d:     30,
		Usage5h:         11,
		Usage1d:         21,
		Usage7d:         31,
		Window5hStart:   &now,
		Window1dStart:   &now,
		Window7dStart:   &now,
		BlockOpenAIFast: true,
	}, service.APIKeyUpdateFields{
		Name:            true,
		Status:          true,
		Quota:           true,
		RateLimits:      true,
		BlockOpenAIFast: true,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	normalized := normalizeSQLWhitespace(capturedSQL)
	for _, accountingColumn := range []string{
		"quota_used",
		"usage_5h",
		"usage_1d",
		"usage_7d",
		"window_5h_start",
		"window_1d_start",
		"window_7d_start",
	} {
		require.False(t, strings.Contains(normalized, `"`+accountingColumn+`"`),
			"generic update must not write accounting column %s: %s", accountingColumn, normalized)
	}
	require.Contains(t, normalized, `"name"`)
	require.Contains(t, normalized, `"quota"`)
	require.Contains(t, normalized, `"rate_limit_5h"`)
}
