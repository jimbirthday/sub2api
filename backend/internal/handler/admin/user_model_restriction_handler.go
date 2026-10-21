package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UserModelRestrictionHandler 管理用户级策略；挂载在已有管理员鉴权与审计链下。
type UserModelRestrictionHandler struct {
	policies *service.UserModelRestrictionService
	users    service.AdminService
}

func NewUserModelRestrictionHandler(policies *service.UserModelRestrictionService, users service.AdminService) *UserModelRestrictionHandler {
	return &UserModelRestrictionHandler{policies: policies, users: users}
}

type restrictionRuleInput struct {
	RuleID         string     `json:"rule_id"`
	Action         string     `json:"action"`
	LimitValue     *int       `json:"limit_value"`
	StartsAt       *time.Time `json:"starts_at"`
	Namespace      string     `json:"namespace"`
	CanonicalModel string     `json:"canonical_model"`
	Enabled        *bool      `json:"enabled"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Reason         string     `json:"reason"`
}

type replaceRestrictionsInput struct {
	Reason           string                  `json:"reason"`
	ExpectedRevision *int64                  `json:"expected_revision"`
	Rules            *[]restrictionRuleInput `json:"rules"`
}

type clearRestrictionsInput struct {
	Reason           string `json:"reason"`
	ExpectedRevision *int64 `json:"expected_revision"`
}

type restrictionRuleDTO struct {
	RuleID         string     `json:"rule_id"`
	Action         string     `json:"action"`
	LimitValue     *int       `json:"limit_value"`
	StartsAt       *time.Time `json:"starts_at"`
	Source         string     `json:"source"`
	Namespace      string     `json:"namespace"`
	CanonicalModel string     `json:"canonical_model"`
	Enabled        bool       `json:"enabled"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Reason         string     `json:"reason"`
	CreatedBy      *int64     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type restrictionSnapshotDTO struct {
	UserID    int64                `json:"user_id"`
	Revision  int64                `json:"revision"`
	UpdatedAt time.Time            `json:"updated_at"`
	UpdatedBy *int64               `json:"updated_by"`
	Rules     []restrictionRuleDTO `json:"rules"`
}

func (h *UserModelRestrictionHandler) List(c *gin.Context) {
	if h == nil || h.policies == nil {
		restrictionError(c, service.ErrUserModelRestrictionUnavailable)
		return
	}
	o := service.ModelRestrictionListOptions{Limit: 20, Search: c.Query("search"), Model: c.Query("model"), State: c.Query("state"), Source: c.Query("source")}
	for _, field := range []struct {
		key    string
		target *int64
	}{{"before_id", &o.BeforeID}, {"user_id", &o.UserID}} {
		if raw, ok := c.GetQuery(field.key); ok {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 {
				response.BadRequest(c, "Invalid cursor or user ID")
				return
			}
			*field.target = value
		}
	}
	if raw, ok := c.GetQuery("limit"); ok {
		value, err := strconv.Atoi(raw)
		if err != nil {
			response.BadRequest(c, "Invalid limit")
			return
		}
		o.Limit = value
	}
	result, err := h.policies.ListCurrent(c.Request.Context(), o)
	if err != nil {
		restrictionError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *UserModelRestrictionHandler) Evaluate(c *gin.Context) {
	id, ok := h.target(c)
	if !ok {
		return
	}
	var input struct {
		Models []service.ModelIdentityInput `json:"models"`
	}
	if !decodeRestrictionBody(c, &input) {
		return
	}
	if len(input.Models) == 0 || len(input.Models) > 50 {
		response.BadRequest(c, "Between 1 and 50 models are required")
		return
	}
	decision, err := h.policies.CheckModels(c.Request.Context(), id, input.Models)
	status := "allowed"
	if errors.Is(err, service.ErrUserModelRestrictionDenied) {
		status = "denied"
	} else if errors.Is(err, service.ErrUserModelRestrictionIdentityUnknown) {
		status = "unresolved"
	} else if err != nil {
		restrictionError(c, err)
		return
	}
	response.Success(c, gin.H{"status": status, "decision": decision})
}

func restrictionSnapshotResponse(s *service.UserModelRestrictionSnapshot) restrictionSnapshotDTO {
	result := restrictionSnapshotDTO{UserID: s.UserID, Revision: s.Revision, UpdatedAt: s.UpdatedAt, UpdatedBy: s.UpdatedBy, Rules: make([]restrictionRuleDTO, 0, len(s.Rules))}
	for _, r := range s.Rules {
		result.Rules = append(result.Rules, restrictionRuleDTO{Namespace: r.Namespace, CanonicalModel: r.CanonicalModel, Enabled: r.Enabled, ExpiresAt: r.ExpiresAt, Reason: r.Reason, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, RuleID: r.RuleID, Action: r.Action, LimitValue: r.LimitValue, StartsAt: r.StartsAt, Source: r.Source})
	}
	return result
}

func restrictionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrUserModelRestrictionInvalid):
		response.BadRequest(c, err.Error())
	case errors.Is(err, service.ErrUserModelRestrictionRevisionConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "Policy revision changed; reload before saving", "USER_MODEL_RESTRICTION_REVISION_CONFLICT", nil)
	case errors.Is(err, service.ErrUserModelRestrictionNotFound), errors.Is(err, service.ErrUserNotFound):
		response.NotFound(c, "User or policy version not found")
	default:
		response.Error(c, http.StatusServiceUnavailable, "User model policy service unavailable")
	}
}

func (h *UserModelRestrictionHandler) target(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return 0, false
	}
	if h == nil || h.users == nil || h.policies == nil {
		restrictionError(c, service.ErrUserModelRestrictionUnavailable)
		return 0, false
	}
	if _, err := h.users.GetUser(c.Request.Context(), id); err != nil {
		restrictionError(c, err)
		return 0, false
	}
	return id, true
}

func decodeRestrictionBody(c *gin.Context, dest any) bool {
	// 严格限制字段和总大小，防止伪造审计身份或超大数组耗尽内存。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		response.BadRequest(c, "Invalid policy request body")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "Expected one JSON object")
		return false
	}
	return true
}

func (h *UserModelRestrictionHandler) Get(c *gin.Context) {
	id, ok := h.target(c)
	if !ok {
		return
	}
	result, err := h.policies.Get(c.Request.Context(), id)
	if err != nil {
		restrictionError(c, err)
		return
	}
	response.Success(c, restrictionSnapshotResponse(result))
}

func (h *UserModelRestrictionHandler) Replace(c *gin.Context) {
	id, ok := h.target(c)
	if !ok {
		return
	}
	var input replaceRestrictionsInput
	if !decodeRestrictionBody(c, &input) {
		return
	}
	if input.ExpectedRevision == nil || input.Rules == nil {
		response.BadRequest(c, "expected_revision and rules are required")
		return
	}
	rules := make([]service.UserModelRestrictionRule, 0, len(*input.Rules))
	for _, r := range *input.Rules {
		if strings.TrimSpace(r.Reason) == "" {
			response.BadRequest(c, "reason is required for every rule")
			return
		}
		if r.Enabled == nil {
			response.BadRequest(c, "enabled is required for every rule")
			return
		}
		rules = append(rules, service.UserModelRestrictionRule{RuleID: r.RuleID, Action: r.Action, LimitValue: r.LimitValue, StartsAt: r.StartsAt, Namespace: r.Namespace, CanonicalModel: r.CanonicalModel, Enabled: *r.Enabled, ExpiresAt: r.ExpiresAt, Reason: r.Reason})
	}
	h.save(c, id, *input.ExpectedRevision, rules, input.Reason)
}

func (h *UserModelRestrictionHandler) Clear(c *gin.Context) {
	id, ok := h.target(c)
	if !ok {
		return
	}
	var input clearRestrictionsInput
	if !decodeRestrictionBody(c, &input) {
		return
	}
	if input.ExpectedRevision == nil {
		response.BadRequest(c, "expected_revision is required")
		return
	}
	h.save(c, id, *input.ExpectedRevision, []service.UserModelRestrictionRule{}, input.Reason)
}

func (h *UserModelRestrictionHandler) save(c *gin.Context, id, revision int64, rules []service.UserModelRestrictionRule, reason string) {
	actor := getAdminIDFromContext(c)
	if actor <= 0 {
		response.Unauthorized(c, "Administrator identity required")
		return
	}
	if strings.TrimSpace(reason) == "" && len(rules) > 0 {
		reason = rules[0].Reason
	}
	payload, _ := json.Marshal(struct {
		Revision int64
		Rules    []service.UserModelRestrictionRule
	}{revision, rules})
	key := c.GetHeader("Idempotency-Key")
	if key == "" && c.Request.Method == http.MethodPut {
		key = "put:" + strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(revision, 10)
	}
	raw, err := h.policies.MutateManagement(c.Request.Context(), service.ModelManagementMutation{ActorID: actor, Key: key, Resource: "policy:" + strconv.FormatInt(id, 10), Reason: reason, Payload: payload}, func(ctx context.Context) (json.RawMessage, error) {
		result, e := h.policies.Replace(ctx, id, revision, &actor, rules)
		if e != nil {
			return nil, e
		}
		return json.Marshal(restrictionSnapshotResponse(result))
	})
	if err != nil {
		h.managementError(c, err, id, false)
		return
	}
	response.Success(c, raw)
}

func (h *UserModelRestrictionHandler) History(c *gin.Context) {
	id, ok := h.target(c)
	if !ok {
		return
	}
	limit := 20
	if values, exists := c.Request.URL.Query()["limit"]; exists {
		raw := values[0]
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			response.BadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	var before int64
	if values, exists := c.Request.URL.Query()["before_revision"]; exists {
		raw := values[0]
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			response.BadRequest(c, "Invalid before_revision")
			return
		}
		before = parsed
	}
	result, err := h.policies.History(c.Request.Context(), id, before, limit)
	if err != nil {
		restrictionError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *UserModelRestrictionHandler) Version(c *gin.Context) {
	id, ok := h.target(c)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revision <= 0 {
		response.BadRequest(c, "Invalid policy revision")
		return
	}
	result, err := h.policies.GetVersion(c.Request.Context(), id, revision)
	if err != nil {
		restrictionError(c, err)
		return
	}
	response.Success(c, restrictionSnapshotResponse(result))
}

// ModelIdentityCatalog returns the versioned provider/transport identity catalog.
func (h *UserModelRestrictionHandler) ModelIdentityCatalog(c *gin.Context) {
	if h == nil || h.policies == nil {
		restrictionError(c, service.ErrUserModelRestrictionUnavailable)
		return
	}
	catalog, err := h.policies.Catalog(c.Request.Context())
	if err != nil {
		restrictionError(c, err)
		return
	}
	response.Success(c, catalog)
}

type replaceModelIdentityCatalogInput struct {
	Reason           string                              `json:"reason"`
	ExpectedRevision *int64                              `json:"expected_revision"`
	Models           []service.ModelIdentityCatalogEntry `json:"models"`
	Aliases          []service.ModelIdentityAlias        `json:"aliases"`
}

// ReplaceModelIdentityCatalog replaces the complete catalog through a revision CAS.
func (h *UserModelRestrictionHandler) ReplaceModelIdentityCatalog(c *gin.Context) {
	if h == nil || h.policies == nil {
		restrictionError(c, service.ErrUserModelRestrictionUnavailable)
		return
	}
	var input replaceModelIdentityCatalogInput
	if !decodeRestrictionBody(c, &input) {
		return
	}
	if input.ExpectedRevision == nil || input.Models == nil || input.Aliases == nil {
		response.BadRequest(c, "expected_revision, models and aliases are required")
		return
	}
	actor := getAdminIDFromContext(c)
	if actor <= 0 {
		response.Unauthorized(c, "Administrator identity required")
		return
	}
	payload, _ := json.Marshal(input)
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		key = "catalog-put:" + strconv.FormatInt(*input.ExpectedRevision, 10)
	}
	raw, err := h.policies.MutateManagement(c.Request.Context(), service.ModelManagementMutation{ActorID: actor, Key: key, Resource: "catalog", Reason: input.Reason, Payload: payload}, func(ctx context.Context) (json.RawMessage, error) {
		catalog, e := h.policies.ReplaceCatalog(ctx, *input.ExpectedRevision, actor, input.Models, input.Aliases)
		if e != nil {
			return nil, e
		}
		return json.Marshal(catalog)
	})
	if err != nil {
		h.managementError(c, err, 0, true)
		return
	}
	response.Success(c, raw)
}

func (h *UserModelRestrictionHandler) managementError(c *gin.Context, err error, id int64, catalog bool) {
	if errors.Is(err, service.ErrModelManagementIdempotencyConflict) {
		response.ErrorWithDetails(c, 409, err.Error(), "IDEMPOTENCY_KEY_REUSED", nil)
		return
	}
	if errors.Is(err, service.ErrUserModelRestrictionRevisionConflict) {
		var current int64
		if catalog {
			if v, e := h.policies.Catalog(c.Request.Context()); e == nil {
				current = v.Revision
			}
		} else {
			if v, e := h.policies.Get(c.Request.Context(), id); e == nil {
				current = v.Revision
			}
		}
		response.ErrorWithDetails(c, 409, "Revision changed; reload before saving", "USER_MODEL_RESTRICTION_REVISION_CONFLICT", map[string]string{"current_revision": strconv.FormatInt(current, 10)})
		return
	}
	restrictionError(c, err)
}

type catalogMutationInput struct {
	ExpectedRevision *int64                             `json:"expected_revision"`
	Reason           string                             `json:"reason"`
	Entry            *service.ModelIdentityCatalogEntry `json:"entry,omitempty"`
	Alias            *service.ModelIdentityAlias        `json:"alias,omitempty"`
}

func (h *UserModelRestrictionHandler) CreateCatalogEntry(c *gin.Context) { h.mutateCatalog(c, "entry") }
func (h *UserModelRestrictionHandler) CreateCatalogAlias(c *gin.Context) { h.mutateCatalog(c, "alias") }
func (h *UserModelRestrictionHandler) DeleteCatalogAlias(c *gin.Context) {
	h.mutateCatalog(c, "delete_alias")
}
func (h *UserModelRestrictionHandler) mutateCatalog(c *gin.Context, kind string) {
	if h == nil || h.policies == nil {
		restrictionError(c, service.ErrUserModelRestrictionUnavailable)
		return
	}
	var input catalogMutationInput
	if !decodeRestrictionBody(c, &input) {
		return
	}
	if input.ExpectedRevision == nil || strings.TrimSpace(input.Reason) == "" || (kind == "entry" && input.Entry == nil) || (kind == "alias" && input.Alias == nil) {
		response.BadRequest(c, "expected_revision, reason and entry/alias are required")
		return
	}
	actor := getAdminIDFromContext(c)
	if actor <= 0 {
		response.Unauthorized(c, "Administrator identity required")
		return
	}
	payload, _ := json.Marshal(struct {
		Kind, AliasID string
		Input         catalogMutationInput
	}{kind, c.Param("alias_id"), input})
	raw, err := h.policies.MutateManagement(c.Request.Context(), service.ModelManagementMutation{ActorID: actor, Key: c.GetHeader("Idempotency-Key"), Resource: "catalog", Reason: input.Reason, Payload: payload}, func(ctx context.Context) (json.RawMessage, error) {
		catalog, e := h.policies.Catalog(ctx)
		if e != nil {
			return nil, e
		}
		if catalog.Revision != *input.ExpectedRevision {
			return nil, service.ErrUserModelRestrictionRevisionConflict
		}
		switch kind {
		case "entry":
			catalog.Models = append(catalog.Models, *input.Entry)
		case "alias":
			catalog.Aliases = append(catalog.Aliases, *input.Alias)
		case "delete_alias":
			found := false
			aliases := make([]service.ModelIdentityAlias, 0, len(catalog.Aliases))
			for _, a := range catalog.Aliases {
				if a.AliasID == c.Param("alias_id") {
					found = true
					continue
				}
				aliases = append(aliases, a)
			}
			if !found {
				return nil, service.ErrUserModelRestrictionNotFound
			}
			catalog.Aliases = aliases
		}
		next, e := h.policies.ReplaceCatalog(ctx, *input.ExpectedRevision, actor, catalog.Models, catalog.Aliases)
		if e != nil {
			return nil, e
		}
		return json.Marshal(next)
	})
	if err != nil {
		h.managementError(c, err, 0, true)
		return
	}
	response.Success(c, raw)
}
