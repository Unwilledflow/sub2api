import { describe, expect, it } from 'vitest'

import { buildInfiniteCanvasFrameSrc } from '../infiniteCanvas'

describe('buildInfiniteCanvasFrameSrc', () => {
  it('keeps the canvas on the current origin without injecting a user key', () => {
    const src = buildInfiniteCanvasFrameSrc({
      origin: 'https://kedaya.ai/',
      theme: 'dark',
      lang: 'zh-CN',
    })
    const url = new URL(src, 'https://kedaya.ai')

    expect(url.pathname).toBe('/canvas/')
    expect(url.searchParams.get('baseUrl')).toBe('https://kedaya.ai')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(url.searchParams.get('apiKey')).toBeNull()
  })
})
