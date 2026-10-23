import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountTrafficControls from '../AccountTrafficControls.vue'
import { accountTrafficAPI, defaultTrafficPolicy } from '@/api/admin/accountTraffic'

vi.mock('@/api/admin/accountTraffic', async () => ({ ...await vi.importActual('@/api/admin/accountTraffic'), accountTrafficAPI: { get: vi.fn(), save: vi.fn() } }))
beforeEach(() => {
  vi.clearAllMocks()
  let policy = defaultTrafficPolicy()
  vi.mocked(accountTrafficAPI.get).mockImplementation(async () => ({ policy: { ...policy }, state: null, state_available: true, hard_limit: 128 }))
  vi.mocked(accountTrafficAPI.save).mockImplementation(async (_id, value) => { policy = { ...value }; return { policy, state_available: true, hard_limit: 128 } })
})
async function open(wrapper: ReturnType<typeof mount>) { (wrapper.get('details').element as HTMLDetailsElement).open = true; await wrapper.get('details').trigger('toggle'); await flushPromises() }

describe('optional account traffic controls', () => {
  it('can disable a policy after invalid edits without submitting hidden invalid values', async () => {
    const wrapper = mount(AccountTrafficControls, { props: { accountId: 42 } }); await open(wrapper)
    await wrapper.get('[data-testid=strict-rpm-toggle]').setValue(true)
    await wrapper.findAll('input[type=number]')[0].setValue(0)
    await wrapper.get('[data-testid=strict-rpm-toggle]').setValue(false)
    await wrapper.findAll('button').find(button => button.text() === '保存流量控制')!.trigger('click'); await flushPromises()
    expect(accountTrafficAPI.save).toHaveBeenCalledWith(42, expect.objectContaining({ strict_rpm_enabled: false, rpm: 60 }))
    expect(wrapper.text()).toContain('已保存')
    wrapper.unmount()
  })
  it('does not load or enable policies until the panel is opened', async () => {
    const wrapper = mount(AccountTrafficControls, { props: { accountId: 42 } })
    expect(accountTrafficAPI.get).not.toHaveBeenCalled()
    await open(wrapper)
    expect((wrapper.get('[data-testid=strict-rpm-toggle]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('[data-testid=adaptive-toggle]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('automatic')
    expect(accountTrafficAPI.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('independently enables strict RPM and automatic adaptation, then allows both to be disabled', async () => {
    const wrapper = mount(AccountTrafficControls, { props: { accountId: 42 } }); await open(wrapper)
    await wrapper.get('[data-testid=strict-rpm-toggle]').setValue(true)
    await wrapper.get('[data-testid=adaptive-toggle]').setValue(true)
    await wrapper.get('select').setValue('automatic')
    const save = () => wrapper.findAll('button').find(button => button.text() === '保存流量控制')!
    await save().trigger('click'); await flushPromises()
    expect(accountTrafficAPI.save).toHaveBeenLastCalledWith(42, expect.objectContaining({ strict_rpm_enabled: true, adaptive_enabled: true, adaptive_mode: 'automatic' }))
    await wrapper.get('[data-testid=strict-rpm-toggle]').setValue(false)
    await wrapper.get('[data-testid=adaptive-toggle]').setValue(false)
    await save().trigger('click'); await flushPromises()
    expect(accountTrafficAPI.save).toHaveBeenLastCalledWith(42, expect.objectContaining({ strict_rpm_enabled: false, adaptive_enabled: false }))
    expect(wrapper.text()).toContain('已保存')
    wrapper.unmount()
  })
  it('explains that observe mode does not change the live concurrency slot', async () => {
    const wrapper = mount(AccountTrafficControls, { props: { accountId: 42 } }); await open(wrapper)
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('automatic')
    await wrapper.get('select').setValue('observe')
    expect(wrapper.text()).toContain('建议模式只写入观测和建议并发')
    wrapper.unmount()
  })
  it('does not save a burst larger than the rolling minute limit', async () => {
    const wrapper = mount(AccountTrafficControls, { props: { accountId: 42 } }); await open(wrapper)
    await wrapper.get('[data-testid=strict-rpm-toggle]').setValue(true)
    const numbers = wrapper.findAll('input[type=number]')
    await numbers[0].setValue(2); await numbers[1].setValue(3)
    await wrapper.findAll('button').find(button => button.text() === '保存流量控制')!.trigger('click')
    expect(accountTrafficAPI.save).not.toHaveBeenCalled(); expect(wrapper.text()).toContain('突发额度不能大于 RPM')
    wrapper.unmount()
  })
  it('keeps adaptive tuning fixed and permits administrator overrides of RPM only', async () => {
    const wrapper = mount(AccountTrafficControls, { props: { accountId: 42 } }); await open(wrapper)
    await wrapper.get('[data-testid=traffic-recommended]').trigger('click')
    expect((wrapper.get('[data-testid=strict-rpm-toggle]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('[data-testid=adaptive-toggle]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('automatic')
    expect(accountTrafficAPI.save).not.toHaveBeenCalled()
    await wrapper.get('[data-testid=strict-rpm-toggle]').setValue(true)
    await wrapper.get('[data-testid=adaptive-toggle]').setValue(true)
    await wrapper.get('select').setValue('automatic')
    await wrapper.get('[data-testid=traffic-recommended]').trigger('click')
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('automatic')
    // Adaptive exposes no per-account tuning: only RPM and burst are editable.
    const numbers = wrapper.findAll('input[type=number]')
    expect(numbers).toHaveLength(2)
    await numbers[0].setValue(240); await numbers[1].setValue(12)
    await wrapper.findAll('button').find(button => button.text() === '保存流量控制')!.trigger('click'); await flushPromises()
    expect(accountTrafficAPI.save).toHaveBeenLastCalledWith(42, {
      strict_rpm_enabled: true, rpm: 240, burst: 12,
      adaptive_enabled: true, adaptive_mode: 'automatic',
      min_concurrency: 50, failure_threshold: 3, failure_window_seconds: 60, recovery_seconds: 60,
    })
    wrapper.unmount()
  })
})
