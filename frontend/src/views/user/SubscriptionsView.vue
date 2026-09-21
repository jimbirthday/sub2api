<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex justify-center py-12">
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"
        ></div>
      </div>

      <!-- Empty State -->
      <div v-else-if="subscriptions.length === 0 && sharedSubscriptions.length === 0" class="card p-12 text-center">
        <div
          class="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700"
        >
          <Icon name="creditCard" size="xl" class="text-gray-400" />
        </div>
        <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('userSubscriptions.noActiveSubscriptions') }}
        </h3>
        <p class="text-gray-500 dark:text-dark-400">
          {{ t('userSubscriptions.noActiveSubscriptionsDesc') }}
        </p>
      </div>

      <!-- Subscriptions Grid -->
      <div v-if="!loading && subscriptions.length" class="grid gap-6 lg:grid-cols-2">
        <div
          v-for="subscription in subscriptions"
          :key="subscription.id"
          class="overflow-hidden rounded-2xl border bg-white dark:bg-dark-800"
          :class="platformBorderClass(subscription.group?.platform || '')"
        >
          <!-- Header -->
          <div
            class="flex items-center justify-between border-b border-gray-100 p-4 dark:border-dark-700"
          >
            <div class="flex items-center gap-3">
              <div :class="['h-1.5 w-1.5 shrink-0 rounded-full', platformAccentDotClass(subscription.group?.platform || '')]" />
              <div>
                <div class="flex items-center gap-2">
                  <h3 class="font-semibold text-gray-900 dark:text-white">
                    {{ subscription.group?.name || `Group #${subscription.group_id}` }}
                  </h3>
                  <span :class="['rounded-md border px-2 py-0.5 text-[11px] font-medium', platformBadgeClass(subscription.group?.platform || '')]">
                    {{ platformLabel(subscription.group?.platform || '') }}
                  </span>
                </div>
                <p v-if="subscription.group?.description" class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ subscription.group.description }}
                </p>
                <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-400 dark:text-gray-500">
                  <span>{{ t('payment.planCard.rate') }}: ×{{ subscription.group?.rate_multiplier ?? 1 }}</span>
                  <span v-if="subscriptionHasPeakRate(subscription)" class="text-amber-700 dark:text-amber-300">
                    {{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(subscription) }}
                  </span>
                </div>
              </div>
            </div>
            <div class="flex items-center gap-2">
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  subscription.status === 'active'
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
                    : subscription.status === 'expired'
                      ? 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
                      : 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
                ]"
              >
                {{ t(`userSubscriptions.status.${subscription.status}`) }}
              </span>
              <button
                v-if="subscription.status === 'active'"
                :class="['rounded-lg px-3 py-1.5 text-xs font-semibold text-white transition-colors', platformButtonClass(subscription.group?.platform || '')]"
                @click="router.push({ path: '/purchase', query: { tab: 'subscription', group: String(subscription.group_id) } })"
              >
                {{ t('payment.renewNow') }}
              </button>
            </div>
          </div>

          <!-- Usage Progress -->
          <div class="space-y-4 p-4">
            <!-- Expiration Info -->
            <div v-if="subscription.expires_at" class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{
                t('userSubscriptions.expires')
              }}</span>
              <span :class="getExpirationClass(subscription.expires_at)">
                {{ formatExpirationDate(subscription.expires_at) }}
              </span>
            </div>
            <div v-else class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{
                t('userSubscriptions.expires')
              }}</span>
              <span class="text-gray-700 dark:text-gray-300">{{
                t('userSubscriptions.noExpiration')
              }}</span>
            </div>

            <!-- Daily Usage -->
            <div v-if="subscription.group?.daily_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.daily') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscription.daily_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.daily_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscription.daily_usage_usd,
                      subscription.group.daily_limit_usd
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscription.daily_usage_usd,
                      subscription.group.daily_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.daily_window_start"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{ formatDailyUsageWindow(subscription) }}
              </p>
            </div>

            <!-- Weekly Usage -->
            <div v-if="subscription.group?.weekly_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.weekly') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscription.weekly_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.weekly_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscription.weekly_usage_usd,
                      subscription.group.weekly_limit_usd
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscription.weekly_usage_usd,
                      subscription.group.weekly_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.weekly_window_start"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription.weekly_window_start, 168)
                  })
                }}
              </p>
            </div>

            <!-- Monthly Usage -->
            <div v-if="subscription.group?.monthly_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.monthly') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscription.monthly_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.monthly_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscription.monthly_usage_usd,
                      subscription.group.monthly_limit_usd
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscription.monthly_usage_usd,
                      subscription.group.monthly_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.monthly_window_start"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription.monthly_window_start, 720)
                  })
                }}
              </p>
            </div>

            <!-- No limits configured - Unlimited badge -->
            <div
              v-if="
                !subscription.group?.daily_limit_usd &&
                !subscription.group?.weekly_limit_usd &&
                !subscription.group?.monthly_limit_usd
              "
              class="flex items-center justify-center rounded-xl bg-gradient-to-r from-emerald-50 to-teal-50 py-6 dark:from-emerald-900/20 dark:to-teal-900/20"
            >
              <div class="flex items-center gap-3">
                <span class="text-4xl text-emerald-600 dark:text-emerald-400">∞</span>
                <div>
                  <p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">
                    {{ t('userSubscriptions.unlimited') }}
                  </p>
                  <p class="text-xs text-emerald-600/70 dark:text-emerald-400/70">
                    {{ t('userSubscriptions.unlimitedDesc') }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <section id="shared-subscriptions" v-if="!loading && sharedSubscriptions.length" class="scroll-mt-20 space-y-3">
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('sharedSubscriptions.purchaseOrderHint') }}</p>
        <div class="grid gap-6 lg:grid-cols-2">
          <article v-for="sub in sharedSubscriptions" :key="`shared-${sub.id}`" class="overflow-hidden rounded-2xl border border-teal-200 bg-white dark:border-teal-900 dark:bg-dark-800">
            <header class="flex items-start justify-between gap-3 border-b border-gray-100 p-4 dark:border-dark-700">
              <div class="min-w-0">
                <h3 class="truncate font-semibold text-gray-900 dark:text-white">{{ sub.plan.name }}</h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">#{{ sub.id }} · {{ formatExpirationDate(sub.expires_at) }}</p>
              </div>
              <button v-if="sub.status === 'active' && Date.parse(sub.expires_at) > Date.now()" class="btn btn-primary btn-sm shrink-0" @click="renewShared(sub)">{{ t('sharedSubscriptions.renew') }}</button>
            </header>
            <div class="space-y-4 p-4">
              <div v-for="window in sub.windows" :key="window.kind" class="space-y-2">
                <div class="flex items-center justify-between gap-3 text-sm">
                  <span class="font-medium text-gray-700 dark:text-gray-300">{{ t(`sharedSubscriptions.${window.kind}`) }}</span>
                  <span class="tabular-nums text-gray-500 dark:text-dark-400">{{ window.limit == null ? `${t('sharedSubscriptions.used')} ${money(window.used)}` : `${money(window.used)} / ${money(window.limit)}` }}</span>
                </div>
                <div v-if="window.limit != null" class="flex h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600" role="progressbar" :aria-valuenow="Math.round(sharedPercent(window))" aria-valuemin="0" aria-valuemax="100" :aria-label="t(`sharedSubscriptions.${window.kind}`)">
                  <div class="h-full bg-teal-500 transition-[width] duration-300" :style="{ width: `${sharedUsedPercent(window)}%` }" />
                  <div class="h-full bg-amber-400 transition-[width] duration-300" :style="{ width: `${sharedReservedPercent(window)}%` }" />
                </div>
                <div class="flex justify-between text-xs text-gray-500 dark:text-dark-400">
                  <span>{{ t('sharedSubscriptions.used') }} {{ money(window.used) }}<template v-if="window.reserved > 0"> · {{ t('sharedSubscriptions.reserved') }} {{ money(window.reserved) }}</template></span>
                  <span v-if="window.limit != null">{{ t('sharedSubscriptions.unused') }} {{ money(Math.max(0, window.limit - window.used - window.reserved)) }}</span>
                  <span v-else>{{ t('sharedSubscriptions.unlimited') }}</span>
                </div>
              </div>
            </div>
          </article>
        </div>
      </section>

      <section v-if="!loading && sharedHistory.length" class="card overflow-hidden">
        <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('sharedSubscriptions.history') }}</h2>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full text-sm">
            <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-900 dark:text-dark-400">
              <tr>
                <th class="px-4 py-2">{{ t('sharedSubscriptions.time') }}</th>
                <th class="px-4 py-2">{{ t('sharedSubscriptions.group') }}</th>
                <th class="px-4 py-2 text-right">{{ t('sharedSubscriptions.cost') }}</th>
                <th class="px-4 py-2 text-right">{{ t('sharedSubscriptions.poolCost') }}</th>
                <th class="px-4 py-2 text-right">{{ t('sharedSubscriptions.walletCost') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="item in sharedHistory" :key="`${item.request_id}-${item.group_id}`">
                <td class="whitespace-nowrap px-4 py-2 text-gray-600 dark:text-dark-300">{{ formatDateTimeToMinute(item.billing_at) }}</td>
                <td class="px-4 py-2 text-gray-600 dark:text-dark-300">#{{ item.group_id }}</td>
                <td class="px-4 py-2 text-right tabular-nums">{{ money(item.cost) }}</td>
                <td class="px-4 py-2 text-right tabular-nums text-teal-700 dark:text-teal-300">{{ money(item.subscription_cost) }}</td>
                <td class="px-4 py-2 text-right tabular-nums">{{ money(item.balance_cost) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { nextTick, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import subscriptionsAPI from '@/api/subscriptions'
import { sharedSubscriptionsAPI, type SharedSettlement, type SharedSubscription } from '@/api/sharedSubscriptions'
import type { UserSubscription } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTimeToMinute } from '@/utils/format'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { platformBorderClass, platformBadgeClass, platformButtonClass, platformLabel } from '@/utils/platformColors'
import {
  getExpirationDateRelation,
  getRemainingDurationParts,
  isOneTimeDailyQuota,
  type RemainingDurationParts
} from '@/utils/subscriptionQuota'

function platformAccentDotClass(p: string): string {
  switch (p) {
    case 'anthropic': return 'bg-orange-500'
    case 'openai': return 'bg-emerald-500'
    case 'antigravity': return 'bg-purple-500'
    case 'gemini': return 'bg-blue-500'
    default: return 'bg-gray-400'
  }
}

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const appStore = useAppStore()

const subscriptions = ref<UserSubscription[]>([])
const sharedSubscriptions = ref<SharedSubscription[]>([])
const sharedHistory = ref<SharedSettlement[]>([])
const loading = ref(true)

function subscriptionHasPeakRate(subscription: UserSubscription): boolean {
  return hasPeakRate(subscription.group)
}

function subscriptionPeakRateLabel(subscription: UserSubscription): string {
  return formatPeakRateWindow(subscription.group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

async function loadSubscriptions() {
  try {
    loading.value = true
    const [legacy, shared, history] = await Promise.all([
      subscriptionsAPI.getMySubscriptions(),
      sharedSubscriptionsAPI.subscriptions(),
      sharedSubscriptionsAPI.history()
    ])
    subscriptions.value = legacy.filter(subscription => isSubscriptionVisible(subscription.status, subscription.expires_at))
    sharedSubscriptions.value = shared.filter(subscription => isSubscriptionVisible(subscription.status, subscription.expires_at))
    sharedHistory.value = history
  } catch (error) {
    console.error('Failed to load subscriptions:', error)
    appStore.showError(t('userSubscriptions.failedToLoad'))
  } finally {
    loading.value = false
  }
  await nextTick()
  if (route.hash === '#shared-subscriptions' && sharedSubscriptions.value.length) {
    document.getElementById('shared-subscriptions')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
  }
}

function isSubscriptionVisible(status: string, expiresAt?: string | null): boolean {
  if (status === 'expired') return false
  if (!expiresAt) return true
  const expiresAtMs = Date.parse(expiresAt)
  return !Number.isFinite(expiresAtMs) || expiresAtMs > Date.now()
}

function money(value: number): string {
  const digits = Math.abs(value) > 0 && Math.abs(value) < 0.01 ? 6 : Math.abs(value) < 1 ? 4 : 2
  return `$${value.toFixed(digits).replace(/0+$/, '').replace(/\.$/, '')}`
}

function sharedPercent(window: SharedSubscription['windows'][number]): number {
  if (window.limit == null || window.limit <= 0) return 0
  return Math.min(100, Math.max(0, ((window.used + window.reserved) / window.limit) * 100))
}

function sharedUsedPercent(window: SharedSubscription['windows'][number]): number {
  if (window.limit == null || window.limit <= 0) return 0
  return Math.min(100, Math.max(0, (window.used / window.limit) * 100))
}

function sharedReservedPercent(window: SharedSubscription['windows'][number]): number {
  if (window.limit == null || window.limit <= 0) return 0
  return Math.min(100 - sharedUsedPercent(window), Math.max(0, (window.reserved / window.limit) * 100))
}

function renewShared(sub: SharedSubscription) {
  void router.push({ path: '/purchase', query: { tab: 'subscription', shared_plan_id: sub.plan_id, renew_subscription_id: sub.id } })
}

function getProgressWidth(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return '0%'
  const percentage = Math.min(((used || 0) / limit) * 100, 100)
  return `${percentage}%`
}

function getProgressBarClass(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return 'bg-gray-400'
  const percentage = ((used || 0) / limit) * 100
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

function formatExpirationDate(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  const relation = getExpirationDateRelation(expires, now)

  if (relation === null) return ''

  if (relation === 'expired') {
    return t('userSubscriptions.status.expired')
  }

  const dateStr = formatDateTimeToMinute(expires)

  if (relation === 'today') {
    return `${dateStr} (${t('common.today')})`
  }
  if (relation === 'tomorrow') {
    return `${dateStr} (${t('common.tomorrow')})`
  }

  return t('userSubscriptions.daysRemaining', { days }) + ` (${dateStr})`
}

function getExpirationClass(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))

  if (diff <= 0) return 'text-red-600 dark:text-red-400 font-medium'
  if (days <= 3) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-orange-600 dark:text-orange-400'
  return 'text-gray-700 dark:text-gray-300'
}

function formatDurationParts(parts: RemainingDurationParts): string {
  if (parts.days > 0) {
    return `${parts.days}d ${parts.hours}h`
  }

  if (parts.hours > 0) {
    return `${parts.hours}h ${parts.minutes}m`
  }

  return `${parts.minutes}m`
}

function formatDailyUsageWindow(subscription: UserSubscription): string {
  if (isOneTimeDailyQuota(subscription) && subscription.expires_at) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    if (!parts) return t('userSubscriptions.windowNotActive')
    return t('userSubscriptions.quotaEndsIn', { time: formatDurationParts(parts) })
  }

  return t('userSubscriptions.resetIn', {
    time: formatResetTime(subscription.daily_window_start, 24)
  })
}

function formatResetTime(windowStart: string | null, windowHours: number): string {
  if (!windowStart) return t('userSubscriptions.windowNotActive')

  const start = new Date(windowStart)
  const end = new Date(start.getTime() + windowHours * 60 * 60 * 1000)
  const parts = getRemainingDurationParts(end)

  return parts ? formatDurationParts(parts) : t('userSubscriptions.windowNotActive')
}

onMounted(() => {
  loadSubscriptions()
})
</script>
