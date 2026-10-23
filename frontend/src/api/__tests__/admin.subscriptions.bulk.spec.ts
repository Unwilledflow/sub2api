import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post } }))

import { subscriptionsAPI } from '../admin/subscriptions'

describe('admin subscription batch APIs', () => {
  beforeEach(() => vi.clearAllMocks())

  it('sends the explicit operation key and returns partial results', async () => {
    const request = { subscription_ids: [3, 5], action: 'extend' as const, days: 7 }
    const result = { success_count: 1, failed_count: 1, results: [
      { subscription_id: 3, success: true }, { subscription_id: 5, success: false, error: 'Not found' }
    ] }
    post.mockResolvedValue({ data: result })
    expect(await subscriptionsAPI.bulkAction(request, 'operation-123')).toEqual(result)
    expect(post).toHaveBeenCalledWith('/admin/subscriptions/bulk-action', request, {
      headers: { 'Idempotency-Key': 'operation-123' }
    })
  })
})
