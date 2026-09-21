import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import SharedSubscriptionsView from '../SharedSubscriptionsView.vue'
import SharedUserSelect from '@/components/admin/shared-subscription/SharedUserSelect.vue'

const mocks = vi.hoisted(() => ({
  path: '/admin/shared-subscriptions/subscriptions',
  plans: vi.fn(), adminPage: vi.fn(), assign: vi.fn(), changePlan: vi.fn(), action: vi.fn(), groups: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn(), history: vi.fn(),
}))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ get path() { return mocks.path } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }) }))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('@/api/admin/groups', () => ({ getAll: mocks.groups, default: {} }))
vi.mock('@/api/sharedSubscriptions', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/sharedSubscriptions')>(),
  sharedSubscriptionsAPI: mocks,
}))
const plan = { id: 1, version: 1, name: 'Starter shared', price: 10, validity_days: 30, group_ids: [1], for_sale: true }
const sub = { id: 20, user_id: 5, user_email: 'test@example.com', plan_id: 1, plan, status: 'active', generation: 3, windows: [], expires_at: '2099-01-01' }
function mountView() {
  return shallowMount(SharedSubscriptionsView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
  } } })
}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.path = '/admin/shared-subscriptions/subscriptions'
  mocks.plans.mockResolvedValue([plan, { ...plan, id: 2, name: 'Pro shared' }])
  mocks.adminPage.mockResolvedValue({ items: [sub], has_more: false })
  mocks.groups.mockResolvedValue([])
  mocks.assign.mockResolvedValue({})
  mocks.changePlan.mockResolvedValue({})
})
describe('shared subscription administration', () => {
  it('keeps plan configuration separate from assignment, user subscriptions and settlements', async () => {
    mocks.path = '/admin/shared-subscriptions/plans'
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('sharedSubscriptions.planManagement')
    expect(wrapper.text()).not.toContain('sharedSubscriptions.grant')
    expect(wrapper.findComponent(SharedUserSelect).exists()).toBe(false)
    expect(mocks.adminPage).not.toHaveBeenCalled()
    expect(mocks.history).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('assigns the searched user with a stable idempotency key across a retry', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(mocks.groups).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('test@example.com')
    expect(wrapper.text()).not.toContain('sharedSubscriptions.history')
    wrapper.findAllComponents(SharedUserSelect)[0].vm.$emit('update:modelValue', 5)
    await wrapper.get('form select').setValue('1')
    mocks.assign.mockRejectedValueOnce(new Error('retry'))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const firstKey = mocks.assign.mock.calls[0][2]
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.assign).toHaveBeenLastCalledWith(5, 1, firstKey)
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ user_id: 5 }))
    expect(wrapper.get('form button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('switches a selected subscription using its current generation', async () => {
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === 'sharedSubscriptions.changePlan')!
    await button.trigger('click')
    const form = wrapper.findAll('form').at(-1)!
    await form.get('select').setValue('2')
    await form.trigger('submit')
    await flushPromises()
    expect(mocks.changePlan).toHaveBeenCalledWith(20, 2, 3)
    expect(mocks.showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })
  it('continues past the first page without truncating older subscriptions', async () => {
    mocks.adminPage.mockResolvedValueOnce({ items: [sub], has_more: true }).mockResolvedValue({ items: [], has_more: false })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'pagination.next')!.trigger('click')
    await flushPromises()
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ before_id: 20 }))
    wrapper.unmount()
  })
})
