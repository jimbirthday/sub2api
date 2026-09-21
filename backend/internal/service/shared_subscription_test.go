package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type sharedAuthorizationStub struct {
	SharedSubscriptionRepository
	funding *SharedFunding
	err     error
}

type sharedQuotaUpdaterStub struct{}

func (*sharedQuotaUpdaterStub) UpdateQuotaUsed(context.Context, int64, float64) error {
	return nil
}
func (*sharedQuotaUpdaterStub) UpdateRateLimitUsage(context.Context, int64, float64) error {
	return nil
}

func (s *sharedAuthorizationStub) Authorize(context.Context, int64, int64, time.Time) (*SharedFunding, error) {
	return s.funding, s.err
}
func TestSharedFundingCopiesCachedKeyAndRetainsUncoveredBehavior(t *testing.T) {
	group := int64(10)
	key := &APIKey{ID: 1, UserID: 2, GroupID: &group}
	repo := &sharedAuthorizationStub{funding: &SharedFunding{UserID: 2, GroupID: 10, Available: true}}
	svc := &APIKeyService{sharedSubscriptions: &SharedSubscriptionService{repo: repo}}
	request, err := svc.WithSharedFunding(context.Background(), key)
	require.NoError(t, err)
	require.NotSame(t, key, request)
	require.Nil(t, key.SharedFunding)
	require.Same(t, repo.funding, request.SharedFunding)
	repo.funding = nil
	uncovered, err := svc.WithSharedFunding(context.Background(), key)
	require.NoError(t, err)
	require.Nil(t, uncovered.SharedFunding)
	repo.err = errors.New("database unavailable")
	_, err = svc.WithSharedFunding(context.Background(), key)
	require.Error(t, err)
}
func TestSharedBillingUsesFinalActualCostExactlyOnce(t *testing.T) {
	// ActualCost already includes group/user/peak pricing; a 4x group must not apply again.
	key := &APIKey{ID: 1, Group: &Group{RateMultiplier: 4}, SharedFunding: &SharedFunding{UserID: 2, GroupID: 10}, Quota: 100, RateLimit5h: 10}
	p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2, ActualCost: 3}, APIKey: key, User: &User{ID: 2}, Account: &Account{ID: 3}, Subscription: &UserSubscription{ID: 9}, IsSubscriptionBill: true}
	usageLog := &UsageLog{SubscriptionID: &p.Subscription.ID, ActualCost: 3}
	cmd := buildUsageBillingCommand("one", usageLog, p)
	require.Equal(t, 3.0, cmd.SharedCost)
	require.Equal(t, cmd.SharedCost, usageLog.ActualCost)
	require.Zero(t, cmd.BalanceCost)
	require.Zero(t, cmd.SubscriptionCost)
	require.Nil(t, cmd.SubscriptionID)
	require.Equal(t, BillingTypeSharedSubscription, cmd.BillingType)
	key.SharedFunding = nil
	p.IsSubscriptionBill = false
	cmd = buildUsageBillingCommand("two", nil, p)
	require.Equal(t, 3.0, cmd.BalanceCost)
	require.Zero(t, cmd.SharedCost)
}

func TestSharedBillingUsesUsageRecordAmountAsSingleSourceOfTruth(t *testing.T) {
	key := &APIKey{ID: 1, Quota: 10, RateLimit5h: 10, SharedFunding: &SharedFunding{UserID: 2, GroupID: 10}}
	usageLog := &UsageLog{ActualCost: 0.0579, BillingType: BillingTypeSharedSubscription}
	p := &postUsageBillingParams{
		// Regression: shared quota previously had no invariant tying this second
		// amount to the usage row, allowing 0.0353 to be consumed for a 0.0579 log.
		Cost:          &CostBreakdown{ActualCost: 0.0353},
		APIKey:        key,
		User:          &User{ID: 2},
		Account:       &Account{ID: 3},
		APIKeyService: &sharedQuotaUpdaterStub{},
	}

	cmd := buildUsageBillingCommand("shared-cost-reconciliation", usageLog, p)

	require.Equal(t, 0.0579, cmd.SharedCost)
	require.Equal(t, cmd.SharedCost, usageLog.ActualCost)
	require.Equal(t, cmd.SharedCost, p.Cost.ActualCost)
	require.Equal(t, cmd.SharedCost, cmd.APIKeyQuotaCost)
	require.Equal(t, cmd.SharedCost, cmd.APIKeyRateLimitCost)
}
func TestSharedWindowsKeepQuotaAcrossRenewalAndQuantizeCost(t *testing.T) {
	start := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	at := start.Add(8 * 24 * time.Hour)
	limit := 10.0
	plan := SharedSubscriptionPlan{DailyLimit: &limit, WeeklyLimit: &limit, MonthlyLimit: &limit}
	windows := SharedWindows(plan, start, at)
	require.True(t, windows[1].StartsAt.Equal(start.Add(7*24*time.Hour)))
	require.True(t, windows[2].StartsAt.Equal(start))
	windows[0].Used = 4
	windows[1].Reserved = 8
	windows[2].Used = 9.5
	require.Equal(t, .5, SharedAvailable(windows))
	cmd := &UsageBillingCommand{SharedFunding: &SharedFunding{GroupID: 1}, SharedCost: .000078125}
	cmd.Normalize()
	require.Equal(t, .00007813, cmd.SharedCost)
}
func TestValidateSharedPlanRejectsInvalidAndAllowsAnyGroupIDs(t *testing.T) {
	p := &SharedSubscriptionPlan{Name: "test", Price: 10, ValidityDays: 30, GroupIDs: []int64{3, 1, 3}}
	require.NoError(t, ValidateSharedPlan(p))
	require.Equal(t, []int64{1, 3}, p.GroupIDs)
	zero := 0.0
	p.DailyLimit = &zero
	require.Error(t, ValidateSharedPlan(p))
	p.DailyLimit = nil
	p.GroupIDs = nil
	require.Error(t, ValidateSharedPlan(p))
}

func TestSharedCreditAllowsZeroWallet(t *testing.T) {
	// No balance repository is configured: credit must avoid the wallet lookup entirely.
	svc := &BillingCacheService{cfg: &config.Config{}}
	user := &User{ID: 1, Balance: 0}
	key := &APIKey{SharedFunding: &SharedFunding{UserID: 1, GroupID: 2, Available: true}}
	for _, typ := range []string{SubscriptionTypeStandard, SubscriptionTypeSubscription} {
		require.NoError(t, svc.CheckBillingEligibility(context.Background(), user, key, &Group{ID: 2, SubscriptionType: typ}, nil, ""))
	}
}
func TestSharedPurchaseTargetsSurviveSignedWechatResume(t *testing.T) {
	svc := NewPaymentResumeService([]byte("shared-subscription-test-signing-key"))
	token, err := svc.CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{OpenID: "test", OrderType: SharedSubscriptionOrderType, PlanID: 7, RenewSubscriptionID: 9, ReplaceSubscriptionID: 11})
	require.NoError(t, err)
	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	require.NoError(t, err)
	require.Equal(t, SharedSubscriptionOrderType, claims.OrderType)
	require.EqualValues(t, 9, claims.RenewSubscriptionID)
	require.EqualValues(t, 11, claims.ReplaceSubscriptionID)
}
