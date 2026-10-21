package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UserModelRestrictionAdmission binds the authenticated user to every later
// provider attempt and performs the public-name deny check before composite
// routing. The final authoritative check remains at the network-write boundary.
func (h *GatewayHandler) UserModelRestrictionAdmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h == nil || c == nil || c.Request == nil {
			c.Next()
			return
		}
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.UserID <= 0 {
			c.Next()
			return
		}
		ctx := service.WithUserModelRestrictionAdmission(c.Request.Context(), h.userModelRestrictionService, apiKey.UserID)
		ctx = service.WithModelIdentityContext(ctx, modelIdentityProtocolForRoute(c.FullPath()), "primary", "")
		if h.userModelRestrictionService == nil {
			ctx = c.Request.Context()
		}
		userLimit, rpmLimit := 0, 0
		if apiKey.User != nil {
			userLimit = apiKey.User.Concurrency
			rpmLimit = apiKey.User.RPMLimit
		}
		if h.concurrencyHelper != nil && h.concurrencyHelper.concurrencyService != nil {
			// Correlation headers can be reused by clients. Generate a separate
			// internal operation ID so separate requests cannot share execution.
			ctx = service.WithModelExecutionBudget(ctx, h.concurrencyHelper.concurrencyService.ModelExecutionBudgetCache(), h.distillationTelemetryService, apiKey.UserID, userLimit, rpmLimit, "", nil)
		}
		c.Request = c.Request.WithContext(ctx)

		// Existing artifact/status/cancel endpoints intentionally remain usable.
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete || isUserModelRestrictionArtifactMutation(c) {
			c.Next()
			return
		}
		body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			// Body validation remains owned by the endpoint and existing size limiter.
			c.Next()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		service.UpdateModelExecutionPayload(ctx, body)
		defer func() {
			if c.Writer.Status() >= http.StatusInternalServerError {
				service.CompleteModelExecution(c.Request.Context(), false)
			}
		}()
		model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
		if model == "" {
			model = userModelRestrictionModelFromParams(c)
		}
		if model == "" {
			c.Next()
			return
		}
		if h.distillationTelemetryService != nil {
			platform := "unknown"
			if apiKey.Group != nil {
				platform = apiKey.Group.Platform
			}
			subject, _ := middleware2.GetAuthSubjectFromContext(c)
			configuredRPM := 0
			if apiKey.User != nil {
				configuredRPM = apiKey.User.RPMLimit
			}
			cohort := "interactive"
			if strings.Contains(c.FullPath(), "/images/batches") {
				cohort = "async_batch"
			}
			telemetryCtx := service.PrepareDistillationTelemetry(service.WithDistillationTelemetry(ctx, h.distillationTelemetryService), service.DistillationTelemetryRequest{UserID: apiKey.UserID, APIKeyID: &apiKey.ID, OperationID: service.ModelExecutionOperationID(ctx), Provider: platform, Model: model, Cohort: cohort, ConfiguredConcurrency: subject.Concurrency, ConfiguredRPM: configuredRPM, PromptMaterial: service.DistillationPromptMaterial(body)})
			c.Request = c.Request.WithContext(telemetryCtx)
		}
		if h.userModelRestrictionService == nil {
			c.Next()
			return
		}
		if err := h.userModelRestrictionService.CheckRequest(ctx, apiKey.UserID, "", model, "", time.Now()); err != nil {
			if !errors.Is(err, service.ErrUserModelRestrictionDenied) && !errors.Is(err, service.ErrUserModelRestrictionIdentityUnknown) {
				err = service.ErrUserModelRestrictionUnavailable
			}
			service.WriteUserModelRestrictionError(c, userModelRestrictionProtocol(c), err)
			return
		}
		c.Next()
	}
}

func userModelRestrictionModelFromParams(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if model := strings.TrimSpace(c.Param("model")); model != "" {
		return model
	}
	action := strings.TrimPrefix(strings.TrimSpace(c.Param("modelAction")), "/")
	if i := strings.LastIndex(action, ":"); i >= 0 {
		return strings.TrimSpace(action[:i])
	}
	return ""
}

func isUserModelRestrictionArtifactMutation(c *gin.Context) bool {
	path := c.FullPath()
	return strings.Contains(path, "/images/batches/:id/cancel") ||
		strings.Contains(path, "/cachedContents/:cacheID") && c.Request.Method == http.MethodPatch
}

func userModelRestrictionProtocol(c *gin.Context) service.UserModelRestrictionProtocol {
	path := c.Request.URL.Path
	if strings.Contains(path, "/v1beta/") {
		return service.UserModelRestrictionProtocolGoogle
	}
	if strings.Contains(path, "/messages") || strings.Contains(path, "/systemone") {
		return service.UserModelRestrictionProtocolAnthropic
	}
	return service.UserModelRestrictionProtocolOpenAI
}

// Model catalog protocol denotes the request operation, not its error envelope.
// Prefix aliases deliberately resolve to the same stable catalog context.
func modelIdentityProtocolForRoute(path string) string {
	switch {
	case strings.Contains(path, "/messages/count_tokens"):
		return "count_tokens"
	case strings.Contains(path, "/messages"):
		return "messages"
	case strings.Contains(path, "/chat/completions"):
		return "chat_completions"
	case strings.Contains(path, "/embeddings"):
		return "embeddings"
	case strings.Contains(path, "/images/"):
		return "images"
	case strings.Contains(path, "/cachedContents"):
		return "cached_content"
	case strings.Contains(path, "/v1beta/"):
		return "gemini"
	case strings.Contains(path, "/responses"), strings.Contains(path, "/alpha/search"), strings.Contains(path, "/realtime/calls"), strings.Contains(path, "/live"):
		return "responses"
	default:
		return ""
	}
}
