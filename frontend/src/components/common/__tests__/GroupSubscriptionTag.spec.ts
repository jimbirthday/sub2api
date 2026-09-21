import { describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { mount } from '@vue/test-utils'
import GroupBadge from '../GroupBadge.vue'
import GroupOptionItem from '../GroupOptionItem.vue'
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
describe('subscription availability tag', () => {
  it('adds a separate (D) badge only to covered groups', () => {
    const wrapper = mount(GroupBadge, { props: { name: 'Group A', subscriptionAvailable: true, showRate: false }, global: { plugins: [createPinia()] } })
    expect(wrapper.text()).toContain('Group A')
    expect(wrapper.get('[aria-label="sharedSubscriptions.subscriptionAvailable"]').text()).toBe('(D)')
    expect(wrapper.text()).not.toContain('sharedSubscriptions.title')
    wrapper.unmount()
    const uncovered = mount(GroupBadge, { props: { name: 'Group B', showRate: false }, global: { plugins: [createPinia()] } })
    expect(uncovered.text()).not.toContain('(D)')
    uncovered.unmount()
  })
  it('renders the same tag in dropdown options', () => {
    const wrapper = mount(GroupOptionItem, { props: { name: 'Group A', platform: 'openai', subscriptionAvailable: true }, global: { plugins: [createPinia()], stubs: { PlatformIcon: true } } })
    expect(wrapper.text()).toContain('(D)')
    wrapper.unmount()
  })
})
