import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountsView from '../AccountsView.vue'
import { DEFAULT_SCHEDULED_TEST_CRON, DEFAULT_SCHEDULED_TEST_PROMPT } from '@/constants/scheduledTest'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  getAvailableModels,
  listByAccount,
  createPlan,
  showError,
  showSuccess
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  getAvailableModels: vi.fn(),
  listByAccount: vi.fn(),
  createPlan: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      getAvailableModels,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    scheduledTests: {
      listByAccount,
      create: createPlan
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showInfo: vi.fn() })
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AccountBulkActionsBarStub = {
  props: ['selectedIds'],
  emits: ['create-scheduled-tests'],
  template: '<button data-test="create-scheduled-tests" @click="$emit(\'create-scheduled-tests\')">create</button>'
}

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <div data-test="select-row"><slot name="cell-select" :row="row" /></div>
      </div>
    </div>
  `
}

describe('admin AccountsView bulk scheduled tests', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal('confirm', vi.fn(() => true))
    listAccounts.mockReset()
    listWithEtag.mockReset()
    getBatchTodayStats.mockReset()
    getUpstreamBillingProbeSettings.mockReset()
    getAllProxies.mockReset()
    getAllGroups.mockReset()
    getAvailableModels.mockReset()
    listByAccount.mockReset()
    createPlan.mockReset()
    showError.mockReset()
    showSuccess.mockReset()

    listAccounts.mockResolvedValue({
      items: [
        { id: 1, name: 'openai-a', platform: 'openai', type: 'apikey', status: 'active' },
        { id: 2, name: 'openai-b', platform: 'openai', type: 'apikey', status: 'active' }
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    getAvailableModels.mockResolvedValue([{ id: 'gpt-6-astra', display_name: 'Astra' }])
    listByAccount.mockImplementation(async (id: number) => (id === 2 ? [{ id: 99 }] : []))
    createPlan.mockResolvedValue({ id: 1 })
  })

  it('creates a default pelican plan for selected accounts and skips accounts that already have one', async () => {
    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
          DataTable: DataTableStub,
          Pagination: true,
          ConfirmDialog: true,
          AccountTableActions: { template: '<div></div>' },
          AccountTableFilters: { template: '<div></div>' },
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: true,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })
    await flushPromises()
    for (const input of wrapper.findAll('[data-test="select-row"] input')) {
      await input.setValue(true)
    }
    await wrapper.get('[data-test="create-scheduled-tests"]').trigger('click')
    await flushPromises()

    expect(listByAccount).toHaveBeenCalledTimes(2)
    expect(createPlan).toHaveBeenCalledTimes(1)
    expect(createPlan).toHaveBeenCalledWith({
      account_id: 1,
      model_id: 'gpt-6-astra',
      prompt_text: DEFAULT_SCHEDULED_TEST_PROMPT,
      cron_expression: DEFAULT_SCHEDULED_TEST_CRON,
      enabled: true,
      max_results: 100,
      auto_recover: false,
      quality_check_enabled: true
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.bulkActions.createScheduledTestsSuccess')
    wrapper.unmount()
  })
})
