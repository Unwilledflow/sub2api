<script setup lang="ts">
import { RouterView, useRouter, useRoute } from 'vue-router'
import { onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Toast from '@/components/common/Toast.vue'
import NavigationProgress from '@/components/common/NavigationProgress.vue'
import AdminComplianceDialog from '@/components/admin/AdminComplianceDialog.vue'
import { resolveRouteDocumentTitle } from '@/router/title'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'
import { useAppStore, useAuthStore, useSubscriptionStore, useAnnouncementStore, useAdminComplianceStore, useAdminSettingsStore } from '@/stores'
import { getSetupStatus } from '@/api/setup'
import { updateFavicon } from '@/utils/branding'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()
const announcementStore = useAnnouncementStore()
const adminComplianceStore = useAdminComplianceStore()
const adminSettingsStore = useAdminSettingsStore()

function updateDocumentTitle() {
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(route, appStore.siteName, customMenuItems)
}

// Watch for site settings changes and update favicon/title
watch(
  () => appStore.siteLogo,
  (newLogo) => {
    if (newLogo) {
      updateFavicon(newLogo)
    }
  },
  { immediate: true }
)

watch(
  [
    () => route.fullPath,
    () => route.meta.title,
    () => route.meta.titleKey,
    () => appStore.siteName,
    () => appStore.cachedPublicSettings?.custom_menu_items,
    () => authStore.isAdmin,
    () => adminSettingsStore.customMenuItems,
  ],
  updateDocumentTitle,
  { deep: true }
)

// Watch for authentication state and manage subscription data + announcements
function onVisibilityChange() {
  if (document.visibilityState === 'visible' && authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }
}

function onAdminComplianceRequired(event: Event) {
  const detail = (event as CustomEvent<Record<string, string>>).detail || {}
  adminComplianceStore.requireAcknowledgement(detail)
}

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated, oldValue) => {
    if (isAuthenticated) {
      if (authStore.isAdmin) {
        adminComplianceStore.fetchStatus().catch((error) => {
          console.error('Failed to fetch admin compliance status:', error)
        })
      }

      // User logged in: preload subscriptions and start polling
      subscriptionStore.fetchActiveSubscriptions().catch((error) => {
        console.error('Failed to preload subscriptions:', error)
      })
      subscriptionStore.startPolling()

      // Announcements: new login vs page refresh restore
      if (oldValue === false) {
        // New login: delay 3s then force fetch
        setTimeout(() => announcementStore.fetchAnnouncements(true), 3000)
      } else {
        // Page refresh restore (oldValue was undefined)
        announcementStore.fetchAnnouncements()
      }

      // Register visibility change listener
      document.addEventListener('visibilitychange', onVisibilityChange)
    } else {
      // User logged out: clear data and stop polling
      subscriptionStore.clear()
      announcementStore.reset()
      adminComplianceStore.reset()
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  },
  { immediate: true }
)

// Route change trigger (throttled by store)
router.afterEach(() => {
  if (authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('admin-compliance-required', onAdminComplianceRequired)
})

onMounted(async () => {
  window.addEventListener('admin-compliance-required', onAdminComplianceRequired)

  // Check if setup is needed
  try {
    const status = await getSetupStatus()
    if (status.needs_setup && route.path !== '/setup') {
      router.replace('/setup')
      return
    }
  } catch {
    // If setup endpoint fails, assume normal mode and continue
  }

  // Load public settings into appStore (will be cached for other components)
  await appStore.fetchPublicSettings()

  // Re-resolve document title now that site settings are available
  updateDocumentTitle()
})
</script>

<template>
  <NavigationProgress />
  <!-- 路由转场：Suspense 必须在 Transition 外层。
       反过来（Transition mode="out-in" 包 Suspense）会在新页面 chunk 下载期间留下
       整段空白：out-in 先卸载旧页面，而入场要等异步组件解析完成，此时 fallback
       既不在旧节点里、也还没成为新节点，页面上什么都没有，看起来就是白屏。
       把 Suspense 放在外面，fallback 会在同一位置立刻接管，页面始终有内容。
       mode 用默认（同时进行）而不是 out-in，新页面就位后旧页面才移除，避免空窗。 -->
  <RouterView v-slot="{ Component, route }">
    <Suspense>
      <Transition name="mo-page">
        <component :is="Component" :key="route.path" />
      </Transition>
      <template #fallback>
        <div class="route-fallback" data-testid="route-loading-fallback" aria-busy="true" aria-live="polite">
          <div class="route-fallback__bar" />
          <span class="sr-only">{{ t('common.loading') }}</span>
        </div>
      </template>
    </Suspense>
  </RouterView>
  <Toast />
  <AnnouncementPopup />
  <AdminComplianceDialog />
</template>

<style scoped>
/*
  Stand-in while the next route's chunk is still downloading. Kept deliberately
  quiet — it only has to prove the app is alive, so it must never look like a
  finished page or push layout around.
*/
.route-fallback {
  min-height: 60vh;
  display: grid;
  place-items: center;
  padding: 48px 24px;
}
.route-fallback__bar {
  width: 128px;
  height: 3px;
  border-radius: 999px;
  background: linear-gradient(90deg, transparent, var(--ds-primary), transparent);
  animation: route-fallback-slide 1.2s ease-in-out infinite;
}
@keyframes route-fallback-slide {
  0% { transform: translateX(-40%); opacity: 0.4; }
  50% { opacity: 1; }
  100% { transform: translateX(40%); opacity: 0.4; }
}
@media (prefers-reduced-motion: reduce) {
  .route-fallback__bar { animation: none; opacity: 0.8; }
}
</style>
