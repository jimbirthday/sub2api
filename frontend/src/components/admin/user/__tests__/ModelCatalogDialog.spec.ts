import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelCatalogDialog from '../ModelCatalogDialog.vue'
const api = vi.hoisted(() => ({ catalog: vi.fn(), replaceCatalog: vi.fn() }))
vi.mock('@/api/admin/modelRestrictions', () => api)
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
describe('ModelCatalogDialog', () => {
  beforeEach(() => vi.resetAllMocks())
  it('loads on an initially open mount and preserves contextual aliases on save', async () => {
    const alias = { alias_id: 'alias-1', namespace: 'openai', transport: 'openai', protocol: 'responses', role: 'tool', provider_context: 'provider-a', alias: 'shared', canonical_model: 'gpt-5' }
    const value = { revision: 8, models: [{ namespace: 'openai', canonical_model: 'gpt-5', display_name: 'GPT', source: 'standard' }], aliases: [alias] }
    api.catalog.mockResolvedValue(value); api.replaceCatalog.mockResolvedValue({ ...value, revision: 9 })
    const wrapper = mount(ModelCatalogDialog, { props: { show: true }, global: { stubs: { teleport: true } } }); await flushPromises()
    expect(api.catalog).toHaveBeenCalledTimes(1)
    expect(wrapper.findAll('input').map(input => (input.element as HTMLInputElement).value)).toContain('provider-a')
    await wrapper.find('input[maxlength="2000"]').setValue('verified alias')
    await wrapper.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.replaceCatalog).toHaveBeenCalledWith(value, 'verified alias')
    wrapper.unmount()
  })
})
