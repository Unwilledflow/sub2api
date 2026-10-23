import { describe, expect, it } from 'vitest'

import router from '@/router'

describe('Infinite Canvas route', () => {
  it('registers the protected route used by the sidebar and dashboard shortcut', () => {
    const route = router.resolve('/infinite-canvas')

    expect(route.name).toBe('InfiniteCanvas')
    expect(route.meta.requiresAuth).toBe(true)
    expect(route.meta.requiresAdmin).toBe(false)
  })
})
