import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixBucket, MonitorMetric, MonitorQualityBucket } from '@/api/channelMonitorV2'
import ChannelMonitorV3Timeline from '../ChannelMonitorV3Timeline.vue'
import { bucketSpanUnit, bucketSpanValue } from '../channelStatus'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      locale: { value: 'zh-CN' },
    }),
  }
})

function metricsWith(overrides: Partial<MonitorMetric>): MonitorMetric {
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

function health(overall: MonitorHealth['overall']): MonitorHealth {
  return { overall, error_rate: overall, ttft: overall, cache: overall, minimum_sample: 50 }
}

function bucket(start: string): MonitorMatrixBucket {
  return { bucket_start: start, metrics: metricsWith({ sample_count: 100 }), health: health('healthy') }
}

function quality(start: string, checked: number, degraded: number, extra: Partial<MonitorQualityBucket> = {}): MonitorQualityBucket {
  return { bucket_start: start, checked, degraded, ...extra }
}

function mountTimeline(props: Record<string, unknown>) {
  return mount(ChannelMonitorV3Timeline, {
    props: { countdownSeconds: 0, length: 4, ...props },
    global: { stubs: { ChannelMonitorV3QualityHistory: true } },
  })
}

describe('ChannelMonitorV3Timeline quality strip', () => {
  it('renders no degradation strip when detection is disabled and no buckets exist', () => {
    const wrapper = mountTimeline({ buckets: [bucket('2026-09-26T05:00:00Z')] })
    expect(wrapper.find('[data-testid="channel-quality-timeline"]').exists()).toBe(false)
  })

  it('no longer renders degradation as a second row of green bars', () => {
    const wrapper = mountTimeline({
      buckets: [bucket('2026-09-26T05:00:00Z'), bucket('2026-09-26T05:05:00Z')],
      qualityBuckets: [quality('2026-09-26T05:05:00Z', 6, 2)],
    })
    // The bar-row degradation strip was removed: probes render as artwork in
    // ChannelMonitorV3QualityHistory, so the timeline keeps a single bar row.
    expect(wrapper.find('[data-testid="channel-quality-timeline"]').exists()).toBe(false)
    expect(wrapper.findAll('.v3-timeline-bars')).toHaveLength(1)
  })

  it('forwards the group id so the degradation strip can fetch its probes', () => {
    const wrapper = mountTimeline({
      buckets: [bucket('2026-09-26T05:00:00Z')],
      qualityEnabled: true,
      groupId: 43,
    })
    const strip = wrapper.getComponent({ name: 'ChannelMonitorV3QualityHistory' })
    expect(strip.props('groupId')).toBe(43)
  })

  it('enables the strip when probes exist even if the flag is missing', () => {
    const wrapper = mountTimeline({
      buckets: [bucket('2026-09-26T05:00:00Z')],
      qualityBuckets: [quality('2026-09-26T05:00:00Z', 4, 0)],
    })
    expect(wrapper.getComponent({ name: 'ChannelMonitorV3QualityHistory' }).props('enabled')).toBe(true)
  })

  it('places a data gap at the NOW end, not at the past end', () => {
    // Three buckets of history plus one missing trailing slot.
    const wrapper = mountTimeline({
      length: 4,
      buckets: [
        bucket('2026-09-26T04:00:00Z'),
        bucket('2026-09-26T04:05:00Z'),
        bucket('2026-09-26T04:10:00Z'),
      ],
    })
    const bars = wrapper.findAll('.v3-timeline-bars')[0].findAll('.v3-soft-glass-bar')
    expect(bars.length).toBe(4)
    // Oldest slots carry real availability bars; the trailing slot is the
    // gray insufficient-sample bar.
    expect(bars[0].classes()).not.toContain('bg-gray-300')
    expect(bars[1].classes()).not.toContain('bg-gray-300')
    expect(bars[2].classes()).not.toContain('bg-gray-300')
    expect(bars[3].classes()).toContain('bg-gray-300')
  })

  it('keeps availability bars painted by availability, not by the health label', () => {
    const wrapper = mountTimeline({
      length: 2,
      buckets: [bucket('2026-09-26T04:00:00Z'), bucket('2026-09-26T04:05:00Z')],
    })
    const bars = wrapper.findAll('.v3-timeline-bars')[0].findAll('.v3-soft-glass-bar')
    // error_rate 0 → 100% availability → deep green.
    expect(bars[0].classes()).toContain('bg-emerald-600')
  })

  it('paints a low-traffic bucket by its availability instead of hiding it as unknown', () => {
    // Health is "unknown" below the scoring floor, which is most low-traffic
    // groups. The bucket still carries real traffic, so it must not be gray.
    const lowTraffic = metricsWith({ sample_count: 4, request_count: 4, error_rate: 0.25 })
    const wrapper = mountTimeline({
      length: 1,
      buckets: [{ bucket_start: '2026-09-26T04:00:00Z', metrics: lowTraffic, health: health('unknown') }],
    })
    const bar = wrapper.findAll('.v3-timeline-bars')[0].findAll('.v3-soft-glass-bar')[0]
    // 75% availability sits in the 60–80 band.
    expect(bar.classes()).toContain('bg-yellow-300')
    expect(bar.classes()).not.toContain('bg-gray-300')
  })

  it('labels the span each slot covers instead of implying one sample per bar', () => {
    const wrapper = mountTimeline({
      length: 2,
      bucketSeconds: 300,
      buckets: [bucket('2026-09-26T04:00:00Z'), bucket('2026-09-26T04:05:00Z')],
    })
    expect(wrapper.text()).toContain('monitorCommon.lastNBuckets')
    expect(wrapper.text()).toContain('monitorCommon.bucketSpan.minute')
  })
})

describe('bucket span formatting', () => {
  it('picks the coarsest whole unit', () => {
    expect(bucketSpanUnit(300)).toBe('minute')
    expect(bucketSpanUnit(3600)).toBe('hour')
    expect(bucketSpanUnit(43200)).toBe('hour')
    expect(bucketSpanUnit(86400)).toBe('day')
  })

  it('reports the width in that unit', () => {
    expect(bucketSpanValue(300)).toBe('5')
    expect(bucketSpanValue(3600)).toBe('1')
    expect(bucketSpanValue(43200)).toBe('12')
    expect(bucketSpanValue(86400)).toBe('1')
    expect(bucketSpanValue(0)).toBe('—')
  })
})
