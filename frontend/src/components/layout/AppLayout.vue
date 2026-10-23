<template>
  <div
    class="relative min-h-screen"
    :class="[
      isConsoleSignal ? 'console-signal' : 'bg-transparent',
      { 'signal-admin': isAdminSignal, 'signal-nav-collapsed': sidebarCollapsed }
    ]"
  >
    <ConsoleAtmosphere v-if="isConsoleSignal" />
    <!-- Background Decoration -->
    <div v-if="!isConsoleSignal" class="pointer-events-none fixed inset-0 z-0 bg-mesh-gradient opacity-15"></div>

    <!-- Sidebar -->
    <AppSidebar />

    <div
      class="app-main-surface relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64', { 'signal-frame': isConsoleSignal, 'signal-reading-pane': isConsoleSignal }]"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main :class="[flushContent || isConsoleSignal ? 'p-0' : 'p-4 md:p-6 lg:p-8', { 'signal-main': isConsoleSignal }]" class="min-w-0">
        <slot />
      </main>    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import '@/styles/console-signal.css'
import '@/styles/console-account.css'
import '@/styles/console-studio.css'
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import { useConsoleSignal } from '@/composables/useConsoleSignal'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import ConsoleAtmosphere from './ConsoleAtmosphere.vue'

const appStore = useAppStore()
const authStore = useAuthStore()
const route = useRoute()
const { isConsoleSignal, isAdminSignal } = useConsoleSignal()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')
const flushContent = computed(() => Boolean(route.meta.flushContent))

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>

<style scoped>
.app-main-surface {
  background:
    linear-gradient(
      90deg,
      color-mix(in srgb, var(--ds-bg, #f8fafc) 92%, transparent) 0%,
      color-mix(in srgb, var(--ds-bg, #f8fafc) 68%, transparent) 58%,
      color-mix(in srgb, var(--ds-bg, #f8fafc) 28%, transparent) 100%
    );
}

.app-main-surface.signal-reading-pane {
  background: var(--signal-bg);
}
</style>
