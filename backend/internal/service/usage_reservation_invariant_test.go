package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCalculateUsageManagementFee_RoundsHalfUpAtTenDecimalPlaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "exact half rounds away from zero",
			base: "0.0000000030",
			want: "0.0000000005",
		},
		{
			name: "below half remains below",
			base: "0.0000000020",
			want: "0.0000000003",
		},
		{
			name: "regular amount",
			base: "1.2345678901",
			want: "0.1851851835",
		},
		{
			name: "zero",
			base: "0",
			want: "0.0000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CalculateUsageManagementFee(decimal.RequireFromString(tt.base), DefaultAdaptiveManagementFeeBPS)
			require.Truef(t, got.Equal(decimal.RequireFromString(tt.want)), "got %s, want %s", got, tt.want)
			require.Equal(t, tt.want, got.StringFixed(10))
		})
	}
}

func TestCalculateUsageReservationHold_IncludesFeeAndCeilsAtTenDecimalPlaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		upper string
		want  string
	}{
		{name: "ordinary upper bound", upper: "1", want: "1.1500000000"},
		{name: "fraction beyond money scale", upper: "0.0000000001", want: "0.0000000002"},
		{name: "zero", upper: "0", want: "0.0000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CalculateUsageReservationHold(decimal.RequireFromString(tt.upper), DefaultAdaptiveManagementFeeBPS)
			require.Equal(t, tt.want, got.StringFixed(10))
		})
	}
}

func TestUsageReservationCommands_PreserveTenDecimalPlaces(t *testing.T) {
	t.Parallel()

	lease := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	parentGroupID := int64(43)
	reserve := &UsageReservationReserveCommand{
		IdempotencyKey:    "reserve-ten-decimals",
		OwnerID:           "test-owner",
		UserID:            41,
		APIKeyID:          42,
		ParentGroupID:     &parentGroupID,
		CanonicalModel:    "claude-opus-4-8",
		PricingSnapshotID: "pricing-ten-decimals",
		FundingSource:     UsageReservationFundingBalance,
		EstimatedBaseCost: decimal.RequireFromString("0.0000000001"),
		LeaseExpiresAt:    lease,
	}
	reserve.Normalize()

	require.Equal(t, int32(1500), reserve.ManagementFeeBPS)
	require.Equal(t, "0.0000000001", reserve.EstimatedBaseCost.StringFixed(10))
	require.NoError(t, reserve.Validate())

	capture := &UsageReservationCaptureCommand{
		ReservationID:      "reservation-ten-decimals",
		OperationKey:       "capture-ten-decimals",
		OwnerID:            "test-owner",
		FencingToken:       1,
		RowVersion:         1,
		ActualBaseCost:     decimal.RequireFromString("0.0000000001"),
		WinningLeafGroupID: 101,
		AttemptNo:          1,
		UsageLogID:         1001,
		UsageLogCreatedAt:  lease,
		EvidenceHash:       HashUsageReservationKey("usage-log-1001"),
	}
	capture.Normalize()

	require.Equal(t, "0.0000000001", capture.ActualBaseCost.StringFixed(10))
	require.NoError(t, capture.Validate())
}

func TestUsageReservationReserveCommand_RequiresPositiveParentGroup(t *testing.T) {
	validCommand := func(parentGroupID *int64) *UsageReservationReserveCommand {
		cmd := &UsageReservationReserveCommand{
			IdempotencyKey:    "parent-group-required",
			OwnerID:           "test-owner",
			UserID:            41,
			APIKeyID:          42,
			ParentGroupID:     parentGroupID,
			CanonicalModel:    "claude-opus-4-8",
			PricingSnapshotID: "pricing-v1",
			FundingSource:     UsageReservationFundingBalance,
			EstimatedBaseCost: decimal.RequireFromString("1"),
		}
		cmd.Normalize()
		return cmd
	}

	positive := int64(43)
	require.NoError(t, validCommand(&positive).Validate())
	require.ErrorIs(t, validCommand(nil).Validate(), ErrUsageReservationInvalid)
	zero := int64(0)
	require.ErrorIs(t, validCommand(&zero).Validate(), ErrUsageReservationInvalid)
	negative := int64(-1)
	require.ErrorIs(t, validCommand(&negative).Validate(), ErrUsageReservationInvalid)
}

func TestUsageReservationFingerprint_DistinguishesTenthDecimalPlace(t *testing.T) {
	t.Parallel()

	lease := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	command := func(amount string) *UsageReservationReserveCommand {
		parentGroupID := int64(43)
		cmd := &UsageReservationReserveCommand{
			IdempotencyKey:    "same-logical-request",
			OwnerID:           "test-owner",
			UserID:            41,
			APIKeyID:          42,
			ParentGroupID:     &parentGroupID,
			CanonicalModel:    "claude-opus-4-8",
			PricingSnapshotID: "pricing-v1",
			FundingSource:     UsageReservationFundingBalance,
			EstimatedBaseCost: decimal.RequireFromString(amount),
			LeaseExpiresAt:    lease,
		}
		cmd.Normalize()
		return cmd
	}

	first := command("1.0000000001")
	second := command("1.0000000002")

	require.NotEqual(t, first.RequestFingerprint, second.RequestFingerprint)
}
