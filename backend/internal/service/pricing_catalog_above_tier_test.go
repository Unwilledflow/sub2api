package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePricingData_AboveTierCatalog(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fields        map[string]any
		threshold     int
		input, output float64
	}{
		{name: "Gemini 200k", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 2.5e-6, "output_cost_per_token_above_200k_tokens": 15e-6}, threshold: 200000, input: 2, output: 1.5},
		{name: "explicit threshold zero disables", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 2.5e-6, "long_context_input_token_threshold": 0}},
		{name: "explicit input one disables", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 2.5e-6, "long_context_input_cost_multiplier": 1}, input: 1},
		{name: "explicit output zero disables", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 2.5e-6, "long_context_output_cost_multiplier": 0}},
		{name: "explicit complete policy wins", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 2.5e-6, "long_context_input_token_threshold": 300000, "long_context_input_cost_multiplier": 3, "long_context_output_cost_multiplier": 4}, threshold: 300000, input: 3, output: 4},
		{name: "smallest threshold", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 2.5e-6, "input_cost_per_token_above_400k_tokens": 5e-6}, threshold: 200000, input: 2, output: 1},
		{name: "cache and priority fields ignored", fields: map[string]any{"cache_read_input_token_cost_above_200k_tokens": 2.5e-7, "input_cost_per_token_above_200k_tokens_priority": 5e-6}},
		{name: "invalid and overflowing thresholds ignored", fields: map[string]any{"input_cost_per_token_above_0k_tokens": 2.5e-6, "input_cost_per_token_above_9223372036854775807k_tokens": 2.5e-6, "output_cost_per_token_above_200k_tokens": "invalid"}},
		{name: "no surcharge", fields: map[string]any{"input_cost_per_token_above_200k_tokens": 1.25e-6, "output_cost_per_token_above_200k_tokens": 10e-6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"input_cost_per_token": 1.25e-6, "output_cost_per_token": 10e-6, "cache_read_input_token_cost": 1.25e-7, "litellm_provider": "vertex_ai-language-models"}
			for key, value := range tc.fields {
				entry[key] = value
			}
			body, err := json.Marshal(map[string]any{"gemini-2.5-pro": entry, "unrelated-model": map[string]any{"input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6}})
			require.NoError(t, err)
			data, err := (&PricingService{}).parsePricingData(body)
			require.NoError(t, err)
			pricing := data["gemini-2.5-pro"]
			require.Equal(t, tc.threshold, pricing.LongContextInputTokenThreshold)
			require.InDelta(t, tc.input, pricing.LongContextInputCostMultiplier, 1e-12)
			require.InDelta(t, tc.output, pricing.LongContextOutputCostMultiplier, 1e-12)
			require.Equal(t, 1.25e-6, pricing.InputCostPerToken)
			require.Equal(t, 1.25e-7, pricing.CacheReadInputTokenCost)
			require.Zero(t, data["unrelated-model"].LongContextInputTokenThreshold)
			require.Zero(t, data["unrelated-model"].LongContextInputCostMultiplier)
		})
	}
}
