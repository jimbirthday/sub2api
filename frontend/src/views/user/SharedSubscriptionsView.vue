<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-2xl font-bold">{{ t(plansOnly ? 'sharedSubscriptions.planManagement' : admin ? 'sharedSubscriptions.subscriptionManagement' : 'sharedSubscriptions.title') }}</h1><p class="mt-2 text-sm text-gray-500">{{ t('sharedSubscriptions.billingHint') }}</p></div>
        <button v-if="plansOnly" class="btn btn-primary" @click="editPlan()">{{ t('sharedSubscriptions.newPlan') }}</button>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <template v-else>
        <section v-if="!admin || plansOnly" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          <article v-for="plan in plans" :key="plan.id" class="card space-y-3 p-5">
            <h2 class="text-lg font-semibold">{{ plan.name }} <span v-if="admin && !plan.for_sale" class="text-sm text-gray-500">{{ t('sharedSubscriptions.offSale') }}</span></h2>
            <p class="text-sm text-gray-500">{{ plan.description }}</p>
            <p class="text-2xl font-bold">${{ plan.price }} <span class="text-sm font-normal">/ {{ plan.validity_days }} {{ t('sharedSubscriptions.days') }}</span></p>
            <p class="text-sm">{{ groupNames(plan) }}</p>
            <dl class="space-y-1 text-sm"><div v-for="kind in kinds" :key="kind" class="flex justify-between"><dt>{{ t(`sharedSubscriptions.${kind}`) }}</dt><dd>{{ money(plan[`${kind}_limit_usd`]) }}</dd></div></dl>
            <div class="flex gap-2">
              <button v-if="!admin && paymentEnabled" class="btn btn-primary" @click="buy(plan)">{{ t('sharedSubscriptions.buy') }}</button>
              <template v-if="admin"><button class="btn btn-secondary" @click="editPlan(plan)">{{ t('common.edit') }}</button><button class="btn btn-secondary" :disabled="busy" @click="remove(plan)">{{ t('common.delete') }}</button></template>
            </div>
          </article>
          <p v-if="!plans.length" class="text-gray-500">{{ t('sharedSubscriptions.noPlans') }}</p>
        </section>
        <section v-if="admin && !plansOnly" class="card space-y-4 p-5">
          <h2 class="font-semibold">{{ t('sharedSubscriptions.grant') }}</h2>
          <form class="flex flex-wrap items-end gap-3" @submit.prevent="grant">
            <div class="w-full space-y-1 sm:w-80"><span>{{ t('sharedSubscriptions.user') }}</span><SharedUserSelect v-model="grantUser" active-only :disabled="busy" /></div>
            <label class="space-y-1">{{ t('sharedSubscriptions.plan') }}<select v-model.number="grantPlan" required class="input block"><option :value="0" disabled>—</option><option v-for="p in plans" :key="p.id" :value="p.id">{{ p.name }}</option></select></label>
            <button class="btn btn-primary" :disabled="busy || !grantUser || !grantPlan">{{ t('sharedSubscriptions.grant') }}</button>
          </form>
          <p class="text-sm text-gray-500">{{ t('sharedSubscriptions.stackingHint') }}</p>
          <form class="flex flex-wrap items-end gap-3" @submit.prevent="refreshSubscriptions">
            <div class="w-full space-y-1 sm:w-80"><span>{{ t('sharedSubscriptions.userFilter') }}</span><SharedUserSelect v-model="filterUser" :disabled="busy" /></div>
            <label>{{ t('sharedSubscriptions.plan') }}<select v-model.number="filterPlan" class="input block"><option :value="0">{{ t('sharedSubscriptions.all') }}</option><option v-for="p in plans" :key="p.id" :value="p.id">{{ p.name }}</option></select></label>
            <label>{{ t('sharedSubscriptions.status') }}<select v-model="filterStatus" class="input block"><option value="">{{ t('sharedSubscriptions.all') }}</option><option v-for="status in ['active', 'paused', 'expired', 'revoked']" :key="status" :value="status">{{ t(`sharedSubscriptions.${status}`) }}</option></select></label>
            <button class="btn btn-secondary" :disabled="busy">{{ t('sharedSubscriptions.filter') }}</button>
          </form>
        </section>
        <section v-if="!plansOnly" class="space-y-3">
          <h2 class="text-xl font-semibold">{{ t(admin ? 'sharedSubscriptions.assigned' : 'sharedSubscriptions.mine') }}</h2>
          <p class="text-sm text-gray-500">{{ t('sharedSubscriptions.windowHint') }}</p>
          <p v-if="!subscriptions.length" class="text-gray-500">{{ t('sharedSubscriptions.empty') }}</p>
          <article v-for="sub in subscriptions" :key="sub.id" class="card space-y-3 p-5">
            <div class="flex flex-wrap justify-between gap-2"><h3 class="font-semibold">{{ sub.plan.name }} #{{ sub.id }} <span v-if="admin">· {{ sub.user_email || `UID ${sub.user_id}` }} {{ sub.username }}</span></h3><span>{{ t(`sharedSubscriptions.${effectiveStatus(sub)}`) }} · {{ t('sharedSubscriptions.expires') }} {{ date(sub.expires_at) }}</span></div>
            <p class="text-sm text-gray-500">{{ groupNames(sub.plan) }}</p>
            <div class="grid gap-3 md:grid-cols-3"><div v-for="w in sub.windows" :key="w.kind" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800"><p>{{ t(`sharedSubscriptions.${w.kind}`) }}: ${{ w.used.toFixed(4) }} / {{ money(w.limit) }}</p><p>{{ t('sharedSubscriptions.reserved') }}: ${{ w.reserved.toFixed(4) }}</p><p class="mt-1 text-gray-500">{{ t('sharedSubscriptions.resets') }} {{ date(w.resets_at) }}</p></div></div>
            <button v-if="!admin && paymentEnabled && renewable(sub)" class="btn btn-secondary" @click="buy(sub.plan, sub.id)">{{ t('sharedSubscriptions.renew') }}</button>
            <div v-if="admin" class="flex flex-wrap gap-2"><button v-if="sub.status !== 'revoked'" class="btn btn-secondary" :disabled="busy" @click="openChangePlan(sub)">{{ t('sharedSubscriptions.changePlan') }}</button><button v-for="action in actions(sub)" :key="action" :disabled="busy" class="btn btn-secondary" @click="act(sub, action)">{{ t(`sharedSubscriptions.${action}`) }}</button></div>
          </article>
        </section>
        <nav v-if="admin && !plansOnly" class="flex justify-end gap-3" :aria-label="t('sharedSubscriptions.subscriptionManagement')">
          <button class="btn btn-secondary" :disabled="busy || !cursors.length" @click="previousPage">{{ t('pagination.previous') }}</button>
          <button class="btn btn-secondary" :disabled="busy || !hasMore" @click="nextPage">{{ t('pagination.next') }}</button>
        </nav>
      </template>
    </div>
    <BaseDialog :show="!!changing" :title="t('sharedSubscriptions.changePlan')" @close="!busy && (changing = null)">
      <form v-if="changing" class="space-y-4" @submit.prevent="changePlan">
        <p>{{ changing.plan.name }} · #{{ changing.id }}</p>
        <p class="text-sm text-gray-500">{{ t('sharedSubscriptions.changePlanHint') }}</p>
        <label class="block">{{ t('sharedSubscriptions.plan') }}<select v-model.number="changeTarget" required class="input mt-1 w-full"><option :value="0" disabled>—</option><option v-for="p in changePlans" :key="p.id" :value="p.id">{{ p.name }}</option></select></label>
        <button class="btn btn-primary" :disabled="busy || !changeTarget">{{ t('common.save') }}</button>
      </form>
    </BaseDialog>
    <BaseDialog :show="!!editing" :title="t('sharedSubscriptions.plan')" @close="editing = null">
      <form v-if="editing" class="space-y-4" @submit.prevent="save">
        <label class="block">{{ t('sharedSubscriptions.name') }}<input v-model="editing.name" class="input mt-1 w-full" required maxlength="100" /></label>
        <label class="block">{{ t('sharedSubscriptions.description') }}<textarea v-model="editing.description" class="input mt-1 w-full" maxlength="10000" /></label>
        <div class="grid grid-cols-2 gap-3"><label>{{ t('sharedSubscriptions.price') }}<input v-model.number="editing.price" class="input mt-1 w-full" type="number" min="0.01" step="0.01" required /></label><label>{{ t('sharedSubscriptions.days') }}<input v-model.number="editing.validity_days" class="input mt-1 w-full" type="number" min="1" max="3650" required /></label></div>
        <fieldset class="space-y-2"><legend>{{ t('sharedSubscriptions.groups') }}</legend><input v-model="search" class="input w-full" :aria-label="t('sharedSubscriptions.searchGroups')" :placeholder="t('sharedSubscriptions.searchGroups')" /><div class="max-h-48 space-y-2 overflow-y-auto rounded border p-3 dark:border-dark-600"><label v-for="g in filteredGroups" :key="g.id" class="flex items-center gap-2"><input v-model="editing.group_ids" type="checkbox" :value="g.id" />{{ g.name }} · {{ g.subscription_type }} · ×{{ g.rate_multiplier }}</label></div></fieldset>
        <p class="text-sm text-gray-500">{{ t('sharedSubscriptions.limitHint') }}</p>
        <label v-for="kind in kinds" :key="kind" class="block">{{ t(`sharedSubscriptions.${kind}`) }}<input v-model="editing[`${kind}_limit_usd`]" class="input mt-1 w-full" type="number" min="0.00000001" step="any" :placeholder="t('sharedSubscriptions.unlimited')" /></label>
        <label class="flex items-center gap-2"><input v-model="editing.for_sale" type="checkbox" />{{ t('sharedSubscriptions.forSale') }}</label>
        <button class="btn btn-primary" :disabled="busy || !editing.group_ids.length">{{ t('common.save') }}</button>
      </form>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import SharedUserSelect from '@/components/admin/shared-subscription/SharedUserSelect.vue'
import { useAppStore } from '@/stores'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { extractApiErrorMessage } from '@/utils/apiError'
import { getAll } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { sharedSubscriptionsAPI as api, type SharedPlan, type SharedSubscription, sharedGrantRequestID } from '@/api/sharedSubscriptions'
const { t } = useI18n()
const route = useRoute(), router = useRouter(), app = useAppStore()
const admin = computed(() => route.path.startsWith('/admin/'))
const plansOnly = computed(() => admin.value && route.path.endsWith('/plans'))
const paymentEnabled = computed(() => resolveFeatureFlag(app.cachedPublicSettings, FeatureFlags.payment))
const plans = ref<SharedPlan[]>([]), subscriptions = ref<SharedSubscription[]>([]), groups = ref<AdminGroup[]>([])
const loading = ref(true), busy = ref(false), error = ref(''), search = ref('')
const editing = ref<SharedPlan | null>(null)
const grantUser = ref<number>(), grantPlan = ref(0), filterUser = ref<number>()
const filterPlan = ref(0), filterStatus = ref(''), beforeID = ref(0), hasMore = ref(false)
const cursors = ref<number[]>([])
const changing = ref<SharedSubscription | null>(null), changeTarget = ref(0)
const changePlans = computed(() => plans.value.filter(p => p.id !== changing.value?.plan_id || p.version !== changing.value?.plan.version))
let reloadPending = false
let grantRequest = ''
let grantIdentity = ''
const kinds = ['daily', 'weekly', 'monthly'] as const
const filteredGroups = computed(() => groups.value.filter(g => g.name.toLowerCase().includes(search.value.toLowerCase())))
const date = (v: string) => new Date(v).toLocaleString()
const money = (n: number | null) => n == null ? t('sharedSubscriptions.unlimited') : `$${n}`
const groupNames = (p: SharedPlan) => p.group_ids.map(id => p.group_names?.[String(id)] || `#${id}`).join(' · ')
const effectiveStatus = (s: SharedSubscription) => s.status === 'active' && Date.parse(s.expires_at) <= Date.now() ? 'expired' : s.status
const renewable = (s: SharedSubscription) => s.status === 'active' && plans.value.some(p => p.id === s.plan_id && p.version === s.plan.version)
const actions = (s: SharedSubscription) => s.status === 'revoked' ? [] : [s.status === 'active' ? 'pause' : 'resume', 'extend', 'reset', 'revoke']
function buy(p: SharedPlan, sub?: number) { void router.push({ path: '/purchase', query: { tab: 'subscription', shared_plan_id: p.id, renew_subscription_id: sub } }) }
async function run(fn: () => Promise<void>) { if (busy.value) return; busy.value = true; error.value = ''; try { await fn() } catch (e) { error.value = extractApiErrorMessage(e) || t('common.error'); app.showError(error.value) } finally { busy.value = false; if (reloadPending) { reloadPending = false; await run(load) } } }
async function loadSubscriptions() {
  if (plansOnly.value) return
  if (admin.value) {
    const page = await api.adminPage({ user_id: filterUser.value || undefined, plan_id: filterPlan.value || undefined, status: filterStatus.value || undefined, before_id: beforeID.value || undefined })
    subscriptions.value = page.items
    hasMore.value = page.has_more
  } else subscriptions.value = await api.subscriptions()
}
async function load() { plans.value = await api.plans(admin.value); if (plansOnly.value) groups.value = await getAll(); await loadSubscriptions() }
async function refreshSubscriptions() { await run(async () => { beforeID.value = 0; cursors.value = []; await loadSubscriptions() }) }
async function nextPage() {
  await run(async () => {
    const previous = beforeID.value
    beforeID.value = subscriptions.value.at(-1)?.id || 0
    try { await loadSubscriptions(); cursors.value.push(previous) } catch (err) { beforeID.value = previous; throw err }
  })
}
async function previousPage() {
  await run(async () => {
    const previous = beforeID.value
    beforeID.value = cursors.value.at(-1) || 0
    try { await loadSubscriptions(); cursors.value.pop() } catch (err) { beforeID.value = previous; throw err }
  })
}
function openChangePlan(sub: SharedSubscription) { changing.value = sub; changeTarget.value = 0 }
async function changePlan() {
  await run(async () => {
    if (!changing.value || !changeTarget.value) return
    await api.changePlan(changing.value.id, changeTarget.value, changing.value.generation)
    changing.value = null
    await loadSubscriptions()
    app.showSuccess(t('sharedSubscriptions.saved'))
  })
}
function editPlan(p?: SharedPlan) { search.value = ''; editing.value = p ? JSON.parse(JSON.stringify(p)) : { id: 0, version: 0, name: '', description: '', price: 10, validity_days: 30, group_ids: [], daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null, for_sale: true } }
async function save() { await run(async () => { if (!editing.value) return; const p = { ...editing.value }; for (const kind of kinds) { const key = `${kind}_limit_usd` as const; p[key] = p[key] == null || String(p[key]) === '' ? null : Number(p[key]) } await api.save(p); editing.value = null; await load(); app.showSuccess(t('sharedSubscriptions.saved')) }) }
async function remove(p: SharedPlan) { if (!window.confirm(t('sharedSubscriptions.deleteConfirm', { name: p.name }))) return; await run(async () => { await api.remove(p.id); await load() }) }
async function grant() { if (!grantUser.value || !grantPlan.value) return; await run(async () => { const identity = `${grantUser.value}:${grantPlan.value}`; if (!grantRequest || identity !== grantIdentity) { grantRequest = sharedGrantRequestID(); grantIdentity = identity } await api.assign(grantUser.value!, grantPlan.value, grantRequest); grantRequest = ''; filterUser.value = grantUser.value; grantUser.value = undefined; filterPlan.value = 0; filterStatus.value = ''; beforeID.value = 0; cursors.value = []; await load(); app.showSuccess(t('sharedSubscriptions.saved')) }) }
async function act(sub: SharedSubscription, action: string) { let days: number | undefined; if (action === 'extend') { const input = window.prompt(t('sharedSubscriptions.extendDays'), '30'); if (input === null) return; days = Number(input); if (!Number.isInteger(days) || days < 1 || days > 3650) { app.showError(t('sharedSubscriptions.invalidDays')); return } } if (!window.confirm(t('sharedSubscriptions.actionConfirm', { action: t(`sharedSubscriptions.${action}`), id: sub.id }))) return; await run(async () => { await api.action(sub.id, action, days); await load() }) }
watch(() => route.path, () => { error.value = ''; editing.value = null; changing.value = null; if (busy.value) reloadPending = true; else void run(load) })
onMounted(async () => { await run(async () => { await load() }); loading.value = false })
</script>
