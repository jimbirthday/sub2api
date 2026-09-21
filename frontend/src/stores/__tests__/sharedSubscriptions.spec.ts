import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSharedSubscriptionStore } from '../sharedSubscriptions'
import type { SharedSubscription } from '@/api/sharedSubscriptions'
const fetch = vi.hoisted(() => vi.fn())
vi.mock('@/api/sharedSubscriptions', () => ({ sharedSubscriptionsAPI: { subscriptions: fetch } }))
const sub = (id = 1, status = 'active', expires = '2026-09-22T00:00:00Z') => ({ id, status, starts_at: '2026-09-01T00:00:00Z', expires_at: expires } as SharedSubscription)
beforeEach(() => { setActivePinia(createPinia()); vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-20T00:00:00Z')); fetch.mockReset() })
afterEach(() => { vi.useRealTimers() })
describe('shared header state', () => {
  it('rejects responses from before logout and keeps new requests independent', async () => {
    let resolve!: (data: SharedSubscription[]) => void
    fetch.mockReturnValueOnce(new Promise(r => { resolve = r }))
    const store = useSharedSubscriptionStore()
    const pending = store.fetch()
    store.clear()
    fetch.mockResolvedValueOnce([sub(2)])
    await store.fetch()
    resolve([sub(1)]); await pending
    expect(store.subscriptions.map(s => s.id)).toEqual([2])
  })
  it('deduplicates, caches, and lets a forced refresh supersede an older fetch', async () => {
    let resolve!: (data: SharedSubscription[]) => void
    fetch.mockReturnValueOnce(new Promise(r => { resolve = r }))
    const store = useSharedSubscriptionStore()
    const first = store.fetch(), duplicate = store.fetch()
    expect(fetch).toHaveBeenCalledTimes(1)
    fetch.mockResolvedValueOnce([sub(2)])
    await store.fetch(true)
    resolve([sub(1)]); await Promise.all([first, duplicate])
    await store.fetch()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(store.subscriptions.map(s => s.id)).toEqual([2])
  })
  it('hides paused, revoked, future and expired subscriptions and expires visible ones as time passes', async () => {
    fetch.mockResolvedValue([sub(), sub(2, 'paused'), sub(3, 'revoked'), sub(4, 'active', '2026-09-19T00:00:00Z'), { ...sub(5), starts_at: '2027-01-01' }])
    const store = useSharedSubscriptionStore(); await store.fetch()
    expect(store.activeSubscriptions.map(s => s.id)).toEqual([1])
    vi.setSystemTime(new Date('2026-09-23T00:00:00Z')); store.updateClock()
    expect(store.activeSubscriptions).toHaveLength(0)
  })
})
