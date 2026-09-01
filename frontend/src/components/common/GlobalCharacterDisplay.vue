<template>
  <div class="global-character-display">
    <!-- 全屏角色背景（半透明） -->
    <div 
      class="character-background"
      :style="{ 
        background: currentGradient,
        opacity: 0.08 
      }"
    ></div>

    <!-- 右下角大型角色展示 -->
    <div 
      class="character-showcase"
      @mouseenter="pauseRotation"
      @mouseleave="resumeRotation"
    >
      <!-- 发光背景 -->
      <div 
        class="glow-background"
        :style="{ boxShadow: currentGlow }"
      ></div>

      <!-- 角色图片（大尺寸） -->
      <div class="character-container">
        <transition name="character-fade" mode="out-in">
          <img
            :key="currentIndex"
            :src="currentCharacter.image"
            :alt="currentCharacter.name"
            class="character-image"
          />
        </transition>
      </div>

      <!-- 角色名称标签 -->
      <div class="character-badge">
        <div class="badge-content">
          <span class="character-name">{{ currentCharacter.name }}</span>
          <span class="character-subtitle">{{ currentCharacter.description }}</span>
        </div>
      </div>

      <!-- 导航点 -->
      <div class="navigation-dots">
        <button
          v-for="(char, idx) in characters"
          :key="idx"
          @click="switchTo(idx)"
          class="nav-dot"
          :class="{ active: idx === currentIndex }"
          :aria-label="char.name"
        >
          <span class="dot-inner"></span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useCharacterTheme } from '@/composables/useCharacterTheme'

interface Character {
  name: string
  description: string
  image: string
  themeId: string
}

const characters: Character[] = [
  {
    name: '紫夜',
    description: '神秘的魔法使',
    image: '/characters/character-purple-cat.jpg',
    themeId: 'purple-night'
  },
  {
    name: '樱音',
    description: '温柔的治愈师',
    image: '/characters/character-pink.jpg',
    themeId: 'sakura'
  },
  {
    name: '冰璃',
    description: '冰雪公主',
    image: '/characters/character-ice-blue.jpg',
    themeId: 'ice-blue'
  },
  {
    name: '米瑟',
    description: '温暖守护者',
    image: '/characters/character-beige.jpg',
    themeId: 'emerald'
  },
  {
    name: '樱舞',
    description: '春日精灵',
    image: '/characters/character-sakura-pink.jpg',
    themeId: 'flame'
  },
  {
    name: '彩梦',
    description: '彩虹歌姬',
    image: '/characters/character-colorful-pink.jpg',
    themeId: 'starry'
  },
  {
    name: '花音',
    description: '花之使者',
    image: '/characters/character-flower.jpg',
    themeId: 'moonlight'
  },
  {
    name: '素心',
    description: '纯白天使',
    image: '/characters/character-sketch-1.jpg',
    themeId: 'amber'
  }
]

const currentIndex = ref(0)
const isPaused = ref(false)
let rotationTimer: number | null = null

const { switchTheme, getTheme } = useCharacterTheme()

const currentCharacter = computed(() => characters[currentIndex.value])

const currentTheme = computed(() => getTheme(currentCharacter.value.themeId))

const currentGradient = computed(() => 
  currentTheme.value?.gradients.background || 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)'
)

const currentGlow = computed(() => 
  `0 0 80px ${currentTheme.value?.glow || 'rgba(102, 126, 234, 0.4)'},
   0 0 120px ${currentTheme.value?.glow || 'rgba(102, 126, 234, 0.3)'}`
)

function switchTo(index: number) {
  currentIndex.value = index
  switchTheme(characters[index].themeId)
  resetRotationTimer()
}

function nextCharacter() {
  const next = (currentIndex.value + 1) % characters.length
  switchTo(next)
}

function pauseRotation() {
  isPaused.value = true
}

function resumeRotation() {
  isPaused.value = false
  resetRotationTimer()
}

function startRotation() {
  rotationTimer = window.setInterval(() => {
    if (!isPaused.value) {
      nextCharacter()
    }
  }, 8000)
}

function resetRotationTimer() {
  if (rotationTimer) {
    clearInterval(rotationTimer)
  }
  startRotation()
}

onMounted(() => {
  // 初始化主题
  switchTheme(characters[0].themeId)
  // 开始自动轮换
  startRotation()
})

onUnmounted(() => {
  if (rotationTimer) {
    clearInterval(rotationTimer)
  }
})
</script>

<style scoped>
.global-character-display {
  position: fixed;
  inset: 0;
  pointer-events: none;
  z-index: 1;
}

/* 全屏背景渐变 */
.character-background {
  position: absolute;
  inset: 0;
  transition: all 1.2s cubic-bezier(0.4, 0, 0.2, 1);
}

/* 右下角角色展示区 */
.character-showcase {
  position: fixed;
  bottom: 0;
  right: 0;
  width: 420px;
  height: 520px;
  pointer-events: auto;
  z-index: 40;
}

/* 发光背景 */
.glow-background {
  position: absolute;
  inset: 0;
  border-radius: 32px 32px 0 0;
  transition: box-shadow 1.2s cubic-bezier(0.4, 0, 0.2, 1);
  pointer-events: none;
}

/* 角色容器 */
.character-container {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  overflow: hidden;
  border-radius: 32px 32px 0 0;
}

/* 角色图片 */
.character-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: center bottom;
  filter: drop-shadow(0 8px 32px rgba(0, 0, 0, 0.25));
  transition: transform 0.6s cubic-bezier(0.4, 0, 0.2, 1);
}

.character-container:hover .character-image {
  transform: scale(1.05) translateY(-8px);
}

/* 角色切换动画 */
.character-fade-enter-active,
.character-fade-leave-active {
  transition: opacity 0.6s cubic-bezier(0.4, 0, 0.2, 1), 
              transform 0.6s cubic-bezier(0.4, 0, 0.2, 1);
}

.character-fade-enter-from {
  opacity: 0;
  transform: translateX(60px) scale(0.95);
}

.character-fade-leave-to {
  opacity: 0;
  transform: translateX(-60px) scale(0.95);
}

/* 角色名称标签 */
.character-badge {
  position: absolute;
  top: 24px;
  left: 24px;
  right: 24px;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  border-radius: 16px;
  padding: 16px 20px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12),
              0 2px 8px rgba(0, 0, 0, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.8);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}

.character-showcase:hover .character-badge {
  transform: translateY(-4px);
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.16),
              0 4px 12px rgba(0, 0, 0, 0.12);
}

.badge-content {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.character-name {
  font-size: 20px;
  font-weight: 700;
  background: linear-gradient(135deg, 
    var(--color-primary-600), 
    var(--color-primary-500)
  );
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  letter-spacing: 0.5px;
}

.character-subtitle {
  font-size: 13px;
  color: var(--color-primary-400);
  font-weight: 500;
}

/* 导航点 */
.navigation-dots {
  position: absolute;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  gap: 12px;
  padding: 12px 20px;
  background: rgba(255, 255, 255, 0.90);
  backdrop-filter: blur(20px);
  border-radius: 24px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.6);
}

.nav-dot {
  width: 32px;
  height: 32px;
  padding: 0;
  border: none;
  background: transparent;
  cursor: pointer;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
}

.nav-dot:hover {
  transform: scale(1.15);
}

.dot-inner {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--color-primary-300);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}

.nav-dot.active .dot-inner {
  width: 18px;
  height: 18px;
  background: var(--color-primary-600);
  box-shadow: 0 0 16px var(--glow-color),
              0 0 8px var(--color-primary-400);
}

.nav-dot.active::before {
  content: '';
  position: absolute;
  inset: -4px;
  border-radius: 50%;
  border: 2px solid var(--color-primary-300);
  animation: pulse-ring 1.5s cubic-bezier(0.4, 0, 0.2, 1) infinite;
}

@keyframes pulse-ring {
  0%, 100% {
    transform: scale(1);
    opacity: 0.6;
  }
  50% {
    transform: scale(1.3);
    opacity: 0;
  }
}

/* 响应式：隐藏在小屏幕 */
@media (max-width: 1024px) {
  .character-showcase {
    display: none;
  }
}

/* Dark mode 适配 */
.dark .character-badge {
  background: rgba(15, 23, 42, 0.95);
  border-color: rgba(255, 255, 255, 0.1);
}

.dark .navigation-dots {
  background: rgba(15, 23, 42, 0.90);
  border-color: rgba(255, 255, 255, 0.1);
}

.dark .character-name {
  -webkit-text-fill-color: transparent;
}

.dark .character-subtitle {
  color: var(--color-primary-300);
}
</style>
