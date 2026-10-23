import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getMatrix, getQualityArtwork, getQualityEvents, repeatedArrayParamsSerializer } from '../channelMonitorV2'

afterEach(() => vi.restoreAllMocks())

describe('channel monitor V2 query serialization', () => {
  it('uses repeated keys without bracket suffixes for array filters', () => {
    const query = repeatedArrayParamsSerializer({
      range: '90m',
      platform: ['openai', 'grok'],
      group_id: [1, 2],
      model: undefined,
      group_by: 'platform_group_model',
    })

    expect(query).toBe('range=90m&platform=openai&platform=grok&group_id=1&group_id=2&group_by=platform_group_model')
    expect(query).not.toContain('%5B%5D')
  })

  it('sends the matrix grouping with the shared filters', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({
      data: { coverage: {}, group_by: 'platform_group', items: [] },
    })

    await getMatrix({ range: '24h', platforms: ['openai'], groupIds: [7], models: [] }, 'platform_group', true)

    expect(get).toHaveBeenCalledWith('/admin/channel-monitor-v2/matrix', expect.objectContaining({
      params: {
        range: '24h',
        platform: ['openai'],
        group_id: [7],
        model: undefined,
        group_by: 'platform_group',
      },
    }))
  })

  it('loads quality history from the user monitor path by default', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { events: [] } })
    await getQualityEvents(43, 30)
    expect(get).toHaveBeenCalledWith('/channel-monitor-v2/quality-events', expect.objectContaining({
      params: { group_id: 43, limit: 30 },
    }))
  })

  it('loads quality artwork from the admin monitor path when requested', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { response_text: '<html></html>' } })
    const html = await getQualityArtwork(43, 11, undefined, true)
    expect(html).toBe('<html></html>')
    expect(get).toHaveBeenCalledWith('/admin/channel-monitor-v2/quality-events/11/artwork', expect.objectContaining({
      params: { group_id: 43 },
    }))
  })
})
