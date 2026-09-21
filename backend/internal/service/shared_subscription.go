package service

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const SharedSubscriptionOrderType = "shared_subscription"
const BillingTypeSharedSubscription int8 = 2

var ErrSharedSubscriptionNotFound = errors.NotFound("SHARED_SUBSCRIPTION_NOT_FOUND", "shared subscription not found")
var ErrSharedPlanNotFound = errors.NotFound("SHARED_PLAN_NOT_FOUND", "shared subscription plan not found")

type SharedSubscriptionPlan struct {
	ID           int64            `json:"id"`
	Version      int              `json:"version"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Price        float64          `json:"price"`
	ValidityDays int              `json:"validity_days"`
	GroupIDs     []int64          `json:"group_ids"`
	GroupNames   map[int64]string `json:"group_names,omitempty"`
	DailyLimit   *float64         `json:"daily_limit_usd"`
	WeeklyLimit  *float64         `json:"weekly_limit_usd"`
	MonthlyLimit *float64         `json:"monthly_limit_usd"`
	ForSale      bool             `json:"for_sale"`
}

type SharedSubscription struct {
	ID         int64                  `json:"id"`
	UserID     int64                  `json:"user_id"`
	UserEmail  string                 `json:"user_email,omitempty"`
	Username   string                 `json:"username,omitempty"`
	PlanID     int64                  `json:"plan_id"`
	Plan       SharedSubscriptionPlan `json:"plan"`
	StartsAt   time.Time              `json:"starts_at"`
	ExpiresAt  time.Time              `json:"expires_at"`
	Status     string                 `json:"status"`
	Generation int                    `json:"generation"`
	// Missing key means inherit the live plan, a null value means unlimited,
	// and a numeric value is a per-subscription limit.
	QuotaOverrides map[string]*float64 `json:"quota_overrides"`
	Windows        []SharedQuotaWindow `json:"windows"`
}

type SharedQuotaValue struct {
	Mode  string   `json:"mode"`
	Value *float64 `json:"value,omitempty"`
}

type SharedQuotaUpdate struct {
	Generation int                         `json:"generation"`
	Limits     map[string]SharedQuotaValue `json:"limits"`
	Used       map[string]float64          `json:"used"`
	Reason     string                      `json:"reason"`
	OperatorID int64                       `json:"-"`
}

type SharedQuotaWindow struct {
	Kind     string    `json:"kind"`
	StartsAt time.Time `json:"starts_at"`
	ResetsAt time.Time `json:"resets_at"`
	Limit    *float64  `json:"limit"`
	Used     float64   `json:"used"`
	Reserved float64   `json:"reserved"`
}

type SharedPoolAuthorization struct {
	ID         int64               `json:"id"`
	Generation int                 `json:"generation"`
	Windows    []SharedQuotaWindow `json:"windows"`
}

// SharedFunding is a per-request immutable authorization, never stored in an auth cache.
type SharedFunding struct {
	UserID    int64                     `json:"user_id"`
	GroupID   int64                     `json:"group_id"`
	BillingAt time.Time                 `json:"billing_at"`
	Pools     []SharedPoolAuthorization `json:"pools"`
	Available bool                      `json:"available"`
}

type SharedSettlement struct {
	RequestID        string    `json:"request_id"`
	APIKeyID         int64     `json:"api_key_id"`
	UserID           int64     `json:"user_id"`
	GroupID          int64     `json:"group_id"`
	Cost             float64   `json:"cost"`
	SubscriptionCost float64   `json:"subscription_cost"`
	BalanceCost      float64   `json:"balance_cost"`
	BillingAt        time.Time `json:"billing_at"`
}

type SharedSubscriptionFilter struct {
	UserID    int64
	PlanID    int64
	Status    string
	BeforeID  int64
	Limit     int
	Page      int
	Search    string
	SortBy    string
	SortOrder string
}

type SharedSubscriptionPage struct {
	Items    []SharedSubscription `json:"items"`
	HasMore  bool                 `json:"has_more"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

type SharedSubscriptionRepository interface {
	ListPage(context.Context, SharedSubscriptionFilter) (*SharedSubscriptionPage, error)
	ChangePlan(context.Context, int64, int64, int) error
	SaveOrder(context.Context, int64, *SharedSubscriptionPlan, int64, int64) error
	FulfillOrder(context.Context, int64) error
	RefundOrder(context.Context, int64, bool) error
	GroupIDs(context.Context, int64) ([]int64, error)
	ListPlans(context.Context, bool) ([]SharedSubscriptionPlan, error)
	GetPlan(context.Context, int64) (*SharedSubscriptionPlan, error)
	SavePlan(context.Context, *SharedSubscriptionPlan) error
	DeletePlan(context.Context, int64) error
	List(context.Context, int64, int) ([]SharedSubscription, error)
	Get(context.Context, int64) (*SharedSubscription, error)
	Authorize(context.Context, int64, int64, time.Time) (*SharedFunding, error)
	Assign(context.Context, int64, *SharedSubscriptionPlan, string) (*SharedSubscription, error)
	Action(context.Context, int64, string, int) error
	UpdateQuota(context.Context, int64, SharedQuotaUpdate) error
	History(context.Context, int64, int) ([]SharedSettlement, error)
}

type SharedSubscriptionService struct {
	repo   SharedSubscriptionRepository
	groups GroupRepository
	users  UserRepository
}

func NewSharedSubscriptionService(repo SharedSubscriptionRepository, groups GroupRepository, users UserRepository) *SharedSubscriptionService {
	return &SharedSubscriptionService{repo: repo, groups: groups, users: users}
}
func (s *SharedSubscriptionService) ListPlans(ctx context.Context, sale bool) ([]SharedSubscriptionPlan, error) {
	return s.repo.ListPlans(ctx, sale)
}
func (s *SharedSubscriptionService) GetPlan(ctx context.Context, id int64) (*SharedSubscriptionPlan, error) {
	return s.repo.GetPlan(ctx, id)
}
func (s *SharedSubscriptionService) List(ctx context.Context, userID int64, limit int) ([]SharedSubscription, error) {
	return s.repo.List(ctx, userID, limit)
}
func (s *SharedSubscriptionService) ListPage(ctx context.Context, f SharedSubscriptionFilter) (*SharedSubscriptionPage, error) {
	if f.UserID < 0 || f.PlanID < 0 || f.BeforeID < 0 {
		return nil, errors.BadRequest("INVALID_FILTER", "invalid subscription filter")
	}
	switch f.Status {
	case "", "active", "paused", "revoked", "expired":
	default:
		return nil, errors.BadRequest("INVALID_STATUS", "invalid status")
	}
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Page < 0 || f.Page > 1000000 || len(f.Search) > 200 {
		return nil, errors.BadRequest("INVALID_FILTER", "invalid page or search")
	}
	if f.Page == 0 {
		f.Page = 1
	}
	f.Search = strings.TrimSpace(f.Search)
	switch f.SortBy {
	case "", "id", "user_email", "plan", "status", "starts_at", "expires_at":
	default:
		return nil, errors.BadRequest("INVALID_SORT", "invalid sort column")
	}
	if f.SortOrder != "" && f.SortOrder != "asc" && f.SortOrder != "desc" {
		return nil, errors.BadRequest("INVALID_SORT", "invalid sort order")
	}
	return s.repo.ListPage(ctx, f)
}

// Changing a subscription preserves its expiry and status, but starts fresh quota
// windows. The generation check prevents retries from resetting it a second time.
func (s *SharedSubscriptionService) ChangePlan(ctx context.Context, id, planID int64, generation int) error {
	if planID <= 0 || generation < 0 {
		return errors.BadRequest("INVALID_PLAN_CHANGE", "invalid target plan or generation")
	}
	return s.repo.ChangePlan(ctx, id, planID, generation)
}
func (s *SharedSubscriptionService) Get(ctx context.Context, id int64) (*SharedSubscription, error) {
	return s.repo.Get(ctx, id)
}
func (s *SharedSubscriptionService) History(ctx context.Context, userID int64, limit int) ([]SharedSettlement, error) {
	return s.repo.History(ctx, userID, limit)
}
func (s *SharedSubscriptionService) DeletePlan(ctx context.Context, id int64) error {
	return s.repo.DeletePlan(ctx, id)
}
func (s *SharedSubscriptionService) Authorize(ctx context.Context, userID, groupID int64) (*SharedFunding, error) {
	if s == nil {
		return nil, nil
	}
	return s.repo.Authorize(ctx, userID, groupID, time.Now())
}
func (s *SharedSubscriptionService) SavePlan(ctx context.Context, p *SharedSubscriptionPlan) error {
	if err := ValidateSharedPlan(p); err != nil {
		return err
	}
	p.GroupNames = make(map[int64]string, len(p.GroupIDs))
	for _, id := range p.GroupIDs {
		g, err := s.groups.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if !g.IsActive() {
			return errors.BadRequest("SHARED_GROUP_INACTIVE", "a selected group is inactive")
		}
		p.GroupNames[id] = g.Name
	}
	return s.repo.SavePlan(ctx, p)
}
func ValidateSharedPlan(p *SharedSubscriptionPlan) error {
	if p == nil {
		return errors.BadRequest("SHARED_PLAN_INVALID", "plan is required")
	}
	p.Name = strings.TrimSpace(p.Name)
	if len(p.Name) == 0 || len(p.Name) > 100 || len(p.Description) > 10000 || p.ValidityDays < 1 || p.ValidityDays > 3650 || math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price <= 0 || p.Price > 1e10 {
		return errors.BadRequest("SHARED_PLAN_INVALID", "invalid name, price or validity days")
	}
	for _, v := range []*float64{p.DailyLimit, p.WeeklyLimit, p.MonthlyLimit} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v <= 0 || *v > 1e10) {
			return errors.BadRequest("SHARED_LIMIT_INVALID", "limits must be positive or null (unlimited)")
		}
	}
	if len(p.GroupIDs) == 0 || len(p.GroupIDs) > 500 {
		return errors.BadRequest("SHARED_GROUP_REQUIRED", "select between 1 and 500 groups")
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(p.GroupIDs))
	for _, id := range p.GroupIDs {
		if id <= 0 {
			return errors.BadRequest("SHARED_GROUP_INVALID", "invalid group")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	p.GroupIDs = ids
	p.Price = QuantizeUsageBillingAmount(p.Price)
	if p.Price <= 0 {
		return errors.BadRequest("SHARED_PLAN_INVALID", "price is below monetary precision")
	}
	for _, v := range []*float64{p.DailyLimit, p.WeeklyLimit, p.MonthlyLimit} {
		if v != nil {
			*v = QuantizeUsageBillingAmount(*v)
			if *v <= 0 {
				return errors.BadRequest("SHARED_LIMIT_INVALID", "limit is below monetary precision")
			}
		}
	}
	return nil
}
func (s *SharedSubscriptionService) Assign(ctx context.Context, userID, planID int64, requestID string) (*SharedSubscription, error) {
	if requestID == "" || len(requestID) > 100 {
		return nil, errors.BadRequest("IDEMPOTENCY_REQUIRED", "request_id is required")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !u.IsActive() {
		return nil, errors.BadRequest("USER_INACTIVE", "user is inactive")
	}
	p, err := s.repo.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	return s.repo.Assign(ctx, userID, p, requestID)
}
func (s *SharedSubscriptionService) Action(ctx context.Context, id int64, action string, days int) error {
	switch action {
	case "pause", "resume", "revoke", "reset":
	case "extend":
		if days < 1 || days > 3650 {
			return errors.BadRequest("INVALID_DAYS", "days must be 1..3650")
		}
	default:
		return errors.BadRequest("INVALID_ACTION", "unknown subscription action")
	}
	return s.repo.Action(ctx, id, action, days)
}

func (s *SharedSubscriptionService) UpdateQuota(ctx context.Context, id int64, update SharedQuotaUpdate) error {
	if id <= 0 || update.Generation < 0 || update.OperatorID <= 0 {
		return errors.BadRequest("SHARED_QUOTA_INVALID", "invalid subscription quota update")
	}
	update.Reason = strings.TrimSpace(update.Reason)
	if update.Reason == "" || len(update.Reason) > 500 {
		return errors.BadRequest("SHARED_QUOTA_REASON_REQUIRED", "a quota adjustment reason is required")
	}
	for _, kind := range []string{"daily", "weekly", "monthly"} {
		limit, ok := update.Limits[kind]
		if !ok {
			return errors.BadRequest("SHARED_QUOTA_INVALID", "all quota limit modes are required")
		}
		switch limit.Mode {
		case "inherit", "unlimited":
		case "custom":
			if limit.Value == nil || math.IsNaN(*limit.Value) || math.IsInf(*limit.Value, 0) || *limit.Value <= 0 || *limit.Value > 1e10 {
				return errors.BadRequest("SHARED_LIMIT_INVALID", "custom limits must be positive")
			}
			v := QuantizeUsageBillingAmount(*limit.Value)
			if v <= 0 {
				return errors.BadRequest("SHARED_LIMIT_INVALID", "custom limit is below monetary precision")
			}
			limit.Value = &v
			update.Limits[kind] = limit
		default:
			return errors.BadRequest("SHARED_QUOTA_INVALID", "invalid quota limit mode")
		}
		used, ok := update.Used[kind]
		if !ok || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 1e10 {
			return errors.BadRequest("SHARED_USAGE_INVALID", "used quota must be zero or positive")
		}
		update.Used[kind] = QuantizeUsageBillingAmount(used)
	}
	return s.repo.UpdateQuota(ctx, id, update)
}

func ApplySharedQuotaOverrides(p SharedSubscriptionPlan, overrides map[string]*float64) SharedSubscriptionPlan {
	for kind, value := range overrides {
		switch kind {
		case "daily":
			p.DailyLimit = value
		case "weekly":
			p.WeeklyLimit = value
		case "monthly":
			p.MonthlyLimit = value
		}
	}
	return p
}
func SharedWindows(p SharedSubscriptionPlan, start, at time.Time) []SharedQuotaWindow {
	daily := timezone.StartOfDay(at)
	week := start.Add(time.Duration(math.Floor(at.Sub(start).Hours()/(24*7))) * 7 * 24 * time.Hour)
	month := start.Add(time.Duration(math.Floor(at.Sub(start).Hours()/(24*30))) * 30 * 24 * time.Hour)
	return []SharedQuotaWindow{{Kind: "daily", StartsAt: daily, ResetsAt: daily.AddDate(0, 0, 1), Limit: p.DailyLimit}, {Kind: "weekly", StartsAt: week, ResetsAt: week.Add(7 * 24 * time.Hour), Limit: p.WeeklyLimit}, {Kind: "monthly", StartsAt: month, ResetsAt: month.Add(30 * 24 * time.Hour), Limit: p.MonthlyLimit}}
}
func SharedAvailable(windows []SharedQuotaWindow) float64 {
	remaining := math.Inf(1)
	for _, w := range windows {
		if w.Limit != nil {
			remaining = math.Min(remaining, *w.Limit-w.Used-w.Reserved)
		}
	}
	return math.Max(0, remaining)
}
