package service

import (
	"math"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// 对齐 users.balance / api_keys.quota_used 的 NUMERIC(20,8)。
const UsageBillingMonetaryScale = 8

var ErrUsageBillingRequestIDRequired = errors.New("usage billing request_id is required")
var ErrUsageBillingRequestConflict = errors.New("usage billing request fingerprint conflict")

const (
	BalanceSettlementPrepared int16 = iota
	BalanceSettlementAuthorized
	BalanceSettlementFinalizationPending
	BalanceSettlementPending
	BalanceSettlementApplied
	BalanceSettlementRefunded
	BalanceSettlementTerminal
)

// UsageBillingCommand describes one billable request that must be applied at most once.
type UsageBillingCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string

	UserID              int64
	AccountID           int64
	SubscriptionID      *int64
	AccountType         string
	Model               string
	ServiceTier         string
	ReasoningEffort     string
	BillingType         int8
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	ImageCount          int
	MediaType           string

	BalanceCost float64
	// BalancePreauthorized means this request already reserved spendable balance
	// in the Redis live wallet. Repository.Apply must only persist the actual
	// amount as finalization_pending; it must not enqueue the database deduction
	// until the Redis reservation has been finalized.
	BalancePreauthorized bool
	SubscriptionCost     float64
	APIKeyQuotaCost      float64
	APIKeyRateLimitCost  float64
	AccountQuotaCost     float64
}

func (c *UsageBillingCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		c.RequestFingerprint = buildUsageBillingFingerprint(c)
	}
}

// canonicalUsageActualCost returns the exact database charge scale while the
// detailed pricing components retain their higher diagnostic precision.
func canonicalUsageActualCost(cost *CostBreakdown) float64 {
	if cost == nil {
		return 0
	}
	return QuantizeUsageBillingAmount(cost.ActualCost)
}

func buildUsageBillingFingerprint(c *UsageBillingCommand) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf(
		"%d|%d|%d|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%s|%d|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f",
		c.UserID,
		c.AccountID,
		c.APIKeyID,
		strings.TrimSpace(c.AccountType),
		strings.TrimSpace(c.Model),
		strings.TrimSpace(c.ServiceTier),
		strings.TrimSpace(c.ReasoningEffort),
		c.BillingType,
		c.InputTokens,
		c.OutputTokens,
		c.CacheCreationTokens,
		c.CacheReadTokens,
		c.ImageCount,
		strings.TrimSpace(c.MediaType),
		valueOrZero(c.SubscriptionID),
		c.BalanceCost,
		c.SubscriptionCost,
		c.APIKeyQuotaCost,
		c.APIKeyRateLimitCost,
		c.AccountQuotaCost,
	)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func HashUsageRequestPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func valueOrZero(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// AccountQuotaState holds the post-increment quota state returned by the DB transaction.
// All values are post-update (i.e., already include the increment).
type AccountQuotaState struct {
	TotalUsed   float64
	TotalLimit  float64
	DailyUsed   float64
	DailyLimit  float64
	WeeklyUsed  float64
	WeeklyLimit float64
}

type UsageBillingApplyResult struct {
	Applied                    bool
	BalanceFinalizationPending bool
	APIKeyQuotaExhausted       bool
	NewBalance                 *float64           // post-deduction balance (nil = no balance deduction)
	BalanceOverdrafted         bool               // true when the sufficient-balance guard missed and debt was still recorded
	QuotaState                 *AccountQuotaState // post-increment quota state (nil = no quota increment)
}

type BalancePreauthorizationCommand struct {
	RequestID                string
	APIKeyID                 int64
	UserID                   int64
	AuthorizationFingerprint string
	HoldAmount               float64
	ExpiresAt                time.Time
}

type BalancePreauthorizationRecord struct {
	RequestID                string
	APIKeyID                 int64
	UserID                   int64
	RequestFingerprint       string
	AuthorizationFingerprint string
	HoldAmount               float64
	Amount                   float64
	Status                   int16
	ExpiresAt                time.Time
	UpdatedAt                time.Time
}

// BatchImageBalanceHoldCommand describes an idempotent balance hold operation.
type BatchImageBalanceHoldCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string
	UserID             int64
	BatchID            string
	HoldAmount         float64
	ActualAmount       float64
}

func (c *BatchImageBalanceHoldCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	c.BatchID = strings.TrimSpace(c.BatchID)
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		c.RequestFingerprint = buildBatchImageBalanceHoldFingerprint(c)
	}
}

func buildBatchImageBalanceHoldFingerprint(c *BatchImageBalanceHoldCommand) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf(
		"%d|%d|%s|%0.10f|%0.10f",
		c.UserID,
		c.APIKeyID,
		strings.TrimSpace(c.BatchID),
		c.HoldAmount,
		c.ActualAmount,
	)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type BatchImageBalanceHoldResult struct {
	Applied       bool
	NewBalance    *float64
	FrozenBalance *float64
}

type UsageBillingRepository interface {
	Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error)
	// PrepareBalancePreauthorization is idempotent for the same request,
	// API key, user, fingerprint, and quantized hold. It returns the existing
	// state instead of reopening a completed state.
	PrepareBalancePreauthorization(ctx context.Context, cmd *BalancePreauthorizationCommand) (*BalancePreauthorizationRecord, error)
	// MarkBalancePreauthorizationAuthorized transitions prepared -> authorized;
	// repeating an already-authorized transition succeeds without regression.
	MarkBalancePreauthorizationAuthorized(ctx context.Context, requestID string, apiKeyID int64) error
	// BeginBalancePreauthorizationFinalization persists the actual charge before
	// Redis settlement. An identical retry in finalization_pending is a no-op.
	BeginBalancePreauthorizationFinalization(ctx context.Context, requestID string, apiKeyID int64, amount float64, requestFingerprint string) error
	// CompleteBalancePreauthorizationSettlement transitions a positive finalized
	// charge to settlement_pending. Repeating it in pending/applied is a no-op.
	CompleteBalancePreauthorizationSettlement(ctx context.Context, requestID string, apiKeyID int64) error
	// BeginBalancePreauthorizationRefund transitions prepared/authorized ->
	// finalization_pending with zero actual charge. Repeating it in zero-amount
	// finalization_pending/refunded is a no-op.
	BeginBalancePreauthorizationRefund(ctx context.Context, requestID string, apiKeyID int64) error
	// CompleteBalancePreauthorizationRefund accepts only a zero-amount finalized
	// record. Repeating it after refunded is a no-op.
	CompleteBalancePreauthorizationRefund(ctx context.Context, requestID string, apiKeyID int64) error
	// ListRecoverableBalancePreauthorizations returns expired prepared/authorized
	// holds and stale finalizations. Callers supply distinct cutoffs so active
	// finalization is not raced by recovery.
	ListRecoverableBalancePreauthorizations(ctx context.Context, authorizationExpiredBefore, finalizationStaleBefore time.Time, limit int) ([]BalancePreauthorizationRecord, error)
	ReserveBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	CaptureBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	ReleaseBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
}

// UsageBalanceSettlementResult describes one user-level database update after
// multiple immutable request charges have been coalesced.
type UsageBalanceSettlementResult struct {
	UserID     int64
	EventCount int
	Amount     float64
	NewBalance float64
}

// UsageBalanceSettlementRepository is the durable consumer side of balance
// billing. Implementations must apply a selected batch and mark its events in
// the same database transaction so a crash can neither lose nor double-charge.
type UsageBalanceSettlementRepository interface {
	FlushPendingBalanceSettlements(ctx context.Context, limit int) ([]UsageBalanceSettlementResult, error)
	DeleteAppliedBalanceSettlements(ctx context.Context, before time.Time, limit int) (int64, error)
}

// QuantizeUsageBillingAmount 把金额舍入到 UsageBillingMonetaryScale 位小数，
// 采用与 PostgreSQL NUMERIC 一致的 half-away-from-zero 规则。
//
// 走 decimal 而不是 math.Round(v*1e8)/1e8：后者在乘除过程中会引入额外的二进制
// 误差，边界值可能被推到错误的一侧。decimal.NewFromFloat 取 float64 的最短十进制
// 表示，正是 PostgreSQL 把 float8 参数转成 numeric 时所用的表示。
func QuantizeUsageBillingAmount(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	quantized, _ := decimal.NewFromFloat(v).Round(UsageBillingMonetaryScale).Float64()
	return quantized
}
