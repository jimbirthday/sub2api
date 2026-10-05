<template>
  <div class="space-y-6 p-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">提示词注入</h1>
        <p class="mt-1 text-sm text-gray-500">按模型、分组和接口把系统提示词加入上游请求。注入 token 按正常用户输入计费。</p>
      </div>
      <button class="btn-primary" :disabled="saving" @click="saveRules">{{ saving ? '保存中…' : '保存全部规则' }}</button>
    </div>

    <div v-for="(rule, index) in rules" :key="rule.id || index" class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-600 dark:bg-dark-800">
      <div class="mb-4 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <input v-model="rule.enabled" type="checkbox" class="h-4 w-4" />
          <input v-model.trim="rule.name" class="input" placeholder="规则名称" />
        </div>
        <button class="text-sm text-red-600" @click="removeRule(index)">删除</button>
      </div>
      <div class="grid gap-4 md:grid-cols-2">
        <label class="field">模型匹配
          <div class="flex gap-2"><select v-model="rule.model_match_type" class="input w-32"><option value="exact">精确</option><option value="prefix">前缀</option></select><input v-model.trim="rule.model_pattern" class="input flex-1" placeholder="例如 claude-3-5" /></div>
        </label>
        <label class="field">接口<select v-model="rule.endpoint" class="input"><option value="any">全部支持接口</option><option value="chat_completions">OpenAI Chat</option><option value="responses">OpenAI Responses</option><option value="messages">Anthropic Messages</option><option value="gemini">Gemini</option></select></label>
        <label class="field">分组范围<select v-model="rule.group_mode" class="input"><option value="all">全部分组</option><option value="include">仅指定分组</option><option value="exclude">排除指定分组</option></select></label>
        <label class="field">优先级<input v-model.number="rule.priority" type="number" class="input" /></label>
        <label class="field">注入位置<select v-model="rule.placement" class="input"><option value="system_prepend">系统提示词前置</option><option value="system_append">系统提示词后置</option></select></label>
        <label class="field">失败策略<select v-model="rule.failure_policy" class="input"><option value="skip">跳过并继续请求</option><option value="reject">拒绝请求</option></select></label>
      </div>
      <label v-if="rule.group_mode !== 'all'" class="field mt-4">选择分组
        <select v-model="rule.group_ids" multiple class="input min-h-24">
          <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option>
        </select>
      </label>
      <label class="field mt-4">提示词内容<textarea v-model="rule.prompt_content" rows="5" class="input" placeholder="要注入的系统提示词" /></label>
      <label class="field mt-4">说明<textarea v-model="rule.description" rows="2" class="input" placeholder="可选" /></label>
    </div>

    <div v-if="rules.length === 0" class="rounded-xl border border-dashed p-10 text-center text-gray-500">还没有规则</div>
    <button class="btn-secondary" @click="addRule">新增规则</button>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { adminAPI } from '@/api/admin'
import type { PromptInjectionRule } from '@/api/admin/promptInjection'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'

const appStore = useAppStore()
const rules = ref<PromptInjectionRule[]>([])
const groups = ref<AdminGroup[]>([])
const saving = ref(false)

const blankRule = (): PromptInjectionRule => ({ id: Date.now(), name: '新规则', prompt_content: '', enabled: true, model_match_type: 'exact', model_pattern: '', group_mode: 'all', group_ids: [], endpoint: 'any', placement: 'system_prepend', priority: 100, failure_policy: 'skip' })
function addRule() { rules.value.push(blankRule()) }
function removeRule(index: number) { rules.value.splice(index, 1) }
async function load() {
  const [loadedRules, loadedGroups] = await Promise.all([adminAPI.promptInjection.list(), adminAPI.groups.getAllIncludingInactive()])
  rules.value = loadedRules
  groups.value = loadedGroups
}
async function saveRules() {
  saving.value = true
  try { rules.value = await adminAPI.promptInjection.save(rules.value); appStore.showSuccess('提示词注入规则已保存') } finally { saving.value = false }
}
onMounted(load)
</script>

<style scoped>
.field { display: flex; flex-direction: column; gap: .45rem; font-size: .875rem; font-weight: 500; }
.input { border: 1px solid rgb(209 213 219); border-radius: .5rem; padding: .55rem .7rem; background: transparent; font-weight: 400; }
.btn-primary, .btn-secondary { border-radius: .5rem; padding: .6rem 1rem; font-weight: 600; }
.btn-primary { background: rgb(37 99 235); color: white; }
.btn-secondary { border: 1px solid rgb(156 163 175); }
</style>
