import { apiClient } from './client'
import type { SubscriptionPlan } from '@/types/payment'

export interface SharedPlan {
  id: number
  version: number
  name: string
  description: string
  price: number
  validity_days: number
  group_ids: number[]
  group_names?: Record<string, string>
  daily_limit_usd: number | null
  weekly_limit_usd: number | null
  monthly_limit_usd: number | null
  for_sale: boolean
}
export interface SharedSubscription {
  id: number
  user_id: number
  user_email?: string
  username?: string
  generation: number
	quota_overrides: Record<string, number | null>
  plan_id: number
  plan: SharedPlan
  starts_at: string
  expires_at: string
  status: string
  windows: { kind: string; starts_at: string; resets_at: string; limit: number | null; used: number; reserved: number }[]
}
export interface SharedSettlement {
  request_id: string
  group_id: number
  cost: number
  subscription_cost: number
  balance_cost: number
  billing_at: string
}
export const sharedSubscriptionsAPI = {
  plans: async (admin = false) => (await apiClient.get<SharedPlan[]>(`${admin ? '/admin' : ''}/shared-subscriptions/plans`)).data ?? [],
  subscriptions: async () => (await apiClient.get<SharedSubscription[]>('/shared-subscriptions')).data ?? [],
  adminPage: async (params: { user_id?: number; plan_id?: number; status?: string; before_id?: number; search?: string; page?: number; page_size?: number; sort_by?: string; sort_order?: string }, signal?: AbortSignal) =>
    (await apiClient.get<{ items: SharedSubscription[]; has_more: boolean; total: number; page: number; page_size: number }>('/admin/shared-subscriptions', { params, signal })).data,
  changePlan: async (id: number, plan_id: number, generation: number) => apiClient.put(`/admin/shared-subscriptions/${id}/plan`, { plan_id, generation }),
  history: async (admin = false, userId?: number) => (await apiClient.get<SharedSettlement[]>(`${admin ? '/admin' : ''}/shared-subscriptions/history`, { params: { user_id: userId } })).data ?? [],
  save: async (plan: SharedPlan) => (await (plan.id ? apiClient.put(`/admin/shared-subscriptions/plans/${plan.id}`, plan) : apiClient.post('/admin/shared-subscriptions/plans', plan))).data,
  remove: async (id: number) => apiClient.delete(`/admin/shared-subscriptions/plans/${id}`),
  assign: async (user_id: number, plan_id: number, request_id: string) => apiClient.post('/admin/shared-subscriptions/assign', { user_id, plan_id, request_id }),
  action: async (id: number, action: string, days?: number) => apiClient.post(`/admin/shared-subscriptions/${id}/action`, { action, days }),
  updateQuota: async (id: number, payload: {
    generation: number
    limits: Record<'daily' | 'weekly' | 'monthly', { mode: 'inherit' | 'custom' | 'unlimited'; value?: number }>
    used: Record<'daily' | 'weekly' | 'monthly', number>
    reason: string
  }) => apiClient.put(`/admin/shared-subscriptions/${id}/quota`, payload),
}
export function sharedCheckoutPlan(p: SharedPlan): SubscriptionPlan {
  return { ...p, shared: true, group_id: 0, validity_unit: 'day', features: [], sort_order: 0 }
}

// UUIDs may be unavailable on HTTP deployments; getRandomValues also works there.
export function sharedGrantRequestID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
}
