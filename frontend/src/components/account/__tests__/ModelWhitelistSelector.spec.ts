import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { copyToClipboard, syncUpstreamModelsMock, showSuccess, showInfo } = vi.hoisted(() => ({
  copyToClipboard: vi.fn().mockResolvedValue(true),
  syncUpstreamModelsMock: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => (key === 'common.copy' ? '复制' : key)
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess,
    showInfo
  })
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels: syncUpstreamModelsMock,
    syncUpstreamModelsPreview: vi.fn()
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

function mountSelector() {
  return mount(ModelWhitelistSelector, {
    props: {
      modelValue: [],
      platform: 'openai'
    },
    global: {
      stubs: {
        ModelIcon: true
      }
    }
  })
}

function findModelRow(wrapper: ReturnType<typeof mountSelector>, modelId: string) {
  const row = wrapper
    .findAll('[data-testid="model-option"]')
    .find(candidate => candidate.text().includes(modelId))

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
    syncUpstreamModelsMock.mockReset()
    showSuccess.mockReset()
    showInfo.mockReset()
  })

  it('copies a model ID without selecting the model', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')

    const copyButton = row.get('[data-testid="copy-model-id"]')
    expect(copyButton.attributes('aria-label')).toBe('复制 gpt-5.6-sol')

    await copyButton.trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('keeps the existing model selection behavior', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')
    await row.get('[data-testid="select-model"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol']]])
    expect(copyToClipboard).not.toHaveBeenCalled()
  })

  it('replaces the whitelist with upstream models instead of appending', async () => {
    syncUpstreamModelsMock.mockResolvedValue({ models: ['gpt-5.6-sol', 'gpt-5.6-luna'] })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: ['gpt-4', 'gpt-5.6-sol'],
        platform: 'openai',
        accountId: 2175
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    await wrapper.get('button.border-emerald-200').trigger('click')
    await flushPromises()

    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(2175)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol', 'gpt-5.6-luna']]])
    expect(showSuccess).toHaveBeenCalled()
  })

  it('does not change the whitelist when upstream returns no models', async () => {
    syncUpstreamModelsMock.mockResolvedValue({ models: [] })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: ['gpt-4'],
        platform: 'openai',
        accountId: 2175
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    await wrapper.get('button.border-emerald-200').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showInfo).toHaveBeenCalled()
  })
})
