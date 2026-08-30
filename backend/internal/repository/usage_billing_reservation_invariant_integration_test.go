//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const adaptiveReservationTestModel = "adaptive-test-model"

type adaptiveReservationFixture struct {
	user      *service.User
	apiKey    *service.APIKey
	leafGroup *service.Group
}

func newAdaptiveReservationFixture(t *testing.T, balance, apiKeyQuota string) adaptiveReservationFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("adaptive-reservation-%s@example.com", uuid.NewString()),
		PasswordHash: "hash",
	})
	leafGroup := mustCreateGroup(t, client, &service.Group{
		Name:             "adaptive-leaf-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeStandard,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &leafGroup.ID,
		Key:     "sk-adaptive-reservation-" + uuid.NewString(),
		Name:    "adaptive reservation",
	})

	_, err := integrationDB.ExecContext(ctx, `
		UPDATE users
		SET balance = $1, adaptive_reserved_balance = 0
		WHERE id = $2
	`, decimal.RequireFromString(balance), user.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE api_keys
		SET quota = $1,
			quota_used = 0,
			reserved_quota_usd = 0,
			usage_5h = 0,
			usage_1d = 0,
			usage_7d = 0,
			reserved_usage_5h_usd = 0,
			reserved_usage_1d_usd = 0,
			reserved_usage_7d_usd = 0
		WHERE id = $2
	`, decimal.RequireFromString(apiKeyQuota), apiKey.ID)
	require.NoError(t, err)

	return adaptiveReservationFixture{user: user, apiKey: apiKey, leafGroup: leafGroup}
}

func adaptiveBalanceReserveCommand(fixture adaptiveReservationFixture, owner, idempotencyKey, estimatedBase string) *service.UsageReservationReserveCommand {
	parentGroupID := fixture.leafGroup.ID
	return &service.UsageReservationReserveCommand{
		IdempotencyKey:    idempotencyKey,
		LogicalRequestID:  "logical-" + idempotencyKey,
		OwnerID:           owner,
		UserID:            fixture.user.ID,
		APIKeyID:          fixture.apiKey.ID,
		ParentGroupID:     &parentGroupID,
		CanonicalModel:    adaptiveReservationTestModel,
		PricingSnapshotID: "pricing-v1",
		PricingGeneration: 1,
		ConfigGeneration:  1,
		FundingSource:     service.UsageReservationFundingBalance,
		EstimatedBaseCost: decimal.RequireFromString(estimatedBase),
		ManagementFeeBPS:  service.DefaultAdaptiveManagementFeeBPS,
		LeaseExpiresAt:    time.Now().UTC().Add(5 * time.Minute),
	}
}

func setAdaptiveAPIKeyRateLimits(t *testing.T, apiKeyID int64, limit string) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `
		UPDATE api_keys
		SET rate_limit_5h = $1, rate_limit_1d = $1, rate_limit_7d = $1
		WHERE id = $2
	`, decimal.RequireFromString(limit), apiKeyID)
	require.NoError(t, err)
}

func adaptiveCaptureCommand(
	t *testing.T,
	reservation *service.UsageBillingReservation,
	owner, operationKey, actualBase string,
	leafGroupID int64,
) *service.UsageReservationCaptureCommand {
	t.Helper()
	require.NotNil(t, reservation.ActiveAttemptNo)
	baseAmount := decimal.RequireFromString(actualBase)
	fee, err := service.CalculateAdaptiveManagementFeeDecimalWithBPS(baseAmount, reservation.ManagementFeeBPS)
	require.NoError(t, err)
	evidenceHash := service.HashUsageReservationKey("usage:" + operationKey)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: "adaptive-evidence-account-" + uuid.NewString(),
	})

	var usageLogID int64
	var usageLogCreatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model, group_id,
			total_cost, actual_cost,
			adaptive_base_cost, adaptive_management_fee_cost, adaptive_total_cost,
			routed_group_id, adaptive_attempt_no, adaptive_pricing_snapshot_id,
			adaptive_reservation_id, adaptive_evidence_hash, adaptive_settlement_status
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, 0,
			$8, $9, $7,
			$6, $10, $11,
			$12, $13, 'pending'
		)
		RETURNING id, created_at
	`, reservation.UserID, reservation.APIKeyID, account.ID, "adaptive-evidence-"+uuid.NewString(),
		reservation.CanonicalModel, leafGroupID, fee.CaptureAmount, fee.BaseAmount, fee.FeeAmount,
		*reservation.ActiveAttemptNo, reservation.PricingSnapshotID, reservation.ID, evidenceHash,
	).Scan(&usageLogID, &usageLogCreatedAt))

	return &service.UsageReservationCaptureCommand{
		ReservationID:      reservation.ID,
		OperationKey:       operationKey,
		OwnerID:            owner,
		FencingToken:       reservation.FencingToken,
		RowVersion:         reservation.RowVersion,
		ActualBaseCost:     baseAmount,
		WinningLeafGroupID: leafGroupID,
		AttemptNo:          *reservation.ActiveAttemptNo,
		UsageLogID:         usageLogID,
		UsageLogCreatedAt:  usageLogCreatedAt,
		EvidenceHash:       evidenceHash,
	}
}

func adaptiveMarkInFlight(
	t *testing.T,
	ctx context.Context,
	repo service.UsageBillingReservationRepository,
	reservation *service.UsageBillingReservation,
	owner string,
	attemptNo int,
	leafGroupID int64,
) *service.UsageBillingReservation {
	t.Helper()
	operationKey := fmt.Sprintf("attempt-start-%d-%s", attemptNo, uuid.NewString())
	result, err := repo.MarkInFlight(ctx, &service.UsageReservationMarkInFlightCommand{
		ReservationID: reservation.ID,
		OperationKey:  operationKey,
		OwnerID:       owner,
		FencingToken:  reservation.FencingToken,
		RowVersion:    reservation.RowVersion,
		AttemptNo:     attemptNo,
		LeafGroupID:   leafGroupID,
		EvidenceHash:  service.HashUsageReservationKey("start:" + operationKey),
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, service.UsageReservationStatusInFlight, result.Reservation.Status)
	return result.Reservation
}

func adaptiveMarkAttemptFailed(
	t *testing.T,
	ctx context.Context,
	repo service.UsageBillingReservationRepository,
	reservation *service.UsageBillingReservation,
	owner string,
	attemptNo int,
) *service.UsageBillingReservation {
	t.Helper()
	operationKey := fmt.Sprintf("attempt-failed-%d-%s", attemptNo, uuid.NewString())
	result, err := repo.MarkAttemptFailed(ctx, &service.UsageReservationAttemptFailedCommand{
		ReservationID: reservation.ID,
		OperationKey:  operationKey,
		OwnerID:       owner,
		FencingToken:  reservation.FencingToken,
		RowVersion:    reservation.RowVersion,
		AttemptNo:     attemptNo,
		EvidenceHash:  adaptiveAttemptFailureEvidence(reservation.ID, attemptNo),
		FailureClass:  "precommit_upstream",
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, service.UsageReservationStatusAuthorized, result.Reservation.Status)
	return result.Reservation
}

func adaptiveReleaseCommand(
	reservation *service.UsageBillingReservation,
	owner, operationKey string,
	failedAttemptNo ...int,
) *service.UsageReservationReleaseCommand {
	command := &service.UsageReservationReleaseCommand{
		ReservationID: reservation.ID,
		OperationKey:  operationKey,
		OwnerID:       owner,
		FencingToken:  reservation.FencingToken,
		RowVersion:    reservation.RowVersion,
		Reason:        "all_upstream_attempts_failed",
	}
	if len(failedAttemptNo) > 0 {
		command.EvidenceHash = adaptiveAttemptFailureEvidence(reservation.ID, failedAttemptNo[0])
	}
	return command
}

func adaptiveAttemptFailureEvidence(reservationID string, attemptNo int) string {
	return service.HashUsageReservationKey(fmt.Sprintf("failure:%s:%d", reservationID, attemptNo))
}

func requireAdaptiveMoney(t *testing.T, got *decimal.Decimal, want string) {
	t.Helper()
	require.NotNil(t, got)
	expected := decimal.RequireFromString(want)
	require.Truef(t, got.Equal(expected), "got %s, want %s", got.StringFixed(10), expected.StringFixed(10))
}

func queryAdaptiveWallet(t *testing.T, userID int64) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	var balance, reserved decimal.Decimal
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT balance, adaptive_reserved_balance
		FROM users
		WHERE id = $1
	`, userID).Scan(&balance, &reserved))
	return balance, reserved
}

func queryAdaptiveAPIKeyAmounts(t *testing.T, apiKeyID int64) (quotaUsed, reservedQuota, usage5h, reserved5h decimal.Decimal) {
	t.Helper()
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT quota_used, reserved_quota_usd, usage_5h, reserved_usage_5h_usd
		FROM api_keys
		WHERE id = $1
	`, apiKeyID).Scan(&quotaUsed, &reservedQuota, &usage5h, &reserved5h))
	return quotaUsed, reservedQuota, usage5h, reserved5h
}

func queryAdaptiveAPIKeyRateAmounts(t *testing.T, apiKeyID int64) (
	usage5h, reserved5h, usage1d, reserved1d, usage7d, reserved7d decimal.Decimal,
) {
	t.Helper()
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT usage_5h, reserved_usage_5h_usd,
			usage_1d, reserved_usage_1d_usd,
			usage_7d, reserved_usage_7d_usd
		FROM api_keys
		WHERE id = $1
	`, apiKeyID).Scan(&usage5h, &reserved5h, &usage1d, &reserved1d, &usage7d, &reserved7d))
	return usage5h, reserved5h, usage1d, reserved1d, usage7d, reserved7d
}

func requireAdaptiveDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.RequireFromString(want)
	require.Truef(t, got.Equal(expected), "got %s, want %s", got.StringFixed(10), expected.StringFixed(10))
}

func TestUsageBillingReservation_BalanceCaptureIncludesManagementFeeAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "10", "10")
	setAdaptiveAPIKeyRateLimits(t, fixture.apiKey.ID, "10")
	repo := NewUsageBillingReservationRepository(integrationDB)
	owner := "gateway-balance-capture"
	reserveCommand := adaptiveBalanceReserveCommand(fixture, owner, "reserve-"+uuid.NewString(), "2")

	held, err := repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)
	require.True(t, held.Applied)
	require.NotNil(t, held.Reservation)
	require.Equal(t, service.UsageReservationStatusHeld, held.Reservation.Status)
	requireAdaptiveDecimal(t, held.Reservation.HeldBaseCost, "2")
	requireAdaptiveDecimal(t, held.Reservation.HeldManagementFee, "0.3")
	requireAdaptiveDecimal(t, held.Reservation.HeldTotal, "2.3")
	requireAdaptiveMoney(t, held.AvailableBalanceAfter, "7.7")
	requireAdaptiveMoney(t, held.AdaptiveReservedBalanceAfter, "2.3")
	requireAdaptiveMoney(t, held.APIKeyReservedQuotaAfter, "2.3")
	requireAdaptiveMoney(t, held.APIKeyReserved5hAfter, "2.3")
	requireAdaptiveMoney(t, held.APIKeyReserved1dAfter, "2.3")
	requireAdaptiveMoney(t, held.APIKeyReserved7dAfter, "2.3")
	requireAdaptiveMoney(t, held.APIKeyQuotaUsedAfter, "0")

	replayedReserve, err := repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)
	require.False(t, replayedReserve.Applied)
	require.Equal(t, held.Reservation.ID, replayedReserve.Reservation.ID)
	conflictingReserve := *reserveCommand
	conflictingReserve.RequestFingerprint = ""
	conflictingReserve.EstimatedBaseCost = decimal.RequireFromString("3")
	_, err = repo.Reserve(ctx, &conflictingReserve)
	require.ErrorIs(t, err, service.ErrUsageReservationFingerprintConflict)

	var reserveEntries int
	var reservedBase, reservedFee decimal.Decimal
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(amount) FILTER (WHERE component = 'base'), 0),
			COALESCE(SUM(amount) FILTER (WHERE component = 'management_fee'), 0)
		FROM usage_billing_ledger
		WHERE reservation_id = $1 AND operation = 'reserve'
	`, held.Reservation.ID).Scan(&reserveEntries, &reservedBase, &reservedFee))
	require.Equal(t, 2, reserveEntries)
	requireAdaptiveDecimal(t, reservedBase, "2")
	requireAdaptiveDecimal(t, reservedFee, "0.3")

	inFlight := adaptiveMarkInFlight(t, ctx, repo, held.Reservation, owner, 1, fixture.leafGroup.ID)
	captureCommand := adaptiveCaptureCommand(t, inFlight, owner, "capture-"+uuid.NewString(), "1", fixture.leafGroup.ID)
	captured, err := repo.Capture(ctx, captureCommand)
	require.NoError(t, err)
	require.True(t, captured.Applied)
	require.Equal(t, service.UsageReservationStatusCaptured, captured.Reservation.Status)
	requireAdaptiveDecimal(t, captured.Reservation.CapturedBaseCost, "1")
	requireAdaptiveDecimal(t, captured.Reservation.CapturedManagementFee, "0.15")
	requireAdaptiveDecimal(t, captured.Reservation.CapturedTotal, "1.15")
	requireAdaptiveMoney(t, captured.AvailableBalanceAfter, "8.85")
	requireAdaptiveMoney(t, captured.AdaptiveReservedBalanceAfter, "0")
	requireAdaptiveMoney(t, captured.APIKeyReservedQuotaAfter, "0")
	requireAdaptiveMoney(t, captured.APIKeyReserved5hAfter, "0")
	requireAdaptiveMoney(t, captured.APIKeyReserved1dAfter, "0")
	requireAdaptiveMoney(t, captured.APIKeyReserved7dAfter, "0")
	requireAdaptiveMoney(t, captured.APIKeyQuotaUsedAfter, "1.15")

	replayedCapture, err := repo.Capture(ctx, captureCommand)
	require.NoError(t, err)
	require.False(t, replayedCapture.Applied)
	require.Equal(t, service.UsageReservationStatusCaptured, replayedCapture.Reservation.Status)
	conflictingCapture := *captureCommand
	conflictingCapture.RequestFingerprint = service.HashUsageReservationKey("different-capture-fingerprint")
	_, err = repo.Capture(ctx, &conflictingCapture)
	require.ErrorIs(t, err, service.ErrUsageReservationFingerprintConflict)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "8.85")
	requireAdaptiveDecimal(t, walletReserved, "0")
	quotaUsed, quotaReserved, usage5h, usage5hReserved := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "1.15")
	requireAdaptiveDecimal(t, quotaReserved, "0")
	requireAdaptiveDecimal(t, usage5h, "1.15")
	requireAdaptiveDecimal(t, usage5hReserved, "0")
	_, _, usage1d, reserved1d, usage7d, reserved7d := queryAdaptiveAPIKeyRateAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, usage1d, "1.15")
	requireAdaptiveDecimal(t, reserved1d, "0")
	requireAdaptiveDecimal(t, usage7d, "1.15")
	requireAdaptiveDecimal(t, reserved7d, "0")

	var ledgerEntries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_ledger WHERE reservation_id = $1
	`, held.Reservation.ID).Scan(&ledgerEntries))
	require.Equal(t, 4, ledgerEntries, "reserve and capture must each have one base and one management-fee entry")
}

func TestUsageBillingReservation_ConcurrentCaptureSettlesExactlyOnce(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "10", "10")
	setAdaptiveAPIKeyRateLimits(t, fixture.apiKey.ID, "10")
	repo := NewUsageBillingReservationRepository(integrationDB)
	owner := "gateway-concurrent-capture"

	held, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(
		fixture, owner, "concurrent-capture-reserve-"+uuid.NewString(), "2",
	))
	require.NoError(t, err)
	inFlight := adaptiveMarkInFlight(t, ctx, repo, held.Reservation, owner, 1, fixture.leafGroup.ID)
	capture := adaptiveCaptureCommand(
		t, inFlight, owner, "concurrent-capture-"+uuid.NewString(), "1", fixture.leafGroup.ID,
	)
	capture.Normalize()
	require.NoError(t, capture.Validate())

	type outcome struct {
		result *service.UsageReservationResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		command := *capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, captureErr := repo.Capture(ctx, &command)
			outcomes <- outcome{result: result, err: captureErr}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	applied := 0
	replayed := 0
	for got := range outcomes {
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		if got.result.Applied {
			applied++
		} else {
			replayed++
		}
	}
	require.Equal(t, 1, applied)
	require.Equal(t, 1, replayed)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "8.85")
	requireAdaptiveDecimal(t, walletReserved, "0")
	quotaUsed, quotaReserved, _, _ := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "1.15")
	requireAdaptiveDecimal(t, quotaReserved, "0")
	usage5h, reserved5h, usage1d, reserved1d, usage7d, reserved7d := queryAdaptiveAPIKeyRateAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, usage5h, "1.15")
	requireAdaptiveDecimal(t, reserved5h, "0")
	requireAdaptiveDecimal(t, usage1d, "1.15")
	requireAdaptiveDecimal(t, reserved1d, "0")
	requireAdaptiveDecimal(t, usage7d, "1.15")
	requireAdaptiveDecimal(t, reserved7d, "0")

	var ledgerEntries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_ledger WHERE reservation_id = $1
	`, held.Reservation.ID).Scan(&ledgerEntries))
	require.Equal(t, 4, ledgerEntries, "concurrent replay must not duplicate settlement ledger entries")
}

func TestUsageBillingReservation_AllFailedReleaseRestoresEveryHoldAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "3", "5")
	repo := NewUsageBillingReservationRepository(integrationDB)
	owner := "gateway-all-failed"

	held, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(fixture, owner, "reserve-"+uuid.NewString(), "1"))
	require.NoError(t, err)
	require.True(t, held.Applied)
	requireAdaptiveMoney(t, held.AvailableBalanceAfter, "1.85")
	requireAdaptiveMoney(t, held.AdaptiveReservedBalanceAfter, "1.15")
	requireAdaptiveMoney(t, held.APIKeyReserved5hAfter, "1.15")
	requireAdaptiveMoney(t, held.APIKeyReserved1dAfter, "1.15")
	requireAdaptiveMoney(t, held.APIKeyReserved7dAfter, "1.15")

	secondLeaf := mustCreateGroup(t, testEntClient(t), &service.Group{
		Name:             "adaptive-second-leaf-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeStandard,
	})
	firstAttempt := adaptiveMarkInFlight(t, ctx, repo, held.Reservation, owner, 1, fixture.leafGroup.ID)
	afterFirstFailure := adaptiveMarkAttemptFailed(t, ctx, repo, firstAttempt, owner, 1)
	secondAttempt := adaptiveMarkInFlight(t, ctx, repo, afterFirstFailure, owner, 2, secondLeaf.ID)
	afterSecondFailure := adaptiveMarkAttemptFailed(t, ctx, repo, secondAttempt, owner, 2)

	releaseCommand := adaptiveReleaseCommand(afterSecondFailure, owner, "release-"+uuid.NewString(), 2)
	released, err := repo.Release(ctx, releaseCommand)
	require.NoError(t, err)
	require.True(t, released.Applied)
	require.Equal(t, service.UsageReservationStatusReleased, released.Reservation.Status)
	requireAdaptiveDecimal(t, released.Reservation.CapturedTotal, "0")
	requireAdaptiveMoney(t, released.AvailableBalanceAfter, "3")
	requireAdaptiveMoney(t, released.AdaptiveReservedBalanceAfter, "0")
	requireAdaptiveMoney(t, released.APIKeyReservedQuotaAfter, "0")
	requireAdaptiveMoney(t, released.APIKeyReserved5hAfter, "0")
	requireAdaptiveMoney(t, released.APIKeyReserved1dAfter, "0")
	requireAdaptiveMoney(t, released.APIKeyReserved7dAfter, "0")
	requireAdaptiveMoney(t, released.APIKeyQuotaUsedAfter, "0")

	replayed, err := repo.Release(ctx, releaseCommand)
	require.NoError(t, err)
	require.False(t, replayed.Applied)
	conflictingRelease := *releaseCommand
	conflictingRelease.RequestFingerprint = service.HashUsageReservationKey("different-release-fingerprint")
	_, err = repo.Release(ctx, &conflictingRelease)
	require.ErrorIs(t, err, service.ErrUsageReservationFingerprintConflict)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "3")
	requireAdaptiveDecimal(t, walletReserved, "0")
	quotaUsed, quotaReserved, usage5h, usage5hReserved := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "0")
	requireAdaptiveDecimal(t, quotaReserved, "0")
	requireAdaptiveDecimal(t, usage5h, "0")
	requireAdaptiveDecimal(t, usage5hReserved, "0")
	_, _, usage1d, reserved1d, usage7d, reserved7d := queryAdaptiveAPIKeyRateAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, usage1d, "0")
	requireAdaptiveDecimal(t, reserved1d, "0")
	requireAdaptiveDecimal(t, usage7d, "0")
	requireAdaptiveDecimal(t, reserved7d, "0")

	var ledgerEntries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_ledger WHERE reservation_id = $1
	`, held.Reservation.ID).Scan(&ledgerEntries))
	require.Equal(t, 4, ledgerEntries)
	var failedAttempts int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM usage_billing_attempts
		WHERE reservation_id = $1 AND status = 'failed'
	`, held.Reservation.ID).Scan(&failedAttempts))
	require.Equal(t, 2, failedAttempts, "both upstream attempts must share the one reservation")
}

func TestUsageBillingReservation_CaptureOverHoldIsAtomic(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "2", "2")
	repo := NewUsageBillingReservationRepository(integrationDB)
	owner := "gateway-over-hold"

	held, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(fixture, owner, "reserve-"+uuid.NewString(), "1"))
	require.NoError(t, err)
	inFlight := adaptiveMarkInFlight(t, ctx, repo, held.Reservation, owner, 1, fixture.leafGroup.ID)
	capture := adaptiveCaptureCommand(t, inFlight, owner, "capture-"+uuid.NewString(), "1.0000000001", fixture.leafGroup.ID)
	_, err = repo.Capture(ctx, capture)
	require.ErrorIs(t, err, service.ErrUsageReservationCaptureExceedsHold)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "0.85")
	requireAdaptiveDecimal(t, walletReserved, "1.15")
	quotaUsed, quotaReserved, _, usage5hReserved := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "0")
	requireAdaptiveDecimal(t, quotaReserved, "1.15")
	requireAdaptiveDecimal(t, usage5hReserved, "1.15")

	var status string
	var capturedTotal decimal.Decimal
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status, captured_total FROM usage_billing_reservations WHERE id = $1
	`, held.Reservation.ID).Scan(&status, &capturedTotal))
	require.Equal(t, service.UsageReservationStatusInFlight, status)
	requireAdaptiveDecimal(t, capturedTotal, "0")

	afterFailure := adaptiveMarkAttemptFailed(t, ctx, repo, inFlight, owner, 1)
	_, err = repo.Release(ctx, adaptiveReleaseCommand(afterFailure, owner, "cleanup-release-"+uuid.NewString(), 1))
	require.NoError(t, err)
}

func TestUsageBillingReservation_ConcurrentReserveNeverOverspends(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "1.15", "1.15")
	repo := NewUsageBillingReservationRepository(integrationDB)

	const workers = 16
	type outcome struct {
		owner  string
		result *service.UsageReservationResult
		err    error
	}
	outcomes := make(chan outcome, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		owner := fmt.Sprintf("concurrent-owner-%d", i)
		command := adaptiveBalanceReserveCommand(fixture, owner, fmt.Sprintf("concurrent-%d-%s", i, uuid.NewString()), "1")
		wg.Add(1)
		go func(owner string, command *service.UsageReservationReserveCommand) {
			defer wg.Done()
			<-start
			result, err := repo.Reserve(ctx, command)
			outcomes <- outcome{owner: owner, result: result, err: err}
		}(owner, command)
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var winner outcome
	successes := 0
	for got := range outcomes {
		if got.err == nil {
			require.NotNil(t, got.result)
			require.True(t, got.result.Applied)
			successes++
			winner = got
			continue
		}
		require.True(t, errors.Is(got.err, service.ErrUsageReservationInsufficientBalance) || errors.Is(got.err, service.ErrUsageReservationAPIKeyQuota), got.err)
	}
	require.Equal(t, 1, successes)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "0")
	requireAdaptiveDecimal(t, walletReserved, "1.15")
	quotaUsed, quotaReserved, _, usage5hReserved := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "0")
	requireAdaptiveDecimal(t, quotaReserved, "1.15")
	requireAdaptiveDecimal(t, usage5hReserved, "1.15")
	require.False(t, balance.IsNegative())

	var reservations, ledgerEntries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_reservations WHERE user_id = $1
	`, fixture.user.ID).Scan(&reservations))
	require.Equal(t, 1, reservations, "failed concurrent reservations must roll back their intent rows")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_ledger WHERE reservation_id = $1
	`, winner.result.Reservation.ID).Scan(&ledgerEntries))
	require.Equal(t, 2, ledgerEntries)

	_, err := repo.Release(ctx, adaptiveReleaseCommand(winner.result.Reservation, winner.owner, "cleanup-release-"+uuid.NewString()))
	require.NoError(t, err)
}

func TestUsageBillingReservation_APIKeyQuotaFailureRollsBackWalletHold(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "100", "1.14")
	repo := NewUsageBillingReservationRepository(integrationDB)

	_, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(fixture, "api-key-limit-owner", "reserve-"+uuid.NewString(), "1"))
	require.ErrorIs(t, err, service.ErrUsageReservationAPIKeyQuota)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "100")
	requireAdaptiveDecimal(t, walletReserved, "0")
	quotaUsed, quotaReserved, usage5h, usage5hReserved := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "0")
	requireAdaptiveDecimal(t, quotaReserved, "0")
	requireAdaptiveDecimal(t, usage5h, "0")
	requireAdaptiveDecimal(t, usage5hReserved, "0")

	var reservations, ledgerEntries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_reservations WHERE user_id = $1
	`, fixture.user.ID).Scan(&reservations))
	require.Zero(t, reservations)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_ledger l
		JOIN usage_billing_reservations r ON r.id = l.reservation_id
		WHERE r.user_id = $1
	`, fixture.user.ID).Scan(&ledgerEntries))
	require.Zero(t, ledgerEntries)
}

func TestUsageBillingReservation_RateWindowResetKeepsActiveHolds(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "10", "10")
	repo := NewUsageBillingReservationRepository(integrationDB)
	setAdaptiveAPIKeyRateLimits(t, fixture.apiKey.ID, "2")

	_, err := integrationDB.ExecContext(ctx, `
		UPDATE api_keys
		SET usage_5h = 0.85,
			usage_1d = 0.85,
			usage_7d = 0.85,
			window_5h_start = NOW(),
			window_1d_start = NOW(),
			window_7d_start = NOW()
		WHERE id = $1
	`, fixture.apiKey.ID)
	require.NoError(t, err)

	firstOwner := "window-first-owner"
	first, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(
		fixture, firstOwner, "window-first-"+uuid.NewString(), "1",
	))
	require.NoError(t, err)
	requireAdaptiveMoney(t, first.APIKeyReserved5hAfter, "1.15")

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE api_keys
		SET window_5h_start = NOW() - INTERVAL '6 hours',
			window_1d_start = NOW() - INTERVAL '25 hours',
			window_7d_start = NOW() - INTERVAL '8 days'
		WHERE id = $1
	`, fixture.apiKey.ID)
	require.NoError(t, err)

	secondOwner := "window-second-owner"
	second, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(
		fixture, secondOwner, "window-second-"+uuid.NewString(), "0.695",
	))
	require.NoError(t, err, "expired actual usage must reset while the first active hold remains reserved")
	requireAdaptiveMoney(t, second.APIKeyReserved5hAfter, "1.94925")

	var usage5h, usage1d, usage7d, reserved5h, reserved1d, reserved7d decimal.Decimal
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT usage_5h, usage_1d, usage_7d,
			reserved_usage_5h_usd, reserved_usage_1d_usd, reserved_usage_7d_usd
		FROM api_keys
		WHERE id = $1
	`, fixture.apiKey.ID).Scan(&usage5h, &usage1d, &usage7d, &reserved5h, &reserved1d, &reserved7d))
	requireAdaptiveDecimal(t, usage5h, "0")
	requireAdaptiveDecimal(t, usage1d, "0")
	requireAdaptiveDecimal(t, usage7d, "0")
	requireAdaptiveDecimal(t, reserved5h, "1.94925")
	requireAdaptiveDecimal(t, reserved1d, "1.94925")
	requireAdaptiveDecimal(t, reserved7d, "1.94925")

	_, err = repo.Reserve(ctx, adaptiveBalanceReserveCommand(
		fixture, "window-third-owner", "window-third-"+uuid.NewString(), "0.05",
	))
	require.ErrorIs(t, err, service.ErrUsageReservationAPIKeyRateLimit,
		"window rollover must not discard active base and management-fee holds")

	_, err = repo.Release(ctx, adaptiveReleaseCommand(second.Reservation, secondOwner, "window-second-release-"+uuid.NewString()))
	require.NoError(t, err)
	_, err = repo.Release(ctx, adaptiveReleaseCommand(first.Reservation, firstOwner, "window-first-release-"+uuid.NewString()))
	require.NoError(t, err)

	_, reservedQuota, _, finalReserved5h := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, reservedQuota, "0")
	requireAdaptiveDecimal(t, finalReserved5h, "0")
}

func TestUsageBillingReservation_StaleMutationCredentialsDoNotMoveFunds(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		wantErr error
		mutate  func(capture *service.UsageReservationCaptureCommand, release *service.UsageReservationReleaseCommand)
	}{
		{
			name:    "capture wrong owner",
			action:  "capture",
			wantErr: service.ErrUsageReservationOwnerConflict,
			mutate: func(capture *service.UsageReservationCaptureCommand, _ *service.UsageReservationReleaseCommand) {
				capture.OwnerID = "wrong-owner"
			},
		},
		{
			name:    "capture stale fence",
			action:  "capture",
			wantErr: service.ErrUsageReservationFenceConflict,
			mutate: func(capture *service.UsageReservationCaptureCommand, _ *service.UsageReservationReleaseCommand) {
				capture.FencingToken++
			},
		},
		{
			name:    "capture stale row version",
			action:  "capture",
			wantErr: service.ErrUsageReservationVersionConflict,
			mutate: func(capture *service.UsageReservationCaptureCommand, _ *service.UsageReservationReleaseCommand) {
				capture.RowVersion++
			},
		},
		{
			name:    "release wrong owner",
			action:  "release",
			wantErr: service.ErrUsageReservationOwnerConflict,
			mutate: func(_ *service.UsageReservationCaptureCommand, release *service.UsageReservationReleaseCommand) {
				release.OwnerID = "wrong-owner"
			},
		},
		{
			name:    "release stale fence",
			action:  "release",
			wantErr: service.ErrUsageReservationFenceConflict,
			mutate: func(_ *service.UsageReservationCaptureCommand, release *service.UsageReservationReleaseCommand) {
				release.FencingToken++
			},
		},
		{
			name:    "release stale row version",
			action:  "release",
			wantErr: service.ErrUsageReservationVersionConflict,
			mutate: func(_ *service.UsageReservationCaptureCommand, release *service.UsageReservationReleaseCommand) {
				release.RowVersion++
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newAdaptiveReservationFixture(t, "5", "5")
			repo := NewUsageBillingReservationRepository(integrationDB)
			owner := "credential-owner-" + uuid.NewString()
			held, reserveErr := repo.Reserve(ctx, adaptiveBalanceReserveCommand(fixture, owner, "credential-reserve-"+uuid.NewString(), "1"))
			require.NoError(t, reserveErr)
			mutationReservation := held.Reservation
			var capture *service.UsageReservationCaptureCommand
			var release *service.UsageReservationReleaseCommand
			if tt.action == "capture" {
				mutationReservation = adaptiveMarkInFlight(t, ctx, repo, held.Reservation, owner, 1, fixture.leafGroup.ID)
				capture = adaptiveCaptureCommand(t, mutationReservation, owner, "invalid-capture-"+uuid.NewString(), "0.5", fixture.leafGroup.ID)
			} else {
				release = adaptiveReleaseCommand(mutationReservation, owner, "invalid-release-"+uuid.NewString())
			}
			tt.mutate(capture, release)
			var err error
			if tt.action == "capture" {
				_, err = repo.Capture(ctx, capture)
			} else {
				_, err = repo.Release(ctx, release)
			}
			require.ErrorIs(t, err, tt.wantErr)

			balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
			requireAdaptiveDecimal(t, balance, "3.85")
			requireAdaptiveDecimal(t, walletReserved, "1.15")
			quotaUsed, quotaReserved, _, reserved5h := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
			requireAdaptiveDecimal(t, quotaUsed, "0")
			requireAdaptiveDecimal(t, quotaReserved, "1.15")
			requireAdaptiveDecimal(t, reserved5h, "1.15")

			var status string
			var rowVersion, fence int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `
				SELECT status, row_version, lease_epoch
				FROM usage_billing_reservations
				WHERE id = $1
			`, held.Reservation.ID).Scan(&status, &rowVersion, &fence))
			require.Equal(t, mutationReservation.Status, status)
			require.Equal(t, mutationReservation.RowVersion, rowVersion)
			require.Equal(t, mutationReservation.FencingToken, fence)

			cleanupReservation := mutationReservation
			if tt.action == "capture" {
				cleanupReservation = adaptiveMarkAttemptFailed(t, ctx, repo, mutationReservation, owner, 1)
			}
			_, err = repo.Release(ctx, adaptiveReleaseCommand(cleanupReservation, owner, "credential-cleanup-"+uuid.NewString(), 1))
			require.NoError(t, err)
		})
	}
}

func TestUsageBillingReservation_RenewQuotaFailureRollsBackEntireExpansion(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "10", "1.5")
	repo := NewUsageBillingReservationRepository(integrationDB)
	owner := "renew-rollback-owner"
	held, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(fixture, owner, "renew-reserve-"+uuid.NewString(), "1"))
	require.NoError(t, err)
	originalLease := held.Reservation.LeaseExpiresAt

	renew := &service.UsageReservationRenewCommand{
		ReservationID:      held.Reservation.ID,
		OperationKey:       "renew-" + uuid.NewString(),
		OwnerID:            owner,
		FencingToken:       held.Reservation.FencingToken,
		RowVersion:         held.Reservation.RowVersion,
		AdditionalBaseCost: decimal.RequireFromString("0.4"),
		LeaseExpiresAt:     originalLease.Add(5 * time.Minute),
	}
	_, err = repo.Renew(ctx, renew)
	require.ErrorIs(t, err, service.ErrUsageReservationAPIKeyQuota)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "8.85")
	requireAdaptiveDecimal(t, walletReserved, "1.15")
	quotaUsed, quotaReserved, _, reserved5h := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "0")
	requireAdaptiveDecimal(t, quotaReserved, "1.15")
	requireAdaptiveDecimal(t, reserved5h, "1.15")

	var estimatedBase, heldTotal decimal.Decimal
	var rowVersion int64
	var lease time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT estimated_base_cost, held_total, row_version, lease_expires_at
		FROM usage_billing_reservations
		WHERE id = $1
	`, held.Reservation.ID).Scan(&estimatedBase, &heldTotal, &rowVersion, &lease))
	requireAdaptiveDecimal(t, estimatedBase, "1")
	requireAdaptiveDecimal(t, heldTotal, "1.15")
	require.Equal(t, held.Reservation.RowVersion, rowVersion)
	require.WithinDuration(t, originalLease, lease, time.Microsecond)

	var ledgerEntries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_ledger WHERE reservation_id = $1
	`, held.Reservation.ID).Scan(&ledgerEntries))
	require.Equal(t, 2, ledgerEntries)

	_, err = repo.Release(ctx, adaptiveReleaseCommand(held.Reservation, owner, "renew-cleanup-"+uuid.NewString()))
	require.NoError(t, err)
}

func TestUsageBillingReservation_ReconcileExpiredOnlyClaimsAndFences(t *testing.T) {
	ctx := context.Background()
	fixture := newAdaptiveReservationFixture(t, "4", "4")
	repo := NewUsageBillingReservationRepository(integrationDB)
	owner := "expired-original-owner"
	held, err := repo.Reserve(ctx, adaptiveBalanceReserveCommand(fixture, owner, "expired-reserve-"+uuid.NewString(), "1"))
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE usage_billing_reservations
		SET lease_expires_at = NOW() - INTERVAL '1 second'
		WHERE id = $1
	`, held.Reservation.ID)
	require.NoError(t, err)

	claimed, err := repo.ReconcileExpired(ctx, &service.UsageReservationReconcileCommand{
		WorkerID: "recovery-worker",
		Limit:    10,
		ClaimTTL: 30 * time.Second,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, claimed.Examined, 1)
	var recovered *service.UsageBillingReservation
	for i := range claimed.Claimed {
		if claimed.Claimed[i].ID == held.Reservation.ID {
			recovered = &claimed.Claimed[i]
			break
		}
	}
	require.NotNil(t, recovered)
	require.Equal(t, service.UsageReservationStatusReconciling, recovered.Status)
	require.Equal(t, "recovery-worker", recovered.OwnerID)
	require.Equal(t, held.Reservation.FencingToken+1, recovered.FencingToken)
	require.Equal(t, held.Reservation.RowVersion+1, recovered.RowVersion)

	balance, walletReserved := queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "2.85")
	requireAdaptiveDecimal(t, walletReserved, "1.15")
	quotaUsed, quotaReserved, _, reserved5h := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
	requireAdaptiveDecimal(t, quotaUsed, "0")
	requireAdaptiveDecimal(t, quotaReserved, "1.15")
	requireAdaptiveDecimal(t, reserved5h, "1.15")

	released, err := repo.Release(ctx, adaptiveReleaseCommand(recovered, recovered.OwnerID, "recovery-release-"+uuid.NewString()))
	require.NoError(t, err)
	require.True(t, released.Applied)
	balance, walletReserved = queryAdaptiveWallet(t, fixture.user.ID)
	requireAdaptiveDecimal(t, balance, "4")
	requireAdaptiveDecimal(t, walletReserved, "0")
}

func TestUsageBillingReservation_SubscriptionReservedUsageCaptureAndRelease(t *testing.T) {
	t.Run("capture moves hold to actual usage including management fee", func(t *testing.T) {
		ctx := context.Background()
		limit := 10.0
		fixture := newAdaptiveReservationFixture(t, "0", "100")
		group := mustCreateGroup(t, testEntClient(t), &service.Group{
			Name:             "adaptive-subscription-" + uuid.NewString(),
			Platform:         service.PlatformAnthropic,
			SubscriptionType: service.SubscriptionTypeSubscription,
			DailyLimitUSD:    &limit,
			WeeklyLimitUSD:   &limit,
			MonthlyLimitUSD:  &limit,
		})
		subscription := mustCreateSubscription(t, testEntClient(t), &service.UserSubscription{
			UserID:          fixture.user.ID,
			GroupID:         group.ID,
			DailyUsageUSD:   8.85,
			WeeklyUsageUSD:  8.85,
			MonthlyUsageUSD: 8.85,
		})
		repo := NewUsageBillingReservationRepository(integrationDB)
		owner := "subscription-capture-owner"
		reserve := adaptiveBalanceReserveCommand(fixture, owner, "subscription-reserve-"+uuid.NewString(), "1")
		reserve.FundingSource = service.UsageReservationFundingSubscription
		reserve.SubscriptionID = &subscription.ID

		held, err := repo.Reserve(ctx, reserve)
		require.NoError(t, err)
		require.True(t, held.Applied)
		requireAdaptiveMoney(t, held.SubscriptionReservedAfter, "1.15")
		requireAdaptiveMoney(t, held.SubscriptionDailyUsageAfter, "8.85")
		requireAdaptiveMoney(t, held.SubscriptionWeeklyUsageAfter, "8.85")
		requireAdaptiveMoney(t, held.SubscriptionMonthlyUsageAfter, "8.85")
		requireAdaptiveMoney(t, held.APIKeyReservedQuotaAfter, "1.15")

		second := adaptiveBalanceReserveCommand(fixture, "subscription-second-owner", "subscription-second-"+uuid.NewString(), "1")
		second.FundingSource = service.UsageReservationFundingSubscription
		second.SubscriptionID = &subscription.ID
		_, err = repo.Reserve(ctx, second)
		require.ErrorIs(t, err, service.ErrUsageReservationSubscriptionLimit)

		inFlight := adaptiveMarkInFlight(t, ctx, repo, held.Reservation, owner, 1, fixture.leafGroup.ID)
		captured, err := repo.Capture(ctx, adaptiveCaptureCommand(t, inFlight, owner, "subscription-capture-"+uuid.NewString(), "0.5", fixture.leafGroup.ID))
		require.NoError(t, err)
		require.True(t, captured.Applied)
		requireAdaptiveDecimal(t, captured.Reservation.CapturedManagementFee, "0.075")
		requireAdaptiveDecimal(t, captured.Reservation.CapturedTotal, "0.575")
		requireAdaptiveMoney(t, captured.SubscriptionReservedAfter, "0")
		requireAdaptiveMoney(t, captured.SubscriptionDailyUsageAfter, "9.425")
		requireAdaptiveMoney(t, captured.SubscriptionWeeklyUsageAfter, "9.425")
		requireAdaptiveMoney(t, captured.SubscriptionMonthlyUsageAfter, "9.425")
		requireAdaptiveMoney(t, captured.APIKeyReservedQuotaAfter, "0")
		requireAdaptiveMoney(t, captured.APIKeyQuotaUsedAfter, "0.575")
		_, _, usage5h, reserved5h := queryAdaptiveAPIKeyAmounts(t, fixture.apiKey.ID)
		requireAdaptiveDecimal(t, usage5h, "0")
		requireAdaptiveDecimal(t, reserved5h, "0")
	})

	t.Run("release leaves actual usage unchanged", func(t *testing.T) {
		ctx := context.Background()
		limit := 10.0
		fixture := newAdaptiveReservationFixture(t, "0", "100")
		group := mustCreateGroup(t, testEntClient(t), &service.Group{
			Name:             "adaptive-subscription-release-" + uuid.NewString(),
			Platform:         service.PlatformAnthropic,
			SubscriptionType: service.SubscriptionTypeSubscription,
			DailyLimitUSD:    &limit,
			WeeklyLimitUSD:   &limit,
			MonthlyLimitUSD:  &limit,
		})
		subscription := mustCreateSubscription(t, testEntClient(t), &service.UserSubscription{
			UserID:          fixture.user.ID,
			GroupID:         group.ID,
			DailyUsageUSD:   2,
			WeeklyUsageUSD:  2,
			MonthlyUsageUSD: 2,
		})
		repo := NewUsageBillingReservationRepository(integrationDB)
		owner := "subscription-release-owner"
		reserve := adaptiveBalanceReserveCommand(fixture, owner, "subscription-release-reserve-"+uuid.NewString(), "1")
		reserve.FundingSource = service.UsageReservationFundingSubscription
		reserve.SubscriptionID = &subscription.ID

		held, err := repo.Reserve(ctx, reserve)
		require.NoError(t, err)
		released, err := repo.Release(ctx, adaptiveReleaseCommand(held.Reservation, owner, "subscription-release-"+uuid.NewString()))
		require.NoError(t, err)
		requireAdaptiveMoney(t, released.SubscriptionReservedAfter, "0")
		requireAdaptiveMoney(t, released.SubscriptionDailyUsageAfter, "2")
		requireAdaptiveMoney(t, released.SubscriptionWeeklyUsageAfter, "2")
		requireAdaptiveMoney(t, released.SubscriptionMonthlyUsageAfter, "2")
		requireAdaptiveMoney(t, released.APIKeyReservedQuotaAfter, "0")
		requireAdaptiveMoney(t, released.APIKeyQuotaUsedAfter, "0")
	})
}
