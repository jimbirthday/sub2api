import { apiClient } from '../client'

export interface PromptInjectionRule {
  id: number
  name: string
  description?: string
  prompt_content: string
  enabled: boolean
  model_match_type: 'exact' | 'prefix'
  model_pattern: string
  group_mode: 'all' | 'include' | 'exclude'
  group_ids?: number[]
  endpoint: 'any' | 'chat_completions' | 'responses' | 'messages' | 'gemini'
  placement: 'system_prepend' | 'system_append'
  priority: number
  failure_policy: 'skip' | 'reject'
  version?: number
  updated_at?: string
}

export async function list(): Promise<PromptInjectionRule[]> {
  const { data } = await apiClient.get<{ rules: PromptInjectionRule[] }>('/admin/prompt-injection/rules')
  return data.rules || []
}

export async function save(rules: PromptInjectionRule[]): Promise<PromptInjectionRule[]> {
  const { data } = await apiClient.put<{ rules: PromptInjectionRule[] }>('/admin/prompt-injection/rules', { rules })
  return data.rules || []
}

export default { list, save }
