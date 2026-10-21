import { mount, flushPromises } from '@vue/test-utils'
import { describe, beforeEach, it, expect, vi } from 'vitest'
import DistillationRiskView from '../DistillationRiskView.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
const api = vi.hoisted(() => ({ overview: vi.fn(), config: vi.fn(), users: vi.fn(), events: vi.fn(), exemptions: vi.fn(), detail: vi.fn(), trends: vi.fn(), saveConfig: vi.fn(), action: vi.fn(), exempt: vi.fn(), revokeExemption: vi.fn() }))
vi.mock('@/api/admin/distillationRisk', () => ({ default: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const snapshot = { mode: 'observe', pending_reviews: 2, active_restrictions: 1, last_processed_at: null, state_counts: {}, active_exemptions: 0 }
const config = { mode: 'observe', revision: 4, rule_version: 1, scoring_config: {}, observation_started_at: '2026-10-01T00:00:00Z' }
const create = () => mount(DistillationRiskView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, DataTable: { props: ['data', 'columns', 'loading'], template: '<div data-test="table">{{ JSON.stringify(data) }}<div v-for="row in data" :key="row.user_id"><slot name="cell-actions" :row="row" /></div></div>' }, Pagination: true, teleport: true } } })
describe('DistillationRiskView', () => {
  beforeEach(() => { vi.resetAllMocks(); api.overview.mockResolvedValue(snapshot); api.config.mockResolvedValue(config); api.users.mockResolvedValue({ items: [], total: 0 }) })
  it('keeps missing processing telemetry visibly unavailable', async () => {
    const wrapper = create(); await flushPromises()
    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).toContain('admin.distillationRisk.modeObserve')
    expect(api.users).toHaveBeenCalledWith({ cursor: true, before_id: undefined, page_size: 20, user_id: undefined, state: '' })
    wrapper.unmount()
  })
  it('does not turn failed overview metrics into zeros', async () => {
    api.overview.mockRejectedValue(new Error('database unavailable'))
    const wrapper = create(); await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toBe('admin.distillationRisk.failed')
    expect(wrapper.text()).not.toContain('admin.distillationRisk.modeObserve')
    wrapper.unmount()
  })
  it('saves config using the read revision and shows conflicts without closing the editor', async () => {
    api.saveConfig.mockRejectedValue({ response: { status: 409 } })
    const wrapper = create(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.distillationRisk.configuration')!.trigger('click')
    await wrapper.find('select').setValue('')
    await wrapper.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.saveConfig.mock.calls[0][0].revision).toBe(4)
    expect(wrapper.text()).toContain('admin.distillationRisk.conflict')
    wrapper.unmount()
  })
  it('keeps the editing revision when a page refresh receives a newer configuration', async () => {
    api.saveConfig.mockRejectedValue({ status: 409 })
    const wrapper = create(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.distillationRisk.configuration')!.trigger('click')
    await wrapper.find('[role="dialog"] input[type="number"]').setValue('0.75')
    api.config.mockResolvedValue({ ...config, revision: 9, scoring_config: { concurrency_saturation: 0.8 } })
    await wrapper.findAll('button').find(button => button.text() === 'common.refresh')!.trigger('click'); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.saveConfig.mock.calls[0][0]).toMatchObject({ revision: 4, scoring_config: { concurrency_saturation: 0.75 } })
    expect((wrapper.find('[role="dialog"] input[type="number"]').element as HTMLInputElement).value).toBe('0.75')
    expect(wrapper.findAll('button').find(button => button.text() === 'common.save')!.attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(button => button.text() === 'admin.modelBans.reloadLatest')!.trigger('click'); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.confirm')!.trigger('click'); await flushPromises()
    expect((wrapper.find('[role="dialog"] input[type="number"]').element as HTMLInputElement).value).toBe('0.8')
    api.saveConfig.mockResolvedValue({ ...config, revision: 10 })
    await wrapper.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.saveConfig.mock.calls[1][0].revision).toBe(9)
    wrapper.unmount()
  })

  it('preserves an action draft on a case conflict until a confirmed reload', async () => {
    const row = { user_id: 42, state: 'suspected', revision: 3, last_score: 90, review_status: 'pending' }
    api.users.mockResolvedValue({ items: [row], total: 1 }); api.trends.mockResolvedValue([])
    api.detail.mockResolvedValue({ case: row, active_restriction: null, active_exemption: null })
    api.action.mockRejectedValue({ status: 409 })
    const wrapper = create(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.distillationRisk.review')!.trigger('click'); await flushPromises()
    await wrapper.find('textarea').setValue('reviewed evidence')
    api.detail.mockResolvedValue({ case: { ...row, revision: 9 }, active_restriction: null, active_exemption: null })
    await wrapper.findAll('button').find(button => button.text() === 'admin.distillationRisk.actionReview')!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'common.confirm')!.trigger('click'); await flushPromises()
    expect(api.action.mock.calls[0][0]).toMatchObject({ expected_revision: 3, reason: 'reviewed evidence', user_id: 42 })
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('reviewed evidence')
    expect(wrapper.findAll('button').find(button => button.text() === 'admin.distillationRisk.actionReview')!.attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(button => button.text() === 'admin.modelBans.reloadLatest')!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'common.confirm')!.trigger('click'); await flushPromises()
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('')
    wrapper.unmount()
  })
  it('stops loading for an invalid filter even with an earlier request pending and clears errors after recovery', async () => {
    let finish!: (value: unknown) => void
    api.users.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = create(); await flushPromises()
    await wrapper.find('input[type="number"]').setValue('0')
    await wrapper.findAll('button').find(button => button.text() === 'admin.distillationRisk.events')!.trigger('click'); await flushPromises()
    expect(wrapper.findComponent({ name: 'CursorPagination' }).props('loading')).toBe(false)
    expect(wrapper.find('[role="alert"]').text()).toBe('admin.distillationRisk.invalid')
    finish({ items: [], total: 0 }); await flushPromises()
    api.events.mockResolvedValue({ items: [], total: 0 })
    await wrapper.find('input[type="number"]').setValue('42')
    await wrapper.findAll('button').find(button => button.text() === 'common.search')!.trigger('click'); await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('provides translated labels for all editable scoring fields in both locales', () => {
    for (const locale of [en, zh]) {
      for (const field of ['concurrency_saturation', 'request_rate_saturation', 'prompt_repeat_ratio', 'interarrival_cv', 'output_input_ratio']) {
        expect((locale.admin.distillationRisk as Record<string, string>)[field]).toBeTruthy()
      }
    }
  })

})
