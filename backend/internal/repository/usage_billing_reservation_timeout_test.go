package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestBeginUsageReservationTxSetsBoundedDatabaseTimeouts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(usageReservationSetLockTimeoutSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(usageReservationSetStatementTimeoutSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	tx, err := beginUsageReservationTx(context.Background(), db)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBeginUsageReservationTxRollsBackWhenTimeoutSetupFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	setupErr := errors.New("timeout setup failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(usageReservationSetLockTimeoutSQL)).WillReturnError(setupErr)
	mock.ExpectRollback()

	tx, err := beginUsageReservationTx(context.Background(), db)
	require.Nil(t, tx)
	require.ErrorIs(t, err, setupErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
