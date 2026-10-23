import { describe, expect, it } from 'vitest'
import { apiIntervalsToForm, collectChannelModelPricing, createDefaultTimePricingForm, formIntervalsToAPI, type PricingFormEntry } from '../types'

describe('pricing TTL and effort roundtrip', () => {
  it('converts hourly cache prices and isolates effort maps without defaults', () => {
    const entry: PricingFormEntry = {
      models: ['custom-model'], billing_mode: 'token', input_price: null, output_price: null,
      cache_write_price: 2, cache_write_1h_price: 4, cache_read_price: null,
      reasoning_effort_multipliers: { high: 1.25, max: 2 },
      image_input_price: null, image_output_price: null, per_request_price: null,
      intervals: [], time_pricing: createDefaultTimePricingForm(),
    }
    const [saved] = collectChannelModelPricing([{ platform: 'anthropic', model_pricing: [entry] }])
    expect(saved.cache_write_1h_price).toBe(4e-6)
    expect(saved.reasoning_effort_multipliers).toEqual({ high: 1.25, max: 2 })
    expect(saved.reasoning_effort_multipliers).not.toBe(entry.reasoning_effort_multipliers)
    const [unset] = collectChannelModelPricing([{ platform: 'anthropic', model_pricing: [{ ...entry, reasoning_effort_multipliers: null }] }])
    expect(unset.reasoning_effort_multipliers).toBeNull()
    const intervals = [{ min_tokens: 0, max_tokens: null, tier_label: '', input_price: null, output_price: null,
      cache_write_price: null, cache_write_1h_price: 4e-6, cache_read_price: null, input_multiplier: null,
      output_multiplier: null, cache_write_multiplier: null, cache_read_multiplier: null, per_request_price: null, sort_order: 0 }]
    expect(apiIntervalsToForm(intervals)[0].cache_write_1h_price).toBe(4)
    expect(formIntervalsToAPI(apiIntervalsToForm(intervals))).toEqual(intervals)
  })
})
