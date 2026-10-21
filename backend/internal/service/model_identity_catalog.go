package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// ModelIdentityInput 的原厂与传输平台分开传入；不能从账户平台或计费名称猜原厂。
type ModelIdentityInput struct {
	Protocol        string `json:"protocol,omitempty"`
	Role            string `json:"role,omitempty"`
	ProviderContext string `json:"provider_context,omitempty"`
	Namespace       string `json:"namespace"`
	Transport       string `json:"transport"`
	Model           string `json:"model"`
}

type CanonicalModelIdentity struct {
	Namespace      string `json:"namespace"`
	CanonicalModel string `json:"canonical_model"`
}

type ModelIdentityCatalogEntry struct {
	Namespace      string `json:"namespace"`
	CanonicalModel string `json:"canonical_model"`
	DisplayName    string `json:"display_name"`
	Source         string `json:"source"`
}

type ModelIdentityAlias struct {
	AliasID         string `json:"alias_id,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	Role            string `json:"role,omitempty"`
	ProviderContext string `json:"provider_context,omitempty"`
	Namespace       string `json:"namespace"`
	Transport       string `json:"transport"`
	Alias           string `json:"alias"`
	CanonicalModel  string `json:"canonical_model"`
}

type ModelIdentityCatalog struct {
	Revision  int64                       `json:"revision"`
	UpdatedAt time.Time                   `json:"updated_at"`
	UpdatedBy *int64                      `json:"updated_by"`
	Models    []ModelIdentityCatalogEntry `json:"models"`
	Aliases   []ModelIdentityAlias        `json:"aliases"`
}

type UserModelRestrictionDecision struct {
	ModelCaps       map[string]ModelRestrictionCaps `json:"model_caps,omitempty"`
	PolicyRevision  int64                           `json:"policy_revision"`
	CatalogRevision int64                           `json:"catalog_revision"`
	Identities      []CanonicalModelIdentity        `json:"identities"`
}

type ModelRestrictionCaps struct {
	Concurrency int `json:"concurrency"`
	RPM         int `json:"rpm"`
}

// UserModelRestrictionListingResult reports which requested model names may be
// advertised. A false entry is either explicitly denied or cannot be resolved
// uniquely while the user has an active restriction package.
type UserModelRestrictionListingResult struct {
	PolicyRevision  int64  `json:"policy_revision"`
	CatalogRevision int64  `json:"catalog_revision"`
	Allowed         []bool `json:"allowed"`
}

// ModelIdentityAdmissionSnapshot 由一次主库 SQL 的语句快照返回，所有依赖模型共享同一版本。
type ModelIdentityAdmissionSnapshot struct {
	PolicyRevision  int64
	CatalogRevision int64
	CheckedAt       time.Time
	ActiveRules     []UserModelRestrictionRule
	Candidates      [][]CanonicalModelIdentity
}

type ModelIdentityRepository interface {
	GetModelIdentityCatalog(context.Context) (*ModelIdentityCatalog, error)
	ReplaceModelIdentityCatalog(context.Context, int64, int64, []ModelIdentityCatalogEntry, []ModelIdentityAlias) (*ModelIdentityCatalog, error)
	GetModelIdentityAdmission(context.Context, int64, []ModelIdentityInput) (*ModelIdentityAdmissionSnapshot, error)
}

func (s *UserModelRestrictionService) identityRepository() (ModelIdentityRepository, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	repo, ok := s.repo.(ModelIdentityRepository)
	if !ok {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return repo, nil
}

func (s *UserModelRestrictionService) Catalog(ctx context.Context) (*ModelIdentityCatalog, error) {
	repo, err := s.identityRepository()
	if err != nil {
		return nil, err
	}
	result, err := repo.GetModelIdentityCatalog(ctx)
	if err != nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return result, nil
}

var customModelNamespacePattern = regexp.MustCompile(`^custom:[a-z0-9][a-z0-9_-]{0,55}$`)

func validModelOrigin(namespace string) bool {
	switch namespace {
	case "openai", "anthropic", "google", "xai", "deepseek", "moonshot", "zhipu", "minimax", "alibaba", "baidu", "mistral", "meta", "cohere", "amazon", "ai21":
		return true
	default:
		return customModelNamespacePattern.MatchString(namespace)
	}
}

func NormalizeModelIdentityCatalog(models []ModelIdentityCatalogEntry, aliases []ModelIdentityAlias) ([]ModelIdentityCatalogEntry, []ModelIdentityAlias, error) {
	invalid := func(reason string) ([]ModelIdentityCatalogEntry, []ModelIdentityAlias, error) {
		return nil, nil, fmt.Errorf("%w: %s", ErrUserModelRestrictionInvalid, reason)
	}
	if models == nil || aliases == nil || len(models) > 10000 || len(aliases) > 30000 {
		return invalid("catalog arrays required; maximum 10000 models and 30000 aliases")
	}
	normalizedModels := make([]ModelIdentityCatalogEntry, len(models))
	normalizedAliases := make([]ModelIdentityAlias, len(aliases))
	identities := make(map[string]struct{}, len(models))
	for i, model := range models {
		if !validRestrictionText(model.Namespace, 64) || !validRestrictionText(model.CanonicalModel, 255) || !validRestrictionText(model.DisplayName, 255) || !validRestrictionText(model.Source, 500) {
			return invalid("invalid catalog text")
		}
		model.Namespace = normalizeRestrictionNamespace(model.Namespace)
		model.CanonicalModel = normalizeRestrictionModel(model.CanonicalModel)
		model.DisplayName = strings.TrimSpace(model.DisplayName)
		model.Source = strings.TrimSpace(model.Source)
		if !validModelOrigin(model.Namespace) || model.CanonicalModel == "" || strings.ContainsAny(model.CanonicalModel, "*?") || model.Source == "" {
			return invalid("registered origin, exact model and source are required")
		}
		key := model.Namespace + "\x00" + model.CanonicalModel
		if _, exists := identities[key]; exists {
			return invalid("duplicate canonical identity")
		}
		identities[key] = struct{}{}
		normalizedModels[i] = model
	}
	aliasesSeen := make(map[string]struct{}, len(aliases))
	aliasIDs := make(map[string]struct{}, len(aliases))
	for i, alias := range aliases {
		if !validRestrictionText(alias.Namespace, 64) || !validRestrictionText(alias.CanonicalModel, 255) || !validRestrictionText(alias.Alias, 255) || !validRestrictionText(alias.Transport, 64) {
			return invalid("invalid alias text")
		}
		if !validRestrictionText(alias.Protocol, 64) || !validRestrictionText(alias.Role, 32) || !validRestrictionText(alias.ProviderContext, 128) {
			return invalid("invalid alias context")
		}
		if alias.AliasID != "" {
			if _, err := uuid.Parse(alias.AliasID); err != nil {
				return invalid("invalid alias ID")
			}
		}
		if alias.AliasID != "" {
			alias.AliasID = strings.ToLower(alias.AliasID)
			if _, exists := aliasIDs[alias.AliasID]; exists {
				return invalid("duplicate alias ID")
			}
			aliasIDs[alias.AliasID] = struct{}{}
		}
		alias.Protocol = strings.ToLower(strings.TrimSpace(alias.Protocol))
		alias.Role = strings.ToLower(strings.TrimSpace(alias.Role))
		alias.ProviderContext = strings.ToLower(strings.TrimSpace(alias.ProviderContext))
		alias.Namespace = normalizeRestrictionNamespace(alias.Namespace)
		alias.Transport = strings.ToLower(strings.TrimSpace(alias.Transport))
		alias.Alias = normalizeRestrictionModel(alias.Alias)
		alias.CanonicalModel = normalizeRestrictionModel(alias.CanonicalModel)
		if alias.Alias == "" || strings.ContainsAny(alias.Alias, "*?") {
			return invalid("aliases must be exact names")
		}
		if alias.Transport != "" && !domain.IsGroupPlatform(alias.Transport) {
			return invalid("unknown alias transport")
		}
		if _, ok := identities[alias.Namespace+"\x00"+alias.CanonicalModel]; !ok {
			return invalid("alias target is not registered in this catalog")
		}
		// 同一原厂、传输范围和别名只能指向一个目标；唯一键不包含目标模型。
		key := alias.Namespace + "\x00" + alias.Transport + "\x00" + alias.Protocol + "\x00" + alias.Role + "\x00" + alias.ProviderContext + "\x00" + alias.Alias
		if _, exists := aliasesSeen[key]; exists {
			return invalid("duplicate alias scope")
		}
		aliasesSeen[key] = struct{}{}
		if _, exists := identities[alias.Namespace+"\x00"+alias.Alias]; exists && alias.Alias != alias.CanonicalModel {
			return invalid("alias shadows a canonical model")
		}
		normalizedAliases[i] = alias
	}
	// Contexts overlap only if every nonempty dimension agrees. Distinct
	// protocol/role/provider contexts may intentionally reuse the same alias.
	groups := make(map[string][]ModelIdentityAlias)
	for _, alias := range normalizedAliases {
		key := alias.Namespace + "\x00" + alias.Alias
		for _, previous := range groups[key] {
			overlap := func(a, b string) bool { return a == "" || b == "" || a == b }
			if previous.CanonicalModel != alias.CanonicalModel && overlap(previous.Transport, alias.Transport) && overlap(previous.Protocol, alias.Protocol) && overlap(previous.Role, alias.Role) && overlap(previous.ProviderContext, alias.ProviderContext) {
				return invalid("overlapping alias contexts resolve to different models")
			}
		}
		groups[key] = append(groups[key], alias)
	}
	return normalizedModels, normalizedAliases, nil
}

func (s *UserModelRestrictionService) ReplaceCatalog(ctx context.Context, expectedRevision, actor int64, models []ModelIdentityCatalogEntry, aliases []ModelIdentityAlias) (*ModelIdentityCatalog, error) {
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 || actor <= 0 {
		return nil, ErrUserModelRestrictionInvalid
	}
	models, aliases, err := NormalizeModelIdentityCatalog(models, aliases)
	if err != nil {
		return nil, err
	}
	repo, err := s.identityRepository()
	if err != nil {
		return nil, err
	}
	result, err := repo.ReplaceModelIdentityCatalog(ctx, expectedRevision, actor, models, aliases)
	if err == ErrUserModelRestrictionRevisionConflict || err == ErrUserModelRestrictionInvalid {
		return nil, err
	}
	if err != nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return result, nil
}

func (s *UserModelRestrictionService) CheckModels(ctx context.Context, userID int64, inputs []ModelIdentityInput) (decision *UserModelRestrictionDecision, resultErr error) {
	startedAt := time.Now()
	defer func() {
		if s == nil || s.audit == nil || userID <= 0 {
			return
		}
		outcome := "allowed"
		reason := ""
		if resultErr != nil {
			switch resultErr {
			case ErrUserModelRestrictionDenied:
				outcome, reason = "denied", "policy_denied"
			case ErrUserModelRestrictionIdentityUnknown:
				outcome, reason = "unresolved", "identity_unresolved"
			default:
				outcome, reason = "unavailable", "policy_unavailable"
			}
		}
		requestID, _ := ctx.Value(ctxkey.RequestID).(string)
		if strings.TrimSpace(requestID) == "" {
			requestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		}
		s.RecordDecisionAudit(ctx, ModelAccessDecisionAudit{
			RequestID: strings.TrimSpace(requestID), OperationID: ModelExecutionOperationID(ctx), UserID: userID,
			PolicyRevision: decisionRevision(decision, true), CatalogRevision: decisionRevision(decision, false),
			Decision: outcome, ReasonCode: reason, Identities: decisionIdentities(decision), Latency: time.Since(startedAt),
		})
	}()
	if userID <= 0 || len(inputs) > 64 {
		return nil, ErrUserModelRestrictionInvalid
	}
	normalized := make([]ModelIdentityInput, len(inputs))
	for i, input := range inputs {
		if !validRestrictionText(input.Protocol, 64) || !validRestrictionText(input.Role, 32) || !validRestrictionText(input.ProviderContext, 128) || !validRestrictionText(input.Namespace, 64) || !validRestrictionText(input.Transport, 64) || !validRestrictionText(input.Model, 255) {
			return nil, ErrUserModelRestrictionIdentityUnknown
		}
		normalized[i] = ModelIdentityInput{Protocol: strings.ToLower(strings.TrimSpace(input.Protocol)),
			Role:            strings.ToLower(strings.TrimSpace(input.Role)),
			ProviderContext: strings.ToLower(strings.TrimSpace(input.ProviderContext)),
			Namespace:       normalizeRestrictionNamespace(input.Namespace), Transport: strings.ToLower(strings.TrimSpace(input.Transport)), Model: normalizeRestrictionModel(input.Model)}
	}
	repo, err := s.identityRepository()
	if err != nil {
		return nil, err
	}
	snapshot, err := repo.GetModelIdentityAdmission(ctx, userID, normalized)
	if err != nil || snapshot == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	decision = &UserModelRestrictionDecision{PolicyRevision: snapshot.PolicyRevision, CatalogRevision: snapshot.CatalogRevision, Identities: []CanonicalModelIdentity{}, ModelCaps: map[string]ModelRestrictionCaps{}}
	// 未设置有效禁用时，未知的新模型保持兼容；有禁用时，所有依赖必须唯一解析。
	unresolved := len(normalized) == 0 || len(snapshot.Candidates) != len(normalized)
	denied := false
	// 历史规则若引用已删除或歧义身份，不能悄悄变成永不命中的禁用。
	for _, rule := range snapshot.ActiveRules {
		if rule.Namespace == "" || rule.CanonicalModel == "" {
			unresolved = true
		}
	}
	seen := make(map[string]struct{})
	for _, candidates := range snapshot.Candidates {
		if len(candidates) != 1 {
			unresolved = true
			continue
		}
		identity := candidates[0]
		key := identity.Namespace + "\x00" + identity.CanonicalModel
		if _, ok := seen[key]; !ok {
			decision.Identities = append(decision.Identities, identity)
			seen[key] = struct{}{}
		}
		for _, rule := range snapshot.ActiveRules {
			if restrictionRuleActive(rule, snapshot.CheckedAt) && normalizeRestrictionNamespace(rule.Namespace) == identity.Namespace && normalizeRestrictionModel(rule.CanonicalModel) == identity.CanonicalModel {
				if rule.Action == "" || rule.Action == "deny_model" {
					denied = true
				} else if rule.LimitValue != nil && *rule.LimitValue > 0 {
					capKey := identity.Namespace + ":" + identity.CanonicalModel
					caps := decision.ModelCaps[capKey]
					if rule.Action == "cap_concurrency" && (caps.Concurrency == 0 || *rule.LimitValue < caps.Concurrency) {
						caps.Concurrency = *rule.LimitValue
					}
					if rule.Action == "cap_rpm" && (caps.RPM == 0 || *rule.LimitValue < caps.RPM) {
						caps.RPM = *rule.LimitValue
					}
					decision.ModelCaps[capKey] = caps
				}
			}
		}
	}
	if len(snapshot.ActiveRules) == 0 {
		return decision, nil
	}
	if denied {
		return decision, ErrUserModelRestrictionDenied
	}
	if unresolved {
		return decision, ErrUserModelRestrictionIdentityUnknown
	}
	return decision, nil
}

func decisionRevision(d *UserModelRestrictionDecision, policy bool) int64 {
	if d == nil {
		return 0
	}
	if policy {
		return d.PolicyRevision
	}
	return d.CatalogRevision
}

func decisionIdentities(d *UserModelRestrictionDecision) []CanonicalModelIdentity {
	if d == nil || len(d.Identities) == 0 {
		return nil
	}
	return append([]CanonicalModelIdentity(nil), d.Identities...)
}

// FilterModelsForListing evaluates a whole catalogue with one repository
// snapshot. Unknown identities remain visible when there are no active rules;
// once a user has restrictions they are hidden so an alias cannot disclose or
// bypass a denied canonical model.
func (s *UserModelRestrictionService) FilterModelsForListing(ctx context.Context, userID int64, inputs []ModelIdentityInput) (*UserModelRestrictionListingResult, error) {
	if userID <= 0 || len(inputs) > 10000 {
		return nil, ErrUserModelRestrictionInvalid
	}
	normalized := make([]ModelIdentityInput, len(inputs))
	for i, input := range inputs {
		if !validRestrictionText(input.Protocol, 64) || !validRestrictionText(input.Role, 32) || !validRestrictionText(input.ProviderContext, 128) || !validRestrictionText(input.Namespace, 64) || !validRestrictionText(input.Transport, 64) || !validRestrictionText(input.Model, 255) {
			return nil, ErrUserModelRestrictionIdentityUnknown
		}
		normalized[i] = ModelIdentityInput{
			Protocol:        strings.ToLower(strings.TrimSpace(input.Protocol)),
			Role:            strings.ToLower(strings.TrimSpace(input.Role)),
			ProviderContext: strings.ToLower(strings.TrimSpace(input.ProviderContext)),
			Namespace:       normalizeRestrictionNamespace(input.Namespace),
			Transport:       strings.ToLower(strings.TrimSpace(input.Transport)),
			Model:           normalizeRestrictionModel(input.Model),
		}
	}
	repo, err := s.identityRepository()
	if err != nil {
		return nil, err
	}
	snapshot, err := repo.GetModelIdentityAdmission(ctx, userID, normalized)
	if err != nil || snapshot == nil || len(snapshot.Candidates) != len(normalized) {
		return nil, ErrUserModelRestrictionUnavailable
	}
	result := &UserModelRestrictionListingResult{
		PolicyRevision:  snapshot.PolicyRevision,
		CatalogRevision: snapshot.CatalogRevision,
		Allowed:         make([]bool, len(normalized)),
	}
	if len(snapshot.ActiveRules) == 0 {
		for i := range result.Allowed {
			result.Allowed[i] = true
		}
		return result, nil
	}
	denied := make(map[string]struct{}, len(snapshot.ActiveRules))
	for _, rule := range snapshot.ActiveRules {
		if restrictionRuleActive(rule, snapshot.CheckedAt) && (rule.Action == "" || rule.Action == "deny_model") {
			namespace := normalizeRestrictionNamespace(rule.Namespace)
			canonical := normalizeRestrictionModel(rule.CanonicalModel)
			if namespace == "" || canonical == "" {
				return nil, ErrUserModelRestrictionUnavailable
			}
			denied[namespace+"\x00"+canonical] = struct{}{}
		}
	}
	for i, candidates := range snapshot.Candidates {
		if len(candidates) != 1 {
			continue
		}
		identity := candidates[0]
		_, blocked := denied[normalizeRestrictionNamespace(identity.Namespace)+"\x00"+normalizeRestrictionModel(identity.CanonicalModel)]
		result.Allowed[i] = !blocked
	}
	return result, nil
}
