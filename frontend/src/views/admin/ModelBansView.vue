<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-3"><div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.modelBans.title') }}</h1><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.modelBans.description') }}</p></div><button class="btn btn-secondary" @click="showCatalog = true">{{ t('admin.modelBans.catalog') }}</button></div>
      </template>
      <template #filters>
        <div class="mb-4 flex gap-2"><button class="btn" :class="view === 'policies' ? 'btn-primary' : 'btn-secondary'" @click="view = 'policies'; reset()">{{ t('admin.modelBans.policies') }}</button><button class="btn" :class="view === 'users' ? 'btn-primary' : 'btn-secondary'" @click="view = 'users'; reset()">{{ t('admin.modelBans.selectUser') }}</button></div>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="reset()">
          <label class="w-full sm:w-80"><span class="input-label">{{ t('admin.modelBans.searchUser') }}</span><input v-model="search" type="search" class="input" /></label>
          <label v-if="view === 'policies'" class="w-full sm:w-64"><span class="input-label">{{ t('admin.modelBans.model') }}</span><input v-model="modelFilter" class="input" /></label>
          <label v-if="view === 'policies'" class="w-full sm:w-48"><span class="input-label">{{ t('common.status') }}</span><select v-model="stateFilter" class="input"><option value="">{{ t('admin.modelBans.allStates') }}</option><option value="active">{{ t('admin.modelBans.active') }}</option><option value="disabled">{{ t('admin.modelBans.disabled') }}</option><option value="pending">{{ t('admin.modelBans.pending') }}</option><option value="expired">{{ t('admin.modelBans.expired') }}</option></select></label>
          <label v-if="view === 'policies'" class="w-full sm:w-40"><span class="input-label">{{ t('admin.modelBans.source') }}</span><select v-model="sourceFilter" class="input"><option value="">{{ t('admin.modelBans.allSources') }}</option><option value="manual">{{ t('admin.modelBans.manual') }}</option><option value="behavior">{{ t('admin.modelBans.behavior') }}</option></select></label>
          <button class="btn btn-primary" :disabled="loading" type="submit">{{ t('common.search') }}</button>
          <button class="btn btn-secondary ml-auto" :disabled="loading" type="button" @click="load"><Icon name="refresh" size="md" class="mr-2" />{{ t('common.refresh') }}</button>
        </form>
        <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
      </template>
      <template #table>
        <DataTable v-if="view === 'policies'" :columns="policyColumns" :data="policies" :loading="loading">
          <template #cell-user_email="{ row }"><span>{{ row.user_email }}</span><div class="text-xs text-gray-500">#{{ row.user_id }}</div></template>
          <template #cell-canonical_model="{ row }"><span class="font-mono text-xs">{{ row.namespace }}:{{ row.canonical_model }}</span></template>
          <template #cell-action="{ row }">{{ row.action === 'cap_concurrency' ? t('admin.modelBans.capConcurrency') : row.action === 'cap_rpm' ? t('admin.modelBans.capRPM') : t('admin.modelBans.denyModel') }}<span v-if="row.limit_value">: {{ row.limit_value }}</span></template>
          <template #cell-source="{ row }">{{ row.source === 'behavior' ? t('admin.modelBans.behavior') : t('admin.modelBans.manual') }}</template>
          <template #cell-active_now="{ row }"><span class="badge" :class="row.active_now ? 'badge-danger' : 'badge-gray'">{{ row.active_now ? t('admin.modelBans.active') : row.enabled ? (row.starts_at && new Date(row.starts_at).getTime() > Date.now() ? t('admin.modelBans.pending') : t('admin.modelBans.expired')) : t('admin.modelBans.disabled') }}</span></template>
          <template #cell-expires_at="{ row }">{{ row.expires_at ? new Date(row.expires_at).toLocaleString() : '—' }}</template>
          <template #cell-actions="{ row }"><button class="btn btn-secondary btn-sm" @click="selectPolicyUser(row.user_id)">{{ t('admin.modelBans.manage') }}</button></template>
          <template #empty><p class="p-12 text-center text-gray-500">{{ t('admin.modelBans.noPolicies') }}</p></template>
        </DataTable>
        <DataTable v-else :columns="columns" :data="users" :loading="loading">
          <template #cell-email="{ row }"><div class="font-medium text-gray-900 dark:text-white">{{ row.email }}</div><div class="text-xs text-gray-500">#{{ row.id }} · {{ row.username }}</div></template>
          <template #cell-actions="{ row }"><button class="btn btn-secondary btn-sm" @click="selectedUser = row">{{ t('admin.modelBans.manage') }}</button></template>
          <template #empty><p class="p-12 text-center text-gray-500">{{ t('admin.modelBans.noUsers') }}</p></template>
        </DataTable>
      </template>
      <template #pagination><CursorPagination v-if="view === 'policies'" :has-previous="cursorHistory.length > 0" :has-next="nextCursor !== null" :loading="loading" @previous="previous" @next="next" /><Pagination v-else-if="total > 0" :page="page" :page-size="20" :total="total" @update:page="page = $event; load()" /></template>
    </TablePageLayout>
    <ModelCatalogDialog :show="showCatalog" @close="showCatalog = false" />
    <UserModelBansModal :show="!!selectedUser" :user="selectedUser" @close="selectedUser = null" @success="load()" />
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import CursorPagination from '@/components/common/CursorPagination.vue'
import { listCurrent, type ModelRestrictionListItem } from '@/api/admin/modelRestrictions'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelCatalogDialog from '@/components/admin/user/ModelCatalogDialog.vue'
import UserModelBansModal from '@/components/admin/user/UserModelBansModal.vue'
import usersAPI from '@/api/admin/users'
import type { AdminUser } from '@/types'
const { t } = useI18n()
const route = useRoute()
const showCatalog = ref(false)
const users = ref<AdminUser[]>([]), selectedUser = ref<AdminUser | null>(null)
const search = ref(''), page = ref(1), total = ref(0), loading = ref(false), error = ref('')
const view = ref<'policies' | 'users'>('policies'), policies = ref<ModelRestrictionListItem[]>([]), modelFilter = ref(''), stateFilter = ref(''), sourceFilter = ref(''), cursor = ref<number | undefined>(), nextCursor = ref<number | null>(null), cursorHistory = ref<(number | undefined)[]>([])
const policyColumns = computed(() => [{ key: 'user_email', label: t('admin.modelBans.user') }, { key: 'canonical_model', label: t('admin.modelBans.model') }, { key: 'action', label: t('admin.modelBans.action') }, { key: 'source', label: t('admin.modelBans.source') }, { key: 'active_now', label: t('common.status') }, { key: 'expires_at', label: t('admin.modelBans.expires') }, { key: 'reason', label: t('admin.modelBans.reason') }, { key: 'actions', label: t('common.actions') }])
function reset() { page.value = 1; cursor.value = undefined; cursorHistory.value = []; load() }
function next() { if (nextCursor.value === null) return; cursorHistory.value.push(cursor.value); cursor.value = nextCursor.value; load() }
function previous() { cursor.value = cursorHistory.value.pop(); load() }
let selectionGeneration = 0
async function selectPolicyUser(id: number) { const request = ++selectionGeneration; try { const user = await usersAPI.getById(id); if (request === selectionGeneration) selectedUser.value = user } catch { if (request === selectionGeneration) error.value = t('admin.modelBans.failed') } }
const columns = computed(() => [{ key: 'email', label: t('admin.modelBans.user') }, { key: 'status', label: t('common.status') }, { key: 'actions', label: t('common.actions') }])
let generation = 0
async function load() {
  const request = ++generation
  loading.value = true; error.value = ''
  try { if (view.value === 'policies') { const result = await listCurrent({ limit: 20, before_id: cursor.value, search: search.value, model: modelFilter.value, state: stateFilter.value, source: sourceFilter.value }); if (request === generation) { policies.value = result.items; nextCursor.value = result.next_before_id } } else { const result = await usersAPI.list(page.value, 20, { search: search.value }); if (request === generation) { users.value = result.items; total.value = result.total } } }
  catch { if (request === generation) error.value = t('admin.modelBans.failed') }
  finally { if (request === generation) loading.value = false }
}
onMounted(async () => {
  await load()
  const id = Number(route.query.user_id)
  if (Number.isSafeInteger(id) && id > 0) {
    try { selectedUser.value = await usersAPI.getById(id) } catch { error.value = t('admin.modelBans.failed') }
  }
})
</script>
