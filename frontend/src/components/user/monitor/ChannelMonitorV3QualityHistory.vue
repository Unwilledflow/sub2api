<template>
  <div
    v-if="enabled && groupId"
    class="channel-signal-quality mt-3 border-t border-white/70 pt-3 dark:border-dark-700/60"
    data-testid="channel-quality-strip"
  >
    <div class="channel-signal-timeline__legend mb-2 flex items-center justify-between text-[11px] font-medium text-gray-400">
      <span>{{ t('channelMonitorV3.qualityStripTitle', { n: events.length }) }}</span>
      <span v-if="degradedCount" class="tabular-nums font-semibold text-red-500 dark:text-red-400">
        {{ t('channelMonitorV3.qualityStripDegraded', { n: degradedCount }) }}
      </span>
    </div>

    <div v-if="loading" class="pb-1 text-[10px] text-gray-400">{{ t('monitorCommon.qualityHistoryLoading') }}</div>
    <div v-else-if="loadFailed" class="pb-1 text-[10px] text-red-500/90 dark:text-red-400/90">{{ t('monitorCommon.qualityHistoryLoadFailed') }}</div>
    <div v-else-if="!events.length" class="pb-1 text-[10px] text-gray-400">{{ emptyHint }}</div>

    <!--
      Each cell is the artwork the probe actually generated. The strip replaces
      the old green bars, which rendered "we have not degraded" as another green
      block and so looked identical to the availability strip above it.
    -->
    <div v-else class="quality-pelican-strip" @mouseleave="clearHover">
      <button
        v-for="(event, index) in events"
        :key="event.id"
        type="button"
        class="quality-pelican"
        :class="[`is-${event.status}`, { 'is-active': hoveredIndex === index }]"
        :data-testid="`quality-pelican-${event.id}`"
        :aria-label="cellLabel(event)"
        @mouseenter="hoverEvent(index, $event)"
        @focus="hoverEvent(index, $event)"
        @blur="clearHover"
      >
        <span class="quality-pelican__frame">
          <iframe
            v-if="thumbnails.get(event.id)"
            class="quality-pelican__thumb"
            :srcdoc="thumbnails.get(event.id)"
            sandbox="allow-scripts"
            scrolling="no"
            tabindex="-1"
            aria-hidden="true"
          />
          <span v-else class="quality-pelican__placeholder" aria-hidden="true" />
        </span>
        <span class="quality-pelican__time tabular-nums">{{ formatChipTime(event.created_at) }}</span>
      </button>
    </div>

    <Teleport to="body">
      <Transition name="v3-timeline-tooltip">
        <div
          v-if="hovered && hoveredEvent"
          class="quality-pelican-popover"
          :style="popoverStyle"
          role="tooltip"
          data-testid="quality-pelican-popover"
        >
          <div class="quality-pelican-popover__head">
            <span class="font-semibold" :class="statusTextClass(hoveredEvent.status)">{{ statusLabel(hoveredEvent.status) }}</span>
            <span class="font-mono text-amber-300/90">#{{ hoveredEvent.id }}</span>
            <span>{{ t(hoveredEvent.quality_provider === 'chanshui' ? 'monitorCommon.qualityChanshui' : 'monitorCommon.qualityPelican') }}</span>
            <span class="text-gray-300">{{ formatFullTime(hoveredEvent.created_at) }}</span>
          </div>
          <div v-if="hoveredEvent.error_message" class="quality-pelican-popover__reason">{{ hoveredEvent.error_message }}</div>

          <!--
            The full-size preview is the same stored HTML the probe produced,
            scaled from its native 960x640 canvas so the artwork is inspected at
            its authored proportions rather than cropped.
          -->
          <div class="quality-pelican-popover__viewport">
            <div v-if="previewState === 'loading'" class="grid h-full place-items-center text-[10px] text-gray-400">
              {{ t('monitorCommon.qualityArtworkLoading') }}
            </div>
            <div v-else-if="previewState === 'failed'" class="grid h-full place-items-center text-[10px] text-red-300">
              {{ t('monitorCommon.qualityArtworkFailed') }}
            </div>
            <iframe
              v-else-if="previewState === 'ready' && previewHtml"
              class="quality-pelican-popover__frame"
              :srcdoc="previewHtml"
              sandbox="allow-scripts"
              title="quality-check artwork"
              data-testid="quality-pelican-artwork"
            />
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getQualityArtwork, getQualityEvents, type MonitorQualityEvent } from '@/api/channelMonitorV2'

const props = withDefaults(defineProps<{
  groupId?: number
  enabled?: boolean
  /** True when detection is switched on but the group has no probe plan yet. */
  detectionArmed?: boolean
}>(), {
  groupId: undefined,
  enabled: false,
  detectionArmed: false,
})

const { t, locale } = useI18n()
const events = ref<MonitorQualityEvent[]>([])
const loading = ref(false)
const loadFailed = ref(false)
const hovered = ref(false)
const hoveredIndex = ref<number | null>(null)
const popoverPosition = ref({ left: 0, top: 0, x: '-50%' })
const previewHtml = ref('')
const previewState = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')

/** Stored artwork per event id, rendered as the strip's thumbnails. */
const thumbnails = reactive(new Map<number, string>())
const previewCache = new Map<number, string>()

const hoveredEvent = computed(() => (hoveredIndex.value === null ? null : events.value[hoveredIndex.value] ?? null))
const degradedCount = computed(() => events.value.filter(event => event.status === 'degraded').length)
let loadGeneration = 0
let inflight: AbortController | null = null

const CHANSHUI = 'chanshui'

/**
 * The chanshui evaluator stores no artwork — it reports an audit summary
 * instead, so those probes get a text-only popover and no thumbnail.
 */
function hasArtwork(event: MonitorQualityEvent): boolean {
  return event.quality_provider !== CHANSHUI
}

async function load() {
  const generation = ++loadGeneration
  if (!props.enabled || !props.groupId) {
    events.value = []
    return
  }
  loading.value = true
  loadFailed.value = false
  try {
    const list = await getQualityEvents(props.groupId, 30)
    if (generation !== loadGeneration) return
    events.value = Array.isArray(list) ? list : []
    void loadThumbnails(generation)
  } catch {
    if (generation !== loadGeneration) return
    loadFailed.value = true
    events.value = []
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}

/**
 * Say which kind of empty this is. A group with detection switched on but no
 * scheduled test plan produces no probes at all, and the old copy ("no records
 * in this range") read as if the probes were merely late.
 */
const emptyHint = computed(() => props.detectionArmed
  ? t('channelMonitorV3.qualityStripArmedEmpty')
  : t('channelMonitorV3.qualityStripEmpty'))

/**
 * Fetch the stored artwork for the visible probes. Requests run sequentially and
 * are capped so a card with 30 probes cannot fire 30 simultaneous render-heavy
 * iframes. The list is newest-first, so the cap keeps the newest probes — the
 * ones a "what is happening now" read actually needs. It used to slice from the
 * head of the array, which on a newest-first list dropped the newest thumbnails.
 */
async function loadThumbnails(generation: number) {
  const groupId = props.groupId
  if (!groupId) return
  const targets = events.value.filter(hasArtwork).slice(0, THUMBNAIL_LIMIT)
  for (const event of targets) {
    if (generation !== loadGeneration) return
    if (thumbnails.has(event.id)) continue
    try {
      const html = await getQualityArtwork(groupId, event.id)
      if (generation !== loadGeneration) return
      if (html) thumbnails.set(event.id, html)
    } catch {
      // A probe without stored artwork simply keeps its placeholder cell.
    }
  }
}

const THUMBNAIL_LIMIT = 18

watch([() => props.groupId, () => props.enabled], () => {
  thumbnails.clear()
  previewCache.clear()
  void load()
}, { immediate: true })

const chipTimeFormatter = computed(() => new Intl.DateTimeFormat(locale.value || undefined, { hour: '2-digit', minute: '2-digit' }))
const fullTimeFormatter = computed(() => new Intl.DateTimeFormat(locale.value || undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }))
function formatChipTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '--:--'
  return chipTimeFormatter.value.format(date)
}

function formatFullTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return fullTimeFormatter.value.format(date)
}

function statusLabel(status: MonitorQualityEvent['status']) {
  if (status === 'degraded' || status === 'audit_fail') return t('monitorCommon.qualityHistoryDegraded')
  if (status === 'failed') return t('monitorCommon.qualityHistoryFailed')
  if (status === 'unknown') return t('monitorCommon.qualityHistoryUnknown')
  return t('monitorCommon.qualityHistoryPass')
}

function statusTextClass(status: MonitorQualityEvent['status']) {
  if (status === 'degraded' || status === 'audit_fail') return 'text-red-400'
  if (status === 'failed') return 'text-amber-300'
  if (status === 'unknown') return 'text-slate-300'
  return 'text-emerald-400'
}

function cellLabel(event: MonitorQualityEvent) {
  return `#${event.id} · ${formatFullTime(event.created_at)} · ${statusLabel(event.status)}`
}

function positionPopover(event?: Event) {
  const target = event?.currentTarget
  if (!(target instanceof HTMLElement) || typeof window === 'undefined') return
  const rect = target.getBoundingClientRect()
  const gutter = 16
  const width = Math.min(360, window.innerWidth - gutter * 2)
  const center = rect.left + rect.width / 2
  if (center + width / 2 > window.innerWidth - gutter) {
    popoverPosition.value = { left: window.innerWidth - gutter, top: rect.top - 8, x: '-100%' }
  } else if (center - width / 2 < gutter) {
    popoverPosition.value = { left: gutter, top: rect.top - 8, x: '0%' }
  } else {
    popoverPosition.value = { left: center, top: rect.top - 8, x: '-50%' }
  }
}

function hoverEvent(index: number, event?: Event) {
  positionPopover(event)
  hovered.value = true
  hoveredIndex.value = index
  const current = events.value[index]
  if (current) void loadPreview(current)
}

function clearHover() {
  hovered.value = false
  hoveredIndex.value = null
  inflight?.abort()
  inflight = null
  previewState.value = 'idle'
  previewHtml.value = ''
}

async function loadPreview(event: MonitorQualityEvent) {
  if (!hasArtwork(event) || !props.groupId) {
    inflight?.abort()
    inflight = null
    previewState.value = 'idle'
    previewHtml.value = ''
    return
  }
  const cached = previewCache.get(event.id)
  if (cached !== undefined) {
    previewHtml.value = cached
    previewState.value = cached ? 'ready' : 'failed'
    return
  }
  // Thumbnails are the same document, so a hover is usually a cache hit.
  const fromStrip = thumbnails.get(event.id)
  if (fromStrip) {
    previewCache.set(event.id, fromStrip)
    previewHtml.value = fromStrip
    previewState.value = 'ready'
    return
  }

  inflight?.abort()
  const controller = new AbortController()
  inflight = controller
  previewState.value = 'loading'
  previewHtml.value = ''
  try {
    const html = await getQualityArtwork(props.groupId, event.id, controller.signal)
    if (inflight !== controller) return
    previewCache.set(event.id, html)
    if (previewCache.size > 30) previewCache.delete(previewCache.keys().next().value!)
    previewHtml.value = html
    previewState.value = html ? 'ready' : 'failed'
  } catch (error) {
    const name = (error as { name?: string } | null)?.name
    if (name === 'CanceledError' || name === 'AbortError') return
    if (inflight !== controller) return
    previewState.value = 'failed'
  }
}

let refreshTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  refreshTimer = setInterval(() => {
    if (props.enabled && !loading.value && !hovered.value && document.visibilityState !== 'hidden') void load()
  }, 60000)
})
onBeforeUnmount(() => {
  loadGeneration++
  if (refreshTimer) clearInterval(refreshTimer)
  inflight?.abort()
})

const popoverStyle = computed(() => ({
  '--tooltip-left': `${popoverPosition.value.left}px`,
  '--tooltip-top': `${popoverPosition.value.top}px`,
  '--tooltip-x': popoverPosition.value.x,
}))
</script>

<style scoped>
.quality-pelican-strip {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  padding-bottom: 2px;
  scrollbar-width: none;
}

.quality-pelican-strip::-webkit-scrollbar {
  display: none;
}

.quality-pelican {
  display: flex;
  flex: 0 0 auto;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  border: 0;
  background: transparent;
  padding: 0;
  cursor: crosshair;
  transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1);
}

.quality-pelican__frame {
  position: relative;
  display: block;
  width: 40px;
  height: 28px;
  overflow: hidden;
  border-radius: 7px;
  background: #fff;
  /* The ring carries the verdict so the artwork itself stays unretouched. */
  box-shadow: 0 0 0 2px rgb(16 185 129 / 0.9), 0 1px 4px rgb(15 23 42 / 0.12);
  transition: box-shadow 160ms ease;
}

.quality-pelican.is-degraded .quality-pelican__frame,
.quality-pelican.is-audit_fail .quality-pelican__frame {
  box-shadow: 0 0 0 2px rgb(239 68 68 / 0.95), 0 1px 4px rgb(15 23 42 / 0.12);
}

.quality-pelican.is-failed .quality-pelican__frame {
  box-shadow: 0 0 0 2px rgb(245 158 11 / 0.95), 0 1px 4px rgb(15 23 42 / 0.12);
}

.quality-pelican.is-unknown .quality-pelican__frame {
  box-shadow: 0 0 0 2px rgb(148 163 184 / 0.9), 0 1px 4px rgb(15 23 42 / 0.12);
}

.quality-pelican__thumb {
  width: 960px;
  height: 640px;
  border: 0;
  background: #fff;
  transform: scale(0.041667);
  transform-origin: top left;
  pointer-events: none;
}

.quality-pelican__placeholder {
  display: block;
  width: 100%;
  height: 100%;
  background: repeating-linear-gradient(45deg, rgb(148 163 184 / 0.16) 0 4px, transparent 4px 8px);
}

.quality-pelican__time {
  font-size: 9px;
  font-weight: 600;
  line-height: 1;
  color: rgb(148 163 184);
}

.quality-pelican.is-active {
  transform: translateY(-2px);
}

.quality-pelican:focus-visible {
  outline: 2px solid rgb(59 130 246 / 0.6);
  outline-offset: 3px;
  border-radius: 8px;
}

.quality-pelican-popover {
  position: fixed;
  left: var(--tooltip-left, 50%);
  top: var(--tooltip-top, 0px);
  z-index: 50;
  width: max-content;
  max-width: min(360px, calc(100vw - 32px));
  transform: translateX(var(--tooltip-x, -50%)) translateY(-100%);
  border: 1px solid rgb(255 255 255 / 0.14);
  border-radius: 10px;
  background: rgb(15 23 42 / 0.96);
  padding: 8px 10px;
  color: rgb(248 250 252);
  box-shadow: 0 14px 30px rgb(15 23 42 / 0.35);
  pointer-events: none;
}

.quality-pelican-popover__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 10px;
  line-height: 1.4;
  white-space: nowrap;
}

.quality-pelican-popover__reason {
  margin-top: 4px;
  max-width: 320px;
  font-size: 10px;
  line-height: 1.45;
  color: rgb(252 165 165);
}

.quality-pelican-popover__viewport {
  position: relative;
  width: 320px;
  height: 213px;
  margin-top: 6px;
  overflow: hidden;
  border-radius: 8px;
  background: #fff;
}

.quality-pelican-popover__frame {
  width: 960px;
  height: 640px;
  border: 0;
  background: #fff;
  transform: scale(0.33333);
  transform-origin: top left;
}

@media (prefers-reduced-motion: reduce) {
  .quality-pelican {
    transition: none;
  }
}
</style>
