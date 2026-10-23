package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AccountTrafficPolicyKey = "account_traffic_control"

type AccountTrafficPolicy struct {
	StrictRPMEnabled     bool   `json:"strict_rpm_enabled"`
	RPM                  int    `json:"rpm"`
	Burst                int    `json:"burst"`
	AdaptiveEnabled      bool   `json:"adaptive_enabled"`
	AdaptiveMode         string `json:"adaptive_mode"`
	MinConcurrency       int    `json:"min_concurrency"`
	FailureThreshold     int    `json:"failure_threshold"`
	FailureWindowSeconds int    `json:"failure_window_seconds"`
	RecoverySeconds      int    `json:"recovery_seconds"`
}

// Adaptive concurrency is intentionally not tunable per account. Operators only
// choose whether it runs and whether it is allowed to touch live slots; the
// controller's timing and floor are fixed so behaviour is uniform and nobody
// has to reason about six interacting numbers to know what an account will do.
const (
	// accountTrafficFloorConcurrency is the lowest concurrency the controller
	// may settle on. A pool throttled below this cannot absorb normal traffic,
	// so the floor is deliberately well above 1.
	accountTrafficFloorConcurrency = 50
	// accountTrafficFailureThreshold is how many upstream 429s inside the
	// window are needed before backing off.
	accountTrafficFailureThreshold = 3
	// accountTrafficFailureWindowSeconds is the sliding window for those 429s.
	accountTrafficFailureWindowSeconds = 60
	// accountTrafficRecoverySeconds is the quiet period required before the
	// controller starts giving concurrency back.
	accountTrafficRecoverySeconds = 60
)

func DefaultAccountTrafficPolicy() AccountTrafficPolicy {
	return AccountTrafficPolicy{
		RPM:                  60,
		Burst:                5,
		AdaptiveEnabled:      true,
		AdaptiveMode:         "automatic",
		MinConcurrency:       accountTrafficFloorConcurrency,
		FailureThreshold:     accountTrafficFailureThreshold,
		FailureWindowSeconds: accountTrafficFailureWindowSeconds,
		RecoverySeconds:      accountTrafficRecoverySeconds,
	}
}

// normalizeAccountTrafficPolicy pins the knobs that are no longer operator
// facing. Accounts saved before this simplification keep their stored JSON (the
// fields stay in the struct so old payloads still decode), but the controller
// always runs on the fixed timing and floor.
func normalizeAccountTrafficPolicy(p AccountTrafficPolicy) AccountTrafficPolicy {
	p.MinConcurrency = accountTrafficFloorConcurrency
	p.FailureThreshold = accountTrafficFailureThreshold
	p.FailureWindowSeconds = accountTrafficFailureWindowSeconds
	p.RecoverySeconds = accountTrafficRecoverySeconds
	return p
}

func ensureDefaultAccountTrafficPolicy(extra map[string]any) (map[string]any, error) {
	if extra == nil {
		extra = map[string]any{}
	}
	if extra[AccountTrafficPolicyKey] != nil {
		if _, err := ParseAccountTrafficPolicy(extra); err != nil {
			return extra, err
		}
		return extra, nil
	}
	encoded, err := json.Marshal(DefaultAccountTrafficPolicy())
	if err != nil {
		return extra, err
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return extra, err
	}
	extra[AccountTrafficPolicyKey] = value
	return extra, nil
}

func unsetAccountTrafficPolicy() AccountTrafficPolicy {
	return AccountTrafficPolicy{RPM: 60, Burst: 5, AdaptiveMode: "observe", MinConcurrency: accountTrafficFloorConcurrency, FailureThreshold: accountTrafficFailureThreshold, FailureWindowSeconds: accountTrafficFailureWindowSeconds, RecoverySeconds: accountTrafficRecoverySeconds}
}

func ParseAccountTrafficPolicy(extra map[string]any) (AccountTrafficPolicy, error) {
	p := unsetAccountTrafficPolicy()
	value, exists := extra[AccountTrafficPolicyKey]
	if !exists || value == nil {
		return p, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return p, err
	}
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, infraerrors.BadRequest("ACCOUNT_TRAFFIC_INVALID", "流量控制配置格式无效")
	}
	// Only the operator-facing switches are validated; the adaptive timing and
	// floor are pinned so legacy or hand-edited values cannot skew behaviour.
	if p.RPM < 1 || p.RPM > 60000 || p.Burst < 1 || p.Burst > p.RPM || (p.AdaptiveMode != "observe" && p.AdaptiveMode != "automatic") {
		return p, infraerrors.BadRequest("ACCOUNT_TRAFFIC_INVALID", "请检查 RPM 与突发额度；突发额度不能大于 RPM")
	}
	return normalizeAccountTrafficPolicy(p), nil
}

func (p AccountTrafficPolicy) Enabled() bool { return p.StrictRPMEnabled || p.AdaptiveEnabled }

func (p AccountTrafficPolicy) Enforces() bool {
	return p.StrictRPMEnabled || (p.AdaptiveEnabled && p.AdaptiveMode == "automatic")
}

type AccountTrafficPlan struct {
	AccountID int64
	Revision  int64
	Signature string
	HardLimit int
	Policy    AccountTrafficPolicy
}

func AccountTrafficPlanFor(a *Account) (AccountTrafficPlan, error) {
	if a == nil {
		return AccountTrafficPlan{}, nil
	}
	p, err := ParseAccountTrafficPolicy(a.Extra)
	if err != nil {
		return AccountTrafficPlan{}, err
	}
	limit := a.Mode1EffectiveConcurrency()
	if p.AdaptiveEnabled && limit < 0 {
		return AccountTrafficPlan{}, infraerrors.BadRequest("ACCOUNT_TRAFFIC_INVALID", "账号并发上限无效")
	}
	hard := adaptiveHardLimit(a, limit, p)
	encoded, _ := json.Marshal(struct {
		Policy AccountTrafficPolicy
		Limit  int
	}{p, hard})
	revision := int64(0)
	if !a.UpdatedAt.IsZero() {
		revision = a.UpdatedAt.UnixMicro()
	}
	return AccountTrafficPlan{AccountID: a.ID, Revision: revision, Signature: fmt.Sprintf("%x", sha256.Sum256(encoded)), HardLimit: hard, Policy: p}, nil
}

// accountTrafficFormDefaultConcurrency is the concurrency the create-account
// form writes into every new account. Operators almost never tune it, so it
// must not silently become a hard ceiling for adaptive mode: an account born
// with the default 10 rejected traffic at 10 in-flight while upstream was
// healthy, and adaptive could never lift it because the administrator's cap
// always wins.
const accountTrafficFormDefaultConcurrency = 10

// accountTrafficAutomaticCeiling is the operating ceiling used when adaptive
// mode is automatic and the saved concurrency is only the form default. With a
// floor of 50 the controller needs real headroom above it, otherwise backing
// off and recovering would happen inside a two-number band and the feature
// would be pointless.
const accountTrafficAutomaticCeiling = 300

// adaptiveHardLimit reports the ceiling the traffic controller should enforce.
//
// Adaptive mode owns concurrency, so the ceiling must be able to hold the
// configured floor: an account saved with a small value (typically the 10 the
// create form writes) would otherwise cap adaptive below its own floor and
// reject healthy traffic. Explicit larger caps and 0 (unlimited) are honoured
// unchanged, so an operator who really wants a tight account can still set a
// value above the floor.
func adaptiveHardLimit(a *Account, limit int, p AccountTrafficPolicy) int {
	if a == nil || !p.AdaptiveEnabled || p.AdaptiveMode != "automatic" {
		return limit
	}
	if limit <= 0 {
		return limit
	}
	if limit >= accountTrafficFloorConcurrency {
		return limit
	}
	if limit == accountTrafficFormDefaultConcurrency {
		return accountTrafficAutomaticCeiling
	}
	// Any other sub-floor value is a deliberate narrow cap; lift it only as far
	// as the floor so the account is usable, without inventing extra headroom.
	return accountTrafficFloorConcurrency
}

// SchedulingConcurrency is the slot ceiling the scheduler should acquire for
// this account. It mirrors adaptiveHardLimit so both limiters agree: when
// adaptive mode owns the account the traffic controller must not be undercut
// by a stale create-form default in the slot layer.
func (a *Account) SchedulingConcurrency() int {
	if a == nil {
		return 0
	}
	p, err := ParseAccountTrafficPolicy(a.Extra)
	if err != nil {
		return a.Concurrency
	}
	return adaptiveHardLimit(a, a.Mode1EffectiveConcurrency(), p)
}

type accountTrafficPlanKey struct{}
type accountTrafficCoveredKey struct{}
type accountTrafficConfigErrorKey struct{}

// Attach only safe policy metadata, never account credentials. GET discovery
// and status polling are outside the model-request budget.
func WithAccountTrafficRequest(req *http.Request, a *Account) *http.Request {
	if req == nil || req.Method == http.MethodGet || req.Method == http.MethodHead {
		return req
	}
	if a == nil || a.Extra[AccountTrafficPolicyKey] == nil {
		return req
	}
	plan, err := AccountTrafficPlanFor(a)
	if err != nil {
		return req.WithContext(context.WithValue(req.Context(), accountTrafficConfigErrorKey{}, err))
	}
	if !plan.Policy.Enabled() {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), accountTrafficPlanKey{}, plan))
}

type AccountTrafficState struct {
	EffectiveConcurrency   int        `json:"effective_concurrency"`
	RecommendedConcurrency int        `json:"recommended_concurrency"`
	InFlight               int        `json:"in_flight"`
	RequestsLastMinute     int        `json:"requests_last_minute"`
	Accepted               int64      `json:"accepted"`
	RejectedRPM            int64      `json:"rejected_rpm"`
	RejectedConcurrency    int64      `json:"rejected_concurrency"`
	Upstream429            int64      `json:"upstream_429"`
	Upstream5xx            int64      `json:"upstream_5xx"`
	Completed              int64      `json:"completed"`
	AverageDurationMS      float64    `json:"average_duration_ms"`
	LastAdjustmentAt       *time.Time `json:"last_adjustment_at,omitempty"`
}

type AccountTrafficAdmission struct {
	Allowed         bool
	SkipObservation bool
	Reason          string
	RetryAfter      time.Duration
	State           AccountTrafficState
}

type AccountTrafficCache interface {
	Acquire(context.Context, AccountTrafficPlan, string) (AccountTrafficAdmission, error)
	Refresh(context.Context, int64, string) (bool, error)
	Finish(context.Context, AccountTrafficPlan, string, int, int64) error
	Snapshot(context.Context, AccountTrafficPlan) (AccountTrafficState, error)
	Sync(context.Context, AccountTrafficPlan) error
}

type AccountTrafficProvider interface{ AccountTrafficController() *AccountTrafficService }

type AccountTrafficLimitError struct {
	Reason     string
	RetryAfter time.Duration
	Status     int
}

func (e *AccountTrafficLimitError) Error() string { return "ACCOUNT_TRAFFIC_LIMIT: " + e.Reason }

func (e *AccountTrafficLimitError) FailoverError() *UpstreamFailoverError {
	status := e.Status
	if status == 0 {
		status = 429
	}
	seconds := int64((e.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	errorType := AccountTrafficErrorType(status)
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"type": errorType, "code": "account_traffic_limit", "message": e.Reason}})
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: http.Header{"Retry-After": []string{fmt.Sprint(seconds)}}, RequestScopedTransient: true, Scope: GatewayFailureScopeAccount, Reason: "account_traffic_limit", NextAccountAction: NextAccountRetry, ClientStatusCode: status, ClientMessage: e.Reason}
}
func (e *AccountTrafficLimitError) As(target any) bool {
	if p, ok := target.(**UpstreamFailoverError); ok {
		*p = e.FailoverError()
		return true
	}
	return false
}

func AccountTrafficErrorType(status int) string {
	if status == http.StatusBadRequest {
		return "invalid_request_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "rate_limit_error"
}
