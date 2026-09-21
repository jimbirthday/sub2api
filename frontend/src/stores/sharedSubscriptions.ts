/** Global state for shared subscriptions shown in the authenticated header. */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { sharedSubscriptionsAPI, type SharedSubscription } from '@/api/sharedSubscriptions'

const CACHE_TTL_MS = 60_000

export const useSharedSubscriptionStore = defineStore('sharedSubscriptions', () => {
  const subscriptions = ref<SharedSubscription[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const lastFetchedAt = ref<number | null>(null)
  let generation = 0
  const now = ref(Date.now())
  let activePromise: Promise<SharedSubscription[]> | null = null
  let pollerInterval: ReturnType<typeof setInterval> | null = null

  const activeSubscriptions = computed(() => {
    return subscriptions.value.filter(sub => sub.status === 'active' && Date.parse(sub.starts_at) <= now.value && Date.parse(sub.expires_at) > now.value)
      .sort((a, b) => Date.parse(a.expires_at) - Date.parse(b.expires_at) || a.id - b.id)
  })

  async function fetch(force = false): Promise<SharedSubscription[]> {
    const now = Date.now()
    if (!force && loaded.value && lastFetchedAt.value && now - lastFetchedAt.value < CACHE_TTL_MS) {
      return subscriptions.value
    }
    if (activePromise && !force) return activePromise
    loading.value = true
    const current = ++generation
    const promise = sharedSubscriptionsAPI.subscriptions()
      .then(data => {
        if (current === generation) {
          subscriptions.value = data
          loaded.value = true
          lastFetchedAt.value = Date.now()
          updateClock()
        }
        return data
      })
      .finally(() => {
        if (activePromise === promise) {
          activePromise = null
          loading.value = false
        }
      })
    activePromise = promise
    return promise
  }

  function startPolling() {
    if (pollerInterval) return
    updateClock()
    pollerInterval = setInterval(() => {
      updateClock()
      if (document.visibilityState === 'visible') void fetch().catch(() => {})
    }, 30_000)
  }

  function stopPolling() {
    if (pollerInterval) clearInterval(pollerInterval)
    pollerInterval = null
  }

  function updateClock() { now.value = Date.now() }

  function clear() {
    generation++
    activePromise = null
    subscriptions.value = []
    loaded.value = false
    lastFetchedAt.value = null
    loading.value = false
    stopPolling()
  }

  return { subscriptions, activeSubscriptions, now, loading, fetch, startPolling, stopPolling, clear, updateClock }
})
