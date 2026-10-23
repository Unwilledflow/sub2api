import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MonitorQualityEvent } from '@/api/channelMonitorV2'
import ChannelMonitorV3QualityHistory from '../ChannelMonitorV3QualityHistory.vue'
import { getQualityArtwork, getQualityEvents } from '@/api/channelMonitorV2'

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

vi.mock('@/api/channelMonitorV2', () => ({
  getQualityEvents: vi.fn(),
  getQualityArtwork: vi.fn(),
}))

const mockedEvents = vi.mocked(getQualityEvents)
const mockedArtwork = vi.mocked(getQualityArtwork)
enableAutoUnmount(afterEach)

function event(id: number, status: MonitorQualityEvent['status']): MonitorQualityEvent {
  return {
    id,
    group_id: 43,
    account_id: 7,
    model_id: 'claude-sonnet-5',
    status,
    error_message: status === 'degraded' ? 'feet do not plausibly contact crank pedals' : status === 'unknown' ? 'quality check inconclusive: script' : '',
    created_at: '2026-09-28T04:28:14+08:00',
  }
}

function mountHistory(props: Record<string, unknown> = {}) {
  return mount(ChannelMonitorV3QualityHistory, {
    props: { groupId: 43, enabled: true, ...props },
    global: { stubs: { teleport: true } },
  })
}

describe('ChannelMonitorV3QualityHistory', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('pauses periodic history requests when the page is hidden', async () => {
    vi.useFakeTimers()
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    mockedEvents.mockResolvedValue([])
    const wrapper = mountHistory()
    try {
      await flushPromises()
      expect(mockedEvents).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(120000)
      expect(mockedEvents).toHaveBeenCalledTimes(1)
      visibility.mockReturnValue('visible')
      await vi.advanceTimersByTimeAsync(60000); await flushPromises()
      expect(mockedEvents).toHaveBeenCalledTimes(2)
    } finally { wrapper.unmount(); visibility.mockRestore(); vi.useRealTimers() }
  })

  it('does not fetch or render when detection is disabled', async () => {
    const wrapper = mountHistory({ enabled: false })
    await flushPromises()
    expect(mockedEvents).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="channel-quality-strip"]').exists()).toBe(false)
  })

  it('renders one pelican cell per probe and marks the verdict on the cell', async () => {
    mockedEvents.mockResolvedValue([event(3, 'unknown'), event(2, 'degraded'), event(1, 'success')])
    mockedArtwork.mockResolvedValue('<html><svg></svg></html>')
    const wrapper = mountHistory()
    await flushPromises()

    expect(mockedEvents).toHaveBeenCalledWith(43, 30)
    const cells = wrapper.findAll('.quality-pelican')
    expect(cells).toHaveLength(3)
    expect(cells[0].classes()).toContain('is-unknown')
    expect(cells[1].classes()).toContain('is-degraded')
    expect(cells[2].classes()).toContain('is-success')
  })

  it('renders the generated artwork as each cell thumbnail', async () => {
    mockedEvents.mockResolvedValue([event(2, 'success'), event(1, 'success')])
    mockedArtwork.mockImplementation(async (_group, id) => `<html><svg data-probe="${id}"></svg></html>`)
    const wrapper = mountHistory()
    await flushPromises()

    const thumbs = wrapper.findAll('.quality-pelican__thumb')
    expect(thumbs).toHaveLength(2)
    // The strip shows the probe's own artwork, not a decorative placeholder.
    expect(thumbs[0].attributes('srcdoc')).toContain('data-probe="2"')
    expect(thumbs[1].attributes('srcdoc')).toContain('data-probe="1"')
  })

  it('shows the empty state when no probes exist', async () => {
    mockedEvents.mockResolvedValue([])
    const wrapper = mountHistory()
    await flushPromises()
    expect(wrapper.text()).toContain('channelMonitorV3.qualityStripEmpty')
  })

  it('says detection is armed but has no plan, rather than "no records"', async () => {
    mockedEvents.mockResolvedValue([])
    const wrapper = mountHistory({ detectionArmed: true })
    await flushPromises()
    expect(wrapper.text()).toContain('channelMonitorV3.qualityStripArmedEmpty')
    expect(wrapper.text()).not.toContain('channelMonitorV3.qualityStripEmpty')
  })

  it('loads thumbnails for the newest probes, not the oldest', async () => {
    // The list is newest-first, so the cap must keep the head of the array.
    const newestFirst = Array.from({ length: 25 }, (_, i) => event(100 - i, 'success'))
    mockedEvents.mockResolvedValue(newestFirst)
    mockedArtwork.mockImplementation(async (_group, id) => `<html data-probe="${id}"></html>`)
    const wrapper = mountHistory()
    await flushPromises()

    const requested = mockedArtwork.mock.calls.map(call => call[1])
    expect(requested).toHaveLength(18)
    expect(requested[0]).toBe(100)
    expect(requested).not.toContain(75)
    expect(wrapper.findAll('.quality-pelican')).toHaveLength(25)
  })

  it('opens a sandboxed full-size preview on hover', async () => {
    mockedEvents.mockResolvedValue([event(2, 'unknown')])
    mockedArtwork.mockResolvedValue('<html><svg></svg></html>')
    const wrapper = mountHistory()
    await flushPromises()

    await wrapper.find('[data-testid="quality-pelican-2"]').trigger('mouseenter')
    await flushPromises()

    const frame = wrapper.find('[data-testid="quality-pelican-artwork"]')
    expect(frame.exists()).toBe(true)
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('srcdoc')).toContain('<svg>')
    expect(wrapper.text()).toContain('quality check inconclusive: script')
  })

  it('does not re-request artwork that the strip already loaded', async () => {
    mockedEvents.mockResolvedValue([event(2, 'success')])
    mockedArtwork.mockResolvedValue('<html></html>')
    const wrapper = mountHistory()
    await flushPromises()
    const callsAfterStrip = mockedArtwork.mock.calls.length

    await wrapper.find('[data-testid="quality-pelican-2"]').trigger('mouseenter')
    await flushPromises()

    // Hover reuses the strip's copy instead of hitting the API again.
    expect(mockedArtwork.mock.calls.length).toBe(callsAfterStrip)
  })

  it('reports a failed artwork load without breaking the popover', async () => {
    mockedEvents.mockResolvedValue([event(3, 'success')])
    mockedArtwork.mockRejectedValue(new Error('boom'))
    const wrapper = mountHistory()
    await flushPromises()
    await wrapper.find('[data-testid="quality-pelican-3"]').trigger('mouseenter')
    await flushPromises()
    expect(wrapper.text()).toContain('monitorCommon.qualityArtworkFailed')
    expect(wrapper.find('[data-testid="quality-pelican-artwork"]').exists()).toBe(false)
  })

  it('never requests artwork for a chanshui probe, which stores no image', async () => {
    mockedEvents.mockResolvedValue([{
      ...event(9, 'degraded'),
      quality_provider: 'chanshui',
      audit_summary: { score: 0, candidate_model: 'gpt-5.6-luna', sections: [] },
    }])
    const wrapper = mountHistory()
    await flushPromises()
    await wrapper.find('[data-testid="quality-pelican-9"]').trigger('mouseenter')
    await flushPromises()

    expect(mockedArtwork).not.toHaveBeenCalled()
    // The cell falls back to a placeholder rather than an empty frame.
    expect(wrapper.find('.quality-pelican__placeholder').exists()).toBe(true)
    expect(wrapper.find('[data-testid="quality-pelican-artwork"]').exists()).toBe(false)
  })
})
