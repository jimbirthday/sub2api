import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import View from '../SharedSubscriptionsView.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import SharedUserSelect from '@/components/admin/shared-subscription/SharedUserSelect.vue'
import Select from '@/components/common/Select.vue'
const mocks = vi.hoisted(() => ({ adminPage: vi.fn(), plans: vi.fn(), assign: vi.fn(), action: vi.fn(), changePlan: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('@/api/sharedSubscriptions', async original => ({ ...await original<typeof import('@/api/sharedSubscriptions')>(), sharedSubscriptionsAPI: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const plan = { id: 1, version: 1, name: 'Starter', group_ids: [1], for_sale: true }
const rows = [
  { id: 20, user_id: 1, user_email: 'one@example.com', plan, plan_id: 1, status: 'active', generation: 3, windows: [] },
  { id: 19, user_id: 2, user_email: 'two@example.com', plan, plan_id: 1, status: 'paused', generation: 4, windows: [] },
  { id: 18, user_id: 3, user_email: 'three@example.com', plan, plan_id: 1, status: 'revoked', generation: 0, windows: [] },
]
function mountView() {
  return shallowMount(View, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    TablePageLayout: { template: '<div><slot name="actions"/><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/></div>' },
  } } })
}
let wrapper: ReturnType<typeof mountView>
beforeEach(async () => {
  vi.clearAllMocks()
  mocks.adminPage.mockResolvedValue({ items: rows, total: 63, page: 1, page_size: 20 })
  mocks.plans.mockResolvedValue([plan, { ...plan, id: 2, name: 'Pro' }])
  mocks.assign.mockResolvedValue({})
  mocks.action.mockResolvedValue({})
  mocks.changePlan.mockResolvedValue({})
  wrapper = mountView(); await flushPromises()
})
afterEach(() => { wrapper.unmount(); vi.useRealTimers() })
describe('shared subscription table management', () => {
  it('uses server pagination, sorting and page sizes and clears selection', async () => {
    expect(wrapper.getComponent(DataTable).props('data')).toHaveLength(3)
    expect(wrapper.getComponent(Pagination).props('total')).toBe(63)
    wrapper.getComponent(DataTable).vm.$emit('update:selectedKeys', [20, 999])
    await flushPromises()
    expect(wrapper.getComponent(DataTable).props('selectedKeys')).toEqual([20])
    wrapper.getComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }), expect.any(AbortSignal))
    expect(wrapper.getComponent(DataTable).props('selectedKeys')).toEqual([])
    wrapper.getComponent(Pagination).vm.$emit('update:pageSize', 50)
    await flushPromises()
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, page_size: 50 }), expect.any(AbortSignal))
    wrapper.getComponent(DataTable).vm.$emit('sort', 'expires_at', 'asc')
    await flushPromises()
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ sort_by: 'expires_at', sort_order: 'asc' }), expect.any(AbortSignal))
  })
  it('debounces search across all records and clears all filters', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    await wrapper.get('[data-test="subscription-search"]').setValue('one@example.com')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'one@example.com', page: 1 }), expect.any(AbortSignal))
    await wrapper.get('[data-test="clear-filters"]').trigger('click'); await flushPromises()
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ search: undefined, status: undefined, plan_id: undefined }), expect.any(AbortSignal))
  })
  it('ignores an older response after a newer search completes', async () => {
    let resolve!: (value: unknown) => void
    mocks.adminPage.mockReturnValueOnce(new Promise(r => { resolve = r }))
    wrapper.getComponent(DataTable).vm.$emit('sort', 'expires_at', 'asc'); await flushPromises()
    mocks.adminPage.mockResolvedValueOnce({ items: [rows[1]], total: 1, page: 1, page_size: 20 })
    await wrapper.get('[data-test="clear-filters"]').trigger('click'); await flushPromises()
    resolve({ items: rows, total: 63, page: 1 }); await flushPromises()
    expect(wrapper.getComponent(DataTable).props('data')).toEqual([rows[1]])
  })
  it('assigns searched users and keeps one request key after a network failure', async () => {
    await wrapper.get('[data-test="open-assign"]').trigger('click')
    const form = wrapper.get('#shared-assign-form')
    form.getComponent(SharedUserSelect).vm.$emit('update:modelValue', 1)
    form.getComponent(Select).vm.$emit('update:modelValue', 2)
    mocks.assign.mockRejectedValueOnce(new Error('offline'))
    await form.trigger('submit'); await flushPromises()
    const requestID = mocks.assign.mock.calls[0][2]
    await form.trigger('submit'); await flushPromises()
    expect(mocks.assign).toHaveBeenLastCalledWith(1, 2, requestID)
    expect(mocks.adminPage).toHaveBeenLastCalledWith(expect.objectContaining({ user_id: 1, page: 1 }), expect.any(AbortSignal))
  })
  it('keeps only failed rows for retry, excluding revoked subscriptions', async () => {
    wrapper.getComponent(DataTable).vm.$emit('update:selectedKeys', [20, 19, 18]); await flushPromises()
    await wrapper.get('[data-test="bulk-reset"]').trigger('click')
    mocks.action.mockImplementation((id: number) => id === 19 ? Promise.reject(new Error('failed')) : Promise.resolve({}))
    await wrapper.get('#shared-operation-form').trigger('submit'); await flushPromises()
    expect(mocks.action.mock.calls.map(call => call[0])).toEqual([20, 19])
    expect(wrapper.get('#shared-operation-form').text()).toContain('two@example.com')
    expect(wrapper.get('#shared-operation-form').text()).not.toContain('one@example.com')
    mocks.action.mockResolvedValue({})
    await wrapper.get('#shared-operation-form').trigger('submit'); await flushPromises()
    expect(mocks.action.mock.calls.map(call => call[0])).toEqual([20, 19, 19])
  })
  it('switches each selected subscription with its current generation', async () => {
    wrapper.getComponent(DataTable).vm.$emit('update:selectedKeys', [20]); await flushPromises()
    await wrapper.get('[data-test="bulk-changePlan"]').trigger('click')
    wrapper.get('#shared-operation-form').getComponent(Select).vm.$emit('update:modelValue', 2)
    await wrapper.get('#shared-operation-form').trigger('submit'); await flushPromises()
    expect(mocks.changePlan).toHaveBeenCalledWith(20, 2, 3)
  })
})
