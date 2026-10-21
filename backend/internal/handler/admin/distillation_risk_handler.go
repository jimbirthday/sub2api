package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DistillationRiskHandler struct {
	risk *service.DistillationRiskService
}

func NewDistillationRiskHandler(risk *service.DistillationRiskService) *DistillationRiskHandler {
	return &DistillationRiskHandler{risk: risk}
}

func (h *DistillationRiskHandler) Overview(c *gin.Context) {
	data, err := h.risk.Overview(c.Request.Context(), time.Now().UTC())
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) Users(c *gin.Context) {
	opts := service.DistillationListOptions{Cursor: c.Query("cursor") == "true", BeforeID: queryInt64(c, "before_id", 0), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "page_size", 20), UserID: queryInt64(c, "user_id", 0), State: c.Query("state")}
	data, err := h.risk.ListCases(c.Request.Context(), opts)
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) User(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	data, err := h.risk.GetCase(c.Request.Context(), id, time.Now().UTC())
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) Trends(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	data, err := h.risk.Trends(c.Request.Context(), id, queryInt(c, "limit", 100))
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) Events(c *gin.Context) {
	opts := service.DistillationListOptions{Cursor: c.Query("cursor") == "true", BeforeID: queryInt64(c, "before_id", 0), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "page_size", 20), UserID: queryInt64(c, "user_id", 0)}
	data, err := h.risk.ListEvents(c.Request.Context(), opts)
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) GetConfig(c *gin.Context) {
	data, err := h.risk.GetConfig(c.Request.Context())
	h.respond(c, data, err)
}

type distillationConfigRequest struct {
	Mode             string          `json:"mode" binding:"required"`
	ScoringConfig    json.RawMessage `json:"scoring_config"`
	ExpectedRevision int64           `json:"expected_revision" binding:"required"`
}

func (h *DistillationRiskHandler) UpdateConfig(c *gin.Context) {
	var req distillationConfigRequest
	if !bindJSON(c, &req) {
		return
	}
	data, err := h.risk.UpdateConfig(c.Request.Context(), req.Mode, req.ScoringConfig, req.ExpectedRevision, adminActorID(c), time.Now().UTC())
	if errors.Is(err, service.ErrDistillationRevisionConflict) {
		if current, e := h.risk.GetConfig(c.Request.Context()); e == nil {
			response.ErrorWithDetails(c, 409, err.Error(), "RISK_CONFIG_REVISION_CONFLICT", map[string]string{"current_revision": strconv.FormatInt(current.Revision, 10)})
			return
		}
	}
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) Exemptions(c *gin.Context) {
	opts := service.DistillationListOptions{Cursor: c.Query("cursor") == "true", BeforeID: queryInt64(c, "before_id", 0), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "page_size", 20), UserID: queryInt64(c, "user_id", 0)}
	data, err := h.risk.ListExemptions(c.Request.Context(), opts, time.Now().UTC())
	h.respond(c, data, err)
}

type distillationExemptionRequest struct {
	UserID           int64     `json:"user_id"`
	StartsAt         time.Time `json:"starts_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	Reason           string    `json:"reason"`
	IdempotencyKey   string    `json:"idempotency_key"`
	ID               int64     `json:"id"`
	ExpectedRevision int64     `json:"expected_revision"`
}

func (h *DistillationRiskHandler) CreateExemption(c *gin.Context) {
	var req distillationExemptionRequest
	if !bindJSON(c, &req) {
		return
	}
	data, err := h.risk.CreateExemption(c.Request.Context(), service.DistillationExemptionMutation{UserID: req.UserID, StartsAt: req.StartsAt, ExpiresAt: req.ExpiresAt, Reason: req.Reason, IdempotencyKey: req.IdempotencyKey, ActorID: adminActorID(c)})
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) RevokeExemption(c *gin.Context) {
	var req distillationExemptionRequest
	if !bindJSON(c, &req) {
		return
	}
	data, err := h.risk.RevokeExemption(c.Request.Context(), service.DistillationExemptionMutation{ID: req.ID, ExpectedRevision: req.ExpectedRevision, IdempotencyKey: req.IdempotencyKey, ActorID: adminActorID(c)})
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) Actions(c *gin.Context) {
	var req struct {
		ExpiresAt        time.Time `json:"expires_at"`
		UserID           int64     `json:"user_id"`
		Type             string    `json:"type"`
		ExpectedRevision int64     `json:"expected_revision"`
		Reason           string    `json:"reason"`
		IdempotencyKey   string    `json:"idempotency_key"`
	}
	if !bindJSON(c, &req) {
		return
	}
	action := service.DistillationAction{ExpiresAt: req.ExpiresAt, UserID: req.UserID, Type: req.Type, ExpectedRevision: req.ExpectedRevision, Reason: req.Reason, IdempotencyKey: req.IdempotencyKey, ActorID: adminActorID(c)}
	data, err := h.risk.ApplyAction(c.Request.Context(), action, time.Now().UTC())
	if errors.Is(err, service.ErrDistillationRevisionConflict) {
		if current, e := h.risk.GetCase(c.Request.Context(), req.UserID, time.Now().UTC()); e == nil && current.Case != nil {
			response.ErrorWithDetails(c, 409, err.Error(), "RISK_CASE_REVISION_CONFLICT", map[string]string{"current_revision": strconv.FormatInt(current.Case.Revision, 10)})
			return
		}
	}
	h.respond(c, data, err)
}

func (h *DistillationRiskHandler) respond(c *gin.Context, data any, err error) {
	if err == nil {
		response.Success(c, data)
		return
	}
	switch {
	case errors.Is(err, service.ErrDistillationInvalid):
		response.BadRequest(c, err.Error())
	case errors.Is(err, service.ErrDistillationRevisionConflict), errors.Is(err, service.ErrDistillationIdempotencyReuse):
		response.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrDistillationNotFound):
		response.NotFound(c, err.Error())
	case errors.Is(err, service.ErrDistillationObservationShort):
		response.Error(c, http.StatusConflict, err.Error())
	default:
		response.InternalError(c, err.Error())
	}
}

func bindJSON(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		response.BadRequest(c, err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid user id")
		return 0, false
	}
	return id, true
}

func queryInt(c *gin.Context, key string, fallback int) int {
	if v, err := strconv.Atoi(c.Query(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}
func queryInt64(c *gin.Context, key string, fallback int64) int64 {
	if v, err := strconv.ParseInt(c.Query(key), 10, 64); err == nil && v > 0 {
		return v
	}
	return fallback
}

func adminActorID(c *gin.Context) int64 { return getAdminIDFromContext(c) }
