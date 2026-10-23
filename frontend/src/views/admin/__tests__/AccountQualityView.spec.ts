import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import AccountQualityView from '../AccountQualityView.vue'
import scheduledTests, { listAll, listAllResults, triggerNow } from '@/api/admin/scheduledTests'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1, role: 'admin' } }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true })
}))
vi.mock('@/api/admin/scheduledTests', () => ({
  listAll: vi.fn(),
  listAllResults: vi.fn(),
  triggerNow: vi.fn(),
  default: { update: vi.fn() }
}))

const mountView = () => mount(AccountQualityView, {
  global: { plugins: [createPinia()], stubs: { Teleport: true, AppLayout: { template: '<main><slot /></main>' } } }
})

describe('quality operations workspace', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listAll).mockResolvedValue([
      { id: 4, account_id: 1, account_name: 'Astra', model_id: 'gpt-6-astra', enabled: true, next_run_at: '2026-09-27T15:00:00Z' }
    ] as any)
    vi.mocked(listAllResults).mockResolvedValue({
      items: [{
        id: 10, plan_id: 4, account_id: 1, account_name: 'Astra', status: 'degraded',
        response_text: '<svg></svg>', error_message: '', latency_ms: 1200, started_at: '2026-09-27T14:00:00Z'
      }],
      next_cursor: 0
    } as any)
    vi.mocked(triggerNow).mockResolvedValue(undefined)
    vi.mocked(scheduledTests.update).mockResolvedValue({} as any)
  })

  it('loads existing plans and history without creating a new plan', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(listAll).toHaveBeenCalled()
    expect(listAllResults).toHaveBeenCalledWith(0)
    expect(wrapper.text()).toContain('Astra')
    expect(wrapper.text()).toContain('gpt-6-astra')
    expect(wrapper.text()).toContain('qualityOps.degraded')
    expect(scheduledTests.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('queues an existing enabled plan through TriggerNow', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text().includes('qualityOps.runShort'))!.trigger('click')
    await flushPromises()
    expect(triggerNow).toHaveBeenCalledWith(4)
    expect(wrapper.text()).toContain('qualityOps.queued')
    wrapper.unmount()
  })
})
