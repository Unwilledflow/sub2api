<template>
  <article class="channel-signal-card group relative z-0 glass-card flex min-h-[286px] flex-col overflow-visible rounded-[24px] p-5 text-left hover:z-20">
    <header class="channel-signal-card__header flex items-start gap-3">
      <span class="channel-signal-card__provider grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="channel-signal-card__name truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ groupLabel }}</div>
        <div class="channel-signal-card__meta mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span v-if="showPlatformBadge" class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-primary-50 px-1.5 py-0.5 font-mono text-[10px] font-medium text-primary-700 dark:bg-dark-700 dark:text-gray-300">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
        </div>
      </div>
      <span
        data-testid="channel-status-sample-badge"
        class="channel-signal-card__sample shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold"
        :class="badgeClass"
      >{{ badgeLabel }}</span>
    </header>

    <div class="channel-signal-card__metrics mt-5 grid grid-cols-2 gap-2 sm:grid-cols-4">
      <div
        v-for="metric in metrics"
        :key="metric.key"
        class="channel-signal-card__metric rounded-2xl border p-3"
        :class="metric.weak
          ? 'border-dashed border-slate-200/70 bg-slate-50/50 dark:border-dark-700/40 dark:bg-dark-900/20'
          : 'border-slate-200/80 bg-slate-50/85 dark:border-dark-700/50 dark:bg-dark-900/40'"
        :title="metric.hint"
        :data-testid="`channel-status-metric-${metric.key}`"
      >
        <div class="flex items-baseline justify-between gap-1">
          <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ metric.label }}</div>
          <div v-if="metric.weak" class="text-[9px] font-medium text-gray-300 dark:text-dark-500" aria-hidden="true">~</div>
        </div>
        <div
          class="mt-1.5 font-mono text-lg font-bold tabular-nums"
          :class="metric.weak ? 'text-gray-400 dark:text-dark-500' : metric.valueClass"
        >{{ metric.value }}</div>
        <div v-if="metric.note" class="mt-0.5 truncate text-[9px] font-medium text-gray-400" :title="metric.note">{{ metric.note }}</div>
      </div>
    </div>

    <p
      v-if="sampleHint"
      class="channel-signal-card__sample-hint mt-2 text-[10px] font-medium text-gray-400"
      data-testid="channel-status-sample-hint"
    >{{ sampleHint }}</p>

    <ChannelMonitorV3Timeline
      class="channel-signal-card__timeline mt-auto"
      :buckets="row.buckets"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
      :bucket-seconds="bucketSeconds"
      :quality-buckets="row.quality_buckets"
      :quality-enabled="row.quality_enabled"
      :quality-armed="row.quality_armed"
      :group-id="row.group_id"
    />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import {
  availabilityTextClass,
  formatMonitorMs,
  formatMonitorPercent,
} from '@/features/channel-monitor-v2/monitorFormat'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from './ProviderIcon.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'
import { cacheRateIsMeaningful, resolveCardMetrics, ttftIsMeaningful } from './channelStatus'

const props = withDefaults(defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  /** Seconds each trajectory slot covers; labels the strip's own scale. */
  bucketSeconds?: number
  userRateMultiplier?: number | null
  showPlatformBadge?: boolean
}>(), {
  bucketSeconds: 0,
  showPlatformBadge: true,
})
const { t, locale } = useI18n()
const { providerLabel, providerBadgeClass } = useChannelMonitorFormat()

// vue-i18n is always installed in the app, but tests and embeds may mount this
// card with a partial i18n stub, so the formatter locale degrades gracefully
// instead of throwing during render.
const formatLocale = computed(() => locale?.value || 'zh-CN')

const groupLabel = computed(() => props.row.group_name || t('channelMonitorV3.unknownGroup'))
const formattedUserRate = computed(() => {
  const value = props.userRateMultiplier
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)}x` : '-'
})

// Read the newest window that actually carries samples, not merely the newest
// window that exists. A one-request bucket yields 0% or 100% cache rate and a
// meaningless TTFT percentile; those values used to be printed as if precise.
const card = computed(() => resolveCardMetrics(props.row))
const activeMetrics = computed(() => card.value.metrics)
const activeHealth = computed(() => card.value.health)

const availabilityPercent = computed(() => (1 - (activeMetrics.value.error_rate ?? 0)) * 100)

const badgeLabel = computed(() => {
  if (card.value.insufficient) return t('channelMonitorV3.unknown')
  const overall = activeHealth.value.overall
  if (overall === 'healthy' || overall === 'warning' || overall === 'critical') {
    return t(`channelMonitorV3.${overall}`)
  }
  return t('channelMonitorV3.unknown')
})

const badgeClass = computed(() => {
  if (card.value.insufficient) {
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
  const overall = activeHealth.value.overall
  if (overall === 'critical') return 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
  if (overall === 'warning') return 'bg-amber-100 text-amber-800 dark:bg-amber-500/20 dark:text-amber-300'
  if (overall === 'healthy') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
})

/** Values whose denominator is too thin render de-emphasised rather than as fact. */
const metrics = computed(() => {
  const m = activeMetrics.value
  const insufficient = card.value.insufficient
  const cacheWeak = insufficient || !cacheRateIsMeaningful(card.value)
  const ttftWeak = insufficient || !ttftIsMeaningful(card.value)
  const ttftSamples = m.ttft?.sample_count ?? 0

  return [
    {
      key: 'cache',
      label: t('channelMonitorV3.cacheRate'),
      value: cacheWeak ? '—' : formatMonitorPercent(m.cache_rate, formatLocale.value),
      valueClass: 'text-gray-900 dark:text-gray-100',
      weak: cacheWeak,
      hint: cacheWeak ? t('channelMonitorV3.cacheRateThin') : '',
      note: '',
    },
    {
      key: 'success',
      label: t('channelMonitorV3.successRate'),
      value: formatMonitorPercent(availabilityPercent.value / 100, formatLocale.value),
      valueClass: availabilityTextClass(availabilityPercent.value),
      weak: insufficient,
      hint: '',
      note: '',
    },
    {
      key: 'ttft',
      label: t('channelMonitorV3.ttft'),
      value: ttftWeak ? '—' : formatMonitorMs(m.ttft?.p50_ms),
      valueClass: 'text-gray-900 dark:text-gray-100',
      weak: ttftWeak,
      hint: ttftWeak ? t('channelMonitorV3.ttftThin') : '',
      // The percentile is kept even when it is too thin to headline, so say how
      // many observations sit behind it instead of leaving the cell blank.
      note: ttftWeak && ttftSamples > 0 && m.ttft?.p50_ms != null
        ? t('channelMonitorV3.ttftSamplesShort', { n: ttftSamples, value: formatMonitorMs(m.ttft.p50_ms) })
        : '',
    },
    {
      key: 'duration',
      label: t('channelMonitorV3.duration'),
      value: formatMonitorMs(m.duration?.p50_ms),
      valueClass: 'text-gray-900 dark:text-gray-100',
      weak: false,
      hint: t('channelMonitorV3.durationHint'),
      note: '',
    },
  ]
})

/**
 * Name the window the numbers came from, including its time. Without this the
 * card silently mixed "latest 5-minute bucket" with "whole selected range",
 * and a rate pulled from an older window read as if it were current.
 */
const sampleHint = computed(() => {
  if (card.value.insufficient) return t('channelMonitorV3.insufficientHint')
  const samples = card.value.requestCount
  if (samples <= 0) return ''
  const scope = card.value.fromRange
    ? t('channelMonitorV3.scopeRange')
    : t('channelMonitorV3.scopeLatestBucket', { time: formatBucketTime(card.value.bucket?.bucket_start) })
  return t('channelMonitorV3.sampleHint', { n: samples, scope })
})

const bucketTimeFormatter = computed(() => new Intl.DateTimeFormat(formatLocale.value, {
  hour: '2-digit', minute: '2-digit',
}))

/** The window start, so the reader can see how fresh the number is. */
function formatBucketTime(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return bucketTimeFormatter.value.format(date)
}
</script>
