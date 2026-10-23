<template>
  <div v-if="homeContent" class="relative min-h-[100dvh]">
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-[100dvh] w-full border-0"
      allowfullscreen
    ></iframe>
    <div v-else v-html="homeContent"></div>
  </div>

  <div v-else class="gateway-shell terminal-container min-h-[100dvh]">
    <!-- ── Ambient background: drifting aurora blobs over a fading grid ─────── -->
    <div class="aurora" aria-hidden="true">
      <span class="aurora-blob aurora-blob-a"></span>
      <span class="aurora-blob aurora-blob-b"></span>
      <span class="aurora-blob aurora-blob-c"></span>
      <span class="aurora-grid"></span>
      <span class="aurora-scan"></span>
    </div>

    <header class="glass-header relative z-30">
      <nav class="glass-nav" aria-label="Primary">
        <a href="#top" class="brand-link" :aria-label="siteName">
          <BrandLogo :src="siteLogo" :alt="siteName" size="sm" />
          <span class="brand-name">{{ siteName }}</span>
        </a>

        <div class="nav-actions">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="icon-button"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <LocaleSwitcher />
          <button
            type="button"
            class="icon-button"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="header-action mo-sheen"
            :aria-label="isAuthenticated ? t('home.dashboard') : t('home.login')"
          >
            <Icon :name="isAuthenticated ? 'grid' : 'login'" size="sm" />
            <span>{{ isAuthenticated ? t('home.dashboard') : t('home.login') }}</span>
          </router-link>
        </div>
      </nav>
    </header>

    <main id="top" class="relative z-10">
      <!-- ── Hero ─────────────────────────────────────────────────────────── -->
      <section class="hero" :style="heroTiltStyle" @mousemove="onHeroMove" @mouseleave="resetHeroTilt">
        <div class="hero-copy">
          <div class="brand-kicker mo-rise">
            <span class="status-dot mo-live" aria-hidden="true"></span>
            {{ t('home.heroKicker') }}
          </div>

          <h1 class="hero-title" :aria-label="siteName">
            <span
              v-for="(chunk, i) in titleChunks"
              :key="i"
              class="title-chunk"
              :style="{ '--chunk-i': i }"
              aria-hidden="true"
            >{{ chunk }}</span>
          </h1>

          <p class="hero-subtitle">{{ t('home.heroSubtitle') }}</p>
          <p class="hero-description">{{ t('home.heroDescription') }}</p>

          <div class="hero-actions">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="primary-action mo-sheen"
            >
              <Icon :name="isAuthenticated ? 'grid' : 'key'" size="sm" />
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="sm" class="action-arrow" />
            </router-link>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="secondary-action"
            >
              <Icon name="book" size="sm" />
              {{ t('home.viewDocs') }}
            </a>
          </div>

          <div class="feature-strip">
            <span v-for="(tag, i) in featureTags" :key="tag" class="feature-tag" :style="{ '--tag-i': i }">
              <Icon name="checkCircle" size="sm" />
              {{ tag }}
            </span>
          </div>
        </div>

        <!-- Live-looking console that types its own request -->
        <section
          class="console-panel"
          :class="{ 'console-panel-tilt': tiltActive }"
          :style="panelTiltStyle"
          :aria-labelledby="quickstartTitleId"
        >
          <div class="console-glow" aria-hidden="true"></div>

          <header class="console-head">
            <div>
              <div class="console-title-row">
                <Icon name="terminal" size="sm" />
                <h2 :id="quickstartTitleId">{{ t('home.quickstart.title') }}</h2>
              </div>
              <p class="console-sub">{{ t('home.quickstart.description') }}</p>
            </div>
            <span class="live-status">
              <span class="status-dot mo-live" aria-hidden="true"></span>
              {{ t('home.quickstart.live') }}
            </span>
          </header>

          <div class="protocol-tabs" role="tablist" :aria-label="t('home.quickstart.protocols')">
            <button
              v-for="protocol in protocols"
              :key="protocol.id"
              type="button"
              role="tab"
              :aria-selected="selectedProtocolId === protocol.id"
              :class="['protocol-tab', { 'protocol-tab-active': selectedProtocolId === protocol.id }]"
              @click="selectedProtocolId = protocol.id"
            >
              {{ protocol.label }}
            </button>
          </div>

          <div class="endpoint-row">
            <span class="method-badge">POST</span>
            <code class="endpoint-code">{{ selectedProtocol.endpoint }}</code>
            <span class="endpoint-ok">
              <span class="status-dot" aria-hidden="true"></span>
              200 OK
            </span>
          </div>

          <div class="code-section">
            <div class="code-head">
              <span class="code-label">{{ t('home.quickstart.request') }}</span>
              <button type="button" class="copy-button" @click="copyCommand">
                <Icon :name="copyState === 'copied' ? 'check' : 'copy'" size="xs" />
                {{ copyButtonLabel }}
              </button>
            </div>
            <pre class="request-code"><code>{{ typedCommand }}<span v-if="typing" class="caret" aria-hidden="true"></span></code></pre>
          </div>

          <div class="response-section">
            <span class="code-label">{{ t('home.quickstart.response') }}</span>
            <pre class="response-block"><code>{{ selectedProtocol.response }}</code></pre>
            <div class="response-metrics">
              <span>168 ms</span>
              <span>31 tokens</span>
              <span>$0.00093</span>
            </div>
          </div>
          <span class="sr-only" aria-live="polite">{{ copyAnnouncement }}</span>
        </section>
      </section>

      <!-- ── Stats: numbers count up once they scroll into view ───────────── -->
      <section ref="statsBandRef" class="stats-band" aria-label="Key metrics">
        <div class="stats-grid">
          <div v-for="(s, i) in statItems" :key="s.label" class="stat-cell" :style="{ '--stat-i': i }">
            <div class="stat-value">{{ displayStats[i] }}</div>
            <div class="stat-label">{{ s.label }}</div>
          </div>
        </div>
      </section>

      <!-- ── Features: cards follow the cursor with a spotlight ───────────── -->
      <section class="section-block reveal" aria-labelledby="features-title">
        <div class="section-inner">
          <h2 id="features-title" class="section-title">{{ t('home.solutions.title') }}</h2>
          <p class="section-subtitle">{{ t('home.solutions.subtitle') }}</p>
          <div class="feature-grid">
            <article
              v-for="(f, i) in featureCards"
              :key="f.title"
              class="feature-card reveal-child"
              :style="{ '--card-i': i }"
              @mousemove="onCardMove($event)"
            >
              <div class="feature-icon">
                <Icon :name="(f.icon as 'key' | 'shield' | 'chart')" size="md" />
              </div>
              <h3 class="feature-title">{{ f.title }}</h3>
              <p class="feature-desc">{{ f.desc }}</p>
            </article>
          </div>
        </div>
      </section>

      <!-- ── Steps ────────────────────────────────────────────────────────── -->
      <section class="section-block reveal" aria-labelledby="steps-title">
        <div class="section-inner">
          <h2 id="steps-title" class="section-title">{{ t('home.steps.title') }}</h2>
          <div class="steps-track">
            <span class="steps-rule" aria-hidden="true"></span>
            <article
              v-for="(s, i) in stepItems"
              :key="s.title"
              class="step-card reveal-child"
              :style="{ '--card-i': i }"
            >
              <div class="step-num">{{ i + 1 }}</div>
              <h3 class="feature-title">{{ s.title }}</h3>
              <p class="feature-desc">{{ s.desc }}</p>
            </article>
          </div>
        </div>
      </section>

      <!-- ── Comparison ───────────────────────────────────────────────────── -->
      <section class="section-block reveal" aria-labelledby="comparison-title">
        <div class="section-inner section-inner-narrow">
          <h2 id="comparison-title" class="section-title">{{ t('home.comparison.title') }}</h2>
          <div class="comparison-table">
            <div class="ct-row ct-head">
              <span>{{ t('home.comparison.headers.feature') }}</span>
              <span>{{ t('home.comparison.headers.official') }}</span>
              <span class="ct-us">{{ t('home.comparison.headers.us') }}</span>
            </div>
            <div
              v-for="(row, i) in comparisonRows"
              :key="row.feature"
              class="ct-row ct-body-row"
              :style="{ '--row-i': i }"
            >
              <span class="ct-feature">{{ row.feature }}</span>
              <span class="ct-official" :data-label="t('home.comparison.headers.official')">{{ row.official }}</span>
              <span class="ct-us" :data-label="t('home.comparison.headers.us')">{{ row.us }}</span>
            </div>
          </div>
        </div>
      </section>

      <!-- ── Providers: infinite marquee, duplicated for a seamless loop ──── -->
      <section class="section-block reveal" aria-labelledby="providers-title">
        <div class="section-inner">
          <h2 id="providers-title" class="section-title">{{ t('home.providers.title') }}</h2>
          <p class="section-subtitle">{{ t('home.providers.description') }}</p>
        </div>
        <div class="marquee" aria-hidden="true">
          <div class="marquee-track">
            <span v-for="(p, i) in marqueeChips" :key="`${p}-${i}`" class="provider-chip">
              <Icon name="sparkles" size="sm" />
              {{ p }}
            </span>
          </div>
        </div>
        <ul class="sr-only">
          <li v-for="p in providerChips" :key="p">{{ p }}</li>
        </ul>
      </section>

      <!-- ── CTA ──────────────────────────────────────────────────────────── -->
      <section class="cta-band reveal" aria-labelledby="cta-title">
        <span class="cta-ring cta-ring-a" aria-hidden="true"></span>
        <span class="cta-ring cta-ring-b" aria-hidden="true"></span>
        <div class="cta-inner">
          <h2 id="cta-title" class="cta-title">{{ t('home.cta.title') }}</h2>
          <p class="cta-desc">{{ t('home.cta.description') }}</p>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/register'"
            class="primary-action cta-button mo-sheen"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.cta.button') }}
            <Icon name="arrowRight" size="sm" class="action-arrow" />
          </router-link>
        </div>
      </section>

      <section class="service-band" aria-label="Service information">
        <div class="service-inner">
          <span class="service-item">
            <Icon name="users" size="sm" />
            {{ t('home.qqGroup', { number: qqGroupNumber }) }}
          </span>
          <span class="service-item">
            <Icon name="globe" size="sm" />
            {{ t('home.mainlandServiceNotice') }}
          </span>
        </div>
      </section>
    </main>

    <footer class="page-footer">
      <div class="footer-inner">
        <p class="footer-risk">{{ t('home.footer.riskStrip') }}</p>
        <LegalFooterLinks />
        <div class="footer-bottom">
          <p>&copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}</p>
          <a :href="githubUrl" target="_blank" rel="noopener noreferrer" class="footer-link">GitHub</a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import BrandLogo from '@/components/common/BrandLogo.vue'
import LegalFooterLinks from '@/components/legal/LegalFooterLinks.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import { readLocalStorage, writeLocalStorage } from '@/utils/safeStorage'
import { isMotionAllowed } from '@/composables/useMotion'

type ProtocolId = 'chat' | 'responses' | 'claude' | 'gemini'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => (appStore.cachedPublicSettings?.home_content || '').trim())
const isHomeContentUrl = computed(() => /^https?:\/\//.test(homeContent.value.trim()))

const isDark = ref(document.documentElement.classList.contains('dark'))
const selectedProtocolId = ref<ProtocolId>('responses')
const copyState = ref<'idle' | 'copied' | 'error'>('idle')
let copyResetTimer: ReturnType<typeof setTimeout> | undefined

const githubUrl = 'https://github.com/Wei-Shaw/sub2api'
const qqGroupNumber = '964834219'
const quickstartTitleId = 'gateway-quickstart-title'

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const currentYear = computed(() => new Date().getFullYear())
const featureTags = computed(() => [
  t('home.tags.subscriptionToApi'),
  t('home.tags.stickySession'),
  t('home.tags.realtimeBilling')
])

/* ---------------------------------------------------------------------------
   Kinetic headline: the wordmark is split so each chunk can fly in on its own
   delay. The project's TS lib does not declare Intl.Segmenter, so the split is
   done with Array.from, which iterates code points and therefore never tears a
   surrogate pair (emoji) in half. CJK stays intact because those are BMP.
   ------------------------------------------------------------------------- */
const titleChunks = computed(() => Array.from(siteName.value))

/* ---------------------------------------------------------------------------
   Stats: each figure counts up from zero the first time the band is visible.
   ------------------------------------------------------------------------- */
const statItems = computed(() => [
  { target: 200, suffix: '+', decimals: 0, label: t('home.stats.models') },
  { target: 4, suffix: '', decimals: 0, label: t('home.stats.protocols') },
  { target: 99.9, suffix: '%', decimals: 1, label: t('home.stats.uptime') },
  { target: 0.0001, suffix: '', decimals: 4, label: t('home.stats.billing') }
])

function formatStat(value: number, decimals: number, suffix: string): string {
  return value.toFixed(decimals) + suffix
}

const statsBandRef = ref<HTMLElement | null>(null)
const displayStats = ref(statItems.value.map((s) => formatStat(s.target, s.decimals, s.suffix)))
let statsFrame = 0

function runCountUp() {
  if (!isMotionAllowed()) return
  const duration = 1500
  const start = performance.now()
  const targets = statItems.value
  const step = (now: number) => {
    const p = Math.min(1, (now - start) / duration)
    // easeOutExpo: fast out of the gate, long settle
    const eased = p === 1 ? 1 : 1 - Math.pow(2, -10 * p)
    displayStats.value = targets.map((s) => formatStat(s.target * eased, s.decimals, s.suffix))
    if (p < 1) statsFrame = requestAnimationFrame(step)
  }
  statsFrame = requestAnimationFrame(step)
}

/* ---------------------------------------------------------------------------
   Pointer physics. Both the console panel and the feature cards react to the
   cursor; the transform is driven by CSS custom properties so the browser can
   keep it on the compositor.
   ------------------------------------------------------------------------- */
const heroTilt = ref({ x: 0, y: 0 })
const tiltActive = ref(false)

const heroTiltStyle = computed(() => ({
  '--hero-tilt-x': `${heroTilt.value.x.toFixed(2)}deg`,
  '--hero-tilt-y': `${heroTilt.value.y.toFixed(2)}deg`
}))

const panelTiltStyle = computed(() => ({
  transform: `perspective(1400px) rotateX(var(--hero-tilt-x)) rotateY(var(--hero-tilt-y))`
}))

function onHeroMove(event: MouseEvent) {
  if (!isMotionAllowed()) return
  const el = event.currentTarget as HTMLElement | null
  if (!el) return
  const rect = el.getBoundingClientRect()
  const px = (event.clientX - rect.left) / rect.width - 0.5
  const py = (event.clientY - rect.top) / rect.height - 0.5
  heroTilt.value = { x: -py * 5.5, y: px * 7 }
  tiltActive.value = true
}

function resetHeroTilt() {
  heroTilt.value = { x: 0, y: 0 }
  tiltActive.value = false
}

/** Spotlight: the card records the cursor position for its radial highlight. */
function onCardMove(event: MouseEvent) {
  const el = event.currentTarget as HTMLElement | null
  if (!el) return
  const rect = el.getBoundingClientRect()
  el.style.setProperty('--mx', `${event.clientX - rect.left}px`)
  el.style.setProperty('--my', `${event.clientY - rect.top}px`)
}

const featureCards = computed(() => [
  { icon: 'key', title: t('home.features.unifiedGateway'), desc: t('home.features.unifiedGatewayDesc') },
  { icon: 'shield', title: t('home.features.multiAccount'), desc: t('home.features.multiAccountDesc') },
  { icon: 'chart', title: t('home.features.balanceQuota'), desc: t('home.features.balanceQuotaDesc') }
])

const stepItems = computed(() => [
  { title: t('home.steps.s1.title'), desc: t('home.steps.s1.desc') },
  { title: t('home.steps.s2.title'), desc: t('home.steps.s2.desc') },
  { title: t('home.steps.s3.title'), desc: t('home.steps.s3.desc') }
])

const comparisonRows = computed(() =>
  (['pricing', 'models', 'management', 'stability', 'control'] as const).map((key) => ({
    feature: t(`home.comparison.items.${key}.feature`),
    official: t(`home.comparison.items.${key}.official`),
    us: t(`home.comparison.items.${key}.us`)
  }))
)

const providerChips = computed(() => [
  'Claude', 'GPT', 'Gemini', 'Grok', 'GLM', 'Kimi', 'DeepSeek', t('home.providers.more')
])
/** The marquee needs two identical halves so the loop has no visible seam. */
const marqueeChips = computed(() => [...providerChips.value, ...providerChips.value])

/* ---------------------------------------------------------------------------
   Scroll reveal. One observer drives both jobs: sections/children fade in once,
   and the stats band counts up the first time it becomes visible. Keeping a
   single observer means one callback, one disconnect, and no chance of the two
   getting out of sync on unmount.
   ------------------------------------------------------------------------- */
let revealObserver: IntersectionObserver | null = null

function revealAll() {
  document.querySelectorAll('.reveal, .reveal-child').forEach((el) => el.classList.add('revealed'))
}

function initReveal() {
  const band = statsBandRef.value
  const targets = document.querySelectorAll('.reveal, .reveal-child')

  // No observer support, or motion is off: show everything, skip the count-up.
  if (!('IntersectionObserver' in window) || !isMotionAllowed()) {
    revealAll()
    return
  }

  revealObserver = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        if (entry.target === band) {
          runCountUp()
        } else {
          entry.target.classList.add('revealed')
        }
        revealObserver?.unobserve(entry.target)
      }
    },
    // A single threshold is enough: 12% for sections, tightened for the band by
    // observing it with its own rootMargin so it fires while still below the fold.
    { threshold: 0.12, rootMargin: '0px 0px -8% 0px' }
  )

  targets.forEach((el) => revealObserver?.observe(el))
  if (band) revealObserver.observe(band)
}

/* ---------------------------------------------------------------------------
   The console types its own request line, character by character.
   ------------------------------------------------------------------------- */
const typedCommand = ref('')
const typing = ref(false)
let typeTimer: ReturnType<typeof setInterval> | null = null

function stopTyping() {
  if (typeTimer) {
    clearInterval(typeTimer)
    typeTimer = null
  }
  typing.value = false
}

function typeCommand() {
  stopTyping()
  const full = selectedProtocol.value.command
  if (!isMotionAllowed()) {
    typedCommand.value = full
    return
  }
  typedCommand.value = ''
  typing.value = true
  let i = 0
  // Roughly constant character throughput regardless of command length.
  typeTimer = setInterval(() => {
    i += 2
    if (i >= full.length) {
      typedCommand.value = full
      stopTyping()
      return
    }
    typedCommand.value = full.slice(0, i)
  }, 12)
}

watch(selectedProtocolId, () => {
  typeCommand()
})

const gatewayOrigin = computed(() => {
  const configured = appStore.cachedPublicSettings?.api_base_url || appStore.apiBaseUrl || window.location.origin
  return configured.replace(/\/+$/, '').replace(/\/v1$/, '')
})

const protocols = computed(() => [
  {
    id: 'chat' as const,
    label: 'Chat',
    endpoint: '/v1/chat/completions',
    command: `curl -X POST "${gatewayOrigin.value}/v1/chat/completions" \\
  -H "Authorization: Bearer sk-••••" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"Hello"}]}'`,
    response: '{\n  "choices": [{ "message": { "role": "assistant", "content": "Ready." } }]\n}'
  },
  {
    id: 'responses' as const,
    label: 'Responses',
    endpoint: '/v1/responses',
    command: `curl -X POST "${gatewayOrigin.value}/v1/responses" \\
  -H "Authorization: Bearer sk-••••" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-5","input":"Hello"}'`,
    response: '{\n  "output": [{ "type": "message", "content": [{ "type": "output_text" }] }]\n}'
  },
  {
    id: 'claude' as const,
    label: 'Claude',
    endpoint: '/v1/messages',
    command: `curl -X POST "${gatewayOrigin.value}/v1/messages" \\
  -H "x-api-key: sk-••••" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"claude-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'`,
    response: '{\n  "content": [{ "type": "text", "text": "Message routed." }]\n}'
  },
  {
    id: 'gemini' as const,
    label: 'Gemini',
    endpoint: '/v1beta/models/gemini-pro:generateContent',
    command: `curl -X POST "${gatewayOrigin.value}/v1beta/models/gemini-pro:generateContent" \\
  -H "x-goog-api-key: sk-••••" \\
  -H "Content-Type: application/json" \\
  -d '{"contents":[{"parts":[{"text":"Hello"}]}]}'`,
    response: '{\n  "candidates": [{ "content": { "parts": [{ "text": "Response ready." }] } }]\n}'
  }
])

const selectedProtocol = computed(() => protocols.value.find((protocol) => protocol.id === selectedProtocolId.value) || protocols.value[0])
const copyButtonLabel = computed(() => {
  if (copyState.value === 'copied') return t('home.quickstart.copied')
  if (copyState.value === 'error') return t('home.quickstart.copyFailed')
  return t('home.quickstart.copy')
})
const copyAnnouncement = computed(() => copyState.value === 'copied' ? t('home.quickstart.copySuccess') : '')

async function copyCommand() {
  try {
    await navigator.clipboard.writeText(selectedProtocol.value.command)
    copyState.value = 'copied'
  } catch {
    copyState.value = 'error'
  }
  if (copyResetTimer) clearTimeout(copyResetTimer)
  copyResetTimer = setTimeout(() => {
    copyState.value = 'idle'
  }, 1800)
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  writeLocalStorage('theme', isDark.value ? 'dark' : 'light')
  // Force paint so porcelain/dark tokens apply immediately
  document.documentElement.style.colorScheme = isDark.value ? 'dark' : 'light'
}

function initTheme() {
  const savedTheme = readLocalStorage('theme')
  let prefersDark = false
  try {
    prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
  } catch {
    prefersDark = false
  }
  if (savedTheme === 'dark' || (!savedTheme && prefersDark)) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) appStore.fetchPublicSettings()
  typeCommand()
  initReveal()
})

onBeforeUnmount(() => {
  revealObserver?.disconnect()
  revealObserver = null
  if (statsFrame) cancelAnimationFrame(statsFrame)
  stopTyping()
  if (copyResetTimer) clearTimeout(copyResetTimer)
})
</script>

<style scoped>
/* ============================================================================
   Landing page — full rebuild on the Graphite token system.
   Layout is a single 1240px column; every colour comes from --ds-* so the two
   skins are handled by design-system.css alone.
   ========================================================================== */

.gateway-shell {
  position: relative;
  overflow-x: clip;
  color: var(--ds-ink);
  background: var(--ds-bg);
  font-family: var(--ds-font-sans);
}

/* ---- Ambient background ------------------------------------------------- */
.aurora {
  position: absolute;
  inset: 0;
  z-index: 0;
  overflow: hidden;
  pointer-events: none;
}

.aurora-blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(90px);
  opacity: 0.5;
}

.aurora-blob-a {
  top: -18%;
  left: -8%;
  width: 46rem;
  height: 46rem;
  background: radial-gradient(circle, color-mix(in srgb, var(--ds-primary) 42%, transparent), transparent 68%);
  animation: blobDrift 26s var(--ds-ease-expo) infinite alternate;
}

.aurora-blob-b {
  top: 4%;
  right: -14%;
  width: 40rem;
  height: 40rem;
  background: radial-gradient(circle, color-mix(in srgb, var(--ds-accent) 34%, transparent), transparent 68%);
  animation: blobDrift 32s var(--ds-ease-expo) infinite alternate-reverse;
}

.aurora-blob-c {
  bottom: -22%;
  left: 34%;
  width: 38rem;
  height: 38rem;
  background: radial-gradient(circle, color-mix(in srgb, var(--ds-violet) 26%, transparent), transparent 70%);
  animation: blobDrift 38s var(--ds-ease-expo) infinite alternate;
}

@keyframes blobDrift {
  from { transform: translate3d(0, 0, 0) scale(1); }
  to { transform: translate3d(4%, -3%, 0) scale(1.12); }
}

.aurora-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(to right, color-mix(in srgb, var(--ds-ink) 7%, transparent) 1px, transparent 1px),
    linear-gradient(to bottom, color-mix(in srgb, var(--ds-ink) 7%, transparent) 1px, transparent 1px);
  background-size: 64px 64px;
  -webkit-mask-image: radial-gradient(ellipse 80% 55% at 50% 22%, #000 15%, transparent 78%);
  mask-image: radial-gradient(ellipse 80% 55% at 50% 22%, #000 15%, transparent 78%);
}

/* A single scanning line sweeps down the page — the one indulgence that makes
   the whole thing feel "powered on". */
.aurora-scan {
  position: absolute;
  left: 0;
  right: 0;
  height: 26rem;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--ds-primary) 9%, transparent), transparent);
  animation: scanDown 11s linear infinite;
}

@keyframes scanDown {
  from { top: -30rem; }
  to { top: 105%; }
}

/* ---- Header ------------------------------------------------------------- */
.glass-header {
  position: sticky;
  top: 0;
  padding: 12px 16px 0;
}

.glass-nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  width: 100%;
  max-width: 1240px;
  height: 60px;
  margin-inline: auto;
  padding-inline: 14px 10px;
  border: 1px solid color-mix(in srgb, var(--ds-border) 88%, transparent);
  border-radius: var(--ds-radius-xl);
  background: color-mix(in srgb, var(--ds-surface) 82%, transparent);
  box-shadow: var(--ds-shadow-sm);
  backdrop-filter: blur(18px) saturate(150%);
}

.brand-link {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 11px;
  color: inherit;
  text-decoration: none;
}

.brand-name {
  overflow: hidden;
  color: var(--ds-ink);
  font-family: var(--ds-font-display);
  font-size: 15px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nav-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 6px;
}

.icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border: 1px solid transparent;
  border-radius: var(--ds-radius);
  color: var(--ds-ink-muted);
  background: transparent;
  transition:
    color var(--ds-dur-fast) linear,
    background-color var(--ds-dur-fast) linear,
    border-color var(--ds-dur-fast) linear,
    transform var(--ds-dur-instant) var(--ds-ease-out);
}

.icon-button:hover {
  border-color: var(--ds-border);
  color: var(--ds-ink);
  background: var(--ds-surface-muted);
}

.icon-button:active {
  transform: scale(0.94);
}

.header-action {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 38px;
  padding-inline: 15px;
  border-radius: var(--ds-radius);
  color: var(--ds-on-primary);
  background: var(--ds-primary);
  font-size: 13px;
  font-weight: 650;
  text-decoration: none;
  box-shadow: var(--ds-shadow-sm);
  transition:
    background-color var(--ds-dur-fast) linear,
    transform var(--ds-dur-instant) var(--ds-ease-out),
    box-shadow var(--ds-dur-base) var(--ds-ease-expo);
}

.header-action:hover {
  background: var(--ds-primary-hover);
  box-shadow: var(--ds-shadow-md);
}

.header-action:active {
  transform: scale(0.97);
}

/* ---- Hero --------------------------------------------------------------- */
.hero {
  display: grid;
  grid-template-columns: 0.88fr 1.12fr;
  gap: 56px;
  align-items: center;
  width: 100%;
  max-width: 1240px;
  margin-inline: auto;
  padding: clamp(48px, 6vw, 84px) 16px clamp(40px, 5vw, 68px);
}

.hero-copy {
  min-width: 0;
}

.brand-kicker {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  padding: 6px 13px;
  border: 1px solid color-mix(in srgb, var(--ds-primary) 24%, transparent);
  border-radius: var(--ds-radius-pill);
  color: var(--ds-primary-ink);
  background: var(--ds-primary-soft);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.01em;
}

.hero-title {
  display: flex;
  flex-wrap: wrap;
  margin: 22px 0 0;
  font-family: var(--ds-font-display);
  font-size: clamp(2.6rem, 1.4rem + 5vw, 4.6rem);
  font-weight: 800;
  line-height: 1.02;
  letter-spacing: -0.03em;
}

/* Each grapheme rides in on its own delay, then a gradient sweeps the row. */
.title-chunk {
  display: inline-block;
  background: linear-gradient(
    100deg,
    var(--ds-ink) 0%,
    var(--ds-ink) 38%,
    var(--ds-primary) 50%,
    var(--ds-accent) 58%,
    var(--ds-ink) 70%,
    var(--ds-ink) 100%
  );
  background-size: 260% auto;
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  animation:
    chunkIn var(--ds-dur-slower) var(--ds-ease-spring) backwards,
    titleSweep 6.5s linear infinite;
  animation-delay: calc(var(--chunk-i) * 55ms), calc(var(--chunk-i) * 55ms + 900ms);
}

@keyframes chunkIn {
  from {
    opacity: 0;
    transform: translateY(0.42em) rotateX(-72deg) scale(0.86);
    filter: blur(7px);
  }
  to {
    opacity: 1;
    transform: none;
    filter: none;
  }
}

@keyframes titleSweep {
  to { background-position: 260% center; }
}

.hero-subtitle {
  margin: 20px 0 0;
  color: var(--ds-ink);
  font-family: var(--ds-font-display);
  font-size: clamp(1.15rem, 0.9rem + 0.9vw, 1.6rem);
  font-weight: 650;
  line-height: 1.25;
  letter-spacing: -0.015em;
}

.hero-description {
  max-width: 34rem;
  margin: 14px 0 0;
  color: var(--ds-ink-muted);
  font-size: 15px;
  line-height: 1.7;
}

.hero-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 30px;
}

.primary-action,
.secondary-action {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 9px;
  min-height: 48px;
  padding-inline: 24px;
  border-radius: var(--ds-radius-lg);
  font-size: 15px;
  font-weight: 650;
  text-decoration: none;
  transition:
    transform var(--ds-dur-instant) var(--ds-ease-out),
    box-shadow var(--ds-dur-base) var(--ds-ease-expo),
    background-color var(--ds-dur-fast) linear,
    border-color var(--ds-dur-fast) linear,
    color var(--ds-dur-fast) linear;
}

.primary-action {
  color: var(--ds-on-primary);
  background: var(--ds-primary);
  box-shadow: var(--ds-shadow-md);
}

.primary-action:hover {
  background: var(--ds-primary-hover);
  box-shadow: var(--ds-shadow-lg);
  transform: translateY(-2px);
}

.primary-action:active {
  transform: translateY(0) scale(0.977);
}

/* The arrow leans forward when the button is hovered. */
.action-arrow {
  transition: transform var(--ds-dur-base) var(--ds-ease-spring);
}

.primary-action:hover .action-arrow {
  transform: translateX(4px);
}

.secondary-action {
  border: 1px solid var(--ds-border);
  color: var(--ds-ink);
  background: color-mix(in srgb, var(--ds-surface) 88%, transparent);
}

.secondary-action:hover {
  border-color: var(--ds-border-strong);
  background: var(--ds-surface-muted);
  transform: translateY(-2px);
}

.feature-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 22px;
  margin-top: 30px;
}

.feature-tag {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--ds-ink-muted);
  font-size: 13px;
  animation: chunkIn var(--ds-dur-slow) var(--ds-ease-expo) backwards;
  animation-delay: calc(700ms + var(--tag-i) * 90ms);
}

.feature-tag :deep(svg) {
  color: var(--ds-primary);
}

/* ---- Console panel ------------------------------------------------------ */
.console-panel {
  position: relative;
  min-width: 0;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius-xl);
  background: color-mix(in srgb, var(--ds-surface) 94%, transparent);
  box-shadow: var(--ds-shadow-lg);
  backdrop-filter: blur(12px);
  transform-style: preserve-3d;
  transition: transform var(--ds-dur-base) var(--ds-ease-expo), box-shadow var(--ds-dur-base) var(--ds-ease-expo);
  will-change: transform;
}

.console-panel-tilt {
  box-shadow: var(--ds-shadow-menu);
}

/* A soft indigo bloom behind the panel, so it reads as lifted off the page. */
.console-glow {
  position: absolute;
  inset: -14% -8%;
  z-index: -1;
  border-radius: 50%;
  background: radial-gradient(ellipse at 50% 40%, color-mix(in srgb, var(--ds-primary) 20%, transparent), transparent 68%);
  animation: glowPulse 6s ease-in-out infinite;
}

@keyframes glowPulse {
  0%, 100% { opacity: 0.65; transform: scale(1); }
  50% { opacity: 1; transform: scale(1.05); }
}

.console-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px;
  border-bottom: 1px solid var(--ds-border);
}

.console-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.console-title-row h2 {
  margin: 0;
  color: var(--ds-ink);
  font-size: 14px;
  font-weight: 700;
}

.console-title-row :deep(svg) {
  color: var(--ds-primary);
}

.console-sub {
  margin: 5px 0 0;
  color: var(--ds-ink-muted);
  font-size: 12px;
}

.live-status {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 7px;
  padding: 5px 11px;
  border: 1px solid color-mix(in srgb, var(--ds-ok) 26%, transparent);
  border-radius: var(--ds-radius-pill);
  color: var(--ds-ok);
  background: var(--ds-ok-soft);
  font-size: 11px;
  font-weight: 650;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 20%, transparent);
}

.protocol-tabs {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 4px;
  margin: 14px 20px;
  padding: 4px;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius-lg);
  background: var(--ds-surface-sunken);
}

.protocol-tab {
  padding: 8px 10px;
  border: 0;
  border-radius: var(--ds-radius-sm);
  color: var(--ds-ink-muted);
  background: transparent;
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
  transition:
    color var(--ds-dur-fast) linear,
    background-color var(--ds-dur-base) var(--ds-ease-expo),
    box-shadow var(--ds-dur-base) var(--ds-ease-expo);
}

.protocol-tab:hover {
  color: var(--ds-primary-ink);
}

.protocol-tab-active {
  color: var(--ds-ink);
  background: var(--ds-surface);
  box-shadow: var(--ds-shadow-xs);
}

.endpoint-row {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 0 20px 14px;
  font-size: 12px;
}

.method-badge {
  flex: 0 0 auto;
  padding: 3px 7px;
  border-radius: var(--ds-radius-xs);
  color: var(--ds-warn);
  background: var(--ds-warn-soft);
  font-family: var(--ds-font-mono);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.endpoint-code {
  min-width: 0;
  overflow: hidden;
  color: var(--ds-ink-muted);
  font-family: var(--ds-font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.endpoint-ok {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  color: var(--ds-ok);
  font-size: 11px;
}

.code-section {
  padding: 0 20px 18px;
}

.code-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 9px;
}

.code-label {
  color: var(--ds-ink-faint);
  font-family: var(--ds-font-mono);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.copy-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 30px;
  padding-inline: 10px;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius);
  color: var(--ds-ink-muted);
  background: var(--ds-surface);
  font-size: 11px;
  font-weight: 650;
  cursor: pointer;
  transition:
    color var(--ds-dur-fast) linear,
    background-color var(--ds-dur-fast) linear,
    transform var(--ds-dur-instant) var(--ds-ease-out);
}

.copy-button:hover {
  color: var(--ds-primary-ink);
  background: var(--ds-surface-muted);
}

.copy-button:active {
  transform: scale(0.95);
}

.request-code,
.response-block {
  overflow-x: auto;
  margin: 0;
  white-space: pre;
  font-family: var(--ds-font-mono);
  font-size: 12px;
  line-height: 1.75;
}

.request-code {
  min-height: 132px;
  padding: 14px;
  border: 1px solid color-mix(in srgb, var(--ds-primary) 16%, transparent);
  border-radius: var(--ds-radius-lg);
  color: var(--ds-surface);
  background: var(--ds-ink);
}

/* Blinking caret while the command is being typed out. */
.caret {
  display: inline-block;
  width: 7px;
  height: 1.05em;
  margin-left: 1px;
  vertical-align: text-bottom;
  background: var(--ds-primary-300);
  animation: caretBlink 1s steps(2, start) infinite;
}

@keyframes caretBlink {
  0%, 49% { opacity: 1; }
  50%, 100% { opacity: 0; }
}

.response-section {
  padding: 14px 20px 18px;
  border-top: 1px solid var(--ds-border);
}

.response-block {
  margin-top: 8px;
  color: var(--ds-accent);
}

.response-metrics {
  display: flex;
  gap: 15px;
  margin-top: 10px;
  color: var(--ds-ink-faint);
  font-family: var(--ds-font-mono);
  font-size: 10px;
}

/* ---- Stats -------------------------------------------------------------- */
.stats-band {
  position: relative;
  border-block: 1px solid var(--ds-border);
  background: color-mix(in srgb, var(--ds-surface) 55%, transparent);
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 22px;
  width: 100%;
  max-width: 1240px;
  margin-inline: auto;
  padding: 38px 16px;
}

.stat-cell {
  text-align: center;
  animation: chunkIn var(--ds-dur-slow) var(--ds-ease-expo) backwards;
  animation-delay: calc(var(--stat-i) * 90ms);
}

.stat-value {
  color: var(--ds-primary-ink);
  font-family: var(--ds-font-numeric);
  font-size: clamp(1.9rem, 1.3rem + 1.9vw, 2.9rem);
  font-weight: 800;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.03em;
  line-height: 1.1;
}

.stat-label {
  margin-top: 7px;
  color: var(--ds-ink-muted);
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

/* ---- Sections ----------------------------------------------------------- */
.section-block {
  padding: clamp(56px, 6vw, 88px) 0;
}

.section-inner {
  width: 100%;
  max-width: 1240px;
  margin-inline: auto;
  padding-inline: 16px;
}

.section-inner-narrow {
  max-width: 900px;
}

.section-title {
  margin: 0;
  color: var(--ds-ink);
  font-family: var(--ds-font-display);
  font-size: clamp(1.5rem, 1.1rem + 1.6vw, 2.2rem);
  font-weight: 750;
  line-height: 1.22;
  letter-spacing: -0.025em;
}

.section-subtitle {
  margin: 12px 0 0;
  color: var(--ds-ink-muted);
  font-size: 14.5px;
  line-height: 1.65;
}

/* ---- Feature cards ------------------------------------------------------ */
.feature-grid {
  display: grid;
  gap: 18px;
  margin-top: 40px;
}

.feature-card {
  position: relative;
  overflow: hidden;
  padding: 26px 24px;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius-xl);
  background: color-mix(in srgb, var(--ds-surface) 92%, transparent);
  transition:
    transform var(--ds-dur-base) var(--ds-ease-spring),
    border-color var(--ds-dur-fast) linear,
    box-shadow var(--ds-dur-base) var(--ds-ease-expo);
}

/* Cursor spotlight: a radial wash that tracks the pointer position. */
.feature-card::before {
  content: "";
  position: absolute;
  inset: 0;
  opacity: 0;
  background: radial-gradient(
    340px circle at var(--mx, 50%) var(--my, 50%),
    color-mix(in srgb, var(--ds-primary) 14%, transparent),
    transparent 62%
  );
  transition: opacity var(--ds-dur-base) linear;
  pointer-events: none;
}

.feature-card:hover::before {
  opacity: 1;
}

.feature-card:hover {
  border-color: color-mix(in srgb, var(--ds-primary) 42%, transparent);
  box-shadow: var(--ds-shadow-lg);
  transform: translateY(-4px);
}

.feature-icon {
  display: grid;
  place-items: center;
  width: 46px;
  height: 46px;
  border-radius: var(--ds-radius-lg);
  color: var(--ds-primary-ink);
  background: var(--ds-primary-soft);
  transition: transform var(--ds-dur-base) var(--ds-ease-spring);
}

.feature-card:hover .feature-icon {
  transform: scale(1.1) rotate(-5deg);
}

.feature-title {
  margin: 17px 0 0;
  color: var(--ds-ink);
  font-size: 16.5px;
  font-weight: 700;
  letter-spacing: -0.012em;
}

.feature-desc {
  margin: 9px 0 0;
  color: var(--ds-ink-muted);
  font-size: 14px;
  line-height: 1.68;
}

/* ---- Steps -------------------------------------------------------------- */
.steps-track {
  position: relative;
  display: grid;
  gap: 18px;
  margin-top: 40px;
}

/* Connector that draws itself across the row as the section reveals. */
.steps-rule {
  position: absolute;
  top: 44px;
  left: 0;
  right: 0;
  height: 1px;
  background: linear-gradient(90deg, transparent, var(--ds-border-strong) 12%, var(--ds-border-strong) 88%, transparent);
  transform: scaleX(0);
  transform-origin: left;
  transition: transform var(--ds-dur-slower) var(--ds-ease-expo);
}

.reveal.revealed .steps-rule {
  transform: scaleX(1);
}

.step-card {
  position: relative;
  padding: 24px;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius-xl);
  background: color-mix(in srgb, var(--ds-surface) 92%, transparent);
  transition:
    transform var(--ds-dur-base) var(--ds-ease-spring),
    border-color var(--ds-dur-fast) linear,
    box-shadow var(--ds-dur-base) var(--ds-ease-expo);
}

.step-card:hover {
  border-color: color-mix(in srgb, var(--ds-primary) 42%, transparent);
  box-shadow: var(--ds-shadow-lg);
  transform: translateY(-4px);
}

.step-num {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 50%;
  color: var(--ds-on-primary);
  background: var(--ds-primary);
  font-family: var(--ds-font-numeric);
  font-size: 17px;
  font-weight: 800;
  box-shadow: 0 0 0 6px color-mix(in srgb, var(--ds-primary) 14%, transparent);
  transition: transform var(--ds-dur-base) var(--ds-ease-spring);
}

.step-card:hover .step-num {
  transform: scale(1.1);
}

/* ---- Comparison --------------------------------------------------------- */
.comparison-table {
  margin-top: 34px;
  overflow: hidden;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius-xl);
  background: color-mix(in srgb, var(--ds-surface) 92%, transparent);
}

.ct-row {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  gap: 14px;
  align-items: center;
  padding: 14px 20px;
}

.ct-head {
  border-bottom: 1px solid var(--ds-border);
  background: var(--ds-table-header);
  color: var(--ds-ink-faint);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.ct-body-row {
  border-top: 1px solid var(--ds-divider);
  font-size: 14px;
  animation: rowIn var(--ds-dur-base) var(--ds-ease-expo) backwards;
  animation-delay: calc(var(--row-i) * 70ms);
  transition: background-color var(--ds-dur-fast) linear;
}

.ct-body-row:hover {
  background: var(--ds-table-row-hover);
}

@keyframes rowIn {
  from {
    opacity: 0;
    transform: translateX(-14px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.ct-feature {
  color: var(--ds-ink);
  font-weight: 650;
}

.ct-official {
  color: var(--ds-ink-faint);
}

.ct-us {
  color: var(--ds-primary-ink);
  font-weight: 650;
}

.ct-head .ct-us {
  color: var(--ds-primary-ink);
}

/* ---- Provider marquee --------------------------------------------------- */
.marquee {
  position: relative;
  margin-top: 32px;
  overflow: hidden;
  -webkit-mask-image: linear-gradient(90deg, transparent, #000 12%, #000 88%, transparent);
  mask-image: linear-gradient(90deg, transparent, #000 12%, #000 88%, transparent);
}

.marquee-track {
  display: flex;
  gap: 12px;
  width: max-content;
  animation: marqueeScroll 32s linear infinite;
}

.marquee:hover .marquee-track {
  animation-play-state: paused;
}

@keyframes marqueeScroll {
  from { transform: translateX(0); }
  to { transform: translateX(-50%); }
}

.provider-chip {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
  padding: 11px 20px;
  border: 1px solid var(--ds-border);
  border-radius: var(--ds-radius-pill);
  color: var(--ds-ink);
  background: color-mix(in srgb, var(--ds-surface) 92%, transparent);
  font-size: 14px;
  font-weight: 650;
  white-space: nowrap;
}

.provider-chip :deep(svg) {
  color: var(--ds-primary);
}

/* ---- CTA ---------------------------------------------------------------- */
.cta-band {
  position: relative;
  overflow: hidden;
  border-block: 1px solid var(--ds-border);
  background: var(--ds-surface-muted);
}

.cta-ring {
  position: absolute;
  top: 50%;
  left: 50%;
  border: 1px solid color-mix(in srgb, var(--ds-primary) 30%, transparent);
  border-radius: 50%;
  transform: translate(-50%, -50%);
  pointer-events: none;
}

.cta-ring-a {
  width: 30rem;
  height: 30rem;
  animation: ringExpand 5.5s var(--ds-ease-expo) infinite;
}

.cta-ring-b {
  width: 30rem;
  height: 30rem;
  animation: ringExpand 5.5s var(--ds-ease-expo) 2.4s infinite;
}

@keyframes ringExpand {
  0% { opacity: 0.55; transform: translate(-50%, -50%) scale(0.55); }
  70% { opacity: 0; transform: translate(-50%, -50%) scale(1.5); }
  100% { opacity: 0; transform: translate(-50%, -50%) scale(1.5); }
}

.cta-inner {
  position: relative;
  z-index: 1;
  max-width: 760px;
  margin-inline: auto;
  padding: clamp(56px, 6vw, 88px) 20px;
  text-align: center;
}

.cta-title {
  margin: 0;
  color: var(--ds-ink);
  font-family: var(--ds-font-display);
  font-size: clamp(1.7rem, 1.2rem + 2vw, 2.6rem);
  font-weight: 800;
  letter-spacing: -0.03em;
}

.cta-desc {
  margin: 14px 0 0;
  color: var(--ds-ink-muted);
  font-size: 15px;
  line-height: 1.65;
}

.cta-button {
  margin-top: 28px;
}

/* ---- Service band + footer --------------------------------------------- */
.service-band {
  border-bottom: 1px solid var(--ds-border);
}

.service-inner {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 30px;
  width: 100%;
  max-width: 1240px;
  margin-inline: auto;
  padding: 18px 16px;
  color: var(--ds-ink-muted);
  font-size: 13px;
}

.service-item {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.service-item :deep(svg) {
  color: var(--ds-primary);
}

.page-footer {
  padding: 30px 16px;
}

.footer-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  width: 100%;
  max-width: 1240px;
  margin-inline: auto;
  color: var(--ds-ink-muted);
  font-size: 13px;
  text-align: center;
}

.footer-risk {
  max-width: 52rem;
  margin: 0;
  color: var(--ds-ink-faint);
  font-size: 12px;
  line-height: 1.75;
}

.footer-bottom {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: center;
  width: 100%;
  justify-content: space-between;
}

.footer-bottom p {
  margin: 0;
}

.footer-link {
  color: var(--ds-ink-muted);
  text-decoration: none;
  transition: color var(--ds-dur-fast) linear;
}

.footer-link:hover {
  color: var(--ds-primary-ink);
}

/* ---- Scroll reveal ------------------------------------------------------ */
.reveal,
.reveal-child {
  opacity: 0;
  transform: translateY(26px);
  transition:
    opacity var(--ds-dur-slow) var(--ds-ease-expo),
    transform var(--ds-dur-slow) var(--ds-ease-expo);
}

.reveal.revealed,
.reveal-child.revealed {
  opacity: 1;
  transform: none;
}

/* Children of a revealed section cascade rather than landing together. */
.reveal-child {
  transition-delay: calc(var(--card-i, 0) * 90ms);
}

/* ---- Responsive --------------------------------------------------------- */
@media (min-width: 768px) {
  .feature-grid,
  .steps-track {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .footer-bottom {
    flex-direction: row;
  }
}

@media (max-width: 1023px) {
  .hero {
    grid-template-columns: 1fr;
    gap: 40px;
  }

  .console-panel {
    transform: none !important;
  }
}

@media (max-width: 639px) {
  .stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 26px 16px;
  }

  .content-rows,
  .ct-row {
    grid-template-columns: 1fr;
    gap: 6px;
  }

  .ct-head {
    display: none;
  }

  .ct-body-row .ct-official::before,
  .ct-body-row .ct-us::before {
    content: attr(data-label) ": ";
    color: var(--ds-ink-faint);
  }

  .brand-name {
    display: none;
  }
}

/* ============================================================================
   Reduced motion: the landing page is the most animated surface in the app, so
   every ambient loop, headline sweep and pointer transform is removed here.
   ========================================================================== */
@media (prefers-reduced-motion: reduce) {
  .aurora-blob,
  .aurora-scan,
  .title-chunk,
  .stat-cell,
  .feature-tag,
  .ct-body-row,
  .marquee-track,
  .cta-ring,
  .console-glow,
  .caret {
    animation: none !important;
  }

  .title-chunk {
    background: none;
    -webkit-text-fill-color: currentColor;
    color: var(--ds-ink);
  }

  .console-panel {
    transform: none !important;
  }

  .reveal,
  .reveal-child {
    opacity: 1;
    transform: none;
    transition: none;
  }

  .steps-rule {
    transform: scaleX(1);
    transition: none;
  }
}

html[data-motion='off'] .aurora-blob,
html[data-motion='off'] .aurora-scan,
html[data-motion='off'] .title-chunk,
html[data-motion='off'] .stat-cell,
html[data-motion='off'] .feature-tag,
html[data-motion='off'] .ct-body-row,
html[data-motion='off'] .marquee-track,
html[data-motion='off'] .cta-ring,
html[data-motion='off'] .console-glow,
html[data-motion='off'] .caret {
  animation: none !important;
}

html[data-motion='off'] .title-chunk {
  background: none;
  -webkit-text-fill-color: currentColor;
  color: var(--ds-ink);
}

html[data-motion='off'] .console-panel {
  transform: none !important;
}

html[data-motion='off'] .reveal,
html[data-motion='off'] .reveal-child {
  opacity: 1;
  transform: none;
}
</style>