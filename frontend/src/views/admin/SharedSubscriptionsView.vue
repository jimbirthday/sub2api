<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('sharedSubscriptions.subscriptionManagement') }}</h1>
          <div class="flex gap-2">
            <button class="btn btn-secondary" :disabled="loading || busy" @click="reload">{{ t('common.refresh') }}</button>
            <button class="btn btn-primary" :disabled="busy" data-test="open-assign" @click="showAssign = true">{{ t('sharedSubscriptions.grant') }}</button>
          </div>
        </div>
      </template>
      <template #filters>
        <div class="space-y-3">
          <div class="flex flex-wrap items-end gap-3">
            <label class="w-full space-y-1 md:w-72">
              <span class="text-xs text-gray-600 dark:text-dark-300">{{ t('common.search') }}</span>
              <input v-model="search" :disabled="busy" class="input w-full" maxlength="200" :placeholder="t('sharedSubscriptions.searchSubscriptions')" data-test="subscription-search" @input="scheduleSearch" @keydown.enter.prevent="applyFilters" />
            </label>
            <div class="w-full space-y-1 sm:w-60">
              <span class="text-xs text-gray-600 dark:text-dark-300">{{ t('sharedSubscriptions.user') }}</span>
              <SharedUserSelect v-model="userId" :disabled="busy" @update:model-value="applyFilters" />
            </div>
            <div class="w-full space-y-1 sm:w-48">
              <span class="text-xs text-gray-600 dark:text-dark-300">{{ t('sharedSubscriptions.plan') }}</span>
              <Select v-model="planId" :options="planOptions" :aria-label="t('sharedSubscriptions.plan')" searchable :disabled="busy" @change="applyFilters" />
            </div>
            <div class="w-full space-y-1 sm:w-36">
              <span class="text-xs text-gray-600 dark:text-dark-300">{{ t('sharedSubscriptions.status') }}</span>
              <Select v-model="status" :options="statusOptions" :aria-label="t('sharedSubscriptions.status')" :disabled="busy" @change="applyFilters" />
            </div>
            <button class="btn btn-secondary" :disabled="busy" data-test="clear-filters" @click="clearFilters">{{ t('sharedSubscriptions.clearFilters') }}</button>
          </div>
          <div v-if="selectedIds.length" class="flex flex-wrap items-center gap-2 rounded-lg bg-primary-50 p-3 dark:bg-primary-900/20">
            <span class="mr-2 text-sm">{{ t('sharedSubscriptions.selectedCount', { count: selectedIds.length }) }}</span>
            <button v-for="action in operationNames" :key="action" class="btn btn-secondary btn-sm" :data-test="`bulk-${action}`" :disabled="busy || !eligibleRows(action, selectedRows).length" @click="openOperation(action, selectedRows)">{{ t(`sharedSubscriptions.${action}`) }}</button>
            <button class="btn btn-ghost btn-sm" :disabled="busy" @click="selectedIds = []">{{ t('common.cancel') }}</button>
          </div>
          <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        </div>
      </template>
      <template #table>
        <DataTable :columns="columns" :data="rows" :loading="loading" row-key="id" selectable
          :selected-keys="selectedIds" :selection-label="selectionLabel" server-side-sort default-sort-key="id" default-sort-order="desc" :actions-count="6"
          @sort="sort" @update:selected-keys="selectRows">
          <template #cell-id="{ row }"><span class="font-mono text-xs">#{{ row.id }}</span></template>
          <template #cell-user_email="{ row }">
            <div class="max-w-64"><p class="truncate font-medium" :title="row.user_email">{{ row.user_email || `#${row.user_id}` }}</p><p class="text-xs text-gray-500 dark:text-dark-400">{{ row.username || '—' }} · UID {{ row.user_id }}</p></div>
          </template>
          <template #cell-plan="{ row }">
            <p class="max-w-56 truncate font-medium" :title="row.plan.name">{{ row.plan.name }}</p>
            <p class="text-xs text-gray-500 dark:text-dark-400">#{{ row.plan_id }} · v{{ row.plan.version }}</p>
          </template>
          <template #cell-groups="{ row }"><p class="max-w-56 whitespace-normal text-xs text-gray-600 dark:text-dark-300">{{ groupNames(row) }}</p></template>
          <template #cell-quota="{ row }">
            <div class="min-w-52 space-y-1 text-xs">
              <div v-for="window in row.windows" :key="window.kind" class="flex justify-between gap-3" :title="`${t('sharedSubscriptions.reserved')}: $${window.reserved.toFixed(4)} · ${t('sharedSubscriptions.resets')} ${date(window.resets_at)}`">
                <span class="text-gray-500 dark:text-dark-400">{{ t(`sharedSubscriptions.${window.kind}`) }}</span>
                <span class="tabular-nums" :class="window.limit !== null && remaining(window) === 0 ? 'text-red-600 dark:text-red-400' : ''">{{ window.limit == null ? t('sharedSubscriptions.unlimited') : `$${remaining(window).toFixed(2)} / $${window.limit.toFixed(2)}` }}</span>
              </div>
            </div>
          </template>
          <template #cell-status="{ row }"><span class="badge" :class="statusClass(row)">{{ t(`sharedSubscriptions.${effectiveStatus(row)}`) }}</span></template>
          <template #cell-starts_at="{ row }"><span class="whitespace-nowrap text-xs">{{ date(row.starts_at) }}</span></template>
          <template #cell-expires_at="{ row }"><span class="whitespace-nowrap text-xs" :class="effectiveStatus(row) === 'expired' ? 'text-red-600 dark:text-red-400' : ''">{{ date(row.expires_at) }}</span></template>
          <template #cell-actions="{ row }">
	            <div class="flex flex-wrap gap-1">
	              <button v-if="row.status !== 'revoked'" class="btn btn-ghost btn-sm" :disabled="busy" :data-test="`row-quota-${row.id}`" @click="openQuota(row)">{{ t('sharedSubscriptions.adjustQuota') }}</button>
	              <button v-for="action in operationNames.filter(action => eligibleRows(action, [row]).length)" :key="action" class="btn btn-ghost btn-sm" :class="action === 'revoke' ? 'text-red-600 dark:text-red-400' : ''" :disabled="busy" :data-test="`row-${action}-${row.id}`" @click="openOperation(action, [row])">{{ t(`sharedSubscriptions.${action}`) }}</button>
              <span v-if="row.status === 'revoked'" class="text-xs text-gray-400">—</span>
            </div>
          </template>
          <template #empty><p class="py-8 text-sm text-gray-500">{{ t('sharedSubscriptions.empty') }}</p></template>
        </DataTable>
      </template>
      <template #pagination>
        <Pagination v-if="total > 0" :page="page" :total="total" :page-size="pageSize" :page-size-options="[10, 20, 50, 100]" show-jump @update:page="changePage" @update:page-size="changePageSize" />
        <p v-else class="text-sm text-gray-500">{{ t('sharedSubscriptions.totalCount', { count: total }) }}</p>
      </template>
    </TablePageLayout>

    <BaseDialog :show="showAssign" :title="t('sharedSubscriptions.grant')" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && (showAssign = false)">
      <form id="shared-assign-form" class="space-y-4" @submit.prevent="assign">
        <div class="space-y-1"><span class="input-label">{{ t('sharedSubscriptions.user') }}</span><SharedUserSelect v-model="assignUser" active-only :disabled="busy" /></div>
        <label class="block space-y-1"><span class="input-label">{{ t('sharedSubscriptions.plan') }}</span><Select v-model="assignPlan" :options="assignOptions" searchable :disabled="busy" :aria-label="t('sharedSubscriptions.plan')" /></label>
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('sharedSubscriptions.stackingHint') }}</p>
        <p v-if="assignError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ assignError }}</p>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="showAssign = false">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !assignUser || !assignPlan">{{ t(busy ? 'common.loading' : 'sharedSubscriptions.grant') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="!!operation" :title="t(`sharedSubscriptions.${operation || 'reset'}`)" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && (operation = null)">
      <form id="shared-operation-form" class="space-y-4" @submit.prevent="submitOperation">
        <p class="text-sm">{{ t('sharedSubscriptions.operationConfirm', { count: targets.length, action: t(`sharedSubscriptions.${operation || 'reset'}`) }) }}</p>
        <div class="max-h-48 space-y-2 overflow-auto rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-900">
          <div v-for="row in targets" :key="row.id"><p>#{{ row.id }} · {{ row.user_email || row.user_id }} · {{ row.plan.name }}</p><p v-if="failures[row.id]" role="alert" class="text-red-600 dark:text-red-400">{{ failures[row.id] }}</p></div>
        </div>
        <p v-if="operation === 'reset'" class="text-sm text-gray-500">{{ t('sharedSubscriptions.resetHint') }}</p>
        <template v-if="operation === 'changePlan'">
          <p class="text-sm text-gray-500">{{ t('sharedSubscriptions.changePlanHint') }}</p>
          <Select v-model="targetPlan" :options="assignOptions" searchable :disabled="busy" :aria-label="t('sharedSubscriptions.plan')" />
        </template>
        <label v-if="operation === 'extend'" class="block space-y-1">{{ t('sharedSubscriptions.extendDays') }}<input v-model.number="extendDays" class="input" type="number" min="1" max="3650" required :disabled="busy" /></label>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="operation = null">{{ t('common.cancel') }}</button><button class="btn" :class="operation === 'revoke' ? 'btn-danger' : 'btn-primary'" :disabled="busy || !targets.length || (operation === 'changePlan' && !targetPlan)">{{ t(busy ? 'common.loading' : 'common.confirm') }}</button></div>
      </form>
	    </BaseDialog>

	    <BaseDialog :show="!!quotaEditing" :title="t('sharedSubscriptions.adjustQuota')" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && (quotaEditing = null)">
	      <form v-if="quotaEditing" class="space-y-4" @submit.prevent="saveQuota">
	        <p class="text-sm text-gray-600 dark:text-dark-300">{{ quotaEditing.user_email || `UID ${quotaEditing.user_id}` }} · {{ quotaEditing.plan.name }} · #{{ quotaEditing.id }}</p>
	        <div v-for="kind in quotaKinds" :key="kind" class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
	          <div class="mb-3 flex items-center justify-between gap-3"><strong class="text-sm text-gray-800 dark:text-gray-200">{{ t(`sharedSubscriptions.${kind}`) }}</strong><span class="text-xs text-gray-500">{{ t('sharedSubscriptions.reserved') }} {{ money(quotaWindow(kind)?.reserved || 0) }}</span></div>
	          <div class="grid gap-3 sm:grid-cols-2">
	            <label class="text-xs text-gray-600 dark:text-dark-300">{{ t('sharedSubscriptions.limitMode') }}<select v-model="quotaForm[kind].mode" class="input mt-1 w-full"><option value="inherit">{{ t('sharedSubscriptions.inheritPlan') }}</option><option value="custom">{{ t('sharedSubscriptions.customLimit') }}</option><option value="unlimited">{{ t('sharedSubscriptions.unlimited') }}</option></select></label>
	            <label v-if="quotaForm[kind].mode === 'custom'" class="text-xs text-gray-600 dark:text-dark-300">{{ t('sharedSubscriptions.limit') }}<input v-model.number="quotaForm[kind].value" class="input mt-1 w-full" type="number" min="0.00000001" step="any" required /></label>
	            <label class="text-xs text-gray-600 dark:text-dark-300">{{ t('sharedSubscriptions.used') }}<input v-model.number="quotaForm[kind].used" class="input mt-1 w-full" type="number" min="0" step="any" required /></label>
	          </div>
	        </div>
	        <label class="block text-sm text-gray-700 dark:text-gray-300">{{ t('sharedSubscriptions.adjustReason') }}<textarea v-model="quotaReason" class="input mt-1 w-full" maxlength="500" required /></label>
	        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sharedSubscriptions.adjustQuotaHint') }}</p>
	        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="quotaEditing = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !quotaReason.trim()">{{ t(busy ? 'common.loading' : 'common.save') }}</button></div>
	      </form>
	    </BaseDialog>
	  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import SharedUserSelect from '@/components/admin/shared-subscription/SharedUserSelect.vue'
import type { Column } from '@/components/common/types'
import { useAppStore } from '@/stores/app'
import { sharedSubscriptionsAPI as api, sharedGrantRequestID, type SharedPlan, type SharedSubscription } from '@/api/sharedSubscriptions'
import { extractApiErrorMessage } from '@/utils/apiError'
import { requestSharedSubscriptionsRefresh } from '@/utils/sharedSubscriptions'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'

const { t } = useI18n()
const app = useAppStore()
const rows = ref<SharedSubscription[]>([]), plans = ref<SharedPlan[]>([])
const loading = ref(false), busy = ref(false), error = ref('')
const search = ref(''), userId = ref<number>(), planId = ref(0), status = ref('')
const page = ref(1), pageSize = ref(Math.min(100, getPersistedPageSize())), total = ref(0)
const sortBy = ref('id'), sortOrder = ref('desc'), selectedIds = ref<number[]>([])
const showAssign = ref(false), assignUser = ref<number>(), assignPlan = ref(0), assignError = ref('')
let assignIdentity = '', assignRequest = ''
let revision = 0
let controller: AbortController | null = null
let searchTimer: ReturnType<typeof setTimeout> | undefined
const operationNames = ['changePlan', 'pause', 'resume', 'extend', 'reset', 'revoke'] as const
type Operation = typeof operationNames[number]
const operation = ref<Operation | null>(null), targets = ref<SharedSubscription[]>([]), failures = ref<Record<number, string>>({})
const targetPlan = ref(0), extendDays = ref(30)
const quotaKinds = ['daily', 'weekly', 'monthly'] as const
type QuotaKind = typeof quotaKinds[number]
type QuotaMode = 'inherit' | 'custom' | 'unlimited'
const quotaEditing = ref<SharedSubscription | null>(null)
const quotaReason = ref('')
const quotaForm = ref<Record<QuotaKind, { mode: QuotaMode; value?: number; used: number }>>({
  daily: { mode: 'inherit', used: 0 }, weekly: { mode: 'inherit', used: 0 }, monthly: { mode: 'inherit', used: 0 },
})
const selectedRows = computed(() => rows.value.filter(row => selectedIds.value.includes(row.id)))
const assignOptions = computed(() => plans.value.map(plan => ({ value: plan.id, label: `${plan.name}${plan.for_sale ? '' : ` · ${t('sharedSubscriptions.offSale')}`}` })))
const planOptions = computed(() => [{ value: 0, label: t('sharedSubscriptions.all') }, ...assignOptions.value])
const statusOptions = computed(() => ['', 'active', 'paused', 'expired', 'revoked'].map(value => ({ value, label: t(`sharedSubscriptions.${value || 'all'}`) })))
const columns = computed<Column[]>(() => [
  { key: 'id', label: 'ID', sortable: true },
  { key: 'user_email', label: t('sharedSubscriptions.user'), sortable: true },
  { key: 'plan', label: t('sharedSubscriptions.plan'), sortable: true },
  { key: 'groups', label: t('sharedSubscriptions.group') },
  { key: 'quota', label: t('sharedSubscriptions.remainingQuota') },
  { key: 'status', label: t('sharedSubscriptions.status'), sortable: true },
  { key: 'starts_at', label: t('sharedSubscriptions.startsAt'), sortable: true },
  { key: 'expires_at', label: t('sharedSubscriptions.expires'), sortable: true },
  { key: 'actions', label: t('common.actions') },
])
const date = (value: string) => new Date(value).toLocaleString()
const effectiveStatus = (row: SharedSubscription) => row.status === 'active' && Date.parse(row.expires_at) <= Date.now() ? 'expired' : row.status
const statusClass = (row: SharedSubscription) => ({ active: 'badge-success', paused: 'badge-warning', expired: 'badge-danger', revoked: 'badge-gray' }[effectiveStatus(row)])
const groupNames = (row: SharedSubscription) => row.plan.group_ids.map(id => row.plan.group_names?.[id] || `#${id}`).join(' · ')
const remaining = (window: SharedSubscription['windows'][number]) => Math.max(0, (window.limit ?? Infinity) - window.used - window.reserved)
const money = (value: number) => `$${value.toFixed(value > 0 && value < 0.01 ? 6 : 4).replace(/0+$/, '').replace(/\.$/, '')}`
const selectionLabel = (row: SharedSubscription) => `#${row.id} · ${row.user_email || row.user_id} · ${row.plan.name}`
function selectRows(ids: Array<string | number>) { if (!busy.value) selectedIds.value = rows.value.filter(row => ids.includes(row.id)).map(row => row.id) }
function eligibleRows(action: Operation, items: SharedSubscription[]) {
  return items.filter(row => row.status !== 'revoked' && (action !== 'pause' || row.status === 'active') && (action !== 'resume' || row.status === 'paused'))
}
async function load() {
  controller?.abort()
  controller = new AbortController()
  const current = ++revision
  loading.value = true; error.value = ''
  try {
    const result = await api.adminPage({ search: search.value.trim() || undefined, user_id: userId.value, plan_id: planId.value || undefined, status: status.value || undefined, page: page.value, page_size: pageSize.value, sort_by: sortBy.value, sort_order: sortOrder.value }, controller.signal)
    if (current !== revision) return
    rows.value = result.items; total.value = result.total; page.value = result.page
    selectedIds.value = selectedIds.value.filter(id => rows.value.some(row => row.id === id))
  } catch (err) {
    if (current === revision) { error.value = extractApiErrorMessage(err) || t('common.error'); rows.value = []; total.value = 0 }
  } finally { if (current === revision) loading.value = false }
}
async function loadPlans() {
  try { plans.value = await api.plans(true) } catch (err) { app.showError(extractApiErrorMessage(err) || t('common.error')) }
}
function applyFilters() { if (busy.value) return; clearTimeout(searchTimer); page.value = 1; selectedIds.value = []; void load() }
function scheduleSearch() { clearTimeout(searchTimer); searchTimer = setTimeout(applyFilters, 300) }
function clearFilters() { search.value = ''; userId.value = undefined; planId.value = 0; status.value = ''; applyFilters() }
function sort(key: string, order: string) { if (busy.value) return; sortBy.value = key; sortOrder.value = order; applyFilters() }
function changePage(value: number) { if (busy.value) return; page.value = value; selectedIds.value = []; void load() }
function changePageSize(value: number) { if (busy.value) return; pageSize.value = Math.min(100, value); applyFilters() }
function reload() { void load(); void loadPlans() }
async function assign() {
  if (busy.value || !assignUser.value || !assignPlan.value) return
  busy.value = true; assignError.value = ''
  const user = assignUser.value, plan = assignPlan.value
  const identity = `${user}:${plan}`
  if (!assignRequest || assignIdentity !== identity) { assignIdentity = identity; assignRequest = sharedGrantRequestID() }
  try {
    await api.assign(user, plan, assignRequest)
    assignRequest = ''; assignUser.value = undefined; showAssign.value = false
    search.value = ''; userId.value = user; planId.value = 0; status.value = ''; page.value = 1; selectedIds.value = []
    requestSharedSubscriptionsRefresh(); await load(); app.showSuccess(t('sharedSubscriptions.saved'))
  } catch (err) { assignError.value = extractApiErrorMessage(err) || t('common.error') }
  finally { busy.value = false }
}
function openOperation(action: Operation, items: SharedSubscription[]) {
  operation.value = action; targets.value = eligibleRows(action, items); failures.value = {}; targetPlan.value = 0; extendDays.value = 30
}
function openQuota(row: SharedSubscription) {
  quotaEditing.value = row
  quotaReason.value = ''
  for (const kind of quotaKinds) {
    const window = row.windows.find(item => item.kind === kind)
    const hasOverride = Object.prototype.hasOwnProperty.call(row.quota_overrides || {}, kind)
    const override = row.quota_overrides?.[kind]
    quotaForm.value[kind] = {
      mode: hasOverride ? (override == null ? 'unlimited' : 'custom') : 'inherit',
      value: override ?? window?.limit ?? undefined,
      used: window?.used || 0,
    }
  }
}
function quotaWindow(kind: QuotaKind) { return quotaEditing.value?.windows.find(window => window.kind === kind) }
async function saveQuota() {
  if (!quotaEditing.value || busy.value || !quotaReason.value.trim()) return
  busy.value = true
  try {
    await api.updateQuota(quotaEditing.value.id, {
      generation: quotaEditing.value.generation,
      limits: Object.fromEntries(quotaKinds.map(kind => [kind, { mode: quotaForm.value[kind].mode, ...(quotaForm.value[kind].mode === 'custom' ? { value: Number(quotaForm.value[kind].value) } : {}) }])) as Record<QuotaKind, { mode: QuotaMode; value?: number }>,
      used: Object.fromEntries(quotaKinds.map(kind => [kind, Number(quotaForm.value[kind].used)])) as Record<QuotaKind, number>,
      reason: quotaReason.value.trim(),
    })
    quotaEditing.value = null
    requestSharedSubscriptionsRefresh()
    await load()
    app.showSuccess(t('sharedSubscriptions.saved'))
  } catch (err) {
    app.showError(extractApiErrorMessage(err) || t('common.error'))
  } finally { busy.value = false }
}
async function submitOperation() {
  if (busy.value || !operation.value || !targets.value.length) return
  const action = operation.value, plan = targetPlan.value, days = extendDays.value
  if (action === 'changePlan' && !plan) return
  if (action === 'extend' && (!Number.isInteger(days) || days < 1 || days > 3650)) { app.showError(t('sharedSubscriptions.invalidDays')); return }
  busy.value = true; failures.value = {}
  const pending = [...targets.value], failed: SharedSubscription[] = [], succeeded: number[] = []
  try {
    // Bounded batches; rows with confirmed success are excluded from retries.
    for (let i = 0; i < pending.length; i += 4) {
      await Promise.all(pending.slice(i, i + 4).map(async row => {
        try {
          if (action === 'changePlan') await api.changePlan(row.id, plan, row.generation)
          else await api.action(row.id, action, action === 'extend' ? days : undefined)
          succeeded.push(row.id)
        } catch (err) { failures.value[row.id] = extractApiErrorMessage(err) || t('common.error'); failed.push(row) }
      }))
    }
    targets.value = failed
    selectedIds.value = selectedIds.value.filter(id => !succeeded.includes(id))
    if (!failed.length) operation.value = null
    if (succeeded.length) app.showSuccess(t('sharedSubscriptions.operationResult', { count: succeeded.length }))
    requestSharedSubscriptionsRefresh(); await load()
  } finally { busy.value = false }
}
onMounted(reload)
onBeforeUnmount(() => { revision++; controller?.abort(); clearTimeout(searchTimer) })
</script>
