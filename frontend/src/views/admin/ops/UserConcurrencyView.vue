<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { opsAPI, type OpsUserConcurrencyStatsResponse } from '@/api/admin/ops'
import AppLayout from '@/components/layout/AppLayout.vue'

const { t } = useI18n()

const loading = ref(false)
const hasLoadedOnce = ref(false)
const errorMessage = ref('')
const stats = ref<OpsUserConcurrencyStatsResponse | null>(null)

const autoRefreshEnabled = ref(true)
const autoRefreshSeconds = ref(10)
let refreshTimer: ReturnType<typeof setInterval> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null
const countdown = ref(autoRefreshSeconds.value)

interface UserRow {
  key: string
  user_id: number
  user_email: string
  username: string
  current_in_use: number
  max_capacity: number
  waiting_in_queue: number
  load_percentage: number
}

function safeNumber(n: unknown): number {
  return typeof n === 'number' && Number.isFinite(n) ? n : 0
}

const rows = computed((): UserRow[] => {
  const userStats = stats.value?.user || {}
  return Object.keys(userStats)
    .map(uid => {
      const u = userStats[uid] || {}
      return {
        key: uid,
        user_id: safeNumber(u.user_id),
        user_email: u.user_email || `User ${uid}`,
        username: u.username || '',
        current_in_use: safeNumber(u.current_in_use),
        max_capacity: safeNumber(u.max_capacity),
        waiting_in_queue: safeNumber(u.waiting_in_queue),
        load_percentage: safeNumber(u.load_percentage)
      }
    })
    .sort((a, b) => b.current_in_use - a.current_in_use || b.load_percentage - a.load_percentage)
})

const enabled = computed(() => stats.value?.enabled ?? true)

function getLoadBarClass(loadPct: number): string {
  if (loadPct >= 90) return 'bg-red-500 dark:bg-red-600'
  if (loadPct >= 70) return 'bg-orange-500 dark:bg-orange-600'
  if (loadPct >= 50) return 'bg-yellow-500 dark:bg-yellow-600'
  return 'bg-green-500 dark:bg-green-600'
}

function getLoadBarStyle(loadPct: number): string {
  return `width: ${Math.min(100, Math.max(0, loadPct))}%`
}

async function loadData() {
  loading.value = true
  errorMessage.value = ''
  try {
    stats.value = await opsAPI.getUserConcurrencyStats()
    hasLoadedOnce.value = true
  } catch (err: any) {
    console.error('[UserConcurrencyView] Failed to load data', err)
    errorMessage.value = err?.response?.data?.detail || t('admin.ops.userConcurrency.loadFailed')
  } finally {
    loading.value = false
  }
}

function startTimers() {
  stopTimers()
  countdown.value = autoRefreshSeconds.value
  if (autoRefreshEnabled.value) {
    refreshTimer = setInterval(() => {
      loadData()
      countdown.value = autoRefreshSeconds.value
    }, autoRefreshSeconds.value * 1000)
    countdownTimer = setInterval(() => {
      countdown.value = Math.max(0, countdown.value - 1)
    }, 1000)
  }
}

function stopTimers() {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
}

function toggleAutoRefresh() {
  autoRefreshEnabled.value = !autoRefreshEnabled.value
  startTimers()
}

function formatTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleTimeString()
}

onMounted(() => {
  loadData()
  startTimers()
})

onUnmounted(stopTimers)
</script>

<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.ops.userConcurrency.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.ops.userConcurrency.description') }}</p>
        </div>
        <div class="flex items-center gap-3">
          <span class="text-xs text-gray-400 dark:text-gray-500">
            {{ t('admin.ops.userConcurrency.collectedAt') }} {{ formatTime(stats?.timestamp) }}
          </span>
          <button
            class="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-2 text-xs font-semibold text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="loading"
            @click="loadData"
          >
            <svg class="h-3.5 w-3.5" :class="{ 'animate-spin': loading }" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            {{ t('common.refresh') }}
          </button>
          <label class="flex cursor-pointer items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
            <input v-model="autoRefreshEnabled" type="checkbox" class="h-4 w-4 rounded" @change="toggleAutoRefresh" />
            <span v-if="autoRefreshEnabled">{{ t('admin.ops.autoRefreshRemaining', { seconds: countdown }) }}</span>
            <span v-else>{{ t('common.disabled') }}</span>
          </label>
        </div>
      </div>

      <div v-if="errorMessage" class="rounded-2xl bg-red-50 p-4 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400">
        {{ errorMessage }}
      </div>

      <div v-if="!enabled" class="rounded-2xl border border-dashed border-gray-300 p-10 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
        {{ t('admin.ops.userConcurrency.disabled') }}
      </div>

      <div v-else class="rounded-3xl bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
        <div v-if="loading && !hasLoadedOnce" class="py-16 text-center text-sm text-gray-400">{{ t('common.loading') }}</div>

        <div v-else-if="rows.length === 0" class="py-16 text-center text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.ops.userConcurrency.empty') }}
        </div>

        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead>
              <tr class="border-b border-gray-200 text-xs uppercase tracking-wider text-gray-500 dark:border-dark-700 dark:text-gray-400">
                <th class="py-3 pr-4 font-semibold">{{ t('admin.ops.userConcurrency.user') }}</th>
                <th class="py-3 pr-4 font-semibold">{{ t('admin.ops.userConcurrency.username') }}</th>
                <th class="py-3 pr-4 font-semibold">{{ t('admin.ops.userConcurrency.email') }}</th>
                <th class="py-3 pr-4 text-right font-semibold">{{ t('admin.ops.userConcurrency.currentInUse') }}</th>
                <th class="py-3 pr-4 text-right font-semibold">{{ t('admin.ops.userConcurrency.maxCapacity') }}</th>
                <th class="py-3 pr-4 text-right font-semibold">{{ t('admin.ops.userConcurrency.waitingInQueue') }}</th>
                <th class="py-3 font-semibold" style="min-width: 160px">{{ t('admin.ops.userConcurrency.loadPercentage') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.key" class="border-b border-gray-100 last:border-0 dark:border-dark-700/60">
                <td class="py-3 pr-4 text-xs font-semibold text-gray-900 dark:text-white">#{{ row.user_id }}</td>
                <td class="py-3 pr-4 text-xs text-gray-700 dark:text-gray-300">{{ row.username || '-' }}</td>
                <td class="py-3 pr-4 text-xs text-gray-500 dark:text-gray-400">{{ row.user_email }}</td>
                <td class="py-3 pr-4 text-right text-xs font-semibold text-gray-900 dark:text-white">{{ row.current_in_use }}</td>
                <td class="py-3 pr-4 text-right text-xs text-gray-700 dark:text-gray-300">{{ row.max_capacity }}</td>
                <td class="py-3 pr-4 text-right text-xs text-gray-700 dark:text-gray-300">{{ row.waiting_in_queue }}</td>
                <td class="py-3">
                  <div class="flex items-center gap-2">
                    <div class="h-2 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                      <div class="h-2 rounded-full" :class="getLoadBarClass(row.load_percentage)" :style="getLoadBarStyle(row.load_percentage)" />
                    </div>
                    <span class="w-12 text-right text-xs tabular-nums text-gray-600 dark:text-gray-300">
                      {{ row.load_percentage.toFixed(0) }}%
                    </span>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </AppLayout>
</template>
