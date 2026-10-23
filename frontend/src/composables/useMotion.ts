/**
 * 主题动效开关。
 *
 * animations.css 里的 `.mo-*` 类默认生效，但用户可能只想关掉「夸张动画」
 * 而保留 OS 层级的 prefers-reduced-motion（或者反过来）。这里在 <html> 上写
 * data-motion="off"，CSS 侧对同名属性有对应规则。
 *
 * 存储键沿用项目现有的 safeStorage 封装，避免 localStorage 在隐私模式下抛错
 * 把整个应用带崩。
 */

import { readLocalStorage, writeLocalStorage } from '@/utils/safeStorage'

const STORAGE_KEY = 'motion'

export type MotionPreference = 'on' | 'off'

/** 读取用户偏好；未设置时返回 'on'（默认有动效）。 */
export function getMotionPreference(): MotionPreference {
  return readLocalStorage(STORAGE_KEY) === 'off' ? 'off' : 'on'
}

/** 把偏好写到 <html data-motion>，CSS 侧据此开关动画。 */
export function applyMotionPreference(pref: MotionPreference = getMotionPreference()): void {
  if (typeof document === 'undefined') return
  if (pref === 'off') {
    document.documentElement.setAttribute('data-motion', 'off')
  } else {
    document.documentElement.removeAttribute('data-motion')
  }
}

/** 设置并持久化偏好，返回实际生效值。 */
export function setMotionPreference(pref: MotionPreference): MotionPreference {
  writeLocalStorage(STORAGE_KEY, pref)
  applyMotionPreference(pref)
  return pref
}

/** 当前是否允许播放主题动画（用户偏好 + OS 偏好同时满足）。 */
export function isMotionAllowed(): boolean {
  if (typeof window === 'undefined') return false
  if (getMotionPreference() === 'off') return false
  try {
    return !window.matchMedia('(prefers-reduced-motion: reduce)').matches
  } catch {
    return true
  }
}
