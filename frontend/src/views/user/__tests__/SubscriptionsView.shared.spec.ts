import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const mocks = vi.hoisted(() => ({
  legacy: vi.fn(),
  shared: vi.fn(),
  history: vi.fn(),
  push: vi.fn(),
  showError: vi.fn(),
  route: { hash: '' },
}))

vi.mock('@/api/subscriptions', () => ({ default: { getMySubscriptions: mocks.legacy } }))
vi.mock('@/api/sharedSubscriptions', () => ({
  sharedSubscriptionsAPI: { subscriptions: mocks.shared, history: mocks.history },
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: {}, showError: mocks.showError }),
}))
vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.push }),
}))
vi.mock('vue-i18n', async original => ({
  ...await original<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

function mountView() {
  return shallowMount(SubscriptionsView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Icon: true,
      },
    },
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.route.hash = ''
  mocks.legacy.mockResolvedValue([])
  mocks.history.mockResolvedValue([])
  mocks.shared.mockResolvedValue([{
    id: 11,
    plan_id: 7,
    status: 'active',
    starts_at: '2026-09-01T00:00:00Z',
    expires_at: '2099-10-01T00:00:00Z',
    plan: { name: 'Shared Pro' },
    windows: [{ kind: 'daily', limit: 10, used: 3, reserved: 2 }],
  }])
})

describe('my subscriptions shared section', () => {
  it('renders shared subscriptions even when no legacy subscription exists', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Shared Pro')
    expect(wrapper.text()).toContain('sharedSubscriptions.used')
    expect(wrapper.text()).toContain('sharedSubscriptions.unused')
    expect(wrapper.text()).not.toContain('sharedSubscriptions.mine')
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(true)
  })

  it('does not render subscriptions that are expired, past due or revoked', async () => {
    mocks.legacy.mockResolvedValue([
      { id: 21, status: 'expired', expires_at: '2099-10-01T00:00:00Z', group_id: 1, group: { name: 'Expired legacy' } },
      { id: 22, status: 'active', expires_at: '2020-10-01T00:00:00Z', group_id: 2, group: { name: 'Past legacy' } },
      { id: 23, status: 'revoked', expires_at: '2099-10-01T00:00:00Z', group_id: 3, group: { name: 'Revoked legacy' } },
    ])
    const active = await mocks.shared()
    mocks.shared.mockResolvedValue([
      ...active,
      { ...active[0], id: 12, status: 'expired', plan: { name: 'Expired shared' } },
      { ...active[0], id: 13, expires_at: '2020-10-01T00:00:00Z', plan: { name: 'Past shared' } },
      { ...active[0], id: 14, status: 'revoked', plan: { name: 'Revoked shared' } },
    ])

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Shared Pro')
    expect(wrapper.text()).not.toContain('Expired shared')
    expect(wrapper.text()).not.toContain('Past shared')
    expect(wrapper.text()).not.toContain('Expired legacy')
    expect(wrapper.text()).not.toContain('Past legacy')
    expect(wrapper.text()).not.toContain('Revoked shared')
    expect(wrapper.text()).not.toContain('Revoked legacy')
  })

  it('opens renewal from the existing My Subscriptions page', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'sharedSubscriptions.renew')!.trigger('click')

    expect(mocks.push).toHaveBeenCalledWith({
      path: '/purchase',
      query: { tab: 'subscription', shared_plan_id: 7, renew_subscription_id: 11 },
    })
  })

  it('scrolls to shared subscriptions after arriving through the detail link', async () => {
    mocks.route.hash = '#shared-subscriptions'
    const target = document.createElement('section')
    target.id = 'shared-subscriptions'
    target.scrollIntoView = vi.fn()
    document.body.appendChild(target)

    const wrapper = mountView()
    await flushPromises()

    expect(target.scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    target.remove()
    wrapper.unmount()
  })
})
