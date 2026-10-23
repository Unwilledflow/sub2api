import type { ApiKey, Group } from '@/types'

export const INFINITE_CANVAS_KEY_NAME = 'Infinite Canvas'
export const INFINITE_CANVAS_FRAME_PATH = '/canvas/'
export const INFINITE_CANVAS_PATH = '/canvas/'

export interface InfiniteCanvasFrameOptions {
  origin: string
  theme?: 'light' | 'dark'
  lang?: string
}

export function buildInfiniteCanvasFrameSrc(options: InfiniteCanvasFrameOptions): string {
  const origin = String(options.origin || '').trim().replace(/\/+$/, '')
  const url = new URL(INFINITE_CANVAS_FRAME_PATH, `${origin}/`)
  url.searchParams.set('baseUrl', origin)
  if (options.theme) {
    url.searchParams.set('theme', options.theme)
  }
  if (options.lang) {
    url.searchParams.set('lang', options.lang)
  }
  return `${url.pathname}${url.search}`
}

export function selectSmartRoutingGroupIds(groups: Array<Pick<Group, 'id'>>): number[] {
  const seen = new Set<number>()
  const ids: number[] = []
  for (const group of groups) {
    if (!Number.isInteger(group.id) || group.id <= 0 || seen.has(group.id)) continue
    seen.add(group.id)
    ids.push(group.id)
  }
  return ids
}

export function findReusableCanvasKey(keys: ApiKey[], name = INFINITE_CANVAS_KEY_NAME): ApiKey | undefined {
  const matches = keys.filter((key) => key.name === name)
  return matches.find(isUsableCanvasKey) ?? matches[0]
}

export function isUsableCanvasKey(key: ApiKey): boolean {
  return key.status === 'active'
    && (!key.expires_at || new Date(key.expires_at).getTime() > Date.now())
    && !(key.quota > 0 && key.quota_used >= key.quota)
}
