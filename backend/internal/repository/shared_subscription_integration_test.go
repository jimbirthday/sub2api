//go:build integration

package repository

import (
	"context"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type sharedFixture struct {
	repo         *sharedSubscriptionRepository
	billing      *usageBillingRepository
	user         *service.User
	key          *service.APIKey
	group, other *service.Group
	plan         *service.SharedSubscriptionPlan
	sub          *service.SharedSubscription
}

func newSharedFixture(t *testing.T, limit float64) sharedFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	f := sharedFixture{repo: &sharedSubscriptionRepository{db: integrationDB, client: client}, billing: &usageBillingRepository{db: integrationDB}}
	f.user = mustCreateUser(t, client, &service.User{Balance: 100})
	f.group = mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), RateMultiplier: 2})
	f.other = mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), RateMultiplier: 3, SubscriptionType: service.SubscriptionTypeSubscription})
	f.key = mustCreateApiKey(t, client, &service.APIKey{UserID: f.user.ID, Key: uuid.NewString(), Name: "shared", GroupID: &f.group.ID, Quota: 1000})
	t.Cleanup(func() {
		cleanupSharedFixture(t, f.user.ID, f.key.ID, f.group.ID, f.other.ID)
	})
	f.plan = &service.SharedSubscriptionPlan{Name: "shared", Price: 10, ValidityDays: 30, GroupIDs: []int64{f.group.ID, f.other.ID}, DailyLimit: &limit, ForSale: true}
	require.NoError(t, f.repo.SavePlan(ctx, f.plan))
	var err error
	f.sub, err = f.repo.Assign(ctx, f.user.ID, f.plan, uuid.NewString())
	require.NoError(t, err)
	return f
}

func cleanupSharedFixture(t *testing.T, userID, keyID, firstGroupID, secondGroupID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := integrationDB.QueryContext(ctx, "SELECT DISTINCT plan_id FROM shared_subscriptions WHERE user_id=$1 UNION SELECT DISTINCT plan_id FROM shared_subscription_grants WHERE user_id=$1", userID)
	if err != nil {
		t.Errorf("query shared fixture plans: %v", err)
		return
	}
	planIDs := []int64{}
	for rows.Next() {
		var planID int64
		if err = rows.Scan(&planID); err != nil {
			rows.Close()
			t.Errorf("scan shared fixture plan: %v", err)
			return
		}
		planIDs = append(planIDs, planID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Errorf("iterate shared fixture plans: %v", err)
		return
	}
	rows.Close()

	statements := []struct {
		query string
		args  []any
	}{
		{"DELETE FROM shared_subscription_quota_adjustments WHERE subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1)", []any{userID}},
		{"DELETE FROM shared_subscription_debits WHERE subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1)", []any{userID}},
		{"DELETE FROM shared_subscription_order_items WHERE order_id IN (SELECT id FROM payment_orders WHERE user_id=$1) OR subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1) OR renew_subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1) OR replace_subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1)", []any{userID}},
		{"DELETE FROM shared_subscription_holds WHERE user_id=$1", []any{userID}},
		{"DELETE FROM shared_subscription_settlements WHERE user_id=$1", []any{userID}},
		{"DELETE FROM shared_subscription_billing_jobs WHERE api_key_id=$1", []any{keyID}},
		{"DELETE FROM shared_subscription_windows WHERE subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1)", []any{userID}},
		{"DELETE FROM shared_subscription_groups WHERE subscription_id IN (SELECT id FROM shared_subscriptions WHERE user_id=$1)", []any{userID}},
		{"DELETE FROM shared_subscription_grants WHERE user_id=$1", []any{userID}},
		{"DELETE FROM shared_subscriptions WHERE user_id=$1", []any{userID}},
		{"DELETE FROM payment_orders WHERE user_id=$1", []any{userID}},
		{"DELETE FROM api_keys WHERE user_id=$1", []any{userID}},
		{"DELETE FROM users WHERE id=$1", []any{userID}},
	}
	for _, statement := range statements {
		if _, err = integrationDB.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("clean shared fixture: %v", err)
			return
		}
	}
	for _, planID := range planIDs {
		if _, err = integrationDB.ExecContext(ctx, "DELETE FROM shared_subscription_plan_groups WHERE plan_id=$1", planID); err != nil {
			t.Errorf("clean shared plan groups: %v", err)
			return
		}
		if _, err = integrationDB.ExecContext(ctx, "DELETE FROM shared_subscription_plans WHERE id=$1", planID); err != nil {
			t.Errorf("clean shared plan: %v", err)
			return
		}
	}
	if _, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1,$2)", firstGroupID, secondGroupID); err != nil {
		t.Errorf("clean shared fixture groups: %v", err)
	}
}
func (f sharedFixture) funding(t *testing.T, group int64) *service.SharedFunding {
	t.Helper()
	v, e := f.repo.Authorize(context.Background(), f.user.ID, group, time.Now())
	require.NoError(t, e)
	require.NotNil(t, v)
	return v
}
func (f sharedFixture) command(t *testing.T, cost float64) *service.UsageBillingCommand {
	return &service.UsageBillingCommand{RequestID: uuid.NewString(), UserID: f.user.ID, APIKeyID: f.key.ID, SharedFunding: f.funding(t, f.group.ID), SharedCost: cost, APIKeyQuotaCost: cost, APIKeyRateLimitCost: cost}
}
func (f sharedFixture) balance(t *testing.T) float64 {
	t.Helper()
	var v float64
	require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", f.user.ID).Scan(&v))
	return v
}
func TestSharedSubscriptionConcurrentPoolsAndRetry(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	funding := f.funding(t, f.group.ID)
	other := f.funding(t, f.other.ID)
	require.Equal(t, funding.Pools[0].ID, other.Pools[0].ID)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pool := funding
			if i%2 == 0 {
				pool = other
			}
			_, err := f.billing.Apply(ctx, &service.UsageBillingCommand{RequestID: fmt.Sprintf("parallel-%s-%d", f.key.Key, i), UserID: f.user.ID, APIKeyID: f.key.ID, SharedFunding: pool, SharedCost: 1, APIKeyQuotaCost: 1})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 90.0, f.balance(t))
	sub, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	for _, w := range sub.Windows {
		require.Equal(t, 10.0, w.Used)
	}
	var cost, pool, wallet float64
	require.NoError(t, integrationDB.QueryRow("SELECT sum(cost),sum(subscription_cost),sum(balance_cost) FROM shared_subscription_settlements WHERE user_id=$1", f.user.ID).Scan(&cost, &pool, &wallet))
	require.Equal(t, 20.0, cost)
	require.Equal(t, 10.0, pool)
	require.Equal(t, cost, pool+wallet)
	cmd := f.command(t, 1.25)
	r, err := f.billing.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, r.Applied)
	require.Equal(t, 1.25, r.WalletConsumed)
	r, err = f.billing.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, r.Applied)
	require.Equal(t, 88.75, f.balance(t))
	conflict := *cmd
	conflict.SharedCost = 2
	conflict.RequestFingerprint = ""
	_, err = f.billing.Apply(ctx, &conflict)
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
}

func TestSharedSubscriptionDifferentPlansCoexistAndConsumeInPurchaseOrder(t *testing.T) {
	f := newSharedFixture(t, 2)
	ctx := context.Background()
	secondPlan := *f.plan
	secondPlan.ID = 0
	secondPlan.Version = 0
	secondPlan.Name = "shared second"
	require.NoError(t, f.repo.SavePlan(ctx, &secondPlan))
	secondSub, err := f.repo.Assign(ctx, f.user.ID, &secondPlan, uuid.NewString())
	require.NoError(t, err)

	funding := f.funding(t, f.group.ID)
	require.Len(t, funding.Pools, 2)
	require.Equal(t, f.sub.ID, funding.Pools[0].ID)
	require.Equal(t, secondSub.ID, funding.Pools[1].ID)

	requestID := uuid.NewString()
	result, err := f.billing.Apply(ctx, &service.UsageBillingCommand{
		RequestID:     requestID,
		UserID:        f.user.ID,
		APIKeyID:      f.key.ID,
		SharedFunding: funding,
		SharedCost:    3,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Zero(t, result.WalletConsumed)

	rows, err := integrationDB.Query("SELECT subscription_id,amount FROM shared_subscription_debits WHERE request_id=$1 AND api_key_id=$2 ORDER BY subscription_id", requestID, f.key.ID)
	require.NoError(t, err)
	defer rows.Close()
	debits := map[int64]float64{}
	for rows.Next() {
		var subscriptionID int64
		var amount float64
		require.NoError(t, rows.Scan(&subscriptionID, &amount))
		debits[subscriptionID] = amount
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 2.0, debits[f.sub.ID])
	require.Equal(t, 1.0, debits[secondSub.ID])
	require.Equal(t, 100.0, f.balance(t))
}

func TestSharedSubscriptionMinWindowSnapshotAndRecovery(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	weekly := 2.0
	f.plan.WeeklyLimit = &weekly
	require.NoError(t, f.repo.SavePlan(ctx, f.plan))
	old, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.NotNil(t, old.Plan.WeeklyLimit)
	require.Equal(t, weekly, *old.Plan.WeeklyLimit)
	require.NoError(t, f.repo.Action(ctx, f.sub.ID, "revoke", 0))
	f.sub, err = f.repo.Assign(ctx, f.user.ID, f.plan, uuid.NewString())
	require.NoError(t, err)
	cmd := f.command(t, 3)
	cmd.Normalize()
	require.NoError(t, f.billing.persistSharedJob(ctx, cmd))
	_, err = integrationDB.Exec("UPDATE shared_subscriptions SET expires_at=NOW()-interval '1 second' WHERE id=$1", f.sub.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE shared_subscription_billing_jobs SET created_at=NOW()-interval '20 seconds' WHERE request_id=$1", cmd.RequestID)
	require.NoError(t, err)
	users, err := f.billing.RecoverSharedBilling(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, f.user.ID, users[0].UserID)
	require.Equal(t, 99.0, f.balance(t))
	users, err = f.billing.RecoverSharedBilling(ctx)
	require.NoError(t, err)
	require.Empty(t, users)
	sub, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	for _, w := range sub.Windows {
		require.Equal(t, 2.0, w.Used)
	}
}

func TestSharedSubscriptionLivePlanAndPerSubscriptionQuota(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	weekly := 8.0
	f.plan.WeeklyLimit = &weekly
	f.plan.GroupIDs = []int64{f.other.ID}
	require.NoError(t, f.repo.SavePlan(ctx, f.plan))

	updated, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, f.plan.Version, updated.Plan.Version)
	require.Equal(t, []int64{f.other.ID}, updated.Plan.GroupIDs)
	require.Equal(t, weekly, *updated.Plan.WeeklyLimit)
	removed, err := f.repo.Authorize(ctx, f.user.ID, f.group.ID, time.Now())
	require.NoError(t, err)
	require.Nil(t, removed)
	require.NotNil(t, f.funding(t, f.other.ID))

	custom := 4.5
	require.NoError(t, f.repo.UpdateQuota(ctx, f.sub.ID, service.SharedQuotaUpdate{
		Generation: updated.Generation,
		OperatorID: f.user.ID,
		Reason:     "customer service correction",
		Limits: map[string]service.SharedQuotaValue{
			"daily":   {Mode: "custom", Value: &custom},
			"weekly":  {Mode: "inherit"},
			"monthly": {Mode: "unlimited"},
		},
		Used: map[string]float64{"daily": 1.25, "weekly": 2.5, "monthly": 3.75},
	}))
	adjusted, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, custom, *adjusted.Plan.DailyLimit)
	require.Equal(t, weekly, *adjusted.Plan.WeeklyLimit)
	require.Nil(t, adjusted.Plan.MonthlyLimit)
	require.Equal(t, 1.25, adjusted.Windows[0].Used)
	require.Equal(t, 2.5, adjusted.Windows[1].Used)
	require.Equal(t, 3.75, adjusted.Windows[2].Used)
	var audits int
	require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM shared_subscription_quota_adjustments WHERE subscription_id=$1", f.sub.ID).Scan(&audits))
	require.Equal(t, 1, audits)

	newDaily := 12.0
	f.plan.DailyLimit = &newDaily
	require.NoError(t, f.repo.SavePlan(ctx, f.plan))
	adjusted, err = f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, custom, *adjusted.Plan.DailyLimit, "custom override must win over live plan changes")
}
func TestSharedSubscriptionBatchCaptureRelease(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprint(capture), func(t *testing.T) {
			f := newSharedFixture(t, 2)
			ctx := context.Background()
			cmd := &service.BatchImageBalanceHoldCommand{RequestID: uuid.NewString(), BatchID: uuid.NewString(), UserID: f.user.ID, APIKeyID: f.key.ID, SharedFunding: f.funding(t, f.group.ID), HoldAmount: 5}
			_, err := f.billing.ReserveBatchImageBalance(ctx, cmd)
			require.NoError(t, err)
			require.Equal(t, 97.0, f.balance(t))
			sub, err := f.repo.Get(ctx, f.sub.ID)
			require.NoError(t, err)
			require.Equal(t, 2.0, sub.Windows[0].Reserved)
			require.NoError(t, f.repo.Action(ctx, f.sub.ID, "reset", 0))
			end := *cmd
			end.RequestID = uuid.NewString()
			end.RequestFingerprint = ""
			end.ActualAmount = 3
			if capture {
				_, err = f.billing.CaptureBatchImageBalance(ctx, &end)
			} else {
				_, err = f.billing.ReleaseBatchImageBalance(ctx, &end)
			}
			require.NoError(t, err)
			expected := 100.0
			if capture {
				expected = 99
			}
			require.Equal(t, expected, f.balance(t))
			if capture {
				_, err = f.billing.CaptureBatchImageBalance(ctx, &end)
			} else {
				_, err = f.billing.ReleaseBatchImageBalance(ctx, &end)
			}
			require.NoError(t, err)
			require.Equal(t, expected, f.balance(t))
			sub, err = f.repo.Get(ctx, f.sub.ID)
			require.NoError(t, err)
			require.Zero(t, sub.Windows[0].Used)
			require.Zero(t, sub.Windows[0].Reserved)
			var held float64
			require.NoError(t, integrationDB.QueryRow("SELECT sum(reserved) FROM shared_subscription_windows WHERE subscription_id=$1", f.sub.ID).Scan(&held))
			require.Zero(t, held)
		})
	}
}
func TestSharedSubscriptionOrderSnapshotRenewRefund(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	client := testEntClient(t)
	createOrder := func(renew int64) int64 {
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		order, err := tx.PaymentOrder.Create().SetUserID(f.user.ID).SetUserEmail(f.user.Email).SetUserName("test").SetRechargeCode(uuid.NewString()).SetClientIP("127.0.0.1").SetSrcHost("test").SetAmount(10).SetPayAmount(10).SetPaymentType("alipay").SetPaymentTradeNo("test").SetOutTradeNo(uuid.NewString()).SetOrderType(service.SharedSubscriptionOrderType).SetStatus(service.OrderStatusRecharging).SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx)
		require.NoError(t, err)
		require.NoError(t, f.repo.SaveOrder(dbent.NewTxContext(ctx, tx), order.ID, f.plan, renew, 0))
		require.NoError(t, tx.Commit())
		return order.ID
	}
	id := createOrder(0)
	require.NoError(t, f.repo.FulfillOrder(ctx, id))
	require.NoError(t, f.repo.FulfillOrder(ctx, id))
	var subID int64
	require.NoError(t, integrationDB.QueryRow("SELECT subscription_id FROM shared_subscription_order_items WHERE order_id=$1", id).Scan(&subID))
	sub, err := f.repo.Get(ctx, subID)
	require.NoError(t, err)
	renewID := createOrder(subID)
	require.NoError(t, f.repo.FulfillOrder(ctx, renewID))
	renewed, err := f.repo.Get(ctx, subID)
	require.NoError(t, err)
	require.InDelta(t, 720, renewed.ExpiresAt.Sub(sub.ExpiresAt).Hours(), .001)
	require.NoError(t, f.repo.RefundOrder(ctx, renewID, true))
	require.NoError(t, f.repo.RefundOrder(ctx, renewID, true))
	refunded, err := f.repo.Get(ctx, subID)
	require.NoError(t, err)
	require.True(t, sub.ExpiresAt.Equal(refunded.ExpiresAt))
	require.NoError(t, f.repo.RefundOrder(ctx, renewID, false))
	restored, err := f.repo.Get(ctx, subID)
	require.NoError(t, err)
	require.True(t, renewed.ExpiresAt.Equal(restored.ExpiresAt))
	pendingRenewal := createOrder(subID)
	require.NoError(t, f.repo.Action(ctx, subID, "revoke", 0))
	require.Error(t, f.repo.FulfillOrder(ctx, pendingRenewal))
	afterRevocation, err := f.repo.Get(ctx, subID)
	require.NoError(t, err)
	require.True(t, afterRevocation.ExpiresAt.Equal(restored.ExpiresAt))
	require.NoError(t, f.repo.DeletePlan(ctx, f.plan.ID))
	_, err = f.repo.Get(ctx, subID)
	require.NoError(t, err)
}

func TestSharedSubscriptionReplacementCanBeRefunded(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	client := testEntClient(t)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	order, err := tx.PaymentOrder.Create().SetUserID(f.user.ID).SetUserEmail(f.user.Email).SetUserName("test").SetRechargeCode(uuid.NewString()).SetClientIP("127.0.0.1").SetSrcHost("test").SetAmount(10).SetPayAmount(10).SetPaymentType("alipay").SetPaymentTradeNo("test").SetOutTradeNo(uuid.NewString()).SetOrderType(service.SharedSubscriptionOrderType).SetStatus(service.OrderStatusRecharging).SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, f.repo.SaveOrder(dbent.NewTxContext(ctx, tx), order.ID, f.plan, 0, f.sub.ID))
	require.NoError(t, tx.Commit())
	require.NoError(t, f.repo.FulfillOrder(ctx, order.ID))

	var replacementID int64
	require.NoError(t, integrationDB.QueryRow("SELECT subscription_id FROM shared_subscription_order_items WHERE order_id=$1", order.ID).Scan(&replacementID))
	require.NotEqual(t, f.sub.ID, replacementID)
	oldSub, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", oldSub.Status)
	replacement, err := f.repo.Get(ctx, replacementID)
	require.NoError(t, err)
	require.Equal(t, "active", replacement.Status)

	require.NoError(t, f.repo.RefundOrder(ctx, order.ID, true))
	oldSub, err = f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, "active", oldSub.Status)
	replacement, err = f.repo.Get(ctx, replacementID)
	require.NoError(t, err)
	require.Equal(t, "revoked", replacement.Status)

	require.NoError(t, f.repo.RefundOrder(ctx, order.ID, false))
	oldSub, err = f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", oldSub.Status)
	replacement, err = f.repo.Get(ctx, replacementID)
	require.NoError(t, err)
	require.Equal(t, "active", replacement.Status)
}

func TestSharedSubscriptionCrossMidnightAndOverlappingPools(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	// Make the request's day rollover deterministic without changing the process clock.
	at := time.Date(2026, 9, 19, 23, 59, 50, 0, time.UTC)
	_, err := integrationDB.Exec("UPDATE shared_subscriptions SET starts_at=$2,expires_at=$3 WHERE id=$1", f.sub.ID, at.Add(-24*time.Hour), at.Add(30*24*time.Hour))
	require.NoError(t, err)
	old, err := f.repo.Authorize(ctx, f.user.ID, f.group.ID, at)
	require.NoError(t, err)
	next, err := f.repo.Authorize(ctx, f.user.ID, f.group.ID, at.Add(time.Minute))
	require.NoError(t, err)
	require.NotEqual(t, old.Pools[0].Windows[0].StartsAt, next.Pools[0].Windows[0].StartsAt)
	for _, funding := range []*service.SharedFunding{next, old} {
		_, err = f.billing.Apply(ctx, &service.UsageBillingCommand{RequestID: uuid.NewString(), UserID: f.user.ID, APIKeyID: f.key.ID, SharedFunding: funding, SharedCost: 10})
		require.NoError(t, err)
	}
	require.Equal(t, 100.0, f.balance(t))
	var dailyTotal float64
	require.NoError(t, integrationDB.QueryRow("SELECT sum(used) FROM shared_subscription_windows WHERE subscription_id=$1 AND kind='daily'", f.sub.ID).Scan(&dailyTotal))
	require.Equal(t, 20.0, dailyTotal)
	// Two commands consume opposite pool orders; locking must still use stable IDs.
	f = newSharedFixture(t, 2)
	_, err = f.repo.Assign(ctx, f.user.ID, f.plan, uuid.NewString())
	require.NoError(t, err)
	a := f.funding(t, f.group.ID)
	b := *a
	b.Pools = append([]service.SharedPoolAuthorization(nil), a.Pools...)
	b.Pools[0], b.Pools[1] = b.Pools[1], b.Pools[0]
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	for _, funding := range []*service.SharedFunding{a, &b} {
		go func(funding *service.SharedFunding) {
			_, err := f.billing.Apply(timeout, &service.UsageBillingCommand{RequestID: uuid.NewString(), UserID: f.user.ID, APIKeyID: f.key.ID, SharedFunding: funding, SharedCost: 3})
			errs <- err
		}(funding)
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	require.Equal(t, 98.0, f.balance(t))
}

func TestSharedSubscriptionBatchPersistsAuthorization(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC)
	limit := 3.0
	funding := &service.SharedFunding{UserID: 1001, GroupID: 42, BillingAt: at, Available: true, Pools: []service.SharedPoolAuthorization{{ID: 7, Generation: 2, Windows: []service.SharedQuotaWindow{{Kind: "daily", StartsAt: at, Limit: &limit}}}}}
	tx := testTx(t)
	repo := newBatchImageRepositoryWithSQL(tx)
	job, err := repo.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{BatchID: batchImageTestID(t, "shared-snapshot"), UserID: 1001, Provider: service.BatchImageProviderGeminiAPI, Model: "gemini-2.5-flash-image", ItemCount: 1, EstimatedCost: 2, SharedFunding: funding})
	require.NoError(t, err)
	require.NotNil(t, job.SharedFunding)
	require.Equal(t, int64(42), job.SharedFunding.GroupID)
	require.Equal(t, 2, job.SharedFunding.Pools[0].Generation)
	require.True(t, at.Equal(job.SharedFunding.BillingAt))
	require.Equal(t, limit, *job.SharedFunding.Pools[0].Windows[0].Limit)
}

func TestSharedSubscriptionChangePlanPreservesExpiryAndAcceptedRequests(t *testing.T) {
	f := newSharedFixture(t, 10)
	ctx := context.Background()
	original := f.command(t, 3)
	_, err := f.billing.Apply(ctx, original)
	require.NoError(t, err)
	accepted := f.command(t, 2)
	target := &service.SharedSubscriptionPlan{Name: "replacement", Price: 20, ValidityDays: 90, GroupIDs: []int64{f.other.ID}, DailyLimit: f.plan.DailyLimit}
	require.NoError(t, f.repo.SavePlan(ctx, target))
	require.NoError(t, f.repo.Action(ctx, f.sub.ID, "pause", 0))
	require.NoError(t, f.repo.ChangePlan(ctx, f.sub.ID, target.ID, 0))
	changed, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, target.ID, changed.PlanID)
	require.Equal(t, "paused", changed.Status)
	require.Equal(t, 1, changed.Generation)
	require.True(t, changed.ExpiresAt.Equal(f.sub.ExpiresAt))
	require.True(t, changed.StartsAt.Equal(f.sub.StartsAt))
	require.Zero(t, changed.Windows[0].Used)
	require.Error(t, f.repo.ChangePlan(ctx, f.sub.ID, f.plan.ID, 0)) // stale/retried mutation
	require.Error(t, f.repo.ChangePlan(ctx, f.sub.ID, target.ID, 1)) // same version cannot reset quotas
	_, err = f.billing.Apply(ctx, accepted)                          // removed group still settles its accepted request
	require.NoError(t, err)
	require.Equal(t, 100.0, f.balance(t))
	require.NoError(t, f.repo.Action(ctx, f.sub.ID, "resume", 0))
	removed, err := f.repo.Authorize(ctx, f.user.ID, f.group.ID, time.Now())
	require.NoError(t, err)
	require.Nil(t, removed)
	funding := f.funding(t, f.other.ID)
	require.Equal(t, 1, funding.Pools[0].Generation)
	require.Zero(t, funding.Pools[0].Windows[0].Used)
	var oldUsage float64
	require.NoError(t, integrationDB.QueryRow("SELECT sum(used) FROM shared_subscription_windows WHERE subscription_id=$1 AND generation=0 AND kind='daily'", f.sub.ID).Scan(&oldUsage))
	require.Equal(t, 5.0, oldUsage)
	require.NoError(t, f.repo.Action(ctx, f.sub.ID, "revoke", 0))
	for _, action := range []string{"resume", "pause", "reset", "extend"} {
		require.Error(t, f.repo.Action(ctx, f.sub.ID, action, 30))
	}
	require.Error(t, f.repo.ChangePlan(ctx, f.sub.ID, f.plan.ID, 1))
	require.NoError(t, f.repo.Action(ctx, f.sub.ID, "revoke", 0))
}

func TestSharedSubscriptionAssignStackAndAdminPagination(t *testing.T) {
	f := newSharedFixture(t, 2)
	ctx := context.Background()
	key := uuid.NewString()
	second, err := f.repo.Assign(ctx, f.user.ID, f.plan, key)
	require.NoError(t, err)
	repeated, err := f.repo.Assign(ctx, f.user.ID, f.plan, key)
	require.NoError(t, err)
	require.Equal(t, second.ID, repeated.ID)
	require.NotEqual(t, f.sub.ID, second.ID)
	first, err := f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, Limit: 1})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Len(t, first.Items, 1)
	require.Equal(t, second.ID, first.Items[0].ID)
	require.Equal(t, f.user.Email, first.Items[0].UserEmail)
	next, err := f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, BeforeID: second.ID, Limit: 1})
	require.NoError(t, err)
	require.False(t, next.HasMore)
	require.Equal(t, f.sub.ID, next.Items[0].ID)
	// Earlier expiry first; a $3 request consumes $2 then $1 from independent pools.
	funding := f.funding(t, f.group.ID)
	require.Len(t, funding.Pools, 2)
	require.Equal(t, f.sub.ID, funding.Pools[0].ID)
	_, err = f.billing.Apply(ctx, &service.UsageBillingCommand{RequestID: uuid.NewString(), UserID: f.user.ID, APIKeyID: f.key.ID, SharedFunding: funding, SharedCost: 3})
	require.NoError(t, err)
	require.Equal(t, 100.0, f.balance(t))
	original, err := f.repo.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 2.0, original.Windows[0].Used)
	newer, err := f.repo.Get(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, 1.0, newer.Windows[0].Used)
	require.NoError(t, f.repo.Action(ctx, second.ID, "pause", 0))
	paused, err := f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, PlanID: f.plan.ID, Status: "paused", Limit: 20})
	require.NoError(t, err)
	require.Len(t, paused.Items, 1)
	require.Equal(t, second.ID, paused.Items[0].ID)
	_, err = integrationDB.Exec("UPDATE shared_subscriptions SET expires_at=NOW()-interval '1 second' WHERE id=$1", f.sub.ID)
	require.NoError(t, err)
	expired, err := f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, Status: "expired", Limit: 20})
	require.NoError(t, err)
	require.Len(t, expired.Items, 1)
	require.Equal(t, f.sub.ID, expired.Items[0].ID)
}

func TestSharedSubscriptionSearchSortAndNumberedPages(t *testing.T) {
	f := newSharedFixture(t, 5)
	ctx := context.Background()
	second, err := f.repo.Assign(ctx, f.user.ID, f.plan, uuid.NewString())
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE users SET notes=$2 WHERE id=$1", f.user.ID, "shared literal 50%_ test")
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE shared_subscriptions SET expires_at=NOW()+interval '1 day' WHERE id=$1", second.ID)
	require.NoError(t, err)
	result, err := f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, Search: "50%_", Page: 1, Limit: 1, SortBy: "expires_at", SortOrder: "asc"})
	require.NoError(t, err)
	require.Equal(t, 2, result.Total)
	require.Equal(t, 1, result.Page)
	require.Equal(t, 1, result.PageSize)
	require.True(t, result.HasMore)
	require.Equal(t, second.ID, result.Items[0].ID)
	result, err = f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, Search: f.user.Email, Page: 2, Limit: 1, SortBy: "expires_at", SortOrder: "asc"})
	require.NoError(t, err)
	require.Equal(t, 2, result.Total)
	require.False(t, result.HasMore)
	require.Equal(t, f.sub.ID, result.Items[0].ID)
	result, err = f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, Search: "50ab", Page: 1, Limit: 20})
	require.NoError(t, err)
	require.Zero(t, result.Total)
	require.Empty(t, result.Items)
	result, err = f.repo.ListPage(ctx, service.SharedSubscriptionFilter{UserID: f.user.ID, Search: f.plan.Name, Page: 999, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 1, result.Page)
	require.Equal(t, 2, result.Total)
}
