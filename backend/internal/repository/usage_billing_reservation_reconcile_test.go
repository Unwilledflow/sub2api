package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageBillingReservationReconcileExpiredAllowsExpiredOwnerTakeover(t *testing.T) {
	matcher := sqlmock.QueryMatcherFunc(func(expected string, actual string) error {
		if expected == usageReservationSetLockTimeoutSQL || expected == usageReservationSetStatementTimeoutSQL {
			if actual != expected {
				return fmt.Errorf("transaction setting mismatch: got %q, want %q", actual, expected)
			}
			return nil
		}
		if !strings.Contains(actual, "FOR UPDATE SKIP LOCKED") {
			return fmt.Errorf("reconcile query does not lock candidates")
		}
		if strings.Contains(actual, "r.owner_id = ''") {
			return fmt.Errorf("reconcile query still restricts claims to the previous owner")
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(usageReservationSetLockTimeoutSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(usageReservationSetStatementTimeoutSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("reconcile expired reservations").
		WithArgs("replacement-worker", 10, int64(30)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	repository := NewUsageBillingReservationRepository(db)
	result, err := repository.ReconcileExpired(context.Background(), &service.UsageReservationReconcileCommand{
		WorkerID: "replacement-worker",
		Limit:    10,
		ClaimTTL: 30 * time.Second,
	})

	require.NoError(t, err)
	require.Empty(t, result.Claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}
