<template>
  <AppLayout>
    <div class="space-y-6">
      <div>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('sharedSubscriptions.calculator') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('sharedSubscriptions.calculatorHint') }}</p>
      </div>

      <div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <section class="card space-y-5 p-5">
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('sharedSubscriptions.plan') }}
            <select v-model.number="selectedPlanId" class="input mt-1 w-full" @change="applyPlan">
              <option :value="0">{{ t('sharedSubscriptions.manualCalculation') }}</option>
              <option v-for="plan in plans" :key="plan.id" :value="plan.id">{{ plan.name }}</option>
            </select>
          </label>

          <div class="grid gap-4 sm:grid-cols-3">
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('sharedSubscriptions.salePrice') }}<input v-model.number="salePrice" class="input mt-1 w-full" type="number" min="0.01" step="0.01" /></label>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('sharedSubscriptions.calculationQuota') }}<input v-model.number="quota" class="input mt-1 w-full" type="number" min="0.01" step="0.01" /></label>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('sharedSubscriptions.targetMargin') }}<div class="relative mt-1"><input v-model.number="targetMarginPercent" class="input w-full pr-8" type="number" min="0" max="99" step="0.1" /><span class="pointer-events-none absolute right-3 top-2 text-sm text-gray-400">%</span></div></label>
          </div>

          <label class="block max-w-xs text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('sharedSubscriptions.defaultGroupMargin') }}<div class="relative mt-1"><input v-model.number="defaultMarginPercent" class="input w-full pr-8" type="number" min="0" max="99" step="0.1" /><span class="pointer-events-none absolute right-3 top-2 text-sm text-gray-400">%</span></div></label>

          <fieldset>
            <legend class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('sharedSubscriptions.groups') }}</legend>
            <input v-model="search" class="input mt-2 w-full" :placeholder="t('sharedSubscriptions.searchGroups')" />
            <div class="mt-2 max-h-80 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-600">
              <label v-for="group in filteredGroups" :key="group.id" class="flex min-h-11 cursor-pointer items-center justify-between gap-3 rounded-lg px-2 hover:bg-gray-50 dark:hover:bg-dark-700">
                <span class="flex min-w-0 items-center gap-2"><input v-model="selectedGroupIds" type="checkbox" :value="group.id" /><span class="truncate text-sm text-gray-800 dark:text-gray-200">{{ group.name }}</span></span>
                <span class="shrink-0 text-xs tabular-nums text-gray-500 dark:text-dark-400">×{{ group.rate_multiplier }} · {{ groupMargin(group) }}%</span>
              </label>
            </div>
          </fieldset>
        </section>

        <aside class="space-y-4">
          <div class="card p-5">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('sharedSubscriptions.conservativeBasis') }}</p>
            <p class="mt-2 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ formatPercent(baseMargin) }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('sharedSubscriptions.conservativeBasisHint') }}</p>
          </div>
          <div class="card space-y-4 p-5">
            <ResultRow :label="t('sharedSubscriptions.theoreticalCost')" :value="money(theoreticalCost)" />
            <ResultRow :label="t('sharedSubscriptions.theoreticalProfit')" :value="money(theoreticalProfit)" :tone="theoreticalProfit < 0 ? 'danger' : 'success'" />
            <ResultRow :label="t('sharedSubscriptions.actualMargin')" :value="formatPercent(actualMargin)" />
            <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
              <ResultRow :label="t('sharedSubscriptions.suggestedQuota')" :value="money(suggestedQuota)" tone="primary" />
              <ResultRow class="mt-3" :label="t('sharedSubscriptions.suggestedPrice')" :value="money(suggestedPrice)" tone="primary" />
            </div>
          </div>
          <p class="px-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('sharedSubscriptions.calculationFormula') }}</p>
        </aside>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { getAll } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { sharedSubscriptionsAPI, type SharedPlan } from '@/api/sharedSubscriptions'

const { t } = useI18n()
const plans = ref<SharedPlan[]>([])
const groups = ref<AdminGroup[]>([])
const selectedPlanId = ref(0)
const selectedGroupIds = ref<number[]>([])
const salePrice = ref(100)
const quota = ref(105)
const targetMarginPercent = ref(5)
const defaultMarginPercent = ref(10)
const search = ref('')
const filteredGroups = computed(() => groups.value.filter(group => group.name.toLowerCase().includes(search.value.trim().toLowerCase())))
const selectedGroups = computed(() => groups.value.filter(group => selectedGroupIds.value.includes(group.id)))

function normalizedMargin(group: AdminGroup): number {
  const value = group.profit_control_enabled ? group.profit_min_margin : defaultMarginPercent.value / 100
  return Math.min(0.99, Math.max(0, Number(value) || 0))
}
function groupMargin(group: AdminGroup): string { return (normalizedMargin(group) * 100).toFixed(1).replace(/\.0$/, '') }
const baseMargin = computed(() => selectedGroups.value.length ? Math.min(...selectedGroups.value.map(normalizedMargin)) : defaultMarginPercent.value / 100)
const costRatio = computed(() => 1 - Math.min(0.99, Math.max(0, baseMargin.value)))
const targetMargin = computed(() => Math.min(0.99, Math.max(0, targetMarginPercent.value / 100)))
const theoreticalCost = computed(() => Math.max(0, Number(quota.value) || 0) * costRatio.value)
const theoreticalProfit = computed(() => Math.max(0, Number(salePrice.value) || 0) - theoreticalCost.value)
const actualMargin = computed(() => salePrice.value > 0 ? theoreticalProfit.value / salePrice.value : 0)
const suggestedQuota = computed(() => costRatio.value > 0 ? Math.max(0, salePrice.value) * (1 - targetMargin.value) / costRatio.value : 0)
const suggestedPrice = computed(() => targetMargin.value < 1 ? Math.max(0, quota.value) * costRatio.value / (1 - targetMargin.value) : 0)

function applyPlan() {
  const plan = plans.value.find(item => item.id === selectedPlanId.value)
  if (!plan) return
  selectedGroupIds.value = [...plan.group_ids]
  salePrice.value = plan.price
  quota.value = plan.monthly_limit_usd ?? plan.weekly_limit_usd ?? plan.daily_limit_usd ?? plan.price
}
function money(value: number): string { return `$${Number.isFinite(value) ? value.toFixed(2) : '0.00'}` }
function formatPercent(value: number): string { return `${(Number.isFinite(value) ? value * 100 : 0).toFixed(2)}%` }

const ResultRow = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true }, tone: { type: String, default: '' } },
  setup(props) {
    return () => h('div', { class: 'flex items-center justify-between gap-4' }, [
      h('span', { class: 'text-sm text-gray-500 dark:text-dark-400' }, props.label),
      h('strong', { class: ['tabular-nums', props.tone === 'danger' ? 'text-red-600 dark:text-red-400' : props.tone === 'success' ? 'text-emerald-600 dark:text-emerald-400' : props.tone === 'primary' ? 'text-primary-600 dark:text-primary-400' : 'text-gray-900 dark:text-white'] }, props.value),
    ])
  },
})

onMounted(async () => {
  const [loadedPlans, loadedGroups] = await Promise.all([sharedSubscriptionsAPI.plans(true), getAll()])
  plans.value = loadedPlans
  groups.value = loadedGroups
})
</script>
