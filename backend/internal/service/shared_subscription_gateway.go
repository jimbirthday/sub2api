package service

import (
	"context"
	"log/slog"
	"time"
)

// WithSharedFunding copies the cached key before attaching per-request state.
func (s *APIKeyService) WithSharedFunding(ctx context.Context, key *APIKey) (*APIKey, error) {
	if s == nil || s.sharedSubscriptions == nil || key == nil || key.GroupID == nil {
		return key, nil
	}
	funding, err := s.sharedSubscriptions.Authorize(ctx, key.UserID, *key.GroupID)
	if err != nil {
		return nil, err
	}
	cp := *key
	cp.SharedFunding = funding
	return &cp, nil
}
func (s *APIKeyService) sharedGroupAccess(ctx context.Context, userID, groupID int64) bool {
	if s.sharedSubscriptions == nil {
		return false
	}
	f, err := s.sharedSubscriptions.Authorize(ctx, userID, groupID)
	return err == nil && f != nil
}
func (s *APIKeyService) addSharedGroupAccess(ctx context.Context, userID int64, ids map[int64]bool) error {
	if s.sharedSubscriptions == nil {
		return nil
	}
	groups, err := s.sharedSubscriptions.repo.GroupIDs(ctx, userID)
	if err != nil {
		return err
	}
	for _, id := range groups {
		ids[id] = true
	}
	return nil
}

type SharedBillingRecovered struct {
	UserID   int64
	APIKeyID int64
	Platform string
	Cost     float64
	Applied  bool
}
type SharedBillingRecovery interface {
	RecoverSharedBilling(context.Context) ([]SharedBillingRecovered, error)
}

func (s *BillingCacheService) startSharedBillingRecovery(repo UsageBillingRepository) {
	recovery, ok := repo.(SharedBillingRecovery)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.sharedRecoveryCancel = cancel
	s.sharedRecoveryWG.Add(1)
	go func() {
		defer s.sharedRecoveryWG.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tick, cancel := context.WithTimeout(ctx, 30*time.Second)
				users, err := recovery.RecoverSharedBilling(tick)
				if err != nil && ctx.Err() == nil {
					slog.Error("shared subscription billing recovery failed", "error", err)
				}
				for _, recovered := range users {
					id := recovered.UserID
					if s.sharedAuthInvalidator != nil {
						s.sharedAuthInvalidator.InvalidateAuthCacheByUserID(tick, id)
					}
					_ = s.InvalidateAPIKeyRateLimit(tick, recovered.APIKeyID)
					if recovered.Applied && recovered.Platform != "" && recovered.Cost > 0 && s.userPlatformQuotaRepo != nil && s.HasUserPlatformQuotaLimit(tick, id, recovered.Platform) {
						s.IncrementUserPlatformQuotaUsage(id, recovered.Platform, recovered.Cost)
						if s.cfg == nil || !s.cfg.Database.UserPlatformQuotaFlusherEnabled {
							if err := s.userPlatformQuotaRepo.IncrementUsageWithReset(tick, id, recovered.Platform, recovered.Cost, time.Now().UTC()); err != nil {
								slog.Error("recovered shared platform quota update failed", "user_id", id, "error", err)
							}
						}
					}
					if err := s.InvalidateUserBalance(tick, id); err != nil {
						slog.Warn("shared billing balance invalidation failed", "user_id", id, "error", err)
					}
				}
				cancel()
			}
		}
	}()
}
