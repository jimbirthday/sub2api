import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SharedSubscriptionCalculatorView from '../SharedSubscriptionCalculatorView.vue'

const mocks = vi.hoisted(() => ({ plans: vi.fn(), groups: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('@/api/sharedSubscriptions', () => ({
  sharedSubscriptionsAPI: { plans: mocks.plans },
}))
vi.mock('@/api/admin/groups', () => ({ getAll: mocks.groups, default: { getAll: mocks.groups } }))
vi.mock('vue-i18n', async original => ({
  ...await original<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

beforeEach(() => {
  vi.clearAllMocks()
  mocks.plans.mockResolvedValue([{
    id: 7,
    name: 'Shared 100',
    price: 100,
    group_ids: [3],
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: 105,
  }])
  mocks.groups.mockResolvedValue([{
    id: 3,
    name: 'Rate 0.2',
    rate_multiplier: 0.2,
    profit_control_enabled: true,
    profit_min_margin: 0.1,
  }])
})

describe('shared subscription calculator', () => {
  it('reads the selected plan groups and calculates the 100/105 example', async () => {
    const wrapper = mount(SharedSubscriptionCalculatorView, {
      global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } },
    })
    await flushPromises()

    await wrapper.get('select').setValue('7')
    await flushPromises()

    expect(wrapper.text()).toContain('×0.2 · 10%')
    expect(wrapper.text()).toContain('$94.50')
    expect(wrapper.text()).toContain('$5.50')
    expect(wrapper.text()).toContain('5.50%')
  })
})
