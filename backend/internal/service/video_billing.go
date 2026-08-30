package service

import (
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	VideoPriceFamilyGrokImagineVideo   = "grok-imagine-video"
	VideoPriceFamilyGrokImagineVideo15 = "grok-imagine-video-1.5"
)

// CanonicalGrokImagineVideoPriceFamily maps known aliases onto stable price keys.
func CanonicalGrokImagineVideoPriceFamily(model string) string {
	if model == "" {
		return ""
	}
	if canonical := xai.CanonicalImagineVideoModel(model); canonical != "" {
		switch canonical {
		case xai.DefaultImagineVideo15Model:
			return VideoPriceFamilyGrokImagineVideo15
		case xai.DefaultImagineVideoModel:
			return VideoPriceFamilyGrokImagineVideo
		}
		if strings.HasPrefix(canonical, "grok-imagine-video-") {
			return canonical
		}
	}
	model = strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"xai/", "x-ai/", "grok/"} {
		if strings.HasPrefix(model, prefix) {
			model = strings.TrimPrefix(model, prefix)
			break
		}
	}
	switch {
	case model == "grok-imagine-video-1.5" || model == "grok-imagine-video-1.5-preview" || model == "grok-video-1.5" || strings.Contains(model, "video-1.5"):
		return VideoPriceFamilyGrokImagineVideo15
	case model == "grok-imagine-video" || model == "grok-imagine-video-preview" || model == "grok-video" || model == "grok-video-latest":
		return VideoPriceFamilyGrokImagineVideo
	default:
		return ""
	}
}

// NormalizeVideoModelPrices canonicalizes model aliases and supported tiers.
func NormalizeVideoModelPrices(in map[string]map[string]float64) map[string]map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	modelKeys := make([]string, 0, len(in))
	for model := range in {
		modelKeys = append(modelKeys, model)
	}
	sort.Strings(modelKeys)
	out := make(map[string]map[string]float64)
	for _, model := range modelKeys {
		family := CanonicalGrokImagineVideoPriceFamily(model)
		if family == "" {
			family = strings.ToLower(strings.TrimSpace(model))
		}
		if family == "" {
			continue
		}
		tiers := out[family]
		if tiers == nil {
			tiers = make(map[string]float64)
		}
		keys := make([]string, 0, len(in[model]))
		for tier := range in[model] {
			keys = append(keys, tier)
		}
		sort.Strings(keys)
		for _, tier := range keys {
			price := in[model][tier]
			normalized, ok := LookupVideoBillingResolution(tier)
			if price >= 0 && ok {
				tiers[normalized] = price
			}
		}
		if len(tiers) > 0 {
			out[family] = tiers
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LookupVideoModelPrice returns a model-specific price or nil for flat/default fallback.
func LookupVideoModelPrice(prices map[string]map[string]float64, model, resolution string) *float64 {
	if len(prices) == 0 {
		return nil
	}
	family := CanonicalGrokImagineVideoPriceFamily(model)
	if family == "" {
		family = strings.ToLower(strings.TrimSpace(model))
	}
	if tiers := prices[family]; len(tiers) > 0 {
		if price, ok := tiers[NormalizeVideoBillingResolutionOrDefault(resolution)]; ok {
			return &price
		}
	}
	return nil
}
