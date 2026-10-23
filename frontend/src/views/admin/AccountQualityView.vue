<template>
  <AppLayout>
    <div class="quality-workspace">
      <div class="workspace-heading">
        <div>
          <p class="workspace-eyebrow">{{ t('qualityOps.workspaceLabel') }}</p>
          <h2>{{ t('qualityOps.workspaceTitle') }}</h2>
          <p class="workspace-subtitle">{{ t('qualityOps.workspaceHint') }}</p>
        </div>
        <div class="heading-actions">
          <span class="updated-label" aria-live="polite">{{ store.updatedAt ? t('qualityOps.updatedAt', { time: clock(store.updatedAt) }) : t('qualityOps.loading') }}</span>
          <button class="btn btn-secondary" :disabled="refreshing" @click="load">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" />{{ t('qualityOps.refresh') }}
          </button>
        </div>
      </div>
      <div v-if="error || notice" class="workspace-message" :class="error ? 'message-error' : 'message-success'" :role="error ? 'alert' : 'status'">
        {{ error || notice }}
        <button :aria-label="t('qualityOps.dismiss')" @click="error = ''; notice = ''"><Icon name="x" size="sm" /></button>
      </div>
      <div class="workspace-summary">
        <div class="summary-cell"><span class="summary-icon"><Icon name="users" size="md" /></span><div><span>{{ t('qualityOps.rulesTotal') }}</span><strong>{{ store.rulesLoaded ? plans.length : '—' }}</strong></div></div>
        <div class="summary-cell"><span class="summary-icon is-teal"><Icon name="play" size="md" /></span><div><span>{{ t('qualityOps.rulesActive') }}</span><strong>{{ store.rulesLoaded ? enabledCount : '—' }}<small v-if="store.rulesLoaded"> / {{ plans.length }}</small></strong></div></div>
        <div class="summary-cell"><span class="summary-icon is-amber"><Icon name="exclamationCircle" size="md" /></span><div><span>{{ t('qualityOps.latestAttention') }}</span><strong>{{ store.operationsLoaded ? attentionCount : '—' }}</strong></div><button v-if="attentionCount" class="summary-link" @click="store.selectedPlanId = null; store.operationFilter = 'attention'">{{ t('qualityOps.view') }}<Icon name="arrowRight" size="xs" /></button></div>
        <div class="summary-cell"><span class="summary-icon"><Icon name="document" size="md" /></span><div><span>{{ t('qualityOps.loadedRounds') }}</span><strong>{{ store.operationsLoaded ? operations.length : '—' }}</strong></div></div>
      </div>
      <div class="workspace-columns">
        <section class="workspace-panel rules-panel" aria-labelledby="quality-rules-heading">
          <header class="panel-heading"><div><h3 id="quality-rules-heading">{{ t('qualityOps.ruleLibrary') }}<span class="count-label">{{ plans.length }}</span></h3><p>{{ t('qualityOps.ruleLibraryHint') }}</p></div></header>
          <div class="rule-search"><Icon name="search" size="sm" /><input v-model="store.search" :aria-label="t('qualityOps.search')" :placeholder="t('qualityOps.searchRule')" /><button v-if="store.search" :aria-label="t('qualityOps.clearSearch')" @click="store.search = ''"><Icon name="x" size="sm" /></button></div>
          <button class="all-accounts" :class="{ selected: store.selectedPlanId === null }" :aria-pressed="store.selectedPlanId === null" @click="store.selectedPlanId = null"><Icon name="users" size="sm" />{{ t('qualityOps.allAccounts') }}<span>{{ plans.length }}</span></button>
          <div class="rules-scroll" data-testid="rules-scroll" :aria-busy="store.rulesLoading">
            <div v-if="store.rulesError" class="panel-error" role="alert">{{ store.rulesError }}<button @click="store.refreshRules(true)">{{ t('qualityOps.retry') }}</button></div>
            <div v-if="!store.rulesLoaded && store.rulesLoading" class="skeleton-stack" role="status" :aria-label="t('qualityOps.loading')"><div v-for="n in 4" :key="n" class="rule-skeleton"><span /><span /><span /></div></div>
            <article v-for="plan in filteredPlans" :key="plan.id" class="rule-card" :class="{ selected: store.selectedPlanId === plan.id }" :data-plan-id="plan.id">
              <div class="rule-card-top">
                <button class="rule-select" :aria-pressed="store.selectedPlanId === plan.id" @click="store.selectedPlanId = plan.id">
                  <span class="account-avatar">{{ name(plan).slice(0, 1) }}</span>
                  <span class="min-w-0"><strong :title="name(plan)">{{ name(plan) }}</strong><span class="rule-meta">#{{ plan.account_id }}<span>·</span>{{ t('qualityOps.title') }} {{ plan.id }}</span></span>
                </button>
                <button class="state-toggle" :class="plan.enabled ? 'state-enabled' : 'state-paused'" :disabled="!!pending[plan.id]" :title="t(plan.enabled ? 'qualityOps.pause' : 'qualityOps.enable')" @click="toggle(plan)"><span />{{ t(plan.enabled ? 'qualityOps.activeShort' : 'qualityOps.paused') }}</button>
              </div>
              <div class="rule-model"><code>{{ plan.model_id }}</code></div>
              <div class="rule-schedule"><span>{{ t('qualityOps.nextRun') }}</span><time :datetime="plan.next_run_at || undefined">{{ plan.enabled ? date(plan.next_run_at) : '—' }}</time></div>
              <footer class="rule-actions">
                <button @click="history(plan)"><Icon name="document" size="xs" />{{ t('qualityOps.historyShort') }}</button>
                <button :disabled="!!pending[plan.id] || !plan.enabled" @click="run(plan)"><Icon name="play" size="xs" />{{ pending[plan.id] === 'run' ? t('qualityOps.submitting') : t('qualityOps.runShort') }}</button>
              </footer>
            </article>
            <div v-if="store.rulesLoaded && !filteredPlans.length" class="panel-empty"><Icon name="inbox" size="lg" /><p>{{ plans.length ? t('qualityOps.noMatchingRules') : t('qualityOps.empty') }}</p></div>
          </div>
        </section>
        <section class="workspace-panel operations-panel" aria-labelledby="quality-operations-heading">
          <header class="panel-heading"><div><h3 id="quality-operations-heading">{{ t('qualityOps.operations') }}</h3><p>{{ t('qualityOps.operationsSubtitle') }}</p></div><button class="icon-button" :disabled="store.operationsLoading" :aria-label="t('qualityOps.refreshOperations')" @click="refreshOperations"><Icon name="refresh" size="sm" :class="{ 'animate-spin': store.operationsLoading }" /></button></header>
          <div class="operations-toolbar">
            <div class="scope-label"><span class="scope-dot" />{{ selectedPlan ? name(selectedPlan) : t('qualityOps.allAccounts') }}<button v-if="selectedPlan" :aria-label="t('qualityOps.clearScope')" @click="store.selectedPlanId = null"><Icon name="x" size="xs" /></button></div>
            <select v-model="store.operationFilter" :aria-label="t('qualityOps.filterRecords')">
              <option value="all">{{ t('qualityOps.allOutcomes') }}</option>
              <option value="attention">{{ t('qualityOps.needsAttention') }}</option>
              <option value="passed">{{ t('qualityOps.passedRounds') }}</option>
              <option value="failed">{{ t('qualityOps.otherRounds') }}</option>
            </select>
          </div>
          <div v-if="store.operationsError" role="alert" class="panel-error">{{ store.operationsError }}<button @click="refreshOperations">{{ t('qualityOps.retry') }}</button></div>
          <div class="operations-scroll" data-testid="operations-scroll" :aria-busy="store.operationsLoading">
            <table class="operations-table">
              <thead><tr><th>{{ t('qualityOps.time') }}</th><th>{{ t('qualityOps.accounts') }}</th><th>{{ t('qualityOps.testResult') }}</th><th><span class="sr-only">{{ t('qualityOps.details') }}</span></th></tr></thead>
              <tbody>
                <template v-if="!store.operationsLoaded && store.operationsLoading"><tr v-for="n in 6" :key="`loading-${n}`" class="loading-row"><td v-for="c in 4" :key="c"><span class="cell-skeleton" /></td></tr></template>
                <tr v-for="operation in filteredOperations" :key="operation.id" :class="{ 'selected-row': detailResult?.id === operation.id }" :data-operation-id="operation.id">
                  <td class="time-cell"><strong>{{ clock(operation.started_at) }}</strong><span>{{ day(operation.started_at) }}</span></td>
                  <td class="account-cell"><button :title="operation.account_name" @click="store.selectedPlanId = operation.plan_id"><strong>{{ operation.account_name || `#${operation.account_id}` }}</strong></button><span>#{{ operation.account_id }} · {{ operation.plan_id }}</span></td>
                  <td><span class="test-count" :class="passed(operation) ? 'test-passed' : 'test-other'"><Icon :name="passed(operation) ? 'checkCircle' : 'exclamationCircle'" size="xs" />{{ resultLabel(operation) }}</span><span class="cell-secondary">{{ t(passed(operation) ? 'qualityOps.roundPassed' : 'qualityOps.roundNotPassed') }}</span></td>
                  <td><button class="detail-button" :aria-label="t('qualityOps.openRound', { account: operation.account_name, time: date(operation.started_at) })" @click="openDetail(operation)"><span>{{ t('qualityOps.details') }}</span><Icon name="arrowRight" size="sm" /></button></td>
                </tr>
              </tbody>
            </table>
            <div v-if="store.operationsLoaded && !filteredOperations.length" class="panel-empty"><Icon name="document" size="lg" /><h4>{{ t('qualityOps.noResults') }}</h4><p>{{ t('qualityOps.filteredEmptyHint') }}</p></div>
          </div>
          <footer class="operations-footer"><span>{{ t('qualityOps.showingLoaded', { shown: filteredOperations.length, loaded: operations.length }) }}</span><button v-if="store.cursor" :disabled="store.moreLoading || store.operationsLoading" @click="moreOperations">{{ store.moreLoading ? t('qualityOps.loading') : t('qualityOps.loadMore') }}<Icon name="arrowDown" size="xs" /></button><span v-else>{{ t('qualityOps.loadedEnd') }}</span></footer>
        </section>
      </div>
    </div>

    <BaseDialog :show="!!detailResult" :title="t('qualityOps.roundDetail')" placement="right" width="wide" close-on-click-outside @close="detailResult = null">
      <template v-if="detailResult">
        <div class="detail-heading">
          <span class="account-avatar">{{ (detailResult.account_name || '?').slice(0, 1) }}</span>
          <div>
            <h3>{{ detailResult.account_name || `#${detailResult.account_id}` }}</h3>
            <p>{{ date(detailResult.started_at) }} · {{ resultLabel(detailResult) }}</p>
          </div>
          <a class="account-management-link" href="/admin/accounts">{{ t('qualityOps.manageAccount') }}<Icon name="externalLink" size="xs" /></a>
        </div>
        <div class="response-heading">{{ t('qualityOps.latency') }}<span v-if="detailResult.latency_ms">{{ (detailResult.latency_ms / 1000).toFixed(1) }}s</span></div>
        <pre class="response-content">{{ detailResult.response_text || detailResult.error_message || t('qualityOps.noAnswer') }}</pre>
      </template>
      <template #footer>
        <div class="detail-footer">
          <button class="btn btn-secondary" :disabled="operationIndex <= 0" @click="navigateOperation(-1)"><Icon name="arrowLeft" size="sm" />{{ t('qualityOps.previousRound') }}</button>
          <span>{{ detailResult ? `${operationIndex + 1} / ${filteredOperations.length}` : t('qualityOps.history') }}</span>
          <button class="btn btn-secondary" :disabled="operationIndex < 0 || operationIndex >= filteredOperations.length - 1" @click="navigateOperation(1)">{{ t('qualityOps.nextRound') }}<Icon name="arrowRight" size="sm" /></button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAccountQualityStore } from '@/stores/accountQuality'
import { useAuthStore } from '@/stores/auth'
import scheduledTests, { triggerNow, type QualityHistoryResult } from '@/api/admin/scheduledTests'
import type { ScheduledTestPlan } from '@/types'

const { t } = useI18n()
const store = useAccountQualityStore()
const auth = useAuthStore()
const { plans, operations } = storeToRefs(store)
const error = ref('')
const notice = ref('')
const pending = ref<Record<number, string>>({})
const detailResult = ref<QualityHistoryResult | null>(null)
let alive = true
let poll: ReturnType<typeof setInterval> | undefined

const identity = () => (auth.user ? `${auth.user.id}:${auth.user.role}` : '')
const refreshing = computed(() => store.rulesLoading || store.operationsLoading)
const enabledCount = computed(() => plans.value.filter((p) => p.enabled).length)
const attentionCount = computed(() => operations.value.filter((op) => !passed(op)).length)
const selectedPlan = computed(() => plans.value.find((p) => p.id === store.selectedPlanId))
const filteredPlans = computed(() => {
  const query = store.search.trim().toLowerCase()
  return plans.value.filter((p) => !query || `${name(p)} ${p.account_id} ${p.id} ${p.model_id}`.toLowerCase().includes(query))
})
const filteredOperations = computed(() => operations.value.filter((op) => {
  if (store.selectedPlanId !== null && op.plan_id !== store.selectedPlanId) return false
  if (store.operationFilter === 'attention') return !passed(op)
  if (store.operationFilter === 'passed') return passed(op)
  if (store.operationFilter === 'failed') return !passed(op)
  return true
}))
const operationIndex = computed(() => filteredOperations.value.findIndex((op) => op.id === detailResult.value?.id))

function name(plan: ScheduledTestPlan) {
  return plan.account_name || `#${plan.account_id ?? plan.id}`
}
function date(value: string | null | undefined) {
  return value ? new Date(value).toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—'
}
function clock(value: string | number | undefined) {
  return value ? new Date(value).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }) : '—'
}
function day(value: string | undefined) {
  return value ? new Date(value).toLocaleDateString() : '—'
}
function passed(op: QualityHistoryResult) {
  return op.status === 'success'
}
function resultLabel(op: QualityHistoryResult) {
  if (op.status === 'success') return t('qualityOps.passed')
  if (op.status === 'degraded') return t('qualityOps.degraded')
  if (op.status === 'unknown') return t('qualityOps.unknown')
  return t('qualityOps.requestError')
}
function message(e: unknown) {
  const err = e as { response?: { data?: { message?: string; error?: string } }; message?: string }
  return err.response?.data?.message || err.response?.data?.error || err.message || t('qualityOps.error')
}
async function load() { await store.refresh() }
async function refreshOperations() { await store.refreshOperations() }
async function moreOperations() { await store.refreshOperations(true) }
async function planAction(plan: ScheduledTestPlan, kind: string, fn: () => Promise<unknown>) {
  if (pending.value[plan.id]) return
  pending.value[plan.id] = kind
  error.value = ''
  notice.value = ''
  const scope = identity()
  try {
    await fn()
    if (!alive || scope !== identity()) return
    if (kind === 'run') notice.value = t('qualityOps.queued')
    await store.refreshRules(true)
  } catch (e) {
    if (alive && scope === identity()) error.value = message(e)
  } finally {
    delete pending.value[plan.id]
  }
}
async function toggle(plan: ScheduledTestPlan) {
  await planAction(plan, 'toggle', () => scheduledTests.update(plan.id, { enabled: !plan.enabled }))
}
async function run(plan: ScheduledTestPlan) {
  await planAction(plan, 'run', () => triggerNow(plan.id))
}
function history(plan: ScheduledTestPlan) {
  store.selectedPlanId = plan.id
  const first = filteredOperations.value[0]
  if (first) detailResult.value = first
}
function openDetail(operation: QualityHistoryResult) {
  detailResult.value = operation
}
function navigateOperation(offset: number) {
  const next = filteredOperations.value[operationIndex.value + offset]
  if (next) detailResult.value = next
}

onMounted(() => {
  void load()
  poll = setInterval(() => {
    if (document.visibilityState === 'visible' && !refreshing.value) {
      void store.refreshRules()
      void store.refreshOperations()
    }
  }, 30_000)
})
onBeforeUnmount(() => {
  alive = false
  if (poll) clearInterval(poll)
})
</script>

<style scoped>
.quality-workspace { max-width: 1660px; margin: 0 auto; @apply space-y-5 text-gray-900 dark:text-gray-100; }
.workspace-heading { @apply flex flex-wrap items-center justify-between gap-4; }
.workspace-eyebrow { @apply mb-1 text-[11px] font-semibold tracking-widest text-primary-600 dark:text-primary-400; }
.workspace-heading h2 { @apply text-2xl font-semibold tracking-tight; }
.workspace-subtitle { @apply mt-1.5 text-sm text-gray-500 dark:text-gray-400; }
.heading-actions { @apply flex flex-wrap items-center gap-2; }
.heading-actions .btn, .detail-footer .btn { @apply inline-flex items-center gap-2; }
.updated-label { @apply mr-2 hidden text-xs tabular-nums text-gray-400 xl:block; }
.workspace-message { @apply flex items-center justify-between gap-4 rounded-xl border px-4 py-3 text-sm; }
.message-error { @apply border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300; }
.message-success { @apply border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300; }
.workspace-summary { @apply grid grid-cols-2 rounded-2xl border border-gray-200/80 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900 lg:grid-cols-4; }
.summary-cell { @apply relative flex items-center gap-3 px-5 py-4; }
.summary-cell + .summary-cell { @apply lg:border-l lg:border-gray-100 lg:dark:border-dark-700; }
.summary-icon { @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-slate-100 text-slate-500 dark:bg-dark-800 dark:text-gray-400; }
.summary-icon.is-teal { @apply bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40; }
.summary-icon.is-amber { @apply bg-amber-50 text-amber-600 dark:bg-amber-950/40; }
.summary-cell div > span { @apply block text-xs text-gray-500 dark:text-gray-400; }
.summary-cell strong { @apply mt-1 block text-2xl font-semibold tabular-nums tracking-tight; }
.summary-cell strong small { @apply text-sm font-normal text-gray-400; }
.summary-link { @apply ml-auto inline-flex items-center gap-1 text-xs text-primary-600; }
.workspace-columns { display: grid; grid-template-columns: minmax(290px, .29fr) minmax(0, .71fr); gap: 20px; }
.workspace-panel { height: clamp(32rem, calc(100dvh - 25rem), 56rem); @apply flex min-w-0 flex-col overflow-hidden rounded-2xl border border-gray-200/80 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.panel-heading { @apply flex shrink-0 items-center justify-between gap-3 px-5 pb-4 pt-5; }
.panel-heading h3 { @apply flex items-center gap-2 text-base font-semibold; }
.panel-heading p { @apply mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.count-label { @apply rounded-md bg-gray-100 px-1.5 py-0.5 text-xs tabular-nums text-gray-500 dark:bg-dark-800; }
.icon-button { @apply flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-gray-200 text-gray-500 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800; }
.rule-search { @apply mx-4 mb-3 flex items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 text-gray-400 dark:border-dark-700 dark:bg-dark-800; }
.rule-search input { @apply h-9 min-w-0 flex-1 bg-transparent text-sm text-gray-800 outline-none dark:text-gray-100; }
.all-accounts { @apply mx-4 mb-3 flex items-center gap-2 rounded-lg px-3 py-2.5 text-left text-sm text-gray-500 dark:text-gray-400; }
.all-accounts span:last-child { @apply ml-auto text-xs tabular-nums; }
.all-accounts.selected { @apply bg-primary-50 font-medium text-primary-700 dark:bg-primary-950/40 dark:text-primary-300; }
.rules-scroll { @apply min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain px-4 pb-4; scrollbar-gutter: stable; }
.rule-card { @apply rounded-xl border border-gray-200 p-3.5 transition-colors dark:border-dark-700; }
.rule-card.selected { @apply border-primary-300 bg-primary-50/30 ring-1 ring-primary-100 dark:border-primary-700 dark:bg-primary-950/20 dark:ring-primary-900; }
.rule-card-top { @apply flex items-start gap-2; }
.rule-select { @apply flex min-w-0 flex-1 items-start gap-2.5 text-left; }
.account-avatar { @apply flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-slate-100 text-xs font-semibold text-slate-600 dark:bg-dark-700 dark:text-gray-300; }
.rule-select strong { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; @apply text-[13px] font-semibold leading-5; }
.rule-meta { @apply mt-1 flex gap-1.5 text-[11px] tabular-nums text-gray-400; }
.state-toggle { @apply inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 py-1 text-[10px] font-medium; }
.state-toggle > span { @apply h-1.5 w-1.5 rounded-full bg-current; }
.state-enabled { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400; }
.state-paused { @apply bg-gray-100 text-gray-500 dark:bg-dark-800; }
.rule-model { @apply mt-3 flex flex-wrap items-center gap-2 text-[11px] text-gray-600 dark:text-gray-400; }
.rule-model code { @apply rounded bg-gray-100 px-1.5 py-0.5 dark:bg-dark-800; }
.rule-schedule { @apply mt-3 flex justify-between gap-2 text-[11px] tabular-nums text-gray-400; }
.rule-schedule time { @apply text-gray-600 dark:text-gray-300; }
.rule-actions { @apply mt-3 flex items-center gap-1 border-t border-gray-100 pt-2 dark:border-dark-700; }
.rule-actions button { @apply inline-flex min-h-8 flex-1 items-center justify-center gap-1 rounded-md text-[11px] text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-800 dark:hover:text-gray-100; }
.rule-actions button:first-child { @apply text-primary-600; }
.operations-toolbar { @apply flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-t border-gray-100 bg-gray-50/60 px-5 py-3 dark:border-dark-700 dark:bg-dark-800/50; }
.scope-label { @apply flex min-w-0 items-center gap-2 text-xs font-medium text-gray-600 dark:text-gray-300; max-width: 65%; }
.scope-dot { @apply h-1.5 w-1.5 shrink-0 rounded-full bg-primary-500; }
.operations-toolbar select { @apply max-w-full rounded-lg border border-gray-200 bg-white px-2 py-1.5 text-xs text-gray-600 dark:border-dark-600 dark:bg-dark-900 dark:text-gray-300; }
.operations-scroll { @apply min-h-0 flex-1 overflow-auto overscroll-contain; scrollbar-gutter: stable; }
.operations-table { @apply w-full text-left text-xs; min-width: 650px; }
.operations-table thead { @apply sticky top-0 z-10 bg-white text-gray-400 dark:bg-dark-900; box-shadow: 0 1px 0 rgb(148 163 184 / .15); }
.operations-table th { @apply whitespace-nowrap px-4 py-3 font-medium; }
.operations-table td { @apply border-b border-gray-100 px-4 py-4 align-middle dark:border-dark-800; }
.operations-table tbody tr:hover, .selected-row { @apply bg-slate-50/80 dark:bg-dark-800/70; }
.time-cell { @apply whitespace-nowrap tabular-nums; }
.time-cell strong { @apply block text-xs font-medium; }
.time-cell > span, .account-cell > span { @apply mt-1 block text-[10px] text-gray-400; }
.account-cell { max-width: 220px; }
.account-cell button { @apply max-w-full text-left hover:text-primary-600; }
.account-cell strong { @apply block truncate font-medium; max-width: 190px; }
.test-count { @apply inline-flex items-center gap-1.5 whitespace-nowrap font-medium tabular-nums; }
.test-passed { @apply text-emerald-600 dark:text-emerald-400; }
.test-other { @apply text-rose-500 dark:text-rose-400; }
.cell-secondary { @apply mt-1.5 block truncate text-[10px] text-gray-400; max-width: 170px; }
.detail-button { @apply inline-flex min-h-8 items-center gap-1 whitespace-nowrap rounded-md px-1 text-xs text-gray-400 hover:text-primary-600; }
.operations-footer { @apply flex min-h-14 shrink-0 items-center justify-between gap-3 border-t border-gray-100 px-5 text-[11px] text-gray-400 dark:border-dark-700; }
.operations-footer button { @apply inline-flex items-center gap-1.5 text-primary-600; }
.panel-error { @apply m-3 flex items-center justify-between gap-3 rounded-lg bg-red-50 p-3 text-xs text-red-700 dark:bg-red-950/40 dark:text-red-300; }
.panel-error button { @apply shrink-0 underline; }
.panel-empty { @apply flex min-h-52 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-gray-400; }
.panel-empty p { @apply max-w-sm text-xs leading-relaxed; }
.rule-skeleton { @apply space-y-3 rounded-xl border border-gray-100 p-5 dark:border-dark-700; }
.cell-skeleton, .rule-skeleton span { @apply block h-3 rounded bg-gray-100 dark:bg-dark-700; animation: quality-pulse 1.7s ease-in-out infinite; }
.rule-skeleton span:first-child { @apply h-5 w-3/4; }
.rule-skeleton span:last-child { @apply w-1/2; }
.skeleton-stack { @apply space-y-3; }
.detail-heading { @apply mb-5 flex items-center gap-3; }
.detail-heading h3 { @apply text-base font-semibold; }
.detail-heading p { @apply mt-1 text-xs text-gray-400; }
.account-management-link { @apply ml-auto inline-flex shrink-0 items-center gap-1 text-xs text-primary-600; }
.response-heading { @apply mb-2 mt-4 flex justify-between text-xs text-gray-500; }
.response-content { @apply max-h-[40dvh] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-4 font-sans text-sm leading-7 text-gray-800 dark:bg-dark-800 dark:text-gray-200; }
.detail-footer { @apply flex items-center justify-between gap-2 text-xs text-gray-400; }
button:disabled { @apply cursor-not-allowed opacity-40; }
@keyframes quality-pulse { 50% { opacity: .45; } }
@media (max-width: 1100px) { .workspace-columns { grid-template-columns: 1fr; } .rules-panel { height: 28rem; } .operations-panel { height: 36rem; } }
@media (prefers-reduced-motion: reduce) { .cell-skeleton, .rule-skeleton span { animation: none; } }
</style>
