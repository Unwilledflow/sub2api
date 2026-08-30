package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrepareUsageLogInsert_AdaptiveColumnsStayOrdered(t *testing.T) {
	t.Parallel()

	baseCost := 1.25
	managementFee := 0.1875
	totalCost := 1.4375
	uncappedBaseCost := 1.25
	platformOverageCost := 0.0
	parentGroupID := int64(101)
	routedGroupID := int64(202)
	attemptNo := 2
	pricingSnapshotID := "pricing-generation-17"
	reservationID := "8ef6ac46-74eb-40b4-9585-125b3b4ec6f6"
	evidenceHash := "1d8d2e16d66d6dd89d08f5f6438c30a04f6f5fc4e54513b5b4f41cfdb640ec81"
	settlementStatus := "pending"

	prepared := prepareUsageLogInsert(&service.UsageLog{
		AdaptiveBaseCost:            &baseCost,
		AdaptiveManagementFeeCost:   &managementFee,
		AdaptiveTotalCost:           &totalCost,
		AdaptiveUncappedBaseCost:    &uncappedBaseCost,
		AdaptivePlatformOverageCost: &platformOverageCost,
		AdaptiveParentGroupID:       &parentGroupID,
		RoutedGroupID:               &routedGroupID,
		AdaptiveAttemptNo:           &attemptNo,
		AdaptivePricingSnapshotID:   &pricingSnapshotID,
		AdaptiveReservationID:       &reservationID,
		AdaptiveEvidenceHash:        &evidenceHash,
		AdaptiveSettlementStatus:    &settlementStatus,
	})

	require.Len(t, usageLogInsertArgTypes, 69)
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	require.Equal(t, "numeric", usageLogInsertArgTypes[55])
	require.Equal(t, "numeric", usageLogInsertArgTypes[56])
	require.Equal(t, "numeric", usageLogInsertArgTypes[57])
	require.Equal(t, "numeric", usageLogInsertArgTypes[58])
	require.Equal(t, "numeric", usageLogInsertArgTypes[59])
	require.Equal(t, "bigint", usageLogInsertArgTypes[60])
	require.Equal(t, "bigint", usageLogInsertArgTypes[61])
	require.Equal(t, "integer", usageLogInsertArgTypes[62])
	require.Equal(t, "text", usageLogInsertArgTypes[63])
	require.Equal(t, "uuid", usageLogInsertArgTypes[64])
	require.Equal(t, "text", usageLogInsertArgTypes[65])
	require.Equal(t, "text", usageLogInsertArgTypes[66])

	require.Same(t, &baseCost, prepared.args[55])
	require.Same(t, &managementFee, prepared.args[56])
	require.Same(t, &totalCost, prepared.args[57])
	require.Same(t, &uncappedBaseCost, prepared.args[58])
	require.Same(t, &platformOverageCost, prepared.args[59])
	require.Equal(t, sql.NullInt64{Int64: parentGroupID, Valid: true}, prepared.args[60])
	require.Equal(t, sql.NullInt64{Int64: routedGroupID, Valid: true}, prepared.args[61])
	require.Equal(t, sql.NullInt64{Int64: int64(attemptNo), Valid: true}, prepared.args[62])
	require.Equal(t, sql.NullString{String: pricingSnapshotID, Valid: true}, prepared.args[63])
	require.Equal(t, sql.NullString{String: reservationID, Valid: true}, prepared.args[64])
	require.Equal(t, sql.NullString{String: evidenceHash, Valid: true}, prepared.args[65])
	require.Equal(t, sql.NullString{String: settlementStatus, Valid: true}, prepared.args[66])
	require.Equal(t, sql.NullString{}, prepared.args[67])
	require.Equal(t, "text", usageLogInsertArgTypes[67])
	require.Equal(t, "timestamptz", usageLogInsertArgTypes[68])
}

func TestUsageLogRepositoryCreateAdaptive_ReusesOnlyMatchingEvidence(t *testing.T) {
	t.Parallel()

	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db, db: db}
	log := adaptiveUsageEvidenceFixture()
	createdAt := time.Date(2026, 7, 22, 2, 3, 4, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text[[:space:]]+FROM usage_billing_reservations").
		WithArgs(*log.AdaptiveReservationID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*log.AdaptiveReservationID))
	mock.ExpectQuery("SELECT id, created_at, user_id, api_key_id, account_id, request_id").
		WithArgs(*log.AdaptiveReservationID).
		WillReturnRows(adaptiveUsageEvidenceRows(log, 801, createdAt, *log.AdaptiveAttemptNo))
	mock.ExpectCommit()

	inserted, err := repo.Create(context.Background(), log)

	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, int64(801), log.ID)
	require.Equal(t, createdAt, log.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryCreateAdaptive_RejectsMismatchedOrDuplicateEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rows func(*service.UsageLog, time.Time) *sqlmock.Rows
	}{
		{
			name: "attempt mismatch",
			rows: func(log *service.UsageLog, createdAt time.Time) *sqlmock.Rows {
				return adaptiveUsageEvidenceRows(log, 802, createdAt, 2)
			},
		},
		{
			name: "multiple evidence rows",
			rows: func(log *service.UsageLog, createdAt time.Time) *sqlmock.Rows {
				rows := adaptiveUsageEvidenceRows(log, 803, createdAt, *log.AdaptiveAttemptNo)
				return rows.AddRow(adaptiveUsageEvidenceRowValues(log, 804, createdAt.Add(time.Nanosecond), *log.AdaptiveAttemptNo)...)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := &usageLogRepository{sql: db, db: db}
			log := adaptiveUsageEvidenceFixture()
			createdAt := time.Date(2026, 7, 22, 2, 3, 5, 0, time.UTC)

			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id::text[[:space:]]+FROM usage_billing_reservations").
				WithArgs(*log.AdaptiveReservationID).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*log.AdaptiveReservationID))
			mock.ExpectQuery("SELECT id, created_at, user_id, api_key_id, account_id, request_id").
				WithArgs(*log.AdaptiveReservationID).
				WillReturnRows(tt.rows(log, createdAt))
			mock.ExpectRollback()

			inserted, err := repo.Create(context.Background(), log)

			require.False(t, inserted)
			require.ErrorIs(t, err, service.ErrAdaptiveUsageEvidenceConflict)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageLogRepositoryCreateAdaptive_RejectsAmbientEntTransaction(t *testing.T) {
	t.Parallel()

	repo := &usageLogRepository{}
	ctx := dbent.NewTxContext(context.Background(), &dbent.Tx{})
	inserted, err := repo.Create(ctx, adaptiveUsageEvidenceFixture())

	require.False(t, inserted)
	require.True(t, errors.Is(err, service.ErrAdaptiveUsageEvidenceTransaction))
}

func adaptiveUsageEvidenceFixture() *service.UsageLog {
	baseCost := 1.25
	managementFee := 0.1875
	totalCost := 1.4375
	uncappedBaseCost := 1.25
	platformOverageCost := 0.0
	parentGroupID := int64(101)
	routedGroupID := int64(202)
	attemptNo := 1
	pricingSnapshotID := "pricing-generation-17"
	reservationID := "8ef6ac46-74eb-40b4-9585-125b3b4ec6f6"
	evidenceHash := service.HashUsageReservationKey("adaptive-evidence")
	settlementStatus := service.AdaptiveSettlementStatusPending
	return &service.UsageLog{
		UserID:                      10,
		APIKeyID:                    20,
		AccountID:                   30,
		RequestID:                   "request-adaptive-1",
		AdaptiveBaseCost:            &baseCost,
		AdaptiveManagementFeeCost:   &managementFee,
		AdaptiveTotalCost:           &totalCost,
		AdaptiveUncappedBaseCost:    &uncappedBaseCost,
		AdaptivePlatformOverageCost: &platformOverageCost,
		AdaptiveParentGroupID:       &parentGroupID,
		RoutedGroupID:               &routedGroupID,
		AdaptiveAttemptNo:           &attemptNo,
		AdaptivePricingSnapshotID:   &pricingSnapshotID,
		AdaptiveReservationID:       &reservationID,
		AdaptiveEvidenceHash:        &evidenceHash,
		AdaptiveSettlementStatus:    &settlementStatus,
	}
}

func adaptiveUsageEvidenceRows(log *service.UsageLog, id int64, createdAt time.Time, attemptNo int) *sqlmock.Rows {
	columns := []string{
		"id", "created_at", "user_id", "api_key_id", "account_id", "request_id",
		"actual_cost", "adaptive_base_cost", "adaptive_management_fee_cost",
		"adaptive_total_cost", "adaptive_uncapped_base_cost", "adaptive_platform_overage_cost",
		"adaptive_parent_group_id", "routed_group_id", "adaptive_attempt_no",
		"adaptive_pricing_snapshot_id", "adaptive_reservation_id", "adaptive_evidence_hash",
		"adaptive_settlement_status",
	}
	return sqlmock.NewRows(columns).AddRow(adaptiveUsageEvidenceRowValues(log, id, createdAt, attemptNo)...)
}

func adaptiveUsageEvidenceRowValues(log *service.UsageLog, id int64, createdAt time.Time, attemptNo int) []driver.Value {
	return []driver.Value{
		id,
		createdAt,
		log.UserID,
		log.APIKeyID,
		log.AccountID,
		log.RequestID,
		log.ActualCost,
		*log.AdaptiveBaseCost,
		*log.AdaptiveManagementFeeCost,
		*log.AdaptiveTotalCost,
		*log.AdaptiveUncappedBaseCost,
		*log.AdaptivePlatformOverageCost,
		*log.AdaptiveParentGroupID,
		*log.RoutedGroupID,
		attemptNo,
		*log.AdaptivePricingSnapshotID,
		*log.AdaptiveReservationID,
		*log.AdaptiveEvidenceHash,
		*log.AdaptiveSettlementStatus,
	}
}
