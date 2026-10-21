import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import { catalog, evaluate, get, replace, replaceCatalog, listCurrent } from '@/api/admin/modelRestrictions'
import risk from '@/api/admin/distillationRisk'
vi.mock('@/i18n', () => ({ getLocale: () => 'en' }))
const originalAdapter = apiClient.defaults.adapter
afterEach(() => { apiClient.defaults.adapter = originalAdapter })
describe('model access management API contracts', () => {
  it('unwraps real API envelopes and preserves contextual aliases, CAS revisions and idempotency headers', async () => {
    const snapshot = { user_id: 42, revision: 4, rules: [] }
    const value = { revision: 8, updated_at: '', updated_by: null, models: [], aliases: [{ namespace: 'openai', transport: 'openai', alias: 'shared', canonical_model: 'gpt-5', protocol: 'responses', role: 'tool', provider_context: 'provider-a' }] }
    const adapter = vi.fn(async config => ({ status: 200, statusText: 'OK', headers: {}, config, data: { code: 0, data: config.url?.endsWith('/catalog') ? value : snapshot } }))
    apiClient.defaults.adapter = adapter
    expect(await get(42)).toEqual(snapshot)
    expect(await catalog()).toEqual(value)
    await replaceCatalog(value, 'verified')
    let request = adapter.mock.calls.at(-1)![0]
    expect(JSON.parse(request.data)).toMatchObject({ expected_revision: 8, aliases: value.aliases })
    expect(request.headers.get('Idempotency-Key')).toBeTruthy()
    await replace(42, 4, [], 'reviewed')
    request = adapter.mock.calls.at(-1)![0]
    expect(JSON.parse(request.data)).toEqual({ expected_revision: 4, rules: [], reason: 'reviewed' })
    expect(request.headers.get('Idempotency-Key')).toBeTruthy()
    await evaluate(42, [{ ...value.aliases[0], model: 'shared' }])
    expect(JSON.parse(adapter.mock.calls.at(-1)![0].data).models[0]).toMatchObject({ protocol: 'responses', role: 'tool', provider_context: 'provider-a' })
  })
  it('unwraps cursor list, case and configuration responses through the shared client', async () => {
    const items = { items: [], total: 0, next_before_id: null }
    const detail = { case: null, active_restriction: null, active_exemption: null }
    const config = { mode: 'observe' as const, scoring_config: {}, revision: 3, rule_version: 1, updated_at: '', observation_started_at: '' }
    const adapter = vi.fn(async request => ({ status: 200, statusText: 'OK', headers: {}, config: request, data: { code: 0, data: request.url?.endsWith('/config') ? config : request.url?.endsWith('/42') ? detail : items } }))
    apiClient.defaults.adapter = adapter
    expect(await listCurrent({ limit: 20 })).toEqual(items)
    expect(await risk.users({ cursor: true, page_size: 20 })).toEqual(items)
    expect(await risk.detail(42)).toEqual(detail)
    expect(await risk.config()).toEqual(config)
    await risk.saveConfig(config)
    expect(JSON.parse(adapter.mock.calls.at(-1)![0].data)).toEqual({ mode: 'observe', scoring_config: {}, expected_revision: 3 })
  })
})
