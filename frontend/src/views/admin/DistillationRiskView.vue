<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.distillationRisk.title') }}</h1><p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.distillationRisk.description') }}</p></div>
        <div class="flex gap-2"><button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="md" class="mr-2" />{{ t('common.refresh') }}</button><button class="btn btn-primary" :disabled="!config" @click="openConfig">{{ t('admin.distillationRisk.configuration') }}</button></div>
      </header>
      <p v-if="stale" role="status" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ t('admin.distillationRisk.stale') }}</p>
      <p v-if="overview?.telemetry_status && overview.telemetry_status !== 'healthy'" role="status" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.distillationRisk.telemetryDegraded') }}</p>
      <p v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div class="card p-5"><p class="text-sm text-gray-500">{{ t('admin.distillationRisk.mode') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview ? modeLabel(overview.mode) : '—' }}</p></div>
        <div class="card p-5"><p class="text-sm text-gray-500">{{ t('admin.distillationRisk.pendingReviews') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview?.pending_reviews ?? '—' }}</p></div>
        <div class="card p-5"><p class="text-sm text-gray-500">{{ t('admin.distillationRisk.activeRestrictions') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview?.active_restrictions ?? '—' }}</p></div>
        <div class="card p-5"><p class="text-sm text-gray-500">{{ t('admin.distillationRisk.lastProcessed') }}</p><p class="mt-2 text-sm font-medium">{{ formatDate(overview?.last_processed_at) }}</p></div>
      </div>
      <div class="rounded-xl border border-primary-200 bg-primary-50 p-4 text-sm text-primary-900 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('admin.distillationRisk.evidenceHint') }}</div>
      <div class="flex flex-wrap items-end gap-3">
        <label class="w-full sm:w-48"><span class="input-label">{{ t('admin.distillationRisk.userID') }}</span><input v-model="userFilter" type="number" min="1" step="1" class="input" /></label>
        <label class="w-full sm:w-48"><span class="input-label">{{ t('common.status') }}</span><select v-model="stateFilter" class="input"><option value="">{{ t('admin.distillationRisk.allStates') }}</option><option v-for="state in states" :key="state" :value="state">{{ stateLabel(state) }}</option></select></label>
        <button class="btn btn-secondary" :disabled="loading" @click="resetList()">{{ t('common.search') }}</button>
        <div class="ml-auto flex flex-wrap gap-2" role="group" :aria-label="t('admin.distillationRisk.sections')"><button v-for="tab in tabs" :key="tab" class="btn" :class="activeTab === tab ? 'btn-primary' : 'btn-secondary'" :aria-pressed="activeTab === tab" @click="activeTab = tab; resetList()">{{ tabLabel(tab) }}</button></div>
      </div>
      <div class="card overflow-hidden">
        <DataTable v-if="activeTab === 'users'" :columns="userColumns" :data="users" :loading="listLoading">
          <template #cell-user_id="{ row }"><span class="font-mono">#{{ row.user_id }}</span></template>
          <template #cell-state="{ row }"><span class="badge" :class="row.state === 'limited' ? 'badge-danger' : 'badge-gray'">{{ stateLabel(row.state) }}</span></template>
          <template #cell-last_score="{ row }"><span class="font-mono">{{ row.last_score ?? '—' }}</span></template>
          <template #cell-updated_at="{ row }">{{ formatDate(row.updated_at) }}</template>
          <template #cell-actions="{ row }"><button class="btn btn-secondary btn-sm" @click="openDetail(row.user_id)">{{ t('admin.distillationRisk.review') }}</button></template>
        </DataTable>
        <DataTable v-else-if="activeTab === 'events'" :columns="eventColumns" :data="events" :loading="listLoading">
          <template #cell-created_at="{ row }">{{ formatDate(row.created_at) }}</template>
          <template #cell-details="{ row }"><details><summary class="cursor-pointer text-primary-600">{{ t('admin.distillationRisk.evidence') }}</summary><pre class="max-w-xl overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(row.details, null, 2) }}</pre></details></template>
        </DataTable>
        <DataTable v-else :columns="exemptionColumns" :data="exemptions" :loading="listLoading">
          <template #cell-expires_at="{ row }">{{ formatDate(row.expires_at) }}</template>
          <template #cell-actions="{ row }"><button v-if="!row.revoked_at && new Date(row.expires_at).getTime() > Date.now()" class="btn btn-secondary btn-sm" :disabled="saving" @click="revokeExemption(row)">{{ t('admin.distillationRisk.revokeExemption') }}</button></template>
        </DataTable>
      </div>
      <CursorPagination :has-previous="cursorHistory.length > 0" :has-next="nextCursor !== null" :loading="listLoading" @previous="previousPage" @next="nextPage" />
      <p v-if="loadedAt" class="text-xs text-gray-500">{{ t('admin.distillationRisk.snapshotAt', { time: formatDate(loadedAt) }) }}</p>
    </div>
    <BaseDialog :show="configOpen" :title="t('admin.distillationRisk.configuration')" :close-on-escape="!saving" :show-close-button="!saving" @close="configOpen = false">
      <div class="space-y-4">
        <p class="text-sm text-gray-500">{{ t('admin.distillationRisk.configHint') }}</p>
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
        <section v-if="configConflict" class="space-y-3 rounded-lg border border-amber-300 p-3 dark:border-amber-700">
          <p class="text-sm">{{ t('admin.modelBans.conflictReview') }}</p>
          <details><summary class="cursor-pointer text-sm">{{ t('admin.modelBans.compareVersions') }}</summary><div class="mt-2 grid gap-3 sm:grid-cols-2"><div><p class="input-label">{{ t('admin.modelBans.yourDraft') }}</p><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify({ mode: draftMode, scoring_config: draftScoring }, null, 2) }}</pre></div><div><p class="input-label">{{ t('admin.modelBans.latestSaved') }}</p><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(configConflict, null, 2) }}</pre></div></div></details>
          <button class="btn btn-secondary btn-sm" :disabled="saving" @click="confirmConfigReload = true">{{ t('admin.modelBans.reloadLatest') }}</button>
        </section>
        <label class="block"><span class="input-label">{{ t('admin.distillationRisk.mode') }}</span><select v-model="draftMode" class="input" :disabled="saving"><option v-for="mode in modes" :key="mode" :value="mode">{{ modeLabel(mode) }}</option></select></label>
        <label v-for="field in scoringFields" :key="field.key" class="block"><span class="input-label">{{ t(field.label) }}</span><input v-model.number="draftScoring[field.key]" type="number" :min="field.min" :max="field.max" step="0.01" class="input" :disabled="saving" /></label>
        <p class="input-hint">{{ t('admin.distillationRisk.observingSince', { time: formatDate(configBase?.observation_started_at) }) }}</p>
      </div>
      <template #footer><button class="btn btn-secondary" :disabled="saving" @click="configOpen = false">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !!configConflict" @click="saveConfig">{{ t('common.save') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="detailOpen" :title="t('admin.distillationRisk.userDetail', { id: selectedID })" width="wide" :close-on-escape="!saving" :show-close-button="!saving" @close="detailOpen = false">
      <div class="space-y-5">
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
        <section v-if="detailConflict" class="space-y-3 rounded-lg border border-amber-300 p-3 dark:border-amber-700">
          <p class="text-sm">{{ t('admin.modelBans.conflictReview') }}</p>
          <details><summary class="cursor-pointer text-sm">{{ t('admin.modelBans.compareVersions') }}</summary><pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{{ JSON.stringify(detailConflict, null, 2) }}</pre></details>
          <button class="btn btn-secondary btn-sm" :disabled="saving" @click="confirmDetailReload = true">{{ t('admin.modelBans.reloadLatest') }}</button>
        </section>
        <p v-if="detailLoading" role="status">{{ t('common.loading') }}</p>
        <template v-else-if="detail">
          <div class="flex flex-wrap gap-3"><span class="badge badge-gray">{{ stateLabel(detail.case?.state ?? 'clear') }}</span><span class="text-sm text-gray-500">{{ t('admin.distillationRisk.score') }}: {{ detail.case?.last_score ?? '—' }}</span><span class="text-sm text-gray-500">{{ t('admin.distillationRisk.reviewStatus') }}: {{ detail.case?.review_status ?? '—' }}</span></div>
          <div v-if="detail.active_restriction" class="rounded-lg border border-amber-200 p-4 text-sm dark:border-amber-700">{{ t('admin.distillationRisk.limitSummary', { concurrency: detail.active_restriction.concurrency_cap, rpm: detail.active_restriction.rpm_cap, until: formatDate(detail.active_restriction.expires_at) }) }}</div>
          <label class="block"><span class="input-label">{{ t('admin.distillationRisk.restrictionUntil') }}</span><input v-model="restrictionUntil" type="datetime-local" class="input" :disabled="saving" /><span class="input-hint">{{ t('admin.distillationRisk.restrictionHint') }}</span></label>
          <label class="block"><span class="input-label">{{ t('admin.distillationRisk.reason') }}</span><textarea v-model="actionReason" class="input" rows="2" maxlength="2000" :disabled="saving" /></label>
          <div class="flex flex-wrap gap-2">
            <button v-for="action in actions" :key="action" class="btn btn-secondary" :disabled="saving || !detail.case || !!detailConflict" @click="pendingAction = action">{{ actionLabel(action) }}</button>
            <button class="btn btn-secondary" :disabled="saving" @click="exemptionOpen = !exemptionOpen">{{ t('admin.distillationRisk.createExemption') }}</button>
          </div>
          <div v-if="exemptionOpen" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600"><label class="block"><span class="input-label">{{ t('admin.distillationRisk.exemptionUntil') }}</span><input v-model="exemptionUntil" type="datetime-local" class="input" /></label><p class="input-hint">{{ t('admin.distillationRisk.exemptionHint') }}</p><button class="btn btn-primary" :disabled="saving" @click="createExemption">{{ t('common.save') }}</button></div>
          <h4 class="font-medium">{{ t('admin.distillationRisk.windows') }}</h4>
          <p v-if="!windows.length" class="text-sm text-gray-500">{{ t('admin.distillationRisk.noWindows') }}</p>
          <section v-for="window in windows" :key="window.window_start" class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <div class="flex flex-wrap justify-between gap-2 text-sm"><span>{{ formatDate(window.window_start) }}</span><strong>{{ t('admin.distillationRisk.score') }}: {{ window.score ?? '—' }}</strong></div>
            <p class="my-2 text-xs text-gray-500">{{ window.status }} · {{ window.workload_units }} {{ t('admin.distillationRisk.units') }} · {{ window.total_tokens.toLocaleString() }} tokens</p>
            <dl class="grid gap-2 text-sm sm:grid-cols-2"><div v-for="(signal, name) in window.signals" :key="name" class="rounded bg-gray-50 p-2 dark:bg-dark-900"><dt class="text-xs text-gray-500">{{ signalLabel(String(name)) }}</dt><dd>{{ signal.available ? `${signal.value?.toFixed(2) ?? '—'} · ${signal.points}` : t('admin.distillationRisk.unavailable') }}</dd></div></dl>
          </section>
        </template>
      </div>
    </BaseDialog>
    <ConfirmDialog :show="confirmDetailReload" :title="t('admin.modelBans.reloadLatest')" :message="t('admin.modelBans.discardDraftConfirm')" danger @cancel="confirmDetailReload = false" @confirm="reloadDetail" />
    <ConfirmDialog :show="confirmConfigReload" :title="t('admin.modelBans.reloadLatest')" :message="t('admin.modelBans.discardDraftConfirm')" danger @cancel="confirmConfigReload = false" @confirm="reloadConfig" />
    <ConfirmDialog :show="!!pendingAction" :title="actionLabel(pendingAction)" :message="t('admin.distillationRisk.actionConfirm')" danger @cancel="pendingAction = ''" @confirm="applyAction" />
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import CursorPagination from '@/components/common/CursorPagination.vue'
import Icon from '@/components/icons/Icon.vue'
import api, { type RiskCase, type RiskConfig, type RiskDetail, type RiskEvent, type RiskExemption, type RiskMode, type RiskOverview, type RiskWindow } from '@/api/admin/distillationRisk'
import { useAppStore } from '@/stores/app'
const { t } = useI18n(), app = useAppStore()
const overview = ref<RiskOverview | null>(null), config = ref<RiskConfig | null>(null), users = ref<RiskCase[]>([]), events = ref<RiskEvent[]>([]), exemptions = ref<RiskExemption[]>([])
const loading = ref(false), listLoading = ref(false), saving = ref(false), error = ref(''), dialogError = ref(''), loadedAt = ref(''), stale = ref(false)
const cursor = ref<number | undefined>(), nextCursor = ref<number | null>(null), cursorHistory = ref<(number | undefined)[]>([])
const userFilter = ref(''), stateFilter = ref(''), tabs = ['users', 'events', 'exemptions'] as const, activeTab = ref<typeof tabs[number]>('users')
const states = ['clear', 'observing', 'suspected', 'limited'], modes: RiskMode[] = ['off', 'observe', 'enforce'], actions = ['in_review', 'confirm', 'dismiss', 'restrict', 'revoke']
const detailConflict = ref<RiskDetail | null>(null), confirmDetailReload = ref(false)
const configBase = ref<RiskConfig | null>(null), configConflict = ref<RiskConfig | null>(null), confirmConfigReload = ref(false)
const configOpen = ref(false), draftMode = ref<RiskMode>('observe'), detailOpen = ref(false), detailLoading = ref(false), selectedID = ref(0), detail = ref<RiskDetail | null>(null), windows = ref<RiskWindow[]>([]), actionReason = ref(''), pendingAction = ref(''), exemptionOpen = ref(false), exemptionUntil = ref(''), restrictionUntil = ref('')
// Explicit dictionaries keep every rendered key visible to locale completeness checks.
const modeLabels = computed(() => ({ off: t('admin.distillationRisk.modeOff'), observe: t('admin.distillationRisk.modeObserve'), enforce: t('admin.distillationRisk.modeEnforce') }))
const stateLabels = computed(() => ({ clear: t('admin.distillationRisk.stateClear'), observing: t('admin.distillationRisk.stateObserving'), suspected: t('admin.distillationRisk.stateSuspected'), limited: t('admin.distillationRisk.stateLimited') }))
const actionLabels = computed(() => ({ in_review: t('admin.distillationRisk.actionReview'), confirm: t('admin.distillationRisk.actionConfirmed'), dismiss: t('admin.distillationRisk.actionDismiss'), restrict: t('admin.distillationRisk.actionRestrict'), revoke: t('admin.distillationRisk.actionRevoke') }))
const signalLabels = computed(() => ({ concurrency_saturation: t('admin.distillationRisk.signalConcurrency'), request_rate_saturation: t('admin.distillationRisk.signalRate'), token_throughput: t('admin.distillationRisk.signalTokens'), prompt_repeat_ratio: t('admin.distillationRisk.signalRepeat'), model_provider_breadth: t('admin.distillationRisk.signalBreadth'), interarrival_regularity: t('admin.distillationRisk.signalRegularity'), output_input_ratio: t('admin.distillationRisk.signalOutput') }))
const modeLabel = (value: string) => modeLabels.value[value as keyof typeof modeLabels.value] ?? value
const stateLabel = (value: string) => stateLabels.value[value as keyof typeof stateLabels.value] ?? value
const actionLabel = (value: string) => actionLabels.value[value as keyof typeof actionLabels.value] ?? value
const signalLabel = (value: string) => signalLabels.value[value as keyof typeof signalLabels.value] ?? value
const tabLabel = (value: string) => value === 'users' ? t('admin.distillationRisk.users') : value === 'events' ? t('admin.distillationRisk.events') : t('admin.distillationRisk.exemptions')
const userColumns = computed(() => [{ key: 'user_id', label: t('admin.distillationRisk.userID') }, { key: 'state', label: t('common.status') }, { key: 'last_score', label: t('admin.distillationRisk.score') }, { key: 'updated_at', label: t('admin.distillationRisk.updated') }, { key: 'actions', label: t('common.actions') }])
const eventColumns = computed(() => [{ key: 'created_at', label: t('admin.distillationRisk.updated') }, { key: 'user_id', label: t('admin.distillationRisk.userID') }, { key: 'event_type', label: t('admin.distillationRisk.eventType') }, { key: 'actor_id', label: t('admin.distillationRisk.actor') }, { key: 'details', label: t('admin.distillationRisk.evidence') }])
const exemptionColumns = computed(() => [{ key: 'user_id', label: t('admin.distillationRisk.userID') }, { key: 'expires_at', label: t('admin.distillationRisk.exemptionUntil') }, { key: 'reason', label: t('admin.distillationRisk.reason') }, { key: 'created_by', label: t('admin.distillationRisk.actor') }, { key: 'actions', label: t('common.actions') }])
function formatDate(value?: string | null) { return value ? new Date(value).toLocaleString() : '—' }
function failure(err: unknown) { const status = (err as { response?: { status?: number }; status?: number })?.response?.status ?? (err as { status?: number })?.status; return status === 409 ? t('admin.distillationRisk.conflict') : t('admin.distillationRisk.failed') }
let listGeneration = 0, detailGeneration = 0
function resetList() { cursor.value = undefined; cursorHistory.value = []; loadList() }
function nextPage() { if (nextCursor.value === null) return; cursorHistory.value.push(cursor.value); cursor.value = nextCursor.value; loadList() }
function previousPage() { cursor.value = cursorHistory.value.pop(); loadList() }
async function loadList() {
  const generation = ++listGeneration
  const id = userFilter.value !== '' ? Number(userFilter.value) : undefined
  if (id !== undefined && (!Number.isSafeInteger(id) || id <= 0)) { error.value = t('admin.distillationRisk.invalid'); listLoading.value = false; return }
  listLoading.value = true; error.value = ''
  const params = { cursor: true, before_id: cursor.value, page_size: 20, user_id: id }
  try {
    if (activeTab.value === 'users') { const data = await api.users({ ...params, state: stateFilter.value }); if (generation === listGeneration) { users.value = data.items ?? []; nextCursor.value = data.next_before_id ?? null } }
    else if (activeTab.value === 'events') { const data = await api.events(params); if (generation === listGeneration) { events.value = data.items ?? []; nextCursor.value = data.next_before_id ?? null } }
    else { const data = await api.exemptions(params); if (generation === listGeneration) { exemptions.value = data.items ?? []; nextCursor.value = data.next_before_id ?? null } }
  } catch (err) { if (generation === listGeneration) error.value = failure(err) }
  finally { if (generation === listGeneration) listLoading.value = false }
}
async function load() {
  loading.value = true; error.value = ''; stale.value = false
  const results = await Promise.allSettled([api.overview(), api.config(), loadList()])
  if (results[0].status === 'fulfilled') overview.value = results[0].value
  else { error.value = failure(results[0].reason); stale.value = true }
  if (results[1].status === 'fulfilled') config.value = results[1].value
  else { config.value = null; error.value = failure(results[1].reason) }
  if (!error.value) loadedAt.value = overview.value?.as_of ?? new Date().toISOString(); else stale.value = true; loading.value = false
}
const scoringFields = [
 { key: 'concurrency_saturation', label: 'admin.distillationRisk.concurrency_saturation', min: 0.01, max: 1 },
 { key: 'request_rate_saturation', label: 'admin.distillationRisk.request_rate_saturation', min: 0.01, max: 1 },
 { key: 'prompt_repeat_ratio', label: 'admin.distillationRisk.prompt_repeat_ratio', min: 0.01, max: 1 },
 { key: 'interarrival_cv', label: 'admin.distillationRisk.interarrival_cv', min: 0.01, max: 1 },
 { key: 'output_input_ratio', label: 'admin.distillationRisk.output_input_ratio', min: 1, max: 100 }
]
const scoringDefaults = { concurrency_saturation: 0.5, request_rate_saturation: 0.2, prompt_repeat_ratio: 0.9, interarrival_cv: 0.15, output_input_ratio: 4 }
const draftScoring = ref<Record<string, number>>({ ...scoringDefaults })
function hydrateConfig(value: RiskConfig) {
  configBase.value = { ...value, scoring_config: { ...value.scoring_config } }
  draftMode.value = value.mode
  draftScoring.value = { ...scoringDefaults, ...value.scoring_config } as Record<string, number>
  configConflict.value = null; confirmConfigReload.value = false; dialogError.value = ''
}
function openConfig() { if (!config.value || saving.value) return; hydrateConfig(config.value); configOpen.value = true }
function reloadConfig() { if (configConflict.value) hydrateConfig(configConflict.value) }
async function saveConfig() {
  if (!configBase.value || saving.value || configConflict.value) return
  if (scoringFields.some(field => !Number.isFinite(draftScoring.value[field.key]) || draftScoring.value[field.key] < field.min || draftScoring.value[field.key] > field.max)) { dialogError.value = t('admin.distillationRisk.invalid'); return }
  saving.value = true; dialogError.value = ''
  try { config.value = await api.saveConfig({ ...configBase.value, mode: draftMode.value, scoring_config: draftScoring.value }); configOpen.value = false; app.showSuccess(t('admin.distillationRisk.saved')); await load() }
  catch (err) {
    dialogError.value = failure(err)
    if (((err as { response?: { status?: number }; status?: number })?.response?.status ?? (err as { status?: number })?.status) === 409) {
      try { configConflict.value = await api.config() } catch { /* Preserve the draft if the latest version is unavailable. */ }
    }
  }
  finally { saving.value = false }
}
async function openDetail(id: number) {
  if (saving.value) return
  const generation = ++detailGeneration
  selectedID.value = id; detail.value = null; detailConflict.value = null; confirmDetailReload.value = false; windows.value = []; dialogError.value = ''; actionReason.value = ''; restrictionUntil.value = ''; exemptionOpen.value = false; exemptionUntil.value = ''; detailOpen.value = true; detailLoading.value = true
  try { const [data, trend] = await Promise.all([api.detail(id), api.trends(id)]); if (generation === detailGeneration) { detail.value = data; windows.value = trend ?? [] } }
  catch (err) { if (generation === detailGeneration) dialogError.value = failure(err) }
  finally { if (generation === detailGeneration) detailLoading.value = false }
}
function reloadDetail() {
  if (!detailConflict.value) return
  detail.value = detailConflict.value; detailConflict.value = null; confirmDetailReload.value = false
  actionReason.value = ''; restrictionUntil.value = ''; exemptionUntil.value = ''; exemptionOpen.value = false; dialogError.value = ''
}
async function applyAction() {
  const action = pendingAction.value; pendingAction.value = ''
  if (!detail.value?.case || saving.value || detailConflict.value) return
  if (!actionReason.value.trim()) { dialogError.value = t('admin.distillationRisk.reasonRequired'); return }
  const generation = detailGeneration, userID = selectedID.value
  const expires = restrictionUntil.value ? new Date(restrictionUntil.value) : null
  if (action === 'restrict' && (!expires || !Number.isFinite(expires.getTime()) || expires.getTime() <= Date.now() || expires.getTime() > Date.now() + 86400000)) { dialogError.value = t('admin.distillationRisk.restrictionHint'); return }
  saving.value = true; dialogError.value = ''
  try { const result = await api.action({ user_id: userID, type: action, expected_revision: detail.value.case.revision, reason: actionReason.value.trim(), ...(action === 'restrict' ? { expires_at: expires!.toISOString() } : {}), idempotency_key: crypto.randomUUID() }); if (generation !== detailGeneration || selectedID.value !== userID) return; detail.value = result; app.showSuccess(t('admin.distillationRisk.saved')); await load() }
  catch (err) {
    if (generation !== detailGeneration || selectedID.value !== userID) return
    dialogError.value = failure(err)
    if (((err as { response?: { status?: number }; status?: number })?.response?.status ?? (err as { status?: number })?.status) === 409) {
      try { const latest = await api.detail(userID); if (generation === detailGeneration) detailConflict.value = latest } catch { /* Preserve the action draft if reload fails. */ }
    }
  }
  finally { saving.value = false }
}
async function createExemption() {
  if (saving.value) return
  const generation = detailGeneration, userID = selectedID.value
  const until = new Date(exemptionUntil.value), now = new Date()
  if (!Number.isFinite(until.getTime()) || until <= now || until.getTime() - now.getTime() > 30 * 86400000 || !actionReason.value.trim()) { dialogError.value = t('admin.distillationRisk.invalid'); return }
  saving.value = true
  try { await api.exempt({ user_id: userID, starts_at: now.toISOString(), expires_at: until.toISOString(), reason: actionReason.value.trim(), idempotency_key: crypto.randomUUID() }); const result = await api.detail(userID); if (generation !== detailGeneration || selectedID.value !== userID) return; exemptionOpen.value = false; app.showSuccess(t('admin.distillationRisk.saved')); detail.value = result; await load() }
  catch (err) { dialogError.value = failure(err) }
  finally { saving.value = false }
}
async function revokeExemption(value: RiskExemption) {
  if (saving.value) return
  saving.value = true
  try { await api.revokeExemption({ id: value.id, expected_revision: value.revision, idempotency_key: crypto.randomUUID() }); app.showSuccess(t('admin.distillationRisk.saved')); await load() }
  catch (err) { error.value = failure(err) }
  finally { saving.value = false }
}
onMounted(load)
</script>
