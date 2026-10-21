import { apiClient } from '../client'
export type RiskMode = 'off' | 'observe' | 'enforce'
export interface RiskConfig { mode: RiskMode; observation_started_at: string; scoring_config: Record<string, unknown>; revision: number; rule_version: number; updated_at: string }
export interface RiskCase { user_id: number; state: string; review_status: string; last_score: number | null; revision: number; updated_at: string; last_eligible_window_end: string | null }
export interface RiskRestriction { id: number; source: string; concurrency_cap: number; rpm_cap: number; starts_at: string; expires_at: string; revision: number }
export interface RiskExemption { id: number; user_id: number; starts_at: string; expires_at: string; revoked_at: string | null; reason: string; revision: number; created_by: number }
export interface RiskEvent { id: number; user_id: number | null; event_type: string; details: Record<string, unknown>; actor_id: number | null; created_at: string }
export interface RiskSignal { available: boolean; value: number | null; threshold: number | null; points: number; reason?: string }
export interface RiskWindow { window_start: string; window_end: string; score: number | null; status: string; workload_units: number; total_tokens: number; signals: Record<string, RiskSignal> }
export interface RiskOverview { as_of?: string; telemetry_status?: string; mode: RiskMode; state_counts: Record<string, number>; active_restrictions: number; active_exemptions: number; pending_reviews: number; last_processed_at: string | null }
export interface RiskDetail { case: RiskCase | null; active_restriction: RiskRestriction | null; active_exemption: RiskExemption | null }
export interface RiskList<T> { items: T[]; total: number; next_before_id?: number | null }
const path = '/admin/distillation-risk'
export const distillationRiskAPI = {
  overview: async (): Promise<RiskOverview> => (await apiClient.get(`${path}/overview`)).data,
  config: async (): Promise<RiskConfig> => (await apiClient.get(`${path}/config`)).data,
  saveConfig: async (value: RiskConfig): Promise<RiskConfig> => (await apiClient.put(`${path}/config`, { mode: value.mode, scoring_config: value.scoring_config, expected_revision: value.revision })).data,
  users: async (params: { page?: number; page_size: number; cursor?: boolean; before_id?: number; user_id?: number; state?: string }): Promise<RiskList<RiskCase>> => (await apiClient.get(`${path}/users`, { params })).data,
  detail: async (id: number): Promise<RiskDetail> => (await apiClient.get(`${path}/users/${id}`)).data,
  trends: async (id: number): Promise<RiskWindow[]> => (await apiClient.get(`${path}/users/${id}/trends`, { params: { limit: 100 } })).data,
  events: async (params: { page?: number; page_size: number; cursor?: boolean; before_id?: number; user_id?: number }): Promise<RiskList<RiskEvent>> => (await apiClient.get(`${path}/events`, { params })).data,
  exemptions: async (params: { page?: number; page_size: number; cursor?: boolean; before_id?: number; user_id?: number }): Promise<RiskList<RiskExemption>> => (await apiClient.get(`${path}/exemptions`, { params })).data,
  action: async (value: { user_id: number; type: string; expires_at?: string; expected_revision: number; reason: string; idempotency_key: string }): Promise<RiskDetail> => (await apiClient.post(`${path}/actions`, value)).data,
  exempt: async (value: { user_id: number; starts_at: string; expires_at: string; reason: string; idempotency_key: string }): Promise<RiskExemption> => (await apiClient.post(`${path}/exemptions`, value)).data,
  revokeExemption: async (value: { id: number; expected_revision: number; idempotency_key: string }): Promise<RiskExemption> => (await apiClient.delete(`${path}/exemptions`, { data: value })).data,
}
export default distillationRiskAPI
