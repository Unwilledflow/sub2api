package service

import (
	"context"
	"math"

	"github.com/shopspring/decimal"
)

// settleAdaptiveCustomerUsage finalizes an Adaptive request:
// base cost B is the leaf-equivalent customer charge; Capture applies the
// immutable 15% management fee and writes the usage evidence row.
// When coordinator is nil the path fails closed so Adaptive traffic cannot
// silently under-bill.
func settleAdaptiveCustomerUsage(
	ctx context.Context,
	coordinator *AdaptiveBillingCoordinator,
	billing *AdaptiveBillingContext,
	usageLog *UsageLog,
	baseActualCost float64,
) error {
	if billing == nil || billing.Probe {
		return nil
	}
	if coordinator == nil {
		return ErrAdaptiveBillingPathNotWired
	}
	if usageLog == nil {
		return ErrAdaptiveBillingContextInvalid
	}
	if err := billing.ValidateForSettlement(); err != nil {
		return err
	}

	base := decimal.Zero
	if !math.IsNaN(baseActualCost) && !math.IsInf(baseActualCost, 0) && baseActualCost > 0 {
		base = decimal.NewFromFloat(baseActualCost)
	}

	_, _, err := coordinator.Capture(ctx, billing, usageLog, base)
	return err
}

// ReleaseAdaptiveCustomerHold releases an authorized Adaptive hold when the
// request never produced verifiable customer usage (all attempts failed or
// cancelled before commit).
func ReleaseAdaptiveCustomerHold(
	ctx context.Context,
	coordinator *AdaptiveBillingCoordinator,
	billing *AdaptiveBillingContext,
	reason string,
) error {
	if billing == nil || billing.Probe {
		return nil
	}
	if coordinator == nil {
		return ErrAdaptiveBillingPathNotWired
	}
	_, err := coordinator.Release(ctx, billing, reason, billing.LastFailedEvidenceHash)
	return err
}
