package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SharedSubscriptionHandler struct {
	svc *service.SharedSubscriptionService
}

func NewSharedSubscriptionHandler(svc *service.SharedSubscriptionService) *SharedSubscriptionHandler {
	return &SharedSubscriptionHandler{svc: svc}
}
func sharedID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}
func (h *SharedSubscriptionHandler) Plans(c *gin.Context) {
	out, err := h.svc.ListPlans(c.Request.Context(), true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) AdminPlans(c *gin.Context) {
	out, err := h.svc.ListPlans(c.Request.Context(), false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) SavePlan(c *gin.Context) {
	var p service.SharedSubscriptionPlan
	if err := c.ShouldBindJSON(&p); err != nil {
		response.BadRequest(c, "invalid plan")
		return
	}
	p.ID = 0
	if c.Param("id") != "" {
		id, ok := sharedID(c)
		if !ok {
			return
		}
		p.ID = id
	}
	if err := h.svc.SavePlan(c.Request.Context(), &p); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, p)
}
func (h *SharedSubscriptionHandler) DeletePlan(c *gin.Context) {
	id, ok := sharedID(c)
	if !ok {
		return
	}
	if err := h.svc.DeletePlan(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
func (h *SharedSubscriptionHandler) Mine(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	out, err := h.svc.List(c.Request.Context(), subject.UserID, 500)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) History(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	out, err := h.svc.History(c.Request.Context(), subject.UserID, 100)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) AdminList(c *gin.Context) {
	var query struct {
		UserID    int64  `form:"user_id" binding:"min=0"`
		PlanID    int64  `form:"plan_id" binding:"min=0"`
		BeforeID  int64  `form:"before_id" binding:"min=0"`
		Limit     int    `form:"limit" binding:"min=0,max=100"`
		Status    string `form:"status"`
		Page      int    `form:"page" binding:"min=0,max=1000000"`
		PageSize  int    `form:"page_size" binding:"min=0,max=100"`
		Search    string `form:"search" binding:"max=200"`
		SortBy    string `form:"sort_by"`
		SortOrder string `form:"sort_order"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BadRequest(c, "invalid subscription filter")
		return
	}
	if query.PageSize > 0 {
		query.Limit = query.PageSize
	}
	out, err := h.svc.ListPage(c.Request.Context(), service.SharedSubscriptionFilter{UserID: query.UserID, PlanID: query.PlanID, Status: query.Status, BeforeID: query.BeforeID, Limit: query.Limit, Page: query.Page, Search: query.Search, SortBy: query.SortBy, SortOrder: query.SortOrder})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) AdminHistory(c *gin.Context) {
	uid, parseErr := strconv.ParseInt(c.Query("user_id"), 10, 64)
	if c.Query("user_id") != "" && (parseErr != nil || uid <= 0) {
		response.BadRequest(c, "invalid user id")
		return
	}
	out, err := h.svc.History(c.Request.Context(), uid, 100)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) Assign(c *gin.Context) {
	var req struct {
		UserID    int64  `json:"user_id"`
		PlanID    int64  `json:"plan_id"`
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID <= 0 || req.PlanID <= 0 {
		response.BadRequest(c, "invalid assignment")
		return
	}
	out, err := h.svc.Assign(c.Request.Context(), req.UserID, req.PlanID, req.RequestID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *SharedSubscriptionHandler) Action(c *gin.Context) {
	id, ok := sharedID(c)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		Days   int    `json:"days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid action")
		return
	}
	if err := h.svc.Action(c.Request.Context(), id, req.Action, req.Days); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *SharedSubscriptionHandler) ChangePlan(c *gin.Context) {
	id, ok := sharedID(c)
	if !ok {
		return
	}
	var req struct {
		PlanID     int64 `json:"plan_id" binding:"required,min=1"`
		Generation *int  `json:"generation" binding:"required,min=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid plan change")
		return
	}
	if err := h.svc.ChangePlan(c.Request.Context(), id, req.PlanID, *req.Generation); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *SharedSubscriptionHandler) UpdateQuota(c *gin.Context) {
	id, ok := sharedID(c)
	if !ok {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	var req service.SharedQuotaUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid quota update")
		return
	}
	req.OperatorID = subject.UserID
	if err := h.svc.UpdateQuota(c.Request.Context(), id, req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}
