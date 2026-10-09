<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div><h1 class="text-2xl font-semibold">提示词注入</h1><p class="mt-1 text-sm text-gray-500">管理按模型、接口和分组生效的系统提示词规则。</p></div>
          <div class="flex gap-2"><button class="btn btn-secondary" :disabled="loading" @click="load">刷新</button><button class="btn btn-primary" @click="openCreate">新增规则</button></div>
        </div>
      </template>
      <template #table>
        <div class="table-wrapper"><table><thead><tr><th>状态</th><th>规则名称</th><th>模型</th><th>接口</th><th>分组</th><th>位置</th><th>优先级</th><th>操作</th></tr></thead><tbody>
          <tr v-for="rule in rules" :key="rule.id"><td><button class="badge" :class="rule.enabled ? 'badge-success' : 'badge-gray'" @click="toggleRule(rule)">{{ rule.enabled ? '启用' : '停用' }}</button></td><td><div class="font-medium">{{ rule.name }}</div><div class="max-w-xs truncate text-xs text-gray-500">{{ rule.prompt_content }}</div></td><td><span class="badge badge-gray">{{ rule.model_match_type === 'prefix' ? '前缀' : '精确' }}</span> {{ rule.model_pattern }}</td><td>{{ endpointLabel(rule.endpoint) }}</td><td>{{ groupLabel(rule) }}</td><td>{{ rule.placement === 'system_append' ? '后置' : '前置' }}</td><td>{{ rule.priority }}</td><td><div class="flex gap-1"><button class="btn btn-secondary btn-sm" @click="openEdit(rule)">编辑</button><button class="btn btn-danger btn-sm" @click="removeRule(rule)">删除</button></div></td></tr>
          <tr v-if="!loading && rules.length === 0"><td colspan="8" class="py-12 text-center text-gray-500">暂无提示词注入规则</td></tr><tr v-if="loading"><td colspan="8" class="py-12 text-center text-gray-500">加载中…</td></tr>
        </tbody></table></div>
      </template>
    </TablePageLayout>
    <BaseDialog :show="dialogOpen" :title="editing ? '编辑提示词规则' : '新增提示词规则'" width="wide" @close="dialogOpen = false">
      <form class="space-y-4" @submit.prevent="saveRule"><div class="grid gap-4 md:grid-cols-2">
        <label class="field">规则名称<input v-model.trim="form.name" class="input" required /></label>
        <label class="field">模型匹配<div class="flex gap-2"><select v-model="form.model_match_type" class="input w-28"><option value="exact">精确</option><option value="prefix">前缀</option></select><input v-model.trim="form.model_pattern" class="input flex-1" required placeholder="模型名称或前缀" /></div></label>
        <label class="field">接口<select v-model="form.endpoint" class="input"><option value="any">全部支持接口</option><option value="chat_completions">OpenAI Chat</option><option value="responses">OpenAI Responses</option><option value="messages">Anthropic Messages</option><option value="gemini">Gemini</option></select></label>
        <label class="field">分组范围<select v-model="form.group_mode" class="input"><option value="all">全部分组</option><option value="include">仅指定分组</option><option value="exclude">排除指定分组</option></select></label>
        <label class="field">注入位置<select v-model="form.placement" class="input"><option value="system_prepend">系统提示词前置</option><option value="system_append">系统提示词后置</option></select></label>
        <label class="field">优先级<input v-model.number="form.priority" type="number" class="input" /></label>
      </div><label v-if="form.group_mode !== 'all'" class="field">选择分组<select v-model="form.group_ids" multiple class="input min-h-24"><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label><label class="field">提示词内容<textarea v-model="form.prompt_content" rows="6" class="input" required /></label><label class="field">失败策略<select v-model="form.failure_policy" class="input"><option value="skip">跳过并继续请求</option><option value="reject">拒绝请求</option></select></label><label class="field">说明<textarea v-model="form.description" rows="2" class="input" /></label><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="dialogOpen = false">取消</button><button class="btn btn-primary" :disabled="saving">保存</button></div></form>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import type { PromptInjectionRule } from '@/api/admin/promptInjection'
import type { AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'

const appStore = useAppStore(); const rules = ref<PromptInjectionRule[]>([]); const groups = ref<AdminGroup[]>([]); const loading = ref(false); const saving = ref(false); const dialogOpen = ref(false); const editing = ref(false); const form = ref<PromptInjectionRule>(blankRule())
function blankRule(): PromptInjectionRule { return { id: Date.now(), name: '', prompt_content: '', enabled: true, model_match_type: 'exact', model_pattern: '', group_mode: 'all', group_ids: [], endpoint: 'any', placement: 'system_prepend', priority: 100, failure_policy: 'skip' } }
function openCreate() { editing.value = false; form.value = blankRule(); dialogOpen.value = true }
function openEdit(rule: PromptInjectionRule) { editing.value = true; form.value = { ...rule, group_ids: [...(rule.group_ids || [])] }; dialogOpen.value = true }
function endpointLabel(endpoint: PromptInjectionRule['endpoint']) { return ({ any: '全部', chat_completions: 'OpenAI Chat', responses: 'Responses', messages: 'Messages', gemini: 'Gemini' } as Record<string, string>)[endpoint] || endpoint }
function groupLabel(rule: PromptInjectionRule) { return rule.group_mode === 'all' ? '全部' : `${rule.group_mode === 'include' ? '指定' : '排除'} ${rule.group_ids?.length || 0} 个` }
async function load() { loading.value = true; try { const result = await Promise.all([adminAPI.promptInjection.list(), adminAPI.groups.getAllIncludingInactive()]); rules.value = result[0]; groups.value = result[1] } finally { loading.value = false } }
async function saveRule() { saving.value = true; try { const next = editing.value ? rules.value.map(r => r.id === form.value.id ? form.value : r) : [...rules.value, form.value]; rules.value = await adminAPI.promptInjection.save(next); dialogOpen.value = false; appStore.showSuccess('提示词注入规则已保存') } finally { saving.value = false } }
async function toggleRule(rule: PromptInjectionRule) { rule.enabled = !rule.enabled; rules.value = await adminAPI.promptInjection.save(rules.value) }
async function removeRule(rule: PromptInjectionRule) { if (!window.confirm(`确定删除规则“${rule.name}”吗？`)) return; rules.value = await adminAPI.promptInjection.save(rules.value.filter(r => r.id !== rule.id)) }
onMounted(load)
</script>

<style scoped>
.field { display: flex; flex-direction: column; gap: .45rem; font-size: .875rem; font-weight: 500; }.input { border: 1px solid rgb(209 213 219); border-radius: .5rem; padding: .55rem .7rem; background: transparent; font-weight: 400; }.btn { border-radius: .5rem; padding: .55rem .85rem; font-weight: 600; }.btn-primary { background: rgb(37 99 235); color: white; }.btn-secondary { border: 1px solid rgb(156 163 175); }.btn-danger { color: rgb(220 38 38); }.btn-sm { padding: .35rem .55rem; font-size: .75rem; }.badge { border-radius: 999px; padding: .2rem .55rem; font-size: .75rem; }.badge-success { background: rgb(220 252 231); color: rgb(22 101 52); }.badge-gray { background: rgb(229 231 235); color: rgb(75 85 99); }
</style>
