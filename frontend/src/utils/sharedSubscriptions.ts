export const SHARED_SUBSCRIPTIONS_REFRESH_EVENT = 'shared-subscriptions-refresh'

export function requestSharedSubscriptionsRefresh(): void {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(SHARED_SUBSCRIPTIONS_REFRESH_EVENT))
  }
}
