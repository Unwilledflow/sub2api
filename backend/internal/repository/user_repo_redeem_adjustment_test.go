package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newRedeemAdjustmentRepoMock(t *testing.T) (*userRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	return newUserRepositoryWithSQL(client, db), mock
}

func TestApplyRedeemBalanceAdjustment_UsesAtomicFloor(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectExec(`UPDATE users SET balance = GREATEST\(balance \+ \$1, 0\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(-7.0, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.ApplyRedeemBalanceAdjustment(context.Background(), 42, -7))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyRedeemConcurrencyAdjustment_UsesAtomicFloor(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectExec(`UPDATE users SET concurrency = GREATEST\(concurrency \+ \$1, 0\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(-7, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.ApplyRedeemConcurrencyAdjustment(context.Background(), 42, -7))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyRedeemAdjustment_MissingUser(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectExec(`UPDATE users SET balance = GREATEST\(balance \+ \$1, 0\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(-1.0, int64(404)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.ApplyRedeemBalanceAdjustment(context.Background(), 404, -1)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetBalanceByID_UsesBalanceOnlyAbsoluteUpdate(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectQuery(`(?s)UPDATE users\s+SET balance = \$1, updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING balance`).
		WithArgs(50.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(50.0))

	balance, err := repo.SetBalanceByID(context.Background(), 42, 50)
	require.NoError(t, err)
	require.InDelta(t, 50, balance, 1e-6)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAdjustBalanceByID_UsesAtomicNonNegativeGuard(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectQuery(`(?s)UPDATE users\s+SET balance = balance \+ \$1, updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance \+ \$1 >= 0\s+RETURNING balance`).
		WithArgs(-7.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(3.0))

	balance, err := repo.AdjustBalanceByID(context.Background(), 42, -7)
	require.NoError(t, err)
	require.InDelta(t, 3, balance, 1e-6)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAdjustBalanceByID_RejectsConcurrentInsufficientBalance(t *testing.T) {
	repo, mock := newRedeemAdjustmentRepoMock(t)
	mock.ExpectQuery(`(?s)UPDATE users\s+SET balance = balance \+ \$1, updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance \+ \$1 >= 0\s+RETURNING balance`).
		WithArgs(-7.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}))
	mock.ExpectQuery(`(?s)SELECT balance\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(5.0))

	_, err := repo.AdjustBalanceByID(context.Background(), 42, -7)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}
