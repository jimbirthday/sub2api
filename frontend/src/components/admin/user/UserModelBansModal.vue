<template>
  <BaseDialog :show="show" :title="t('admin.modelBans.title')" width="wide" :close-on-escape="!saving" :show-close-button="!saving" @close="close">
    <div class="space-y-5">
      <div class="rounded-xl bg-primary-50 p-4 dark:bg-primary-900/20">
        <p class="font-medium text-gray-900 dark:text-white">{{ user?.email }} <span class="text-gray-500">#{{ user?.id }}</span></p>
        <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.modelBans.crossGroup') }}</p>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <section v-if="conflictSnapshot" class="space-y-3 rounded-lg border border-amber-300 p-3 dark:border-amber-700">
        <p class="text-sm">{{ t('admin.modelBans.conflictReview') }}</p>
        <details><summary class="cursor-pointer text-sm">{{ t('admin.modelBans.compareVersions') }}</summary><div class="mt-2 grid gap-3 sm:grid-cols-2"><div><p class="input-label">{{ t('admin.modelBans.yourDraft') }}</p><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(rules, null, 2) }}</pre></div><div><p class="input-label">{{ t('admin.modelBans.latestSaved') }}</p><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(conflictSnapshot.rules, null, 2) }}</pre></div></div></details>
        <button class="btn btn-secondary btn-sm" :disabled="saving" @click="confirmReload = true">{{ t('admin.modelBans.reloadLatest') }}</button>
      </section>
      <div v-if="loading" role="status" class="py-8 text-center">{{ t('common.loading') }}</div>
      <template v-else-if="snapshot">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <span class="text-sm text-gray-500">{{ t('admin.modelBans.revision', { revision: snapshot.revision }) }}</span>
          <div class="flex gap-2">
            <button class="btn btn-secondary btn-sm" :disabled="saving" @click="loadHistory()">{{ t('admin.modelBans.history') }}</button>
            <button class="btn btn-secondary btn-sm" :disabled="saving" @click="addRule"><Icon name="plus" size="sm" class="mr-1" />{{ t('admin.modelBans.add') }}</button>
          </div>
        </div>
        <label class="block"><span class="input-label">{{ t('admin.modelBans.changeReason') }}</span><input v-model="changeReason" class="input" required maxlength="2000" :disabled="saving" /></label>
        <p v-if="!rules.length" class="py-8 text-center text-gray-500">{{ t('admin.modelBans.empty') }}</p>
        <section v-for="(rule, index) in rules" :key="index" class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block"><span class="input-label">{{ t('admin.modelBans.provider') }}</span>
              <input v-model="rule.namespace" class="input" list="restriction-providers" maxlength="64" :disabled="saving" required />
            </label>
            <label class="block"><span class="input-label">{{ t('admin.modelBans.model') }}</span>
              <input v-model="rule.canonical_model" :list="`restriction-models-${index}`" class="input font-mono" maxlength="255" :disabled="saving" required />
            </label>
            <label class="block"><span class="input-label">{{ t('admin.modelBans.action') }}</span><select v-model="rule.action" class="input" :disabled="saving"><option value="deny_model">{{ t('admin.modelBans.denyModel') }}</option><option value="cap_concurrency">{{ t('admin.modelBans.capConcurrency') }}</option><option value="cap_rpm">{{ t('admin.modelBans.capRPM') }}</option></select></label>
            <label v-if="rule.action && rule.action !== 'deny_model'" class="block"><span class="input-label">{{ t('admin.modelBans.limit') }}</span><input v-model.number="rule.limit_value" type="number" min="1" step="1" class="input" :disabled="saving" /></label>
            <label class="block"><span class="input-label">{{ t('admin.modelBans.starts') }}</span><input v-model="rule.localStart" type="datetime-local" class="input" :disabled="saving" /></label>
            <label class="block"><span class="input-label">{{ t('admin.modelBans.expires') }}</span>
              <input v-model="rule.localExpiry" type="datetime-local" class="input" :disabled="saving" />
              <span class="input-hint">{{ t('admin.modelBans.permanentHint') }}</span>
            </label>
            <label class="block"><span class="input-label">{{ t('admin.modelBans.reason') }}</span>
              <input v-model="rule.reason" class="input" maxlength="2000" required :disabled="saving" />
            </label>
          </div>
          <p v-if="modelAliases.some(alias => alias.namespace === rule.namespace && alias.canonical_model === rule.canonical_model)" class="mt-3 break-all text-xs text-gray-500">{{ t('admin.modelBans.aliases') }}: {{ modelAliases.filter(alias => alias.namespace === rule.namespace && alias.canonical_model === rule.canonical_model).map(alias => alias.alias).join(', ') }}</p>
          <datalist :id="`restriction-models-${index}`"><option v-for="model in modelCandidates.filter(item => item.namespace === rule.namespace)" :key="model.canonical_model" :value="model.canonical_model">{{ model.display_name }}</option></datalist>
          <div class="mt-4 flex items-center justify-between">
            <label class="flex items-center gap-2 text-sm"><input v-model="rule.enabled" type="checkbox" :disabled="saving" />{{ t('admin.modelBans.enabled') }}</label>
            <button class="btn btn-secondary btn-sm text-red-600" :disabled="saving" @click="rules.splice(index, 1)">{{ t('common.delete') }}</button>
          </div>
        </section>
        <datalist id="restriction-providers"><option v-for="provider in providers" :key="provider" :value="provider" /></datalist>
        <p class="input-hint">{{ t('admin.modelBans.exactHint') }}</p>
        <form class="space-y-3 border-t pt-4 dark:border-dark-600" @submit.prevent="evaluatePolicy">
          <div class="flex flex-wrap items-end gap-3">
            <label class="min-w-0 flex-1"><span class="input-label">{{ t('admin.modelBans.evaluate') }}</span><input v-model="evaluationModel" class="input" maxlength="255" :disabled="evaluating" required /></label>
            <button class="btn btn-secondary" :disabled="evaluating || !evaluationModel.trim()">{{ evaluating ? t('common.loading') : t('admin.modelBans.evaluate') }}</button>
          </div>
          <details><summary class="cursor-pointer text-sm text-gray-600 dark:text-gray-300">{{ t('admin.modelBans.evaluationContext') }}</summary>
            <div class="mt-3 grid gap-3 sm:grid-cols-2">
              <label v-for="field in evaluationFields" :key="field.key" class="block"><span class="input-label">{{ t(field.label) }}</span><input v-model="evaluationContext[field.key]" class="input" :maxlength="field.max" :disabled="evaluating" /></label>
            </div>
          </details>
          <p v-if="evaluation" class="text-sm" role="status">{{ evaluation }}</p>
        </form>
        <div v-if="historyOpen" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <h4 class="mb-3 font-medium">{{ t('admin.modelBans.history') }}</h4>
          <ul class="space-y-2 text-sm">
            <li v-for="item in historyItems" :key="item.revision" class="flex flex-wrap items-center justify-between gap-2">
              <span>#{{ item.revision }} · {{ new Date(item.created_at).toLocaleString() }} · {{ t('admin.modelBans.ruleCount', { count: item.rule_count }) }} · #{{ item.created_by ?? '—' }}</span>
              <button class="btn btn-secondary btn-sm" :disabled="saving" @click="previewVersion(item.revision)">{{ t('admin.modelBans.viewVersion') }}</button>
            </li>
          </ul>
          <button v-if="historyCursor" class="btn btn-secondary btn-sm mt-3" :disabled="historyLoading" @click="loadHistory(true)">{{ t('admin.modelBans.more') }}</button>
          <pre v-if="historyPreview" class="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ historyPreview }}</pre>
        </div>
      </template>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-between gap-3">
        <button class="btn btn-secondary text-red-600" :disabled="saving || loading || !snapshot?.rules.some(rule => rule.source !== 'behavior')" @click="confirmClear = true">{{ t('admin.modelBans.clear') }}</button>
        <div class="flex gap-3">
          <button class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="saving || loading || !snapshot || !!conflictSnapshot" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
      </div>
    </template>
  </BaseDialog>
  <ConfirmDialog :show="confirmReload" :title="t('admin.modelBans.reloadLatest')" :message="t('admin.modelBans.discardDraftConfirm')" danger @cancel="confirmReload = false" @confirm="reloadLatest" />
  <ConfirmDialog :show="confirmClear" :title="t('admin.modelBans.clear')" :message="t('admin.modelBans.clearConfirm')" danger @cancel="confirmClear = false" @confirm="clearPolicy" />
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import modelRestrictionsAPI, { catalog, type CatalogModel, type CatalogAlias, evaluate, type ModelRestrictionRule, type ModelRestrictionSnapshot, type ModelRestrictionHistory } from '@/api/admin/modelRestrictions'

const props = defineProps<{ show: boolean; user: { id: number; email: string } | null }>()
const emit = defineEmits<{ close: []; success: [] }>()
const { t } = useI18n()
const app = useAppStore()
const modelCandidates = ref<CatalogModel[]>([])
const modelAliases = ref<CatalogAlias[]>([])
const evaluationModel = ref(''), evaluation = ref(''), evaluating = ref(false)
const evaluationContext = ref({ namespace: '', transport: '', protocol: '', role: 'primary', provider_context: '' })
const evaluationFields = [
  { key: 'namespace', label: 'admin.modelBans.provider', max: 64 },
  { key: 'transport', label: 'admin.modelBans.transport', max: 64 },
  { key: 'protocol', label: 'admin.modelBans.protocol', max: 64 },
  { key: 'role', label: 'admin.modelBans.role', max: 32 },
  { key: 'provider_context', label: 'admin.modelBans.providerContext', max: 128 }
] as const
const providers = ['anthropic', 'openai', 'google', 'xai', 'deepseek']
type EditRule = ModelRestrictionRule & { localExpiry: string; localStart: string }
const snapshot = ref<ModelRestrictionSnapshot | null>(null)
const rules = ref<EditRule[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref(''), changeReason = ref('')
const confirmClear = ref(false), confirmReload = ref(false), conflictSnapshot = ref<ModelRestrictionSnapshot | null>(null)
const historyOpen = ref(false)
const historyLoading = ref(false)
const historyItems = ref<ModelRestrictionHistory['items']>([])
const historyCursor = ref<number | null>(null)
const historyPreview = ref('')
let generation = 0, previewGeneration = 0
function toLocal(value: string | null) {
  if (!value) return ''
  const date = new Date(value)
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
function hydrate(value: ModelRestrictionSnapshot) {
  snapshot.value = value
  changeReason.value = value.rules.find(rule => rule.reason.trim())?.reason ?? ''
  rules.value = value.rules.filter(rule => rule.source !== 'behavior').map(rule => ({ ...rule, localExpiry: toLocal(rule.expires_at), localStart: toLocal(rule.starts_at ?? null) }))
}
function message(err: unknown) {
  const status = (err as { response?: { status?: number }; status?: number })?.response?.status ?? (err as { status?: number })?.status
  return status === 409 ? t('admin.modelBans.conflict') : t('admin.modelBans.failed')
}
watch(() => [props.show, props.user?.id] as const, async ([show, id]) => {
  const request = ++generation
  conflictSnapshot.value = null; confirmReload.value = false
  error.value = ''; changeReason.value = ''; evaluation.value = ''; evaluationContext.value = { namespace: '', transport: '', protocol: '', role: 'primary', provider_context: '' }; historyLoading.value = false; historyCursor.value = null; snapshot.value = null; historyOpen.value = false; historyItems.value = []; historyPreview.value = ''
  if (!show || !id) return
  loading.value = true
  catalog().then(result => { if (request === generation) { modelCandidates.value = result.models; modelAliases.value = result.aliases ?? [] } }).catch(() => { if (request === generation) modelCandidates.value = [] })
  try { const result = await modelRestrictionsAPI.get(id); if (request === generation) hydrate(result) }
  catch (err) { if (request === generation) error.value = message(err) }
  finally { if (request === generation) loading.value = false }
}, { immediate: true })
function addRule() { rules.value.push({ namespace: 'openai', canonical_model: '', enabled: true, expires_at: null, reason: '', localExpiry: '', localStart: '', action: 'deny_model', limit_value: null }) }
function close() { if (!saving.value) emit('close') }
async function save() {
  if (!props.user || !snapshot.value || saving.value) return
  if (!changeReason.value.trim()) { error.value = t('admin.modelBans.reasonRequired'); return }
  const values: ModelRestrictionRule[] = []
  const seen = new Set<string>()
  for (const rule of rules.value) {
    const namespace = rule.namespace.trim().toLowerCase(), canonical = rule.canonical_model.trim().toLowerCase()
    const key = `${namespace}\u0000${canonical}\u0000${rule.action ?? "deny_model"}`
    if (!rule.reason.trim() || !namespace || !canonical || seen.has(key) || namespace.includes('*') || canonical.includes('*')) { error.value = t('admin.modelBans.invalid'); return }
    seen.add(key)
    const date = rule.localExpiry ? new Date(rule.localExpiry) : null
    if (date && !Number.isFinite(date.getTime())) { error.value = t('admin.modelBans.invalid'); return }
    const start = rule.localStart ? new Date(rule.localStart) : null
    if (start && (!Number.isFinite(start.getTime()) || date && date <= start)) { error.value = t('admin.modelBans.invalid'); return }
    const action = rule.action ?? 'deny_model', limit = action === 'deny_model' ? null : rule.limit_value
    if (action !== 'deny_model' && (!Number.isSafeInteger(limit) || !limit || limit <= 0)) { error.value = t('admin.modelBans.invalid'); return }
    values.push({ namespace, canonical_model: canonical, enabled: rule.enabled, expires_at: date?.toISOString() ?? null, reason: rule.reason, ...(rule.rule_id ? { rule_id: rule.rule_id } : {}), ...(rule.action ? { action, limit_value: limit } : {}), ...(start ? { starts_at: start.toISOString() } : {}) })
  }
  const request = generation, userID = props.user.id, revision = snapshot.value.revision
  saving.value = true; error.value = ''
  try { const result = await modelRestrictionsAPI.replace(userID, revision, values, changeReason.value.trim()); if (request !== generation || props.user?.id !== userID) return; hydrate(result); app.showSuccess(t('admin.modelBans.saved')); emit('success') }
  catch (err) { if (request === generation) { error.value = message(err); if (isConflict(err)) await loadConflictingSnapshot(userID, request) } }
  finally { saving.value = false }
}
async function clearPolicy() {
  confirmClear.value = false
  if (!props.user || !snapshot.value || saving.value) return
  if (!changeReason.value.trim()) { error.value = t('admin.modelBans.reasonRequired'); return }
  const request = generation, userID = props.user.id, revision = snapshot.value.revision
  saving.value = true
  try { const result = await modelRestrictionsAPI.clear(userID, revision, changeReason.value.trim()); if (request !== generation || props.user?.id !== userID) return; hydrate(result); app.showSuccess(t('admin.modelBans.saved')); emit('success') }
  catch (err) { if (request === generation) { error.value = message(err); if (isConflict(err)) await loadConflictingSnapshot(userID, request) } }
  finally { saving.value = false }
}
function isConflict(err: unknown) { return ((err as { response?: { status?: number }; status?: number })?.response?.status ?? (err as { status?: number })?.status) === 409 }
async function loadConflictingSnapshot(userID: number, request: number) {
  try { const latest = await modelRestrictionsAPI.get(userID); if (request === generation) conflictSnapshot.value = latest } catch { /* Retain the draft and conflict message when reload fails. */ }
}
function reloadLatest() {
  confirmReload.value = false
  if (!conflictSnapshot.value) return
  hydrate(conflictSnapshot.value); conflictSnapshot.value = null; error.value = ''; changeReason.value = ''
}
async function loadHistory(more = false) {
  if (!props.user || historyLoading.value) return
  const request = generation, userID = props.user.id
  historyOpen.value = true; historyLoading.value = true
  try { const result = await modelRestrictionsAPI.history(userID, more ? historyCursor.value ?? undefined : undefined); if (request !== generation || props.user?.id !== userID) return; historyItems.value = more ? [...historyItems.value, ...result.items] : result.items; historyCursor.value = result.next_before_revision }
  catch (err) { if (request === generation) error.value = message(err) }
  finally { if (request === generation) historyLoading.value = false }
}
async function evaluatePolicy() {
  if (!props.user || evaluating.value) return
  const request = generation, userID = props.user.id
  evaluating.value = true; evaluation.value = ''
  try { const result = await evaluate(userID, [{ ...evaluationContext.value, model: evaluationModel.value.trim() }]); if (request === generation) evaluation.value = result.status === 'allowed' ? t('admin.modelBans.allowed') : result.status === 'denied' ? t('admin.modelBans.denied') : t('admin.modelBans.unresolved') }
  catch (err) { if (request === generation) error.value = message(err) }
  finally { evaluating.value = false }
}
async function previewVersion(revision: number) {
  if (!props.user) return
  const request = generation, preview = ++previewGeneration, userID = props.user.id
  try { const result = await modelRestrictionsAPI.version(userID, revision); if (request === generation && preview === previewGeneration && props.user?.id === userID) historyPreview.value = JSON.stringify(result.rules, null, 2) }
  catch (err) { if (request === generation) error.value = message(err) }
}
</script>
