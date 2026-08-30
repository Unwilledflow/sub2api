package service

import (
	"math"
	"strings"
)

const (
	AdaptiveRoutingPreferenceIntelligence = "intelligence"
	AdaptiveRoutingPreferencePrice        = "price"
)

// NormalizeAdaptiveMaxRateMultiplier returns nil for unlimited (nil input or <0).
// Zero is a valid ceiling (only free leaves). NaN/Inf are rejected as unlimited.
func NormalizeAdaptiveMaxRateMultiplier(raw *float64) *float64 {
	if raw == nil {
		return nil
	}
	v := *raw
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return nil
	}
	out := v
	return &out
}

// NormalizeAdaptiveRoutingPreference validates and defaults preference.
func NormalizeAdaptiveRoutingPreference(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return AdaptiveRoutingPreferenceIntelligence, nil
	}
	switch v {
	case AdaptiveRoutingPreferenceIntelligence, AdaptiveRoutingPreferencePrice:
		return v, nil
	default:
		return "", ErrInvalidAdaptiveRoutingPreference
	}
}

// AdaptiveRouteModeFromPreference maps API key preference to planner mode.
func AdaptiveRouteModeFromPreference(pref string) AdaptiveRouteMode {
	if strings.EqualFold(strings.TrimSpace(pref), AdaptiveRoutingPreferencePrice) {
		return AdaptiveRouteModePrice
	}
	return AdaptiveRouteModeIntelligence
}
