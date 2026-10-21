package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. Pinned discovery takes precedence over local account model mappings;
// when disabled, groups with explicit mappings are generated locally;
// otherwise ChatGPT manifests are proxied verbatim and custom API key manifests
// receive provider-compatibility normalization plus short-lived caching.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI and Composite groups")
		return
	}

	ifNoneMatch := c.GetHeader("If-None-Match")
	manifestIfNoneMatch := ifNoneMatch
	if h.userModelRestrictionService != nil && apiKey.UserID > 0 {
		// A group-level validator cannot validate a user-filtered representation.
		manifestIfNoneMatch = ""
	}
	// 固定账号分支：开启后只用选定账号拉取 manifest，不经过调度器；
	// 全部不可用/全部失败时按 FallbackToScheduler 决定回退调度器或返回错误。
	if apiKey.Group.Platform == service.PlatformOpenAI &&
		apiKey.Group.CodexModelsManifestConfig.Enabled {
		pinnedManifest, pinnedAccount, pinnedErr := h.gatewayService.FetchPinnedCodexModelsManifest(
			c.Request.Context(),
			apiKey.Group,
			c.Query("client_version"),
		)
		if pinnedErr != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if !apiKey.Group.CodexModelsManifestConfig.FallbackToScheduler {
				if errors.Is(pinnedErr, service.ErrNoPinnedCodexModelsAccounts) {
					h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available pinned OpenAI accounts")
					return
				}
				h.errorResponse(c, infraerrors.Code(pinnedErr), "upstream_error", infraerrors.Message(pinnedErr))
				return
			}
			// 回退开启：跌入下方调度器循环。
		} else {
			// 让 ops 错误日志携带实际拉取成功的首个固定账号。
			setOpsSelectedAccount(c, pinnedAccount.ID, pinnedAccount.Platform)
			if err := h.gatewayService.MergeGroupConfiguredCodexModels(c.Request.Context(), apiKey.Group, pinnedManifest, manifestIfNoneMatch); err != nil {
				h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
				return
			}
			if c.Request.Context().Err() != nil {
				return
			}
			if err := filterCodexManifestByUser(c.Request.Context(), pinnedManifest, h.userModelRestrictionService, apiKey.UserID, apiKey.Group.Platform, ifNoneMatch); err != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Model access policy is temporarily unavailable")
				return
			}
			writeOpenAIModelsResponse(c, pinnedManifest)
			return
		}
	}

	if !apiKey.Group.CodexModelsManifestConfig.Enabled {
		configuredManifest, configured, err := h.gatewayService.BuildGroupConfiguredCodexModelsManifest(
			c.Request.Context(),
			apiKey.Group,
			manifestIfNoneMatch,
		)
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
			return
		}
		if configured {
			if err := filterCodexManifestByUser(c.Request.Context(), configuredManifest, h.userModelRestrictionService, apiKey.UserID, apiKey.Group.Platform, ifNoneMatch); err != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Model access policy is temporarily unavailable")
				return
			}
			writeOpenAIModelsResponse(c, configuredManifest)
			return
		}
	}

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastUpstreamErr error

	for {
		account, err := h.gatewayService.SelectAccountForModelWithExclusions(c.Request.Context(), apiKey.GroupID, "", "", failedAccountIDs)
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)

		// The client ETag represents the final group-specific body, so fetch the
		// source manifest before applying local filtering and alias metadata.
		manifest, err := h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), "")
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if service.IsRetryableCodexModelsManifestError(err) && switchCount < maxAccountSwitches {
				failedAccountIDs[account.ID] = struct{}{}
				switchCount++
				lastUpstreamErr = err
				continue
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if err := h.gatewayService.CompleteAPIKeyCodexModelsManifestForClient(manifest, account); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to complete Codex models manifest")
			return
		}
		if err := service.ApplyPinnedCodexModelsMapping(manifest, account, apiKey.Group); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to apply model mappings")
			return
		}
		if err := h.gatewayService.MergeGroupConfiguredCodexModels(c.Request.Context(), apiKey.Group, manifest, manifestIfNoneMatch); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
		if err := filterCodexManifestByUser(c.Request.Context(), manifest, h.userModelRestrictionService, apiKey.UserID, account.Platform, ifNoneMatch); err != nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Model access policy is temporarily unavailable")
			return
		}

		writeOpenAIModelsResponse(c, manifest)
		return
	}
}

func filterCodexManifestByUser(ctx context.Context, manifest *service.OpenAIModelsResponse, policies *service.UserModelRestrictionService, userID int64, platform, ifNoneMatch string) error {
	if manifest == nil || policies == nil || userID <= 0 {
		return nil
	}
	if manifest.NotModified || len(manifest.Body) == 0 {
		return fmt.Errorf("user-filtered manifest has no response body")
	}
	var envelope struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(manifest.Body, &envelope); err != nil {
		return err
	}
	ids := make([]string, len(envelope.Models))
	for i, raw := range envelope.Models {
		var descriptor struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(raw, &descriptor); err != nil || descriptor.Slug == "" {
			return fmt.Errorf("invalid Codex model catalogue entry")
		}
		ids[i] = descriptor.Slug
	}
	allowedIDs, err := filterUserModelRestrictionIDsChecked(ctx, policies, userID, platform, ids)
	if err != nil {
		return err
	}
	allowed := make(map[string]int, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id]++
	}
	kept := make([]json.RawMessage, 0, len(allowedIDs))
	for i, raw := range envelope.Models {
		if allowed[ids[i]] > 0 {
			kept = append(kept, raw)
			allowed[ids[i]]--
		}
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(manifest.Body, &generic); err != nil {
		return err
	}
	generic["models"], _ = json.Marshal(kept)
	body, err := json.Marshal(generic)
	if err != nil {
		return err
	}
	manifest.Body = body
	manifest.ETag = service.CodexModelsManifestETag(body)
	manifest.NotModified = service.CodexModelsManifestETagMatches(ifNoneMatch, manifest.ETag)
	if manifest.NotModified {
		manifest.Body = nil
	}
	return nil
}
