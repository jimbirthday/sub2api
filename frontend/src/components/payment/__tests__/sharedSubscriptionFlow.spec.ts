import { describe, expect, it } from 'vitest'
import { buildCreateOrderPayload, createPaymentRecoverySnapshot, readPaymentRecoverySnapshot } from '../paymentFlow'
import { parseWechatResumeRoute } from '@/views/user/paymentWechatResume'
import { sharedCheckoutPlan, sharedGrantRequestID, type SharedPlan } from '@/api/sharedSubscriptions'

const plan: SharedPlan = { id: 7, version: 2, name: 'Shared', description: '', price: 10, validity_days: 30, group_ids: [1, 2], group_names: { 1: 'Balance', 2: 'Legacy' }, daily_limit_usd: 5, weekly_limit_usd: null, monthly_limit_usd: null, for_sale: true }
describe('shared subscription payment', () => {
  it('generates assignment idempotency keys without the HTTPS-only randomUUID API', () => {
    const first = sharedGrantRequestID()
    expect(first).toMatch(/^[0-9a-f]{32}$/)
    expect(sharedGrantRequestID()).not.toBe(first)
  })
  it('uses the matching plan type when recovering an unsigned WeChat amount', () => {
    const shared = sharedCheckoutPlan(plan)
    const legacy = { ...shared, shared: false, price: 99 }
    const query = { openid: 'wx-user', order_type: 'shared_subscription', plan_id: '7' }
    expect(parseWechatResumeRoute(query, [legacy, shared], 100)?.orderAmount).toBe(10)
    expect(parseWechatResumeRoute({ ...query, order_type: 'subscription' }, [shared, legacy], 100)?.orderAmount).toBe(99)
  })
  it('retains the new order type and explicit renewal across checkout', () => {
    const payload = buildCreateOrderPayload({ amount: 10, paymentType: 'wxpay', orderType: 'shared_subscription', planId: 7, renewSubscriptionId: 12, isMobile: true, isWechatBrowser: true })
    expect(payload).toMatchObject({ order_type: 'shared_subscription', plan_id: 7, renew_subscription_id: 12 })
    const ordinary = buildCreateOrderPayload({ amount: 10, paymentType: 'alipay', orderType: 'balance', renewSubscriptionId: 12, isMobile: false, isWechatBrowser: false })
    expect(ordinary.renew_subscription_id).toBeUndefined()
  })
  it('sends replacement independently from renewal', () => {
    const payload = buildCreateOrderPayload({ amount: 10, paymentType: 'alipay', orderType: 'shared_subscription', planId: 7, replaceSubscriptionId: 18, isMobile: false, isWechatBrowser: false })
    expect(payload).toMatchObject({ order_type: 'shared_subscription', plan_id: 7, replace_subscription_id: 18 })
    expect(payload.renew_subscription_id).toBeUndefined()
  })
  it('restores shared orders without converting them to wallet recharge', () => {
    const snapshot = createPaymentRecoverySnapshot({ orderId: 1, amount: 10, qrCode: '', expiresAt: new Date(Date.now() + 60000).toISOString(), paymentType: 'wxpay', payUrl: '', outTradeNo: '', clientSecret: '', intentId: '', currency: 'CNY', countryCode: '', paymentEnv: '', payAmount: 70, orderType: 'shared_subscription', paymentMode: '', resumeToken: '' })
    expect(readPaymentRecoverySnapshot(JSON.stringify(snapshot))?.orderType).toBe('shared_subscription')
  })
  it('resumes WeChat shared checkout even if legacy plans have the same ID', () => {
    const parsed = parseWechatResumeRoute({ wechat_resume_token: 'signed', order_type: 'shared_subscription', plan_id: '7' }, [sharedCheckoutPlan(plan)], 100)
    expect(parsed).toMatchObject({ orderType: 'shared_subscription', planId: 7, wechatResumeToken: 'signed' })
    expect(sharedCheckoutPlan(plan)).toMatchObject({ shared: true, group_ids: [1, 2] })
    expect(sharedCheckoutPlan(plan).rate_multiplier).toBeUndefined()
  })
})
