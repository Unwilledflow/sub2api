import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import ChannelMonitorV3Card from '../ChannelMonitorV3Card.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, te: () => true }),
  }
})

function metrics(): MonitorMetric {
  return {
    success_requests: 80,
    error_requests: 20,
    request_count: 100,
    sample_count: 100,
    token_count: 1000,
    rpm: 1,
    tpm: 10,
    error_rate: 0.2,
    cache_rate: 0.5,
    cache_rate_numerator: 50,
    cache_rate_denominator: 100,
    ttft: { sample_count: 100, p50_ms: 200, p95_ms: 400, avg_ms: 250 },
    duration: { sample_count: 100, p50_ms: 800, p95_ms: 1200, avg_ms: 900 },
  }
}

function health(overall: MonitorHealth['overall']): MonitorHealth {
  return {
    overall,
    error_rate: overall,
    ttft: overall,
    cache: overall,
    minimum_sample: 50,
  }
}

function row(overall: MonitorHealth['overall']): MonitorMatrixRow {
  return {
    platform: 'openai',
    group_id: 1,
    group_name: 'Codex',
    metrics: metrics(),
    health: health(overall),
    buckets: [
      {
        bucket_start: '2026-09-09T12:00:00Z',
        metrics: metrics(),
        health: health(overall),
      },
    ],
  }
}

function mountCard(overall: MonitorHealth['overall']) {
  return mount(ChannelMonitorV3Card, {
    props: {
      row: row(overall),
      countdownSeconds: 0,
      timelineLength: 12,
    },
    global: {
      stubs: {
        ProviderIcon: true,
        ChannelMonitorV3Timeline: true,
        ChannelMonitorV3QualityHistory: true,
      },
    },
  })
}

describe('ChannelMonitorV3Card metrics', () => {
  function metricText(overall: MonitorHealth['overall'], m: MonitorMetric): Record<string, string> {
    const wrapper = mount(ChannelMonitorV3Card, {
      props: {
        row: { ...row(overall), metrics: m, buckets: [{ bucket_start: '2026-09-09T12:00:00Z', metrics: m, health: health(overall) }] },
        countdownSeconds: 0,
        timelineLength: 12,
      },
      global: { stubs: { ProviderIcon: true, ChannelMonitorV3Timeline: true } },
    })
    const out: Record<string, string> = {}
    for (const key of ['cache', 'success', 'ttft', 'duration']) {
      const cell = wrapper.find(`[data-testid="channel-status-metric-${key}"]`)
      out[key] = cell.exists() ? cell.text() : 'MISSING'
    }
    wrapper.unmount()
    return out
  }

  it('renders four metrics including request duration', () => {
    const cells = metricText('healthy', metrics())
    expect(cells.duration).not.toBe('MISSING')
    expect(cells.duration).toContain('channelMonitorV3.duration')
    expect(cells.duration).toContain('800ms')
  })

  it('still names the TTFT percentile when it is too thin to headline', () => {
    // Two observations behind a 1.2s percentile: de-emphasised, but the value
    // is said out loud instead of the cell going blank.
    const thin = { ...metrics(), ttft: { sample_count: 2, p50_ms: 1200, p95_ms: 1500, avg_ms: 1300 } }
    const cells = metricText('warning', thin)
    expect(cells.ttft).toContain('—')
    expect(cells.ttft).toContain('channelMonitorV3.ttftSamplesShort')
  })
})

describe('ChannelMonitorV3Card status badge', () => {
  it('shows the health label when samples are sufficient', () => {
    for (const overall of ['healthy', 'warning', 'critical'] as const) {
      const wrapper = mountCard(overall)
      expect(wrapper.get('[data-testid="channel-status-sample-badge"]').text()).toBe(`channelMonitorV3.${overall}`)
      wrapper.unmount()
    }
  })

  it('shows 样本不足 when health is unknown', () => {
    const wrapper = mountCard('unknown')
    expect(wrapper.get('[data-testid="channel-status-sample-badge"]').text()).toBe('channelMonitorV3.unknown')
  })
})
