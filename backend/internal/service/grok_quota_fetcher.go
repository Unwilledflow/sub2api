package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const grokQuotaSnapshotExtraKey = "grok_usage_snapshot"

type GrokQuotaFetcher struct{}

func NewGrokQuotaFetcher() *GrokQuotaFetcher {
	return &GrokQuotaFetcher{}
}

func (f *GrokQuotaFetcher) BuildUsageInfo(account *Account) *UsageInfo {
	now := time.Now()
	usage := &UsageInfo{
		Source:             "passive",
		UpdatedAt:          &now,
		GrokFreeTokenLimit: xai.GrokFreeRolling24hTokenLimit,
	}
	if account == nil {
		usage.ErrorCode = "quota_unknown"
		usage.Error = "Grok quota is unknown until billing is probed or an upstream response includes xAI rate-limit headers"
		return usage
	}

	billing, _ := grokBillingSnapshotFromExtra(account.Extra)
	snapshot, err := grokQuotaSnapshotFromExtra(account.Extra)
	activeProbeClearsForbidden := newerSuccessfulGrokActiveProbeClearsBillingForbidden(billing, snapshot)
	if billing != nil {
		usage.GrokBilling = billing
		if billing.Plan != "" {
			usage.SubscriptionTier = billing.Plan
			usage.SubscriptionTierRaw = billing.Plan
		}
		if parsedAt, parseErr := time.Parse(time.RFC3339, billing.UpdatedAt); parseErr == nil {
			usage.UpdatedAt = &parsedAt
		}
		if billing.FetchedAt != "" {
			usage.GrokLastQuotaProbeAt = billing.FetchedAt
		}
		usage.GrokQuotaSnapshotState = "billing_observed"
		usage.GrokLastStatusCode = billing.StatusCode
		switch billing.StatusCode {
		case 401:
			usage.NeedsReauth = true
			usage.ErrorCode = "unauthenticated"
		case 403:
			usage.IsForbidden = true
			usage.ForbiddenType = "forbidden"
			usage.ErrorCode = "forbidden"
		case 429:
			usage.ErrorCode = "rate_limited"
		}
	}

	if err != nil || snapshot == nil {
		applyGrokCredentialUsageFallback(usage, account, billing, nil)
		if billing == nil {
			usage.ErrorCode = "quota_unknown"
			usage.Error = "Grok quota is unknown until billing is probed or an upstream response includes xAI rate-limit headers"
		}
		return usage
	}

	if parsedAt, parseErr := time.Parse(time.RFC3339, snapshot.UpdatedAt); parseErr == nil {
		if billing == nil || usage.UpdatedAt == nil || parsedAt.After(*usage.UpdatedAt) {
			usage.UpdatedAt = &parsedAt
		}
	}
	usage.GrokRequestQuota = snapshot.Requests
	usage.GrokTokenQuota = snapshot.Tokens
	usage.GrokRetryAfterSeconds = snapshot.RetryAfterSeconds
	if usage.SubscriptionTier == "" {
		usage.SubscriptionTier = snapshot.SubscriptionTier
		usage.SubscriptionTierRaw = snapshot.SubscriptionTier
	}
	if usage.GrokEntitlementStatus == "" {
		usage.GrokEntitlementStatus = snapshot.EntitlementStatus
	}
	if usage.GrokLastQuotaProbeAt == "" {
		usage.GrokLastQuotaProbeAt = snapshot.LastProbeAt
	}
	usage.GrokLastHeadersSeenAt = snapshot.LastHeadersSeenAt
	if activeProbeClearsForbidden {
		usage.IsForbidden = false
		usage.ForbiddenType = ""
		usage.ErrorCode = ""
		usage.GrokLastQuotaProbeAt = snapshot.LastProbeAt
		usage.GrokLastStatusCode = snapshot.StatusCode
	} else if snapshot.StatusCode >= http.StatusBadRequest || usage.GrokLastStatusCode == 0 {
		usage.GrokLastStatusCode = snapshot.StatusCode
	}
	if snapshot.HasObservedHeaders() {
		if usage.GrokQuotaSnapshotState == "" {
			usage.GrokQuotaSnapshotState = "observed"
		}
	} else if billing == nil {
		usage.GrokQuotaSnapshotState = "no_headers"
		usage.ErrorCode = "quota_unknown"
		usage.Error = "No xAI quota headers observed on the latest Grok probe"
	}

	if usage.ErrorCode == "" {
		switch snapshot.StatusCode {
		case 401:
			usage.NeedsReauth = true
			usage.ErrorCode = "unauthenticated"
		case 403:
			usage.IsForbidden = true
			usage.ForbiddenType = "forbidden"
			usage.ErrorCode = "forbidden"
			if usage.GrokEntitlementStatus == "" {
				usage.GrokEntitlementStatus = "forbidden"
			}
		case 429:
			usage.ErrorCode = "rate_limited"
		}
	}
	if accountGrokNeedsReauth(account) {
		usage.NeedsReauth = true
		if usage.ErrorCode == "" {
			usage.ErrorCode = "spending_limit"
		}
	}
	applyGrokCredentialUsageFallback(usage, account, billing, snapshot)
	if activeProbeClearsForbidden && strings.TrimSpace(snapshot.EntitlementStatus) == "" &&
		strings.EqualFold(strings.TrimSpace(usage.GrokEntitlementStatus), "forbidden") {
		usage.GrokEntitlementStatus = ""
	}
	return usage
}

func newerSuccessfulGrokActiveProbeClearsBillingForbidden(billing *xai.BillingSummary, snapshot *xai.QuotaSnapshot) bool {
	if billing == nil || billing.StatusCode != http.StatusForbidden || snapshot == nil ||
		snapshot.StatusCode != http.StatusOK || strings.TrimSpace(snapshot.ObservationSource) != "active_probe" {
		return false
	}

	billingAt, billingOK := firstGrokObservationTime(billing.UpdatedAt, billing.FetchedAt)
	probeAt, probeOK := firstGrokObservationTime(snapshot.LastProbeAt, snapshot.UpdatedAt)
	// Both snapshots use second precision, so a billing request followed by the
	// active probe in the same refresh can legitimately have equal timestamps.
	return billingOK && probeOK && !probeAt.Before(billingAt)
}

func firstGrokObservationTime(values ...string) (time.Time, bool) {
	for _, value := range values {
		parsedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
		if err == nil {
			return parsedAt, true
		}
	}
	return time.Time{}, false
}

func applyGrokCredentialUsageFallback(usage *UsageInfo, account *Account, billing *xai.BillingSummary, snapshot *xai.QuotaSnapshot) {
	if usage == nil || account == nil {
		return
	}
	if usage.GrokEntitlementStatus == "" {
		usage.GrokEntitlementStatus = strings.TrimSpace(account.GetCredential("entitlement_status"))
	}
	applyGrokResolvedSubscriptionTier(usage, account, billing, snapshot)
}

func applyGrokResolvedSubscriptionTier(usage *UsageInfo, account *Account, billing *xai.BillingSummary, snapshot *xai.QuotaSnapshot) {
	if usage == nil || account == nil {
		return
	}
	if jwtTier := xai.SubscriptionTierFromJWT(account.GetCredential("access_token")); jwtTier != "" {
		usage.SubscriptionTier = jwtTier
		usage.SubscriptionTierRaw = jwtTier
		return
	}
	signal := strings.TrimSpace(account.GetCredential("subscription_tier"))
	if signal == "" && snapshot != nil {
		signal = strings.TrimSpace(snapshot.SubscriptionTier)
	}
	if signal == "" && billing != nil {
		signal = strings.TrimSpace(billing.Plan)
	}
	var limit *float64
	if billing != nil {
		limit = billing.MonthlyLimitCents
	}
	if plan := xai.CanonicalGrokPlan(limit, signal, snapshot); plan != "" {
		usage.SubscriptionTier = plan
		if usage.SubscriptionTierRaw == "" {
			usage.SubscriptionTierRaw = firstNonEmpty(signal, plan)
		}
		return
	}
	if usage.SubscriptionTier == "" && signal != "" {
		usage.SubscriptionTier = signal
		usage.SubscriptionTierRaw = signal
	}
}

func grokBillingSnapshotFromExtra(extra map[string]any) (*xai.BillingSummary, error) {
	return grokSnapshotFromExtra[xai.BillingSummary](extra, grokBillingExtraKey, "grok billing snapshot")
}

func stampGrokQuotaSnapshotForPlan(account *Account, snapshot *xai.QuotaSnapshot, model string) {
	if snapshot == nil {
		return
	}
	if strings.TrimSpace(snapshot.Model) == "" {
		model = strings.TrimSpace(model)
		if model != "" {
			snapshot.Model = xai.ResolveGrokTextResponsesModelID(model)
		}
	}
	var prev *xai.QuotaSnapshot
	if account != nil {
		prev, _ = grokQuotaSnapshotFromExtra(account.Extra)
	}
	snapshot.ApplyGrok45ResponsesPlanSignal(prev)
}

func grokQuotaSnapshotFromExtra(extra map[string]any) (*xai.QuotaSnapshot, error) {
	return grokSnapshotFromExtra[xai.QuotaSnapshot](extra, grokQuotaSnapshotExtraKey, "grok quota snapshot")
}

func grokSnapshotFromExtra[T any](extra map[string]any, key, label string) (*T, error) {
	if extra == nil {
		return nil, nil
	}
	raw, ok := extra[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch snapshot := raw.(type) {
	case *T:
		return snapshot, nil
	case T:
		return &snapshot, nil
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", label, err)
		}
		var out T
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("unmarshal %s: %w", label, err)
		}
		return &out, nil
	}
}
