//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGetModelPricing_AstraAndLunaDynamicCatalogPrecedesFallback(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			card := &LiteLLMModelPricing{
				InputCostPerToken: 17e-6, OutputCostPerToken: 29e-6,
				CacheCreationInputTokenCost: 19e-6, CacheReadInputTokenCost: 3e-6,
				LongContextInputTokenThreshold: 123456,
				LongContextInputCostMultiplier: 3, LongContextOutputCostMultiplier: 4,
			}
			original := *card
			svc := NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: card}})
			pricing, err := svc.GetModelPricing(model)
			require.NoError(t, err)
			require.Equal(t, card.InputCostPerToken, pricing.InputPricePerToken)
			require.Equal(t, card.OutputCostPerToken, pricing.OutputPricePerToken)
			require.Equal(t, card.CacheCreationInputTokenCost, pricing.CacheCreationPricePerToken)
			require.Equal(t, card.CacheReadInputTokenCost, pricing.CacheReadPricePerToken)
			require.Equal(t, 123456, pricing.LongContextInputThreshold)
			require.Equal(t, 3.0, pricing.LongContextInputMultiplier)
			require.Equal(t, 4.0, pricing.LongContextOutputMultiplier)
			require.Equal(t, original, *card)
		})
	}
}
