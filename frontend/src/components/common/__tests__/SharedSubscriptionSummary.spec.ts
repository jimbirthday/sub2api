import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises } from '@vue/test-utils'
import SharedSubscriptionSummary from '../SharedSubscriptionSummary.vue'
const fetch = vi.hoisted(() => vi.fn())
const push = vi.hoisted(() => vi.fn())
vi.mock('@/api/sharedSubscriptions', () => ({ sharedSubscriptionsAPI: { subscriptions: fetch } }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, number>) => `${key}${params ? ` ${Object.values(params).join(' ')}` : ''}` }) }))
beforeEach(() => { setActivePinia(createPinia()); vi.clearAllMocks(); vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-20T00:00:00Z')) })
afterEach(() => { vi.useRealTimers() })
describe('shared credit summary', () => {
	  it('shows the plan name, progress summary and every plan in the expandable details', async () => {
    const plan = { id: 1, status: 'active', starts_at: '2026-09-01', expires_at: '2026-09-22', plan: { name: 'Shared Pro' }, windows: [{ limit: 10, used: 3, reserved: 2 }, { limit: 20, used: 17, reserved: 1 }, { limit: null, used: 50, reserved: 0 }] }
    fetch.mockResolvedValue([plan, { ...plan, id: 2, plan: { name: 'Unlimited' }, windows: [{ limit: null, used: 20, reserved: 10 }] }, { ...plan, id: 3, plan: { name: 'Third' } }])
    const wrapper = mount(SharedSubscriptionSummary); await flushPromises()
    expect(wrapper.findAll('[data-test="shared-summary"]')).toHaveLength(3)
	    expect(wrapper.text()).toContain('$2')
    expect(wrapper.text()).toContain('sharedSubscriptions.daysToExpiry 2')
	    expect(wrapper.text()).toContain('sharedSubscriptions.unlimited')
	    expect(wrapper.get('summary').text()).toContain('Shared Pro +2')
	    expect(wrapper.get('summary').text()).not.toContain('$17 / $20')
	    expect(wrapper.get('[data-test="shared-summary-menu"]').classes()).toContain('w-20')
	    expect(wrapper.find('[role="progressbar"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('uses the smallest absolute remaining window, matching backend capacity', async () => {
    fetch.mockResolvedValue([{ id: 1, status: 'active', starts_at: '2026-09-01', expires_at: '2026-09-22', plan: { name: 'Shared Pro' }, windows: [
      { kind: 'daily', limit: 1, used: 0.9, reserved: 0 },
      { kind: 'monthly', limit: 100, used: 95, reserved: 0 },
    ] }])
    const wrapper = mount(SharedSubscriptionSummary); await flushPromises()
    expect(wrapper.get('[data-test="shared-summary"]').text()).toContain('$0.9 / $1')
    wrapper.unmount()
  })

  it('closes the menu and navigates to the subscription detail section', async () => {
    const plan = { id: 1, status: 'active', starts_at: '2026-09-01', expires_at: '2026-09-22', plan: { name: 'Shared Pro' }, windows: [{ kind: 'daily', limit: 10, used: 3, reserved: 0 }] }
    fetch.mockResolvedValue([plan])
    push.mockResolvedValue(undefined)
    const target = document.createElement('section')
    target.id = 'shared-subscriptions'
    target.scrollIntoView = vi.fn()
    document.body.appendChild(target)
    const wrapper = mount(SharedSubscriptionSummary)
    await flushPromises()
    const menu = wrapper.get('details').element as HTMLDetailsElement
    menu.open = true

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(menu.open).toBe(false)
    expect(push).toHaveBeenCalledWith({ path: '/subscriptions', hash: '#shared-subscriptions' })
    expect(target.scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    target.remove()
    wrapper.unmount()
  })
})
