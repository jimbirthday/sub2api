<template>
  <details ref="menuRef" v-if="store.activeSubscriptions.length" class="group relative w-20 shrink-0 sm:w-24" data-test="shared-summary-menu">
    <summary class="flex min-h-11 cursor-pointer list-none items-center gap-1.5 rounded-xl border border-teal-200/80 bg-teal-50/60 px-2 py-1.5 transition hover:border-teal-300 hover:bg-teal-50 dark:border-teal-900 dark:bg-teal-950/30 dark:hover:border-teal-700" :aria-label="t('subscriptionProgress.viewDetails')">
      <div class="min-w-0 flex-1">
        <span class="block truncate text-[11px] font-semibold leading-4 text-gray-900 dark:text-white" :title="summaryName">{{ summaryName }}</span>
        <div v-if="summaryWindow?.limit != null" class="mt-1 h-1 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600" role="progressbar" :aria-valuenow="Math.round(percent(summaryWindow))" aria-valuemin="0" aria-valuemax="100">
          <div class="h-full rounded-full bg-teal-500 transition-[width] duration-300" :style="{ width: `${percent(summaryWindow)}%` }" />
        </div>
        <div v-else class="mt-1 h-1 overflow-hidden rounded-full bg-teal-100 dark:bg-teal-900/40">
	          <div class="h-full w-1/3 rounded-full bg-teal-500/70" />
        </div>
      </div>
      <svg class="h-3.5 w-3.5 shrink-0 text-gray-400 transition-transform group-open:rotate-180" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.168l3.71-3.938a.75.75 0 1 1 1.08 1.04l-4.25 4.51a.75.75 0 0 1-1.08 0l-4.25-4.51a.75.75 0 0 1 .02-1.06Z" clip-rule="evenodd" /></svg>
    </summary>

    <div class="absolute right-0 z-50 mt-2 w-[min(24rem,calc(100vw-1rem))] space-y-3 rounded-2xl border border-gray-200 bg-white p-3 shadow-xl dark:border-dark-600 dark:bg-dark-800">
      <article v-for="sub in store.activeSubscriptions" :key="sub.id" class="rounded-xl border border-gray-100 p-3 dark:border-dark-700" data-test="shared-summary">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white" :title="sub.plan.name">{{ sub.plan.name }}</h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400" :title="new Date(sub.expires_at).toLocaleString()">{{ expiry(sub.expires_at) }}</p>
          </div>
          <span class="shrink-0 text-xs font-medium tabular-nums text-teal-700 dark:text-teal-300">{{ remainingLabel(sub) }}</span>
        </div>
        <div v-for="window in sub.windows" :key="window.kind" class="mt-3">
          <div class="mb-1 flex items-center justify-between text-[11px]">
            <span class="font-medium text-gray-600 dark:text-gray-300">{{ t(`sharedSubscriptions.${window.kind}`) }}</span>
            <span class="tabular-nums text-gray-500 dark:text-dark-400">{{ window.limit == null ? `${t('sharedSubscriptions.used')} ${money(window.used)}` : `${money(window.used)} / ${money(window.limit)}` }}</span>
          </div>
          <div v-if="window.limit != null" class="flex h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600" role="progressbar" :aria-label="t(`sharedSubscriptions.${window.kind}`)" :aria-valuenow="Math.round(percent(window))" aria-valuemin="0" aria-valuemax="100">
            <div class="h-full bg-teal-500" :style="{ width: `${usedPercent(window)}%` }" :title="`${t('sharedSubscriptions.used')} ${money(window.used)}`" />
            <div class="h-full bg-amber-400" :style="{ width: `${reservedPercent(window)}%` }" :title="`${t('sharedSubscriptions.reserved')} ${money(window.reserved)}`" />
          </div>
          <div class="mt-1 flex justify-between text-[10px] text-gray-500 dark:text-dark-400">
            <span>{{ t('sharedSubscriptions.used') }} {{ money(window.used) }}<template v-if="window.reserved"> · {{ t('sharedSubscriptions.reserved') }} {{ money(window.reserved) }}</template></span>
            <span>{{ window.limit == null ? t('sharedSubscriptions.unlimited') : `${t('sharedSubscriptions.unused')} ${money(remaining(window))}` }}</span>
          </div>
        </div>
      </article>
      <button type="button" class="btn btn-secondary min-h-11 w-full" @click="goToSubscriptions">{{ t('subscriptionProgress.viewDetails') }}</button>
    </div>
  </details>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useSharedSubscriptionStore } from '@/stores/sharedSubscriptions'
import type { SharedSubscription } from '@/api/sharedSubscriptions'

type Window = SharedSubscription['windows'][number]
const { t } = useI18n()
const router = useRouter()
const store = useSharedSubscriptionStore()
const menuRef = ref<HTMLDetailsElement | null>(null)
const first = computed(() => store.activeSubscriptions[0])
const summaryName = computed(() => {
  const sub = first.value
  if (!sub) return ''
  const extra = store.activeSubscriptions.length - 1
  return extra > 0 ? `${sub.plan.name} +${extra}` : sub.plan.name
})
const summaryWindow = computed(() => constrainedWindow(first.value))

function constrainedWindow(sub?: SharedSubscription): Window | undefined {
  if (!sub?.windows?.length) return undefined
  const limited = sub.windows.filter(window => window.limit != null && window.limit > 0)
  if (!limited.length) return sub.windows[0]
  return [...limited].sort((a, b) => remaining(a) - remaining(b) || percent(b) - percent(a))[0]
}
function remaining(window: Window): number { return window.limit == null ? Infinity : Math.max(0, window.limit - window.used - window.reserved) }
function percent(window?: Window): number { return !window || window.limit == null || window.limit <= 0 ? 0 : Math.min(100, Math.max(0, ((window.used + window.reserved) / window.limit) * 100)) }
function usedPercent(window: Window): number { return window.limit == null || window.limit <= 0 ? 0 : Math.min(100, Math.max(0, (window.used / window.limit) * 100)) }
function reservedPercent(window: Window): number { return window.limit == null || window.limit <= 0 ? 0 : Math.min(100 - usedPercent(window), Math.max(0, (window.reserved / window.limit) * 100)) }
function money(value: number): string {
  if (!Number.isFinite(value)) return t('sharedSubscriptions.unlimited')
  const digits = Math.abs(value) > 0 && Math.abs(value) < 0.01 ? 6 : Math.abs(value) < 1 ? 4 : 2
  return `$${value.toFixed(digits).replace(/0+$/, '').replace(/\.$/, '')}`
}
function remainingLabel(sub: SharedSubscription): string {
  const window = constrainedWindow(sub)
  return !window || window.limit == null ? t('sharedSubscriptions.unlimited') : `${t('sharedSubscriptions.unused')} ${money(remaining(window))}`
}
function expiry(value: string): string {
  const duration = Math.max(0, Date.parse(value) - store.now)
  return duration < 86_400_000
    ? t('sharedSubscriptions.hoursToExpiry', { hours: Math.max(1, Math.ceil(duration / 3_600_000)) })
    : t('sharedSubscriptions.daysToExpiry', { days: Math.ceil(duration / 86_400_000) })
}
async function goToSubscriptions() {
  if (menuRef.value) menuRef.value.open = false
  await router.push({ path: '/subscriptions', hash: '#shared-subscriptions' })
  document.getElementById('shared-subscriptions')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
}
onMounted(() => { store.updateClock(); void store.fetch().catch(() => {}) })
</script>
