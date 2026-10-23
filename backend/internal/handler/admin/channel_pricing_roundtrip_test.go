package admin

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChannelPricingCacheTTLAndEffortRoundTrip(t *testing.T) {
	var req updateChannelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model_pricing":[{"models":["claude-fable-5-1"],"cache_write_1h_price":0.00002,"reasoning_effort_multipliers":{"max":1.5},"intervals":[{"min_tokens":0,"cache_write_1h_price":0.00003}]}]}`), &req))
	require.NotNil(t, req.ModelPricing)
	pricing := pricingRequestToService(*req.ModelPricing, true)
	require.Len(t, pricing, 1)
	require.NotNil(t, pricing[0].CacheWrite1hPrice)
	require.Equal(t, 0.00002, *pricing[0].CacheWrite1hPrice)
	require.Equal(t, 1.5, pricing[0].ReasoningEffortMultipliers["max"])
	require.Len(t, pricing[0].Intervals, 1)
	require.Equal(t, 0.00003, *pricing[0].Intervals[0].CacheWrite1hPrice)
	body, err := json.Marshal(pricingToResponse(&pricing[0]))
	require.NoError(t, err)
	var response map[string]any
	require.NoError(t, json.Unmarshal(body, &response))
	require.Equal(t, 0.00002, response["cache_write_1h_price"])
	require.Equal(t, map[string]any{"max": 1.5}, response["reasoning_effort_multipliers"])
	require.Equal(t, 0.00003, response["intervals"].([]any)[0].(map[string]any)["cache_write_1h_price"])
	stats := pricingRequestToService(*req.ModelPricing, false)
	require.Nil(t, stats[0].ReasoningEffortMultipliers)
	require.Equal(t, pricing[0].CacheWrite1hPrice, stats[0].CacheWrite1hPrice)
}
