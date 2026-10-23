import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))

import { getGrokMediaEligibility, updateGrokMediaEligibility } from '@/api/admin/accounts'

describe('admin Grok media eligibility API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('reads and updates the dedicated eligibility endpoint', async () => {
    const state = { account_id: 12, mode: 'auto', eligible: false, reason: 'billing_inconclusive' }
    get.mockResolvedValue({ data: state })
    put.mockResolvedValue({ data: { ...state, mode: 'enabled', eligible: true, reason: 'override_enabled' } })

    expect(await getGrokMediaEligibility(12)).toEqual(state)
    expect(get).toHaveBeenCalledWith('/admin/accounts/12/grok-media-eligibility')
    expect(await updateGrokMediaEligibility(12, 'enabled')).toMatchObject({ mode: 'enabled' })
    expect(put).toHaveBeenCalledWith('/admin/accounts/12/grok-media-eligibility', { mode: 'enabled' })
  })
})
