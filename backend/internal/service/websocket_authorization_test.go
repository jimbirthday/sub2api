package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type wsAuthRepo struct {
	APIKeyRepository
	key *APIKey
	err error
}

func (r *wsAuthRepo) GetByKeyForAuth(context.Context, string) (*APIKey, error) {
	return r.key, r.err
}

func wsAuthKey() *APIKey {
	gid := int64(3)
	return &APIKey{ID: 1, UserID: 2, Key: "sk-original", Status: StatusActive, GroupID: &gid,
		User:  &User{ID: 2, Status: StatusActive, Balance: 10},
		Group: &Group{ID: 3, Status: StatusActive, Platform: PlatformOpenAI}}
}

func TestRefreshWebSocketAuthorization(t *testing.T) {
	for _, simple := range []bool{false, true} {
		for _, mutation := range []string{"active", "deleted", "disabled", "expired", "quota", "user-disabled", "group-disabled", "binding", "ip-denied", "db-error"} {
			t.Run(mutation+map[bool]string{true: "/simple", false: "/normal"}[simple], func(t *testing.T) {
				original, fresh := wsAuthKey(), wsAuthKey()
				repo := &wsAuthRepo{key: fresh}
				var expected error
				switch mutation {
				case "deleted":
					repo.err = ErrAPIKeyNotFound
					expected = repo.err
				case "disabled":
					fresh.Status = StatusDisabled
					expected = ErrWebSocketAuthorizationRevoked
				case "expired":
					past := time.Now().Add(-time.Hour)
					fresh.ExpiresAt = &past
					if !simple {
						expected = ErrAPIKeyExpired
					}
				case "quota":
					fresh.Quota = 1
					fresh.QuotaUsed = 1
					if !simple {
						expected = ErrAPIKeyQuotaExhausted
					}
				case "user-disabled":
					fresh.User.Status = StatusDisabled
					expected = ErrWebSocketAuthorizationRevoked
				case "group-disabled":
					fresh.Group.Status = StatusDisabled
					expected = ErrWebSocketAuthorizationRevoked
				case "binding":
					gid := int64(4)
					fresh.GroupID = &gid
					expected = ErrWebSocketAuthorizationRevoked
				case "ip-denied":
					fresh.IPWhitelist = []string{"10.0.0.0/8"}
					expected = ErrWebSocketAuthorizationRevoked
				case "db-error":
					repo.err = errors.New("database down")
					expected = repo.err
				}
				cfg := &config.Config{}
				if simple {
					cfg.RunMode = config.RunModeSimple
				}
				svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
				// No GetByKey/cache method exists on this stub: any cached lookup
				// would panic, even though the opening key is still active.
				key, err := svc.RefreshWebSocketAuthorization(context.Background(), original, "127.0.0.1")
				if expected != nil {
					require.ErrorIs(t, err, expected)
					require.Nil(t, key)
				} else {
					require.NoError(t, err)
					require.Same(t, fresh, key)
				}
				require.Equal(t, StatusActive, original.Status)
			})
		}
	}
}

type wsWindowRepo struct {
	APIKeyRepository
	data *APIKeyRateLimitData
	err  error
}

func (r *wsWindowRepo) GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error) {
	return r.data, r.err
}

func TestCheckWebSocketBillingEligibility(t *testing.T) {
	for _, mode := range []string{"normal", "simple", "simple-windows"} {
		for _, scenario := range []string{"valid", "balance", "5h", "1d", "7d", "db-error"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				cfg := &config.Config{}
				if mode != "normal" {
					cfg.RunMode = config.RunModeSimple
				}
				cfg.SimpleModeKeyRateLimitEnabled = mode == "simple-windows"
				key := wsAuthKey()
				now := time.Now()
				repo := &wsWindowRepo{data: &APIKeyRateLimitData{Window5hStart: &now, Window1dStart: &now, Window7dStart: &now}}
				var expected error
				switch scenario {
				case "balance":
					key.User.Balance = 0
					if mode == "normal" {
						expected = ErrInsufficientBalance
					}
				case "5h":
					key.RateLimit5h = 1
					repo.data.Usage5h = 1
					expected = ErrAPIKeyRateLimit5hExceeded
				case "1d":
					key.RateLimit1d = 1
					repo.data.Usage1d = 1
					expected = ErrAPIKeyRateLimit1dExceeded
				case "7d":
					key.RateLimit7d = 1
					repo.data.Usage7d = 1
					expected = ErrAPIKeyRateLimit7dExceeded
				case "db-error":
					key.RateLimit5h = 1
					repo.err = errors.New("database down")
					expected = ErrBillingServiceUnavailable
				}
				if mode == "simple" {
					expected = nil
				}
				svc := &BillingCacheService{cfg: cfg, apiKeyRateLimitLoader: repo}
				_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
				if expected != nil {
					require.ErrorIs(t, err, expected)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

type wsSubRepo struct {
	UserSubscriptionRepository
	sub *UserSubscription
	err error
}

func (r *wsSubRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	return r.sub, r.err
}

func TestCheckWebSocketSubscriptionDoesNotFallBackToBalance(t *testing.T) {
	key := wsAuthKey()
	key.Group.SubscriptionType = SubscriptionTypeSubscription
	repo := &wsSubRepo{err: ErrSubscriptionNotFound}
	svc := &BillingCacheService{subRepo: repo}
	_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)

	now := time.Now()
	limit := 1.0
	key.Group.DailyLimitUSD = &limit
	repo.err = nil
	repo.sub = &UserSubscription{ID: 7, UserID: 2, GroupID: 3, Status: SubscriptionStatusActive,
		ExpiresAt: now.Add(time.Hour), DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now, DailyUsageUSD: 1}
	_, err = svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)
	repo.sub.DailyUsageUSD = 0
	sub, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.NoError(t, err)
	require.NotSame(t, repo.sub, sub)
}

func TestRefreshWebSocketSharedAuthorization(t *testing.T) {
	original := wsAuthKey()
	repo := &wsAuthRepo{key: wsAuthKey()}
	fundingRepo := &sharedAuthorizationStub{funding: &SharedFunding{UserID: 2, GroupID: 3, Available: true}}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	svc.sharedSubscriptions = &SharedSubscriptionService{repo: fundingRepo}
	first, err := svc.RefreshWebSocketAuthorization(context.Background(), original, "127.0.0.1")
	require.NoError(t, err)
	require.True(t, first.SharedFunding.Available)
	fundingRepo.funding = &SharedFunding{UserID: 2, GroupID: 3, Available: false}
	second, err := svc.RefreshWebSocketAuthorization(context.Background(), original, "127.0.0.1")
	require.NoError(t, err)
	require.False(t, second.SharedFunding.Available)
	require.True(t, first.SharedFunding.Available, "accepted turn retains its authorization")
	fundingRepo.err = errors.New("shared authorization DB unavailable")
	_, err = svc.RefreshWebSocketAuthorization(context.Background(), original, "127.0.0.1")
	require.ErrorIs(t, err, fundingRepo.err)
}

type wsRPMCounter struct {
	UserRPMCache
	calls int
}

func (c *wsRPMCounter) IncrementUserRPM(context.Context, int64) (int, error) {
	c.calls++
	return c.calls, nil
}

func TestWebSocketBillingRechecksDoNotCountRPM(t *testing.T) {
	key := wsAuthKey()
	key.User.RPMLimit = 2
	key.Group = nil
	counter := &wsRPMCounter{}
	svc := &BillingCacheService{userRPMCache: counter}
	for i := 0; i < 4; i++ {
		_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
		require.NoError(t, err)
	}
	require.Zero(t, counter.calls)
	require.NoError(t, svc.CheckWebSocketRPM(context.Background(), key))
	require.Equal(t, 1, counter.calls)
	_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, 1, counter.calls)
}
