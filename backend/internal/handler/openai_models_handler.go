package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *GatewayHandler) pinnedOpenAIModels(c *gin.Context, group *service.Group) {
	if c.Request.Context().Err() != nil {
		return
	}
	if h.openAIGatewayService == nil {
		writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "OpenAI model discovery is not configured")
		return
	}
	etag := c.GetHeader("If-None-Match")
	clientETag := etag
	if c.Param("model") != "" {
		etag = "" // A collection ETag cannot validate a single-model representation.
	}
	// The upstream validator is shared across users. Once a user policy is in
	// play, a 304 based on that shared validator could make the client reuse a
	// catalogue that still contains a model newly denied for this user.
	apiKey, hasAPIKey := middleware2.GetAPIKeyFromContext(c)
	if hasAPIKey && apiKey != nil && h.userModelRestrictionService != nil && apiKey.UserID > 0 {
		etag = ""
	}
	response, account, err := h.openAIGatewayService.FetchPinnedOpenAIModelsList(
		c.Request.Context(), group, h.maxAccountSwitches, etag,
	)
	if c.Request.Context().Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrNoPinnedCodexModelsAccounts) {
			writeOpenAIModelsError(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI model discovery accounts")
			return
		}
		writeOpenAIModelsError(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
		return
	}
	setOpsSelectedAccount(c, account.ID, account.Platform)
	if apiKey, ok := middleware2.GetAPIKeyFromContext(c); ok && apiKey != nil && apiKey.UserID > 0 && h.userModelRestrictionService != nil && response != nil && !response.NotModified {
		filtered, filterErr := filterOpenAIModelsBodyByUser(c.Request.Context(), response.Body, h.userModelRestrictionService, apiKey.UserID, account.Platform)
		if filterErr != nil {
			writeOpenAIModelsError(c, http.StatusServiceUnavailable, "api_error", "Model access policy is temporarily unavailable")
			return
		}
		response.Body = filtered
		response.ETag = service.CodexModelsManifestETag(filtered)
		response.NotModified = service.CodexModelsManifestETagMatches(clientETag, response.ETag)
		if response.NotModified {
			response.Body = nil
		}
	}
	writeOpenAIModelsResponse(c, response)
}

func filterOpenAIModelsBodyByUser(ctx context.Context, body []byte, policies *service.UserModelRestrictionService, userID int64, platform string) ([]byte, error) {
	if policies == nil || userID <= 0 {
		return body, nil
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	ids := make([]string, len(envelope.Data))
	for i, raw := range envelope.Data {
		var model struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &model); err != nil || model.ID == "" {
			return nil, fmt.Errorf("invalid model catalogue entry")
		}
		ids[i] = model.ID
	}
	allowedIDs, err := filterUserModelRestrictionIDsChecked(ctx, policies, userID, platform, ids)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]int, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id]++
	}
	kept := make([]json.RawMessage, 0, len(allowedIDs))
	for i, raw := range envelope.Data {
		if allowed[ids[i]] > 0 {
			kept = append(kept, raw)
			allowed[ids[i]]--
		}
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, err
	}
	generic["data"], _ = json.Marshal(kept)
	return json.Marshal(generic)
}

func writeOpenAIModelsError(c *gin.Context, status int, errorType, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": errorType, "message": message}})
}

func writeOpenAIModelsResponse(c *gin.Context, manifest *service.OpenAIModelsResponse) {
	if c.Param("model") != "" {
		writeRetrievedModel(c, manifest.Body)
		return
	}
	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}

// Both discovery endpoints consume the same final catalogue, after group/platform
// selection and allowlist filtering. Preserve every field on the selected entry.
func writeModelsListResponse(c *gin.Context, models any) {
	response := gin.H{"object": "list", "data": models}
	if c.Param("model") == "" {
		c.JSON(http.StatusOK, response)
		return
	}
	body, err := json.Marshal(response)
	if err != nil {
		writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "Failed to encode model catalogue")
		return
	}
	writeRetrievedModel(c, body)
}

func writeRetrievedModel(c *gin.Context, body []byte) {
	var catalog struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue")
		return
	}
	modelID := c.Param("model")
	for _, raw := range catalog.Data {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(raw, &model); err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue entry")
			return
		}
		var id string
		if err := json.Unmarshal(model["id"], &id); err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue ID")
			return
		}
		if id == modelID {
			c.Data(http.StatusOK, "application/json", raw)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": "model_not_found", "param": "model",
		"message": fmt.Sprintf("Model %q does not exist or is not available for this group", modelID),
	}})
}
