import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { useAuthStore } from './auth'
import { listAll, listAllResults, type QualityHistoryResult } from '@/api/admin/scheduledTests'
import type { ScheduledTestPlan } from '@/types'

export const useAccountQualityStore = defineStore('accountQuality', () => {
  const auth = useAuthStore()
  const plans = ref<ScheduledTestPlan[]>([])
  const operations = ref<QualityHistoryResult[]>([])
  const cursor = ref(0)
  const rulesLoaded = ref(false)
  const operationsLoaded = ref(false)
  const rulesLoading = ref(false)
  const operationsLoading = ref(false)
  const moreLoading = ref(false)
  const rulesError = ref('')
  const operationsError = ref('')
  const updatedAt = ref(0)
  const search = ref('')
  const selectedPlanId = ref<number | null>(null)
  const operationFilter = ref('all')
  let ruleVersion = 0
  let operationVersion = 0
  let rulesFlight: Promise<void> | null = null
  const errorText = (e: unknown) => (e instanceof Error ? e.message : 'qualityOps.error')

  function reset() {
    ruleVersion++
    operationVersion++
    plans.value = []
    operations.value = []
    cursor.value = 0
    rulesLoaded.value = operationsLoaded.value = false
    rulesLoading.value = operationsLoading.value = moreLoading.value = false
    rulesError.value = operationsError.value = ''
    search.value = ''
    selectedPlanId.value = null
    operationFilter.value = 'all'
    updatedAt.value = 0
    rulesFlight = null
  }
  watch(() => (auth.user ? `${auth.user.id}:${auth.user.role}` : ''), reset, { flush: 'sync' })

  function refreshRules(force = false): Promise<void> {
    if (rulesFlight && !force) return rulesFlight
    const version = ++ruleVersion
    rulesLoading.value = true
    rulesError.value = ''
    const flight = (async () => {
      try {
        const data = await listAll()
        if (version !== ruleVersion) return
        plans.value = data
        rulesLoaded.value = true
        if (selectedPlanId.value && !data.some((p) => p.id === selectedPlanId.value)) selectedPlanId.value = null
        updatedAt.value = Date.now()
      } catch (e) {
        if (version === ruleVersion) rulesError.value = errorText(e)
      } finally {
        if (version === ruleVersion) {
          rulesLoading.value = false
          rulesFlight = null
        }
      }
    })()
    rulesFlight = flight
    return flight
  }

  async function refreshOperations(append = false) {
    if (append && (moreLoading.value || operationsLoading.value || !cursor.value)) return
    const version = ++operationVersion
    if (append) moreLoading.value = true
    else {
      operationsLoading.value = true
      moreLoading.value = false
    }
    operationsError.value = ''
    try {
      const page = await listAllResults(append ? cursor.value : 0)
      if (version !== operationVersion) return
      const unique = new Map<number, QualityHistoryResult>()
      for (const item of [...(append ? operations.value : []), ...page.items]) unique.set(item.id, item)
      operations.value = [...unique.values()]
      cursor.value = page.next_cursor
      operationsLoaded.value = true
      updatedAt.value = Date.now()
    } catch (e) {
      if (version === operationVersion) operationsError.value = errorText(e)
    } finally {
      if (version === operationVersion) operationsLoading.value = moreLoading.value = false
    }
  }

  async function refresh() {
    await Promise.all([refreshRules(), refreshOperations()])
  }

  return {
    plans,
    operations,
    cursor,
    rulesLoaded,
    operationsLoaded,
    rulesLoading,
    operationsLoading,
    moreLoading,
    rulesError,
    operationsError,
    updatedAt,
    search,
    selectedPlanId,
    operationFilter,
    refresh,
    refreshRules,
    refreshOperations,
    reset
  }
})
