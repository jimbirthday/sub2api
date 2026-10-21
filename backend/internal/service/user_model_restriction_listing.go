package service

import (
	"context"
	"time"
)

type ModelRestrictionListOptions struct {
	BeforeID int64
	UserID   int64
	Limit    int
	Search   string
	Model    string
	State    string
	Source   string
}

type ModelRestrictionListItem struct {
	Action         string     `json:"action"`
	LimitValue     *int       `json:"limit_value"`
	StartsAt       *time.Time `json:"starts_at"`
	Source         string     `json:"source"`
	ID             int64      `json:"id"`
	UserID         int64      `json:"user_id"`
	UserEmail      string     `json:"user_email"`
	Revision       int64      `json:"revision"`
	Namespace      string     `json:"namespace"`
	CanonicalModel string     `json:"canonical_model"`
	Enabled        bool       `json:"enabled"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Reason         string     `json:"reason"`
	CreatedBy      *int64     `json:"created_by"`
	ActiveNow      bool       `json:"active_now"`
}

type ModelRestrictionList struct {
	Items        []ModelRestrictionListItem `json:"items"`
	NextBeforeID *int64                     `json:"next_before_id"`
}

type modelRestrictionLister interface {
	ListCurrentRestrictions(context.Context, ModelRestrictionListOptions) (*ModelRestrictionList, error)
}

func (s *UserModelRestrictionService) ListCurrent(ctx context.Context, options ModelRestrictionListOptions) (*ModelRestrictionList, error) {
	if options.BeforeID < 0 || options.UserID < 0 || options.Limit < 1 || options.Limit > 100 || len(options.Search) > 200 || len(options.Model) > 255 {
		return nil, ErrUserModelRestrictionInvalid
	}
	if options.Source != "" && options.Source != "manual" && options.Source != "behavior" {
		return nil, ErrUserModelRestrictionInvalid
	}
	switch options.State {
	case "", "active", "disabled", "expired", "pending":
	default:
		return nil, ErrUserModelRestrictionInvalid
	}
	if s == nil || s.repo == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	repo, ok := s.repo.(modelRestrictionLister)
	if !ok {
		return nil, ErrUserModelRestrictionUnavailable
	}
	result, err := repo.ListCurrentRestrictions(ctx, options)
	if err != nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return result, nil
}
