import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

import { listAll, listAllResults, triggerNow } from '@/api/admin/scheduledTests'

describe('admin scheduled test workspace APIs', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
  })

  it('lists all plans and history, then queues an enabled plan', async () => {
    get.mockResolvedValueOnce({ data: [{ id: 4, account_id: 1, account_name: 'Astra' }] })
    get.mockResolvedValueOnce({ data: { items: [{ id: 10, account_id: 1 }], next_cursor: 0 } })
    post.mockResolvedValue({})

    expect(await listAll()).toEqual([{ id: 4, account_id: 1, account_name: 'Astra' }])
    expect(get).toHaveBeenNthCalledWith(1, '/admin/scheduled-test-plans')
    expect(await listAllResults(10)).toEqual({ items: [{ id: 10, account_id: 1 }], next_cursor: 0 })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/scheduled-test-results', { params: { before_id: 10 } })
    await triggerNow(4)
    expect(post).toHaveBeenCalledWith('/admin/scheduled-test-plans/4/run')
  })
})
