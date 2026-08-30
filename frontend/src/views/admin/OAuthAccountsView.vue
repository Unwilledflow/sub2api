<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="rounded-lg border border-gray-200 bg-white px-3 py-3 shadow-sm dark:border-dark-700 dark:bg-dark-800 sm:px-4">
          <div class="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
            <div class="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row">
              <label class="relative min-w-0 flex-1 sm:max-w-sm">
                <span class="sr-only">{{ t('admin.accounts.oauthPage.search') }}</span>
                <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                <input
                  v-model="params.search"
                  type="search"
                  class="input h-9 pl-9"
                  :placeholder="t('admin.accounts.oauthPage.search')"
                  @input="handleSearch"
                />
              </label>
              <label class="min-w-0 sm:w-40">
                <span class="sr-only">{{ t('admin.accounts.oauthPage.statusFilter') }}</span>
                <select v-model="params.status" class="input h-9" @change="reloadPage">
                  <option value="">{{ t('admin.accounts.allStatus') }}</option>
                  <option value="active">{{ t('admin.accounts.status.active') }}</option>
                  <option value="inactive">{{ t('admin.accounts.status.inactive') }}</option>
                  <option value="error">{{ t('admin.accounts.status.error') }}</option>
                </select>
              </label>
            </div>

            <div class="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:justify-end">
              <button class="btn btn-secondary h-9 px-3" :disabled="loading" :title="t('common.refresh')" @click="refreshAll">
                <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
                <span>{{ t('common.refresh') }}</span>
              </button>
              <button class="btn btn-primary h-9 px-3" @click="showCreate = true">
                <Icon name="plus" size="sm" />
                <span>{{ t('admin.accounts.createAccount') }}</span>
              </button>
            </div>
          </div>

          <div class="mt-3 flex flex-col gap-2 border-t border-gray-100 pt-3 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
            <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span>{{ t('admin.accounts.oauthPage.total', { count: pagination.total }) }}</span>
              <span class="text-gray-300 dark:text-dark-500">/</span>
              <span>{{ t('admin.accounts.oauthPage.selected', { count: selectedIds.length }) }}</span>
              <span
                :class="[
                  'inline-flex items-center gap-1 rounded px-2 py-1 font-medium',
                  invalidAccountIds.length
                    ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
                    : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'
                ]"
              >
                <Icon v-if="invalidLoading" name="refresh" size="xs" class="animate-spin" />
                {{ t('admin.accounts.oauthPage.invalid', { count: invalidAccountIds.length }) }}
              </span>
            </div>
            <div class="grid grid-cols-1 gap-2 sm:flex">
              <button
                data-test="batch-test"
                class="btn btn-secondary h-8 px-3 text-xs"
                :disabled="selectedIds.length === 0 || batchTesting"
                @click="runBatchTest"
              >
                <Icon :name="batchTesting ? 'refresh' : 'play'" size="sm" :class="batchTesting ? 'animate-spin' : ''" />
                {{ batchTesting ? t('admin.accounts.oauthPage.batchTesting') : t('admin.accounts.oauthPage.batchTest') }}
              </button>
              <button
                class="btn btn-secondary h-8 border-red-200 px-3 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-300 dark:hover:bg-red-900/20"
                :disabled="selectedIds.length === 0 || deleting"
                @click="requestSelectedDelete"
              >
                <Icon name="trash" size="sm" />
                {{ t('admin.accounts.oauthPage.deleteSelected') }}
              </button>
              <button
                class="btn btn-danger h-8 px-3 text-xs"
                :disabled="invalidAccountIds.length === 0 || invalidLoading || deleting"
                @click="requestInvalidDelete"
              >
                <Icon name="xCircle" size="sm" />
                {{ t('admin.accounts.oauthPage.deleteInvalid') }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <template #table>
        <div class="table-wrapper h-full">
          <div v-if="loadError" class="flex h-full min-h-56 flex-col items-center justify-center gap-3 px-4 text-center">
            <Icon name="exclamationTriangle" class="text-red-500" />
            <p class="text-sm text-gray-600 dark:text-gray-300">{{ loadError }}</p>
            <button class="btn btn-secondary h-8 px-3 text-xs" @click="refreshAll">{{ t('common.retry') }}</button>
          </div>

          <template v-else>
            <div class="hidden h-full overflow-auto lg:block">
              <table class="min-w-[980px] table-fixed">
                <thead class="sticky top-0 z-10">
                  <tr>
                    <th class="w-12">
                      <input
                        type="checkbox"
                        class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                        :checked="allVisibleSelected"
                        :aria-label="t('admin.accounts.oauthPage.selectPage')"
                        @change="toggleVisibleSelection"
                      />
                    </th>
                    <th class="w-[22%]">{{ t('admin.accounts.columns.name') }}</th>
                    <th class="w-[10%]">{{ t('admin.accounts.oauthPage.plan') }}</th>
                    <th class="w-[18%]">{{ t('admin.accounts.columns.groups') }}</th>
                    <th class="w-[18%]">{{ t('admin.accounts.columns.status') }}</th>
                    <th class="w-[14%]">{{ t('admin.accounts.columns.createdAt') }}</th>
                    <th class="w-32 text-right">{{ t('admin.accounts.columns.actions') }}</th>
                  </tr>
                </thead>
                <tbody v-if="loading">
                  <tr v-for="index in 7" :key="index">
                    <td colspan="7"><div class="h-5 animate-pulse rounded bg-gray-100 dark:bg-dark-700" /></td>
                  </tr>
                </tbody>
                <tbody v-else-if="accounts.length">
                  <tr v-for="account in accounts" :key="account.id" class="hover:bg-gray-50/70 dark:hover:bg-dark-700/40">
                    <td>
                      <input
                        type="checkbox"
                        class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                        :checked="isSelected(account.id)"
                        :aria-label="t('admin.accounts.oauthPage.selectAccount', { name: account.name })"
                        @change="toggleSelection(account.id)"
                      />
                    </td>
                    <td>
                      <div class="min-w-0">
                        <div class="flex min-w-0 items-center gap-2">
                          <span class="truncate font-medium text-gray-900 dark:text-white" :title="account.name">{{ account.name }}</span>
                          <span v-if="isInvalid(account)" class="badge badge-danger shrink-0 text-[10px]">{{ t('admin.accounts.oauthPage.invalidShort') }}</span>
                        </div>
                        <div class="mt-1 flex min-w-0 items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                          <span class="font-mono">#{{ account.id }}</span>
                          <span v-if="accountEmail(account)" class="truncate" :title="accountEmail(account)">{{ accountEmail(account) }}</span>
                        </div>
                      </div>
                    </td>
                    <td>
                      <div class="flex flex-col gap-1 text-xs">
                        <span class="font-medium text-gray-700 dark:text-gray-200">{{ accountPlan(account) }}</span>
                        <span v-if="account.parent_account_id" class="text-gray-500 dark:text-gray-400">
                          {{ t('admin.accounts.oauthPage.shadowOf', { id: account.parent_account_id }) }}
                        </span>
                      </div>
                    </td>
                    <td>
                      <div v-if="account.groups?.length" class="flex flex-wrap gap-1">
                        <span v-for="group in account.groups.slice(0, 3)" :key="group.id" class="rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                          {{ group.name }}
                        </span>
                        <span v-if="account.groups.length > 3" class="text-[11px] text-gray-400">+{{ account.groups.length - 3 }}</span>
                      </div>
                      <span v-else class="text-xs text-gray-400">{{ t('admin.accounts.oauthPage.ungrouped') }}</span>
                    </td>
                    <td>
                      <AccountStatusIndicator :account="account" />
                      <p
                        v-if="batchTestResult(account.id)"
                        :class="[
                          'mt-1 text-[11px] font-medium',
                          batchTestResult(account.id)?.success ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-500'
                        ]"
                        :title="batchTestResult(account.id)?.error"
                      >
                        {{ batchTestResultLabel(account.id) }}
                      </p>
                      <p v-if="account.error_message" class="mt-1 line-clamp-2 text-[11px] leading-4 text-red-500" :title="account.error_message">
                        {{ account.error_message }}
                      </p>
                    </td>
                    <td class="text-xs text-gray-500 dark:text-gray-400">{{ formatDateTime(account.created_at) }}</td>
                    <td>
                      <div class="flex justify-end gap-1">
                        <button class="icon-action" :title="t('admin.accounts.testAccount')" @click="openTest(account)"><Icon name="play" size="sm" /></button>
                        <button class="icon-action" :title="t('common.edit')" @click="openEdit(account)"><Icon name="edit" size="sm" /></button>
                        <button class="icon-action icon-action-danger" :title="t('common.delete')" @click="requestSingleDelete(account)"><Icon name="trash" size="sm" /></button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div v-if="!loading && accounts.length" class="divide-y divide-gray-100 dark:divide-dark-700 lg:hidden">
              <article v-for="account in accounts" :key="account.id" class="px-3 py-3 sm:px-4">
                <div class="flex items-start gap-3">
                  <input
                    type="checkbox"
                    class="mt-1 h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                    :checked="isSelected(account.id)"
                    :aria-label="t('admin.accounts.oauthPage.selectAccount', { name: account.name })"
                    @change="toggleSelection(account.id)"
                  />
                  <div class="min-w-0 flex-1">
                    <div class="flex min-w-0 flex-wrap items-center gap-2">
                      <span class="min-w-0 truncate font-medium text-gray-900 dark:text-white">{{ account.name }}</span>
                      <span class="font-mono text-[11px] text-gray-400">#{{ account.id }}</span>
                      <span v-if="isInvalid(account)" class="badge badge-danger text-[10px]">{{ t('admin.accounts.oauthPage.invalidShort') }}</span>
                    </div>
                    <p v-if="accountEmail(account)" class="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">{{ accountEmail(account) }}</p>
                    <div class="mt-2 flex flex-wrap items-center gap-2">
                      <AccountStatusIndicator :account="account" />
                      <span
                        v-if="batchTestResult(account.id)"
                        :class="[
                          'text-[11px] font-medium',
                          batchTestResult(account.id)?.success ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-500'
                        ]"
                      >
                        {{ batchTestResultLabel(account.id) }}
                      </span>
                      <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ accountPlan(account) }}</span>
                      <span v-if="account.groups?.length" class="text-[11px] text-gray-500 dark:text-gray-400">
                        {{ account.groups.slice(0, 2).map(group => group.name).join(' / ') }}
                      </span>
                    </div>
                    <p v-if="account.error_message" class="mt-2 line-clamp-2 text-xs leading-5 text-red-500">{{ account.error_message }}</p>
                  </div>
                  <div class="grid shrink-0 grid-cols-3 gap-1">
                    <button class="icon-action" :title="t('admin.accounts.testAccount')" @click="openTest(account)"><Icon name="play" size="sm" /></button>
                    <button class="icon-action" :title="t('common.edit')" @click="openEdit(account)"><Icon name="edit" size="sm" /></button>
                    <button class="icon-action icon-action-danger" :title="t('common.delete')" @click="requestSingleDelete(account)"><Icon name="trash" size="sm" /></button>
                  </div>
                </div>
              </article>
            </div>

            <div v-if="loading" class="space-y-3 p-4 lg:hidden">
              <div v-for="index in 5" :key="index" class="h-24 animate-pulse rounded bg-gray-100 dark:bg-dark-700" />
            </div>

            <div v-if="!loading && accounts.length === 0" class="flex min-h-56 flex-col items-center justify-center gap-2 px-4 text-center">
              <Icon name="key" class="text-gray-300 dark:text-dark-500" />
              <p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.accounts.oauthPage.empty') }}</p>
              <button class="btn btn-primary mt-1 h-8 px-3 text-xs" @click="showCreate = true">
                <Icon name="plus" size="sm" />
                {{ t('admin.accounts.createAccount') }}
              </button>
            </div>
          </template>
        </div>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <CreateAccountModal
      v-if="showCreate"
      :show="showCreate"
      :proxies="proxies"
      :groups="groups"
      @close="showCreate = false"
      @created="handleCreated"
    />
    <EditAccountModal
      v-if="showEdit && editingAccount"
      :show="showEdit"
      :account="editingAccount"
      :proxies="proxies"
      :groups="groups"
      @close="closeEdit"
      @updated="handleUpdated"
    />
    <AccountTestModal
      v-if="showTest && testingAccount"
      :show="showTest"
      :account="testingAccount"
      @close="closeTest"
    />
    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.accounts.oauthPage.deleteConfirmTitle')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { OpenAIOAuthBatchDeleteResult, OpenAIOAuthBatchTestItem } from '@/api/admin/accounts'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import Icon from '@/components/icons/Icon.vue'
import { useTableLoader } from '@/composables/useTableLoader'
import { useAppStore } from '@/stores/app'
import type { Account, AdminGroup, Proxy as AccountProxy } from '@/types'
import { formatDateTime } from '@/utils/format'
import { isPermanentlyInvalidOpenAIOAuthAccount } from '@/utils/oauthAccounts'

const CreateAccountModal = defineAsyncComponent(() => import('@/components/account/CreateAccountModal.vue'))
const EditAccountModal = defineAsyncComponent(() => import('@/components/account/EditAccountModal.vue'))
const AccountTestModal = defineAsyncComponent(() => import('@/components/admin/account/AccountTestModal.vue'))

type OAuthFilters = {
  search: string
  status: string
}

type DeleteRequest = {
  kind: 'selected' | 'invalid' | 'single'
  ids: number[]
  accountName?: string
}

const { t } = useI18n()
const appStore = useAppStore()
const proxies = ref<AccountProxy[]>([])
const groups = ref<AdminGroup[]>([])
const selectedIds = ref<number[]>([])
const invalidAccountIds = ref<number[]>([])
const invalidLoading = ref(false)
const loadError = ref('')
const deleting = ref(false)
const batchTesting = ref(false)
const batchTestResults = ref<Record<number, OpenAIOAuthBatchTestItem>>({})
const showCreate = ref(false)
const showEdit = ref(false)
const showTest = ref(false)
const showDeleteConfirm = ref(false)
const editingAccount = ref<Account | null>(null)
const testingAccount = ref<Account | null>(null)
const deleteRequest = ref<DeleteRequest | null>(null)

const {
  items: accounts,
  loading,
  params,
  pagination,
  load: baseLoad,
  reload: baseReload,
  handlePageChange: baseHandlePageChange,
  handlePageSizeChange: baseHandlePageSizeChange
} = useTableLoader<Account, OAuthFilters>({
  fetchFn: (page, pageSize, filters, options) => adminAPI.accounts.list(page, pageSize, {
    platform: 'openai',
    type: 'oauth',
    status: filters.status,
    search: filters.search,
    lite: '1',
    sort_by: 'created_at',
    sort_order: 'desc'
  }, options),
  initialParams: { search: '', status: '' },
  pageSize: 100
})

const selectedIdSet = computed(() => new Set(selectedIds.value))
const allVisibleSelected = computed(() => accounts.value.length > 0 && accounts.value.every(account => selectedIdSet.value.has(account.id)))
const deleteConfirmMessage = computed(() => {
  const request = deleteRequest.value
  if (!request) return ''
  if (request.kind === 'single') {
    return t('admin.accounts.oauthPage.deleteSingleConfirm', { name: request.accountName ?? '' })
  }
  if (request.kind === 'invalid') {
    return t('admin.accounts.oauthPage.deleteInvalidConfirm', { count: request.ids.length })
  }
  return t('admin.accounts.oauthPage.deleteSelectedConfirm', { count: request.ids.length })
})

const isSelected = (id: number) => selectedIdSet.value.has(id)
const isInvalid = (account: Account) => isPermanentlyInvalidOpenAIOAuthAccount(account)
const batchTestResult = (id: number) => batchTestResults.value[id]
const batchTestResultLabel = (id: number) => {
  const result = batchTestResult(id)
  if (!result) return ''
  if (result.success) {
    return t('admin.accounts.oauthPage.testPassed', { latency: result.latency_ms ?? 0 })
  }
  if (result.skipped) return t('admin.accounts.oauthPage.testSkipped')
  return t('admin.accounts.oauthPage.testFailed')
}

const accountEmail = (account: Account): string => {
  const email = account.credentials?.email ?? account.extra?.email ?? account.parent_email
  return typeof email === 'string' ? email : ''
}

const accountPlan = (account: Account): string => {
  const plan = account.credentials?.plan_type ?? account.extra?.plan_type ?? account.parent_plan_type
  return typeof plan === 'string' && plan.trim() ? plan : t('admin.accounts.oauthPage.planUnknown')
}

const loadPage = async (reset = false) => {
  loadError.value = ''
  try {
    if (reset) await baseReload()
    else await baseLoad()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : t('common.error')
  }
}

const fetchAllOpenAIOAuthAccounts = async (): Promise<Account[]> => {
  const pageSize = 1000
  const filters = {
    platform: 'openai',
    type: 'oauth',
    lite: '1',
    sort_by: 'created_at',
    sort_order: 'desc' as const
  }
  const first = await adminAPI.accounts.list(1, pageSize, filters)
  const result = [...first.items]
  const pageCount = Math.ceil(first.total / pageSize)
  for (let page = 2; page <= pageCount; page += 1) {
    const next = await adminAPI.accounts.list(page, pageSize, filters)
    result.push(...next.items)
  }
  return result
}

const refreshInvalidAccounts = async () => {
  invalidLoading.value = true
  try {
    const allAccounts = await fetchAllOpenAIOAuthAccounts()
    invalidAccountIds.value = allAccounts
      .filter(isInvalid)
      .sort((left, right) => Number(Boolean(right.parent_account_id)) - Number(Boolean(left.parent_account_id)))
      .map(account => account.id)
  } catch (error) {
    console.error('Failed to inspect invalid OpenAI OAuth accounts:', error)
    invalidAccountIds.value = []
  } finally {
    invalidLoading.value = false
  }
}

const loadReferences = async () => {
  try {
    const [proxyRows, groupRows] = await Promise.all([
      adminAPI.proxies.getAll(),
      adminAPI.groups.getAll()
    ])
    proxies.value = proxyRows
    groups.value = groupRows
  } catch (error) {
    console.error('Failed to load account references:', error)
  }
}

const refreshAll = async () => {
  await Promise.all([loadPage(), refreshInvalidAccounts()])
}

const reloadPage = async () => {
  selectedIds.value = []
  await loadPage(true)
}

const handleSearch = useDebounceFn(() => {
  void reloadPage()
}, 250)

const handlePageChange = (page: number) => {
  baseHandlePageChange(page)
}

const handlePageSizeChange = (pageSize: number) => {
  selectedIds.value = []
  baseHandlePageSizeChange(pageSize)
}

const toggleSelection = (id: number) => {
  selectedIds.value = isSelected(id)
    ? selectedIds.value.filter(selectedID => selectedID !== id)
    : [...selectedIds.value, id]
}

const toggleVisibleSelection = (event: Event) => {
  const checked = (event.target as HTMLInputElement).checked
  const visibleIDs = accounts.value.map(account => account.id)
  if (checked) {
    selectedIds.value = Array.from(new Set([...selectedIds.value, ...visibleIDs]))
  } else {
    const visibleSet = new Set(visibleIDs)
    selectedIds.value = selectedIds.value.filter(id => !visibleSet.has(id))
  }
}

const queueDelete = (request: DeleteRequest) => {
  deleteRequest.value = { ...request, ids: Array.from(new Set(request.ids)) }
  showDeleteConfirm.value = true
}

const requestSelectedDelete = () => queueDelete({ kind: 'selected', ids: selectedIds.value })
const requestInvalidDelete = () => queueDelete({ kind: 'invalid', ids: invalidAccountIds.value })
const requestSingleDelete = (account: Account) => queueDelete({ kind: 'single', ids: [account.id], accountName: account.name })

const runBatchTest = async () => {
  if (selectedIds.value.length === 0 || batchTesting.value) return
  batchTesting.value = true
  try {
    const result = await adminAPI.accounts.batchTestOpenAIOAuth(selectedIds.value)
    const nextResults = { ...batchTestResults.value }
    for (const item of result.results) nextResults[item.account_id] = item
    batchTestResults.value = nextResults

    if (result.failed === 0 && result.skipped === 0) {
      appStore.showSuccess(t('admin.accounts.oauthPage.batchTestSuccess', { success: result.success }))
    } else {
      appStore.showWarning(t('admin.accounts.oauthPage.batchTestPartial', {
        success: result.success,
        failed: result.failed,
        skipped: result.skipped
      }))
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('common.error'))
  } finally {
    batchTesting.value = false
  }
}

const cancelDelete = () => {
  showDeleteConfirm.value = false
  deleteRequest.value = null
}

const mergeDeleteResults = (results: OpenAIOAuthBatchDeleteResult[]): OpenAIOAuthBatchDeleteResult => ({
  total: results.reduce((sum, result) => sum + result.total, 0),
  deleted: results.reduce((sum, result) => sum + result.deleted, 0),
  deleted_ids: results.flatMap(result => result.deleted_ids),
  skipped: results.flatMap(result => result.skipped),
  failed: results.flatMap(result => result.failed)
})

const deleteInBatches = async (ids: number[]) => {
  const results: OpenAIOAuthBatchDeleteResult[] = []
  for (let index = 0; index < ids.length; index += 1000) {
    results.push(await adminAPI.accounts.batchDeleteOpenAIOAuth(ids.slice(index, index + 1000)))
  }
  return mergeDeleteResults(results)
}

const confirmDelete = async () => {
  const request = deleteRequest.value
  if (!request || request.ids.length === 0 || deleting.value) return
  deleting.value = true
  try {
    const result = await deleteInBatches(request.ids)
    if (result.deleted > 0) {
      appStore.showSuccess(t('admin.accounts.oauthPage.deleteSuccess', { count: result.deleted }))
    }
    if (result.failed.length > 0 || result.skipped.length > 0) {
      appStore.showWarning(t('admin.accounts.oauthPage.deletePartial', {
        deleted: result.deleted,
        failed: result.failed.length,
        skipped: result.skipped.length
      }))
    }
    const deletedSet = new Set(result.deleted_ids)
    selectedIds.value = selectedIds.value.filter(id => !deletedSet.has(id))
    cancelDelete()
    await Promise.all([loadPage(), refreshInvalidAccounts()])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('common.error'))
  } finally {
    deleting.value = false
  }
}

const openEdit = (account: Account) => {
  editingAccount.value = account
  showEdit.value = true
}

const closeEdit = () => {
  showEdit.value = false
  editingAccount.value = null
}

const openTest = (account: Account) => {
  testingAccount.value = account
  showTest.value = true
}

const closeTest = () => {
  showTest.value = false
  testingAccount.value = null
}

const handleCreated = async () => {
  showCreate.value = false
  await Promise.all([loadPage(true), refreshInvalidAccounts()])
}

const handleUpdated = async () => {
  closeEdit()
  await Promise.all([loadPage(), refreshInvalidAccounts()])
}

onMounted(() => {
  void Promise.all([loadPage(), loadReferences(), refreshInvalidAccounts()])
})
</script>

<style scoped>
.icon-action {
  @apply inline-flex h-8 w-8 items-center justify-center rounded-md text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white;
}

.icon-action-danger {
  @apply hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-300;
}
</style>
