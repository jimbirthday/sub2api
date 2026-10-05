package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetPromptInjectionRules returns the rules used by gateway request handlers.
func (h *SettingHandler) GetPromptInjectionRules(c *gin.Context) {
	rules, err := h.settingService.GetPromptInjectionRules(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}

// SavePromptInjectionRules replaces the complete rule set atomically at the
// settings level. The admin page uses this endpoint for create/edit/delete.
func (h *SettingHandler) SavePromptInjectionRules(c *gin.Context) {
	var payload struct {
		Rules []service.PromptInjectionRule `json:"rules"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, "invalid prompt injection rules")
		return
	}
	if err := h.settingService.SavePromptInjectionRules(c.Request.Context(), payload.Rules); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	rules, err := h.settingService.GetPromptInjectionRules(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}
