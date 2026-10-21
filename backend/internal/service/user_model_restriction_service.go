package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

var (
	ErrUserModelRestrictionInvalid          = errors.New("invalid user model restriction")
	ErrUserModelRestrictionNotFound         = errors.New("user model restriction version not found")
	ErrUserModelRestrictionRevisionConflict = errors.New("user model restriction revision conflict")
	ErrUserModelRestrictionUnavailable      = errors.New("user model restriction database unavailable")
	ErrUserModelRestrictionDenied           = errors.New("user model restriction denied")
	ErrUserModelRestrictionIdentityUnknown  = errors.New("user model restriction model identity unknown")
)

// RestrictionNamespaceForPlatform keeps transport/platform names separate from
// the canonical provider namespace stored by the policy. Callers may override
// it when a Composite route has already resolved its concrete provider.
func RestrictionNamespaceForPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformAnthropic:
		return "anthropic"
	case PlatformGemini:
		return "google"
	case PlatformAntigravity, PlatformComposite:
		// 传输平台可能承载多个原厂，必须由最终模型解析提供原厂身份。
		return ""
	case PlatformOpenAI:
		return "openai"
	case PlatformGrok:
		return "xai"
	case PlatformDeepseek:
		return "deepseek"
	default:
		return strings.ToLower(strings.TrimSpace(platform))
	}
}

type UserModelRestrictionRule struct {
	RuleID         string
	Action         string
	LimitValue     *int
	StartsAt       *time.Time
	Source         string
	Namespace      string
	CanonicalModel string
	Enabled        bool
	ExpiresAt      *time.Time
	Reason         string
	CreatedBy      *int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type UserModelRestrictionSnapshot struct {
	UserID    int64
	Revision  int64
	UpdatedAt time.Time
	UpdatedBy *int64
	Rules     []UserModelRestrictionRule
}

// UserModelRestrictionRepository persists immutable snapshots and their CAS head.
type UserModelRestrictionVersion struct {
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by"`
	RuleCount int64     `json:"rule_count"`
}

type UserModelRestrictionHistory struct {
	Items              []UserModelRestrictionVersion `json:"items"`
	NextBeforeRevision *int64                        `json:"next_before_revision"`
}

type UserModelRestrictionRepository interface {
	GetVersion(ctx context.Context, userID, revision int64) (*UserModelRestrictionSnapshot, error)
	ListVersions(ctx context.Context, userID, beforeRevision int64, limit int) (*UserModelRestrictionHistory, error)
	GetSnapshot(ctx context.Context, userID int64) (*UserModelRestrictionSnapshot, error)
	ReplaceSnapshot(ctx context.Context, userID, expectedRevision int64, updatedBy *int64, rules []UserModelRestrictionRule) (*UserModelRestrictionSnapshot, error)
}

type UserModelRestrictionService struct {
	repo  UserModelRestrictionRepository
	audit ModelAccessDecisionAuditRepository
}

func NewUserModelRestrictionService(repo UserModelRestrictionRepository) *UserModelRestrictionService {
	s := &UserModelRestrictionService{repo: repo}
	if audit, ok := repo.(ModelAccessDecisionAuditRepository); ok {
		s.audit = audit
	}
	return s
}

func (s *UserModelRestrictionService) SetDecisionAuditRepository(audit ModelAccessDecisionAuditRepository) {
	if s != nil {
		s.audit = audit
	}
}

func (s *UserModelRestrictionService) RecordDecisionAudit(ctx context.Context, d ModelAccessDecisionAudit) {
	if s == nil || s.audit == nil || d.UserID <= 0 {
		return
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	// Client disconnects must not erase a denial; keep this best-effort write
	// bounded so a broken audit sink cannot stall provider admission.
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := s.audit.RecordModelAccessDecision(auditCtx, d); err != nil {
		slog.Warn("model access decision audit failed", "user_id", d.UserID, "decision", d.Decision, "error", err)
	}
}

func (s *UserModelRestrictionService) Get(ctx context.Context, userID int64) (*UserModelRestrictionSnapshot, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	snapshot, err := s.repo.GetSnapshot(ctx, userID)
	if err != nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return snapshot, nil
}

// Replace atomically creates revision expectedRevision+1. An empty rules slice
// is intentional and still advances the immutable package revision.
func (s *UserModelRestrictionService) Replace(ctx context.Context, userID, expectedRevision int64, updatedBy *int64, rules []UserModelRestrictionRule) (*UserModelRestrictionSnapshot, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	if userID <= 0 || expectedRevision < 0 || expectedRevision == math.MaxInt64 || updatedBy == nil || *updatedBy <= 0 || rules == nil || len(rules) > 1000 {
		return nil, ErrUserModelRestrictionInvalid
	}
	// 复制输入，避免校验或规范化改变调用者持有的规则快照。
	normalized := make([]UserModelRestrictionRule, len(rules))
	seen := make(map[string]struct{}, len(rules))
	seenIDs := make(map[string]bool, len(rules))
	for i, rule := range rules {
		if rule.Action == "" {
			rule.Action = "deny_model"
		}
		if rule.Source != "" && rule.Source != "manual" {
			return nil, ErrUserModelRestrictionInvalid
		}
		rule.Source = "manual"
		if rule.RuleID != "" {
			if _, err := uuid.Parse(rule.RuleID); err != nil {
				return nil, ErrUserModelRestrictionInvalid
			}
			if seenIDs[rule.RuleID] {
				return nil, ErrUserModelRestrictionInvalid
			}
			seenIDs[rule.RuleID] = true
		}
		switch rule.Action {
		case "deny_model":
			if rule.LimitValue != nil {
				return nil, ErrUserModelRestrictionInvalid
			}
		case "cap_concurrency", "cap_rpm":
			if rule.LimitValue == nil || *rule.LimitValue <= 0 {
				return nil, ErrUserModelRestrictionInvalid
			}
		default:
			return nil, ErrUserModelRestrictionInvalid
		}
		if rule.StartsAt != nil && rule.ExpiresAt != nil && !rule.ExpiresAt.After(*rule.StartsAt) {
			return nil, ErrUserModelRestrictionInvalid
		}
		if !validRestrictionText(rule.Namespace, 64) || !validRestrictionText(rule.CanonicalModel, 255) || !validRestrictionText(rule.Reason, 2000) {
			return nil, fmt.Errorf("%w: invalid rule text", ErrUserModelRestrictionInvalid)
		}
		rule.Namespace = normalizeRestrictionNamespace(rule.Namespace)
		rule.CanonicalModel = normalizeRestrictionModel(rule.CanonicalModel)
		if rule.Namespace == "" || rule.CanonicalModel == "" {
			return nil, fmt.Errorf("%w: namespace and canonical_model are required", ErrUserModelRestrictionInvalid)
		}
		key := rule.Namespace + "\x00" + rule.CanonicalModel + "\x00" + rule.Action
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate model rule", ErrUserModelRestrictionInvalid)
		}
		seen[key] = struct{}{}
		// 此字段表示版本快照的写入人，不能信任客户端携带的身份。
		actor := *updatedBy
		rule.CreatedBy = &actor
		normalized[i] = rule
	}
	rules = normalized
	snapshot, err := s.repo.ReplaceSnapshot(ctx, userID, expectedRevision, updatedBy, rules)
	if err != nil {
		if errors.Is(err, ErrUserModelRestrictionRevisionConflict) || errors.Is(err, ErrUserModelRestrictionInvalid) {
			return nil, err
		}
		return nil, ErrUserModelRestrictionUnavailable
	}
	return snapshot, nil
}

// CheckCanonical is the final synchronous authorization check. It intentionally
// compares the already-resolved namespace/canonical pair and treats unknown
// models as undecidable rather than guessing a billing alias.
func (s *UserModelRestrictionService) CheckCanonical(ctx context.Context, userID int64, namespace, canonical string, now time.Time) (bool, error) {
	_, err := s.CheckModels(ctx, userID, []ModelIdentityInput{{Namespace: namespace, Model: canonical}})
	if errors.Is(err, ErrUserModelRestrictionDenied) {
		return false, nil
	}
	return err == nil, err
}

// CheckRequest performs conservative name-based early denial before routing.
// A nil result is not a final authorization: the resolved upstream identity must
// still pass CheckCanonical immediately before forwarding.
func (s *UserModelRestrictionService) CheckRequest(ctx context.Context, userID int64, platform, requestedModel, mappedModel string, now time.Time) error {
	requested := normalizeRestrictionModel(requestedModel)
	mapped := normalizeRestrictionModel(mappedModel)
	if requested == "" && mapped == "" {
		return ErrUserModelRestrictionIdentityUnknown
	}
	snapshot, err := s.Get(ctx, userID)
	if err != nil {
		return err
	}
	// 尚未完成原厂解析时，按两个已知名称拒绝；不能把代理平台当成原厂绕过限制。
	for _, rule := range snapshot.Rules {
		if !restrictionRuleActive(rule, now) || (rule.Action != "" && rule.Action != "deny_model") {
			continue
		}
		model := normalizeRestrictionModel(rule.CanonicalModel)
		if model != "" && (model == requested || model == mapped) {
			requestID, _ := ctx.Value(ctxkey.RequestID).(string)
			s.RecordDecisionAudit(ctx, ModelAccessDecisionAudit{RequestID: requestID, OperationID: ModelExecutionOperationID(ctx), UserID: userID, PolicyRevision: snapshot.Revision, Decision: "denied", ReasonCode: "public_model_denied", Identities: []CanonicalModelIdentity{{Namespace: rule.Namespace, CanonicalModel: rule.CanonicalModel}}})
			return ErrUserModelRestrictionDenied
		}
	}
	return nil
}

func restrictionRuleActive(rule UserModelRestrictionRule, now time.Time) bool {
	return rule.Enabled && (rule.StartsAt == nil || !rule.StartsAt.After(now)) && (rule.ExpiresAt == nil || rule.ExpiresAt.After(now))
}

func normalizeRestrictionNamespace(namespace string) string {
	return strings.ToLower(strings.TrimSpace(namespace))
}

func normalizeRestrictionModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func validRestrictionText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}

func (s *UserModelRestrictionService) GetVersion(ctx context.Context, userID, revision int64) (*UserModelRestrictionSnapshot, error) {
	if userID <= 0 || revision <= 0 {
		return nil, ErrUserModelRestrictionInvalid
	}
	if s == nil || s.repo == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	result, err := s.repo.GetVersion(ctx, userID, revision)
	if errors.Is(err, ErrUserModelRestrictionNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return result, nil
}

func (s *UserModelRestrictionService) History(ctx context.Context, userID, beforeRevision int64, limit int) (*UserModelRestrictionHistory, error) {
	if userID <= 0 || beforeRevision < 0 || limit < 1 || limit > 100 {
		return nil, ErrUserModelRestrictionInvalid
	}
	if s == nil || s.repo == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	result, err := s.repo.ListVersions(ctx, userID, beforeRevision, limit)
	if err != nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return result, nil
}
