import { apiClient } from '../client'

export interface ModelRestrictionRule {
  rule_id?: string
  action?: 'deny_model' | 'cap_concurrency' | 'cap_rpm'
  limit_value?: number | null
  starts_at?: string | null
  source?: string
  namespace: string
  canonical_model: string
  enabled: boolean
  expires_at: string | null
  reason: string
}
export interface ModelRestrictionSnapshot {
  user_id: number
  revision: number
  updated_at: string
  updated_by: number | null
  rules: ModelRestrictionRule[]
}
export interface ModelRestrictionHistory {
  items: { revision: number; created_at: string; created_by: number | null; rule_count: number }[]
  next_before_revision: number | null
}
const path = (userID: number) => `/admin/users/${userID}/model-restrictions`
export async function get(userID: number): Promise<ModelRestrictionSnapshot> {
  return (await apiClient.get(path(userID))).data
}
export async function replace(userID: number, revision: number, rules: ModelRestrictionRule[], reason = rules[0]?.reason ?? ""): Promise<ModelRestrictionSnapshot> {
  return (await apiClient.put(path(userID), { expected_revision: revision, rules, reason }, { headers: { 'Idempotency-Key': crypto.randomUUID() } })).data
}
export async function clear(userID: number, revision: number, reason: string): Promise<ModelRestrictionSnapshot> {
  return (await apiClient.delete(path(userID), { data: { expected_revision: revision, reason }, headers: { 'Idempotency-Key': crypto.randomUUID() } })).data
}
export async function history(userID: number, before?: number): Promise<ModelRestrictionHistory> {
  return (await apiClient.get(`${path(userID)}/history`, { params: { limit: 20, before_revision: before } })).data
}
export async function version(userID: number, revision: number): Promise<ModelRestrictionSnapshot> {
  return (await apiClient.get(`${path(userID)}/history/${revision}`)).data
}
export const modelRestrictionsAPI = { get, replace, clear, history, version }
export default modelRestrictionsAPI

export interface CatalogModel { namespace: string; canonical_model: string; display_name: string; source: string }
export interface CatalogAlias { alias_id?: string; protocol?: string; role?: string; provider_context?: string; namespace: string; transport: string; alias: string; canonical_model: string }
export interface ModelCatalog { revision: number; updated_at: string; updated_by: number | null; models: CatalogModel[]; aliases: CatalogAlias[] }
export async function catalog(): Promise<ModelCatalog> { return (await apiClient.get('/admin/model-restrictions/catalog')).data }
export async function replaceCatalog(value: ModelCatalog, reason: string): Promise<ModelCatalog> {
  return (await apiClient.put('/admin/model-restrictions/catalog', { expected_revision: value.revision, models: value.models, aliases: value.aliases, reason }, { headers: { 'Idempotency-Key': crypto.randomUUID() } })).data
}

export interface ModelRestrictionListItem extends ModelRestrictionRule { id: number; user_id: number; user_email: string; revision: number; active_now: boolean; created_by: number | null }
export async function listCurrent(params: { limit?: number; before_id?: number; user_id?: number; search?: string; model?: string; state?: string; source?: string }): Promise<{ items: ModelRestrictionListItem[]; next_before_id: number | null }> {
  return (await apiClient.get('/admin/model-restrictions', { params })).data
}
export interface ModelEvaluationInput { namespace: string; transport: string; model: string; protocol?: string; role?: string; provider_context?: string }
export async function evaluate(userID: number, models: ModelEvaluationInput[]): Promise<{ status: 'allowed' | 'denied' | 'unresolved'; decision: unknown }> {
  return (await apiClient.post(`${path(userID)}/evaluate`, { models })).data
}
