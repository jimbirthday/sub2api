import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import SharedUserSelect from '../SharedUserSelect.vue'
import Select from '@/components/common/Select.vue'
const list = vi.hoisted(() => vi.fn())
const getById = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/users', () => ({ list, getById }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
beforeEach(() => { list.mockReset() })
describe('shared subscription user search', () => {
  it('searches active users and keeps the selected label across new searches', async () => {
    list.mockResolvedValue({ items: [{ id: 9, email: 'nine@example.com', username: 'Nine' }] })
    const wrapper = shallowMount(SharedUserSelect, { props: { activeOnly: true } })
    const select = wrapper.getComponent(Select)
    select.vm.$emit('search', 'nine')
    await flushPromises()
    expect(list).toHaveBeenCalledWith(1, 30, { search: 'nine', status: 'active' })
    expect(select.props('options')).toEqual([{ value: 9, label: 'nine@example.com · Nine (#9)' }])
    select.vm.$emit('update:modelValue', 9)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([9])
    list.mockResolvedValue({ items: [] })
    select.vm.$emit('search', 'new search')
    await flushPromises()
    expect(select.props('options')).toHaveLength(1)
    wrapper.unmount()
  })
  it('resolves a user set programmatically by the assignment form', async () => {
    getById.mockResolvedValue({ id: 9, email: 'nine@example.com' })
    const wrapper = shallowMount(SharedUserSelect, { props: { modelValue: 9 } })
    await flushPromises()
    expect(wrapper.getComponent(Select).props('options')).toEqual([{ value: 9, label: 'nine@example.com (#9)' }])
    wrapper.unmount()
  })
  it('ignores older search responses that arrive after the latest response', async () => {
    let resolveOld!: (value: unknown) => void
    list.mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValueOnce({ items: [{ id: 2, email: 'new@example.com' }] })
    const wrapper = shallowMount(SharedUserSelect)
    const select = wrapper.getComponent(Select)
    select.vm.$emit('search', 'old')
    select.vm.$emit('search', 'new')
    await flushPromises()
    resolveOld({ items: [{ id: 1, email: 'old@example.com' }] })
    await flushPromises()
    expect(select.props('options')).toEqual([{ value: 2, label: 'new@example.com (#2)' }])
    wrapper.unmount()
  })
})
