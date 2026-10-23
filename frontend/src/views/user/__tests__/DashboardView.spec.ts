import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'

const {
  getDashboardStats,
  getDashboardSnapshotV2,
  query,
  getMyPlatformQuotas,
  refreshUser,
} = vi.hoisted(() => ({
  getDashboardStats: vi.fn(),
  getDashboardSnapshotV2: vi.fn(),
  query: vi.fn(),
  getMyPlatformQuotas: vi.fn(),
  refreshUser: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { balance: 10 },
    isSimpleMode: false,
    refreshUser,
  }),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardStats,
    getDashboardSnapshotV2,
    query,
  },
}))

vi.mock('@/api/user', () => ({
  getMyPlatformQuotas,
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))
vi.mock('@/components/common/LoadingSpinner.vue', () => ({
  default: { template: '<div class="spinner" />' },
}))
vi.mock('@/components/user/dashboard/UserDashboardStats.vue', () => ({
  default: { template: '<div class="stats" />', props: ['stats', 'balance', 'isSimple', 'platformQuotas'] },
}))
vi.mock('@/components/user/dashboard/UserDashboardCharts.vue', () => ({
  default: { template: '<div class="charts" />' },
}))
vi.mock('@/components/user/dashboard/UserDashboardRecentUsage.vue', () => ({
  default: { template: '<div class="recent" />' },
}))
vi.mock('@/components/user/dashboard/UserDashboardQuickActions.vue', () => ({
  default: { template: '<div class="actions" />' },
}))
vi.mock('@/components/user/dashboard/UserDashboardModels.vue', () => ({
  default: { template: '<div class="models" />' },
}))
vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon" />' },
}))

import DashboardView from '../DashboardView.vue'

const i18n = createI18n({
  legacy: false,
  locale: 'zh',
  missingWarn: false,
  fallbackWarn: false,
  messages: { zh: {} },
})

describe('user DashboardView', () => {
  beforeEach(() => {
    getDashboardStats.mockReset().mockResolvedValue({ total_requests: 1 })
    getDashboardSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [] })
    query.mockReset().mockResolvedValue({ items: [] })
    getMyPlatformQuotas.mockReset().mockResolvedValue({ platform_quotas: [] })
    refreshUser.mockReset().mockResolvedValue({})
  })

  it('首屏加载概览，最近用量点开后才取 5 条', async () => {
    getDashboardStats.mockResolvedValue({ total_requests: 1 })
    getDashboardSnapshotV2.mockResolvedValue({ trend: [], models: [] })
    query.mockResolvedValue({ items: [] })
    getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
    refreshUser.mockResolvedValue({})

    const wrapper = mount(DashboardView, {
      global: {
        plugins: [i18n],
      },
    })
    await flushPromises()

    expect(wrapper.find('.stats').exists()).toBe(true)
    expect(getDashboardStats).toHaveBeenCalledTimes(1)
    expect(getDashboardSnapshotV2).toHaveBeenCalledTimes(1)
    expect(query).not.toHaveBeenCalled()
    expect(refreshUser).toHaveBeenCalledTimes(1)

    const recent = wrapper.get('[data-detail="recent"]').element as HTMLDetailsElement
    await new Promise<void>((resolve) => {
      recent.addEventListener('toggle', () => resolve(), { once: true })
      recent.open = true
    })
    await flushPromises()
    expect(query).toHaveBeenCalledWith(
      expect.objectContaining({ page: 1, page_size: 5, sort_by: 'created_at', sort_order: 'desc' }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    wrapper.unmount()
  })
})
