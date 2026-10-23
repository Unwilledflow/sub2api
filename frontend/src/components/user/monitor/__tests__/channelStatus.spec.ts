import { describe, expect, it } from 'vitest'
import type { MonitorHealth, MonitorMatrixBucket, MonitorMetric } from '@/api/channelMonitorV2'
import {
  CARD_MIN_CACHE_TOKENS,
  CARD_MIN_SAMPLE,
  cacheRateIsMeaningful,
  resolveCardMetrics,
  ttftIsMeaningful,
} from '../channelStatus'

function metrics(overrides: Partial<MonitorMetric> = {}): MonitorMetric {
  return {
    success_requests: 0,
    error_requests: 0,
    request_count: 0,
    sample_count: 0,
    token_count: 0,
    rpm: 0,
    tpm: 0,
    error_rate: 0,
    cache_rate: 0,
    cache_rate_numerator: 0,
    cache_rate_denominator: 0,
    ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    ...overrides,
  }
}

function health(overall: MonitorHealth['overall'] = 'healthy'): MonitorHealth {
  return { overall, error_rate: overall, ttft: overall, cache: overall, minimum_sample: 50 }
}

function bucket(start: string, m: MonitorMetric): MonitorMatrixBucket {
  return { bucket_start: start, metrics: m, health: health() }
}

describe('resolveCardMetrics', () => {
  it('shows the newest window that carries traffic, even when it is thin', () => {
    const row = {
      metrics: metrics({ sample_count: 500, request_count: 500 }),
      health: health(),
      buckets: [
        bucket('2026-09-26T05:00:00Z', metrics({ sample_count: 300, cache_rate: 0.8 })),
        // The newest window is thin, but it is what is happening now. Walking
        // back to the older, heavier window printed an hour-old cache rate as
        // if it were current; the card names its window instead.
        bucket('2026-09-26T05:05:00Z', metrics({ sample_count: 6, cache_rate: 1 })),
      ],
    }
    const card = resolveCardMetrics(row)
    expect(card.bucket?.bucket_start).toBe('2026-09-26T05:05:00Z')
    expect(card.metrics.cache_rate).toBe(1)
    expect(card.fromRange).toBe(false)
  })

  it('falls back to the range aggregate only when no window has traffic', () => {
    const row = {
      metrics: metrics({ sample_count: 900, request_count: 900, cache_rate: 0.75 }),
      health: health(),
      buckets: [
        bucket('2026-09-26T05:00:00Z', metrics({ sample_count: 0, cache_rate: 0 })),
        bucket('2026-09-26T05:05:00Z', metrics({ sample_count: 0, cache_rate: 0 })),
      ],
    }
    const card = resolveCardMetrics(row)
    expect(card.bucket).toBeNull()
    expect(card.metrics.cache_rate).toBe(0.75)
    expect(card.fromRange).toBe(true)
    expect(card.insufficient).toBe(false)
  })

  it('flags insufficiency when neither a window nor the aggregate qualifies', () => {
    const row = {
      metrics: metrics({ sample_count: 1, cache_rate: 1 }),
      health: health('healthy'),
      buckets: [bucket('2026-09-26T05:00:00Z', metrics({ sample_count: 1, cache_rate: 1 }))],
    }
    const card = resolveCardMetrics(row)
    expect(card.insufficient).toBe(true)
  })

  it('honours the backend floor it is given without changing which window is shown', () => {
    const row = {
      metrics: metrics({ sample_count: 12 }),
      health: health(),
      buckets: [bucket('2026-09-26T05:00:00Z', metrics({ sample_count: 12, cache_rate: 0.5 }))],
    }
    // The window is always the newest one; the floor only grades its weight.
    expect(resolveCardMetrics(row).bucket?.bucket_start).toBe('2026-09-26T05:00:00Z')
    expect(resolveCardMetrics(row, 20).insufficient).toBe(true)
    expect(resolveCardMetrics(row, 10).insufficient).toBe(false)
  })

  it('reads sample_count rather than the redacted request_count', () => {
    // The public API zeroes request_count but keeps sample_count.
    const row = {
      metrics: metrics({ sample_count: 400, request_count: 0 }),
      health: health(),
      buckets: [],
    }
    const card = resolveCardMetrics(row)
    expect(card.requestCount).toBe(400)
    expect(card.insufficient).toBe(false)
  })

  it('ignores buckets without a timestamp or metrics', () => {
    const row = {
      metrics: metrics({ sample_count: 100 }),
      health: health(),
      buckets: [
        { bucket_start: '', metrics: metrics({ sample_count: 999 }), health: health() },
        { bucket_start: '2026-09-26T05:00:00Z', metrics: undefined as unknown as MonitorMetric, health: health() },
      ],
    }
    expect(resolveCardMetrics(row).bucket).toBeNull()
  })

  it('uses the same sample floor the backend grades with', () => {
    // Production runs health_thresholds.minimum_sample = 5. A higher floor here
    // made the card reject windows the backend had already graded.
    expect(CARD_MIN_SAMPLE).toBe(5)
  })
})

describe('cacheRateIsMeaningful', () => {
  it('rejects a cache rate built on very few cached tokens', () => {
    const row = {
      metrics: metrics({ sample_count: 60, cache_rate_denominator: 12, cache_rate: 1 }),
      health: health(),
      buckets: [],
    }
    // 60 requests but only 12 cache-scored tokens: 100% is not a real hit rate.
    expect(cacheRateIsMeaningful(resolveCardMetrics(row))).toBe(false)
  })

  it('accepts a cache rate with a substantial denominator', () => {
    const row = {
      metrics: metrics({ sample_count: 60, cache_rate_denominator: CARD_MIN_CACHE_TOKENS }),
      health: health(),
      buckets: [],
    }
    expect(cacheRateIsMeaningful(resolveCardMetrics(row))).toBe(true)
  })

  it('rejects everything when the whole card is insufficient', () => {
    const row = { metrics: metrics({ sample_count: 1 }), health: health(), buckets: [] }
    expect(cacheRateIsMeaningful(resolveCardMetrics(row))).toBe(false)
  })
})

describe('ttftIsMeaningful', () => {
  it('rejects a percentile derived from a handful of samples', () => {
    const row = {
      metrics: metrics({
        sample_count: 80,
        ttft: { sample_count: 3, p50_ms: 120, p95_ms: 200, avg_ms: 130 },
      }),
      health: health(),
      buckets: [],
    }
    expect(ttftIsMeaningful(resolveCardMetrics(row))).toBe(false)
  })

  it('accepts a percentile with enough samples', () => {
    const row = {
      metrics: metrics({
        sample_count: 80,
        ttft: { sample_count: 80, p50_ms: 120, p95_ms: 200, avg_ms: 130 },
      }),
      health: health(),
      buckets: [],
    }
    expect(ttftIsMeaningful(resolveCardMetrics(row))).toBe(true)
  })
})
