package service

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
)

var ErrWebSocketAuthorizationRevoked = infraerrors.Forbidden("WS_AUTHORIZATION_REVOKED", "authorization changed; please reconnect")

// RefreshWebSocketAuthorization bypasses both auth caches. The connection's
// identity and routing stay fixed; each accepted turn owns a fresh snapshot.
func (s *APIKeyService) RefreshWebSocketAuthorization(ctx context.Context, original *APIKey, clientIP string) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil || original == nil {
		return nil, ErrBillingServiceUnavailable
	}
	key, err := s.lookupAPIKeyForAuth(ctx, original.Key)
	if err != nil {
		return nil, err
	}
	if key == nil || key.ID != original.ID || key.UserID != original.UserID ||
		(key.GroupID == nil) != (original.GroupID == nil) ||
		(key.GroupID != nil && *key.GroupID != *original.GroupID) {
		return nil, ErrWebSocketAuthorizationRevoked
	}
	// The auth projection deliberately omits the credential column.
	key.Key = original.Key
	if key.Status != StatusActive && key.Status != StatusAPIKeyExpired && key.Status != StatusAPIKeyQuotaExhausted {
		return nil, ErrWebSocketAuthorizationRevoked
	}
	if key.User == nil || key.User.DeletedAt != nil || !key.User.IsActive() {
		return nil, ErrWebSocketAuthorizationRevoked
	}
	if key.GroupID != nil && (key.Group == nil || !key.Group.IsActive()) {
		return nil, ErrWebSocketAuthorizationRevoked
	}
	if key.Group != nil && original.Group != nil &&
		(key.Group.Platform != original.Group.Platform || key.Group.SubscriptionType != original.Group.SubscriptionType) {
		return nil, ErrWebSocketAuthorizationRevoked
	}
	if allowed, _ := ip.CheckIPRestrictionWithCompiledRules(clientIP, ip.CompileIPRules(key.IPWhitelist), ip.CompileIPRules(key.IPBlacklist)); !allowed {
		return nil, ErrWebSocketAuthorizationRevoked
	}
	simple := s.cfg != nil && s.cfg.RunMode == config.RunModeSimple
	if !simple {
		if key.Status == StatusAPIKeyExpired || key.IsExpired() {
			return nil, ErrAPIKeyExpired
		}
		if key.Status == StatusAPIKeyQuotaExhausted || key.IsQuotaExhausted() {
			return nil, ErrAPIKeyQuotaExhausted
		}
		key, err = s.WithSharedFunding(ctx, key)
		if err != nil {
			return nil, err
		}
	}
	if key.SharedFunding == nil && key.Group != nil && !key.Group.IsSubscriptionType() && !key.User.CanBindGroup(key.Group.ID, key.Group.IsExclusive) {
		return nil, ErrGroupNotAllowed
	}
	return key, nil
}

// CheckWebSocketBillingEligibility reads committed spending state, without
// counting RPM. Callers count RPM separately once per logical generation.
func (s *BillingCacheService) CheckWebSocketBillingEligibility(ctx context.Context, key *APIKey, platform string) (*UserSubscription, error) {
	if s == nil || key == nil || key.User == nil {
		return nil, ErrBillingServiceUnavailable
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		if s.cfg.SimpleModeKeyRateLimitEnabled {
			return nil, s.checkSimpleModeAPIKeyRateLimits(ctx, key)
		}
		return nil, nil
	}
	var sub *UserSubscription
	if key.SharedFunding == nil && key.Group != nil && key.Group.IsSubscriptionType() {
		if s.subRepo == nil {
			return nil, ErrBillingServiceUnavailable
		}
		loaded, err := s.subRepo.GetActiveByUserIDAndGroupID(ctx, key.User.ID, key.Group.ID)
		if err != nil {
			return nil, err
		}
		if loaded == nil {
			return nil, ErrSubscriptionNotFound
		}
		copy := *loaded
		sub = &copy
		if sub.Status != SubscriptionStatusActive || sub.DeletedAt != nil {
			return nil, ErrSubscriptionNotFound
		}
		// Reuse the existing CAS maintenance and authoritative reread, rather
		// than inventing another subscription window reset policy.
		validator := &SubscriptionService{userSubRepo: s.subRepo, billingCacheService: s, now: time.Now}
		maintenance, err := validator.ValidateAndCheckLimits(sub, key.Group)
		if maintenance {
			sub, err = validator.EnsureWindowMaintenance(ctx, sub)
			if err != nil {
				return nil, err
			}
			_, err = validator.ValidateAndCheckLimits(sub, key.Group)
		}
		if err != nil {
			return nil, err
		}
		// Billing admission rejects a fully consumed window (the middleware's
		// additional-cost validator permits equality when additionalCost is 0).
		if key.Group.HasDailyLimit() && sub.DailyUsageUSD >= *key.Group.DailyLimitUSD {
			return nil, ErrDailyLimitExceeded
		}
		if key.Group.HasWeeklyLimit() && sub.WeeklyUsageUSD >= *key.Group.WeeklyLimitUSD {
			return nil, ErrWeeklyLimitExceeded
		}
		if key.Group.HasMonthlyLimit() && sub.MonthlyUsageUSD >= *key.Group.MonthlyLimitUSD {
			return nil, ErrMonthlyLimitExceeded
		}
	} else {
		if (key.SharedFunding == nil || !key.SharedFunding.Available) && s.balanceBelowEligibilityThreshold(key.User.Balance) {
			return nil, ErrInsufficientBalance
		}
		if err := s.checkUserPlatformQuotaEligibility(ctx, key.User.ID, platform); err != nil {
			return nil, err
		}
	}
	if err := s.checkSimpleModeAPIKeyRateLimits(ctx, key); err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *BillingCacheService) CheckWebSocketRPM(ctx context.Context, key *APIKey) error {
	if s == nil || key == nil || key.User == nil {
		return ErrBillingServiceUnavailable
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	return s.checkRPM(ctx, key.User, key.Group)
}

// Database failures must not be mistaken for an account/upstream failure.
func WebSocketAuthorizationIsPolicyError(err error) bool {
	return err != nil && !errors.Is(err, context.DeadlineExceeded) && infraerrors.Code(err) >= 400 && infraerrors.Code(err) < 500
}
