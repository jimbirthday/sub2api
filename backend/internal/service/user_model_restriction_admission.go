package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type userModelRestrictionAdmissionKey struct{}

type userModelRestrictionAdmission struct {
	service *UserModelRestrictionService
	userID  int64
}

type modelIdentityContextKey struct{}
type ModelIdentityContext struct {
	Protocol        string
	Role            string
	ProviderContext string
}

func WithModelIdentityContext(ctx context.Context, protocol, role, providerContext string) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, modelIdentityContextKey{}, ModelIdentityContext{Protocol: strings.ToLower(strings.TrimSpace(protocol)), Role: strings.ToLower(strings.TrimSpace(role)), ProviderContext: strings.ToLower(strings.TrimSpace(providerContext))})
}

type UserModelRestrictionProtocol string

const (
	UserModelRestrictionProtocolOpenAI    UserModelRestrictionProtocol = "openai"
	UserModelRestrictionProtocolAnthropic UserModelRestrictionProtocol = "anthropic"
	UserModelRestrictionProtocolGoogle    UserModelRestrictionProtocol = "google"
)

// WithUserModelRestrictionAdmission carries the authenticated user and policy
// evaluator across handler, retry, async and provider boundaries.
func WithUserModelRestrictionAdmission(ctx context.Context, service *UserModelRestrictionService, userID int64) context.Context {
	if ctx == nil || service == nil || userID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, userModelRestrictionAdmissionKey{}, userModelRestrictionAdmission{service: service, userID: userID})
}

// EnforceUserModelRestriction checks one coherent policy snapshot against all
// models that an upstream attempt will invoke. The account is the selected
// concrete provider account; group transport names are deliberately ignored.
func EnforceUserModelRestriction(ctx context.Context, account *Account, models ...string) error {
	decision, err := checkUserModelRestriction(ctx, account, models...)
	if err != nil {
		return err
	}
	if err := ReserveAndConfirmModelExecution(ctx, decision); err != nil {
		return err
	}
	if err := PersistModelExecutionAttempt(ctx, account); err != nil {
		return err
	}
	return nil
}

// EnforceUserModelRestrictionWithoutBudget is for metadata/preflight requests
// that reach an upstream but do not start a generation operation.
func EnforceUserModelRestrictionWithoutBudget(ctx context.Context, account *Account, models ...string) error {
	_, err := checkUserModelRestriction(ctx, account, models...)
	return err
}

func checkUserModelRestriction(ctx context.Context, account *Account, models ...string) (*UserModelRestrictionDecision, error) {
	if ctx == nil {
		return nil, nil
	}
	admission, ok := ctx.Value(userModelRestrictionAdmissionKey{}).(userModelRestrictionAdmission)
	if !ok || admission.service == nil || admission.userID <= 0 {
		return nil, nil
	}
	inputs := make([]ModelIdentityInput, 0, len(models))
	identityContext, _ := ctx.Value(modelIdentityContextKey{}).(ModelIdentityContext)
	seen := make(map[string]struct{}, len(models))
	for index, model := range models {
		model = strings.TrimSpace(model)
		role := identityContext.Role
		if role == "" {
			role = "primary"
		}
		if index > 0 {
			role = "tool"
		}
		key := role + ":" + strings.ToLower(model)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		providerContext := identityContext.ProviderContext
		if providerContext == "" && account != nil {
			providerContext = account.Platform
		}
		namespace := ""
		if index == 0 {
			// The selected account proves the primary request model's origin. Extra
			// driver/tool dependencies must resolve through the identity catalog;
			// assigning the account namespace to them would invent provenance.
			namespace = explicitRestrictionNamespaceForAccount(account)
		}
		inputs = append(inputs, ModelIdentityInput{
			Protocol: identityContext.Protocol, Role: role, ProviderContext: providerContext,
			Namespace: namespace,
			Transport: userModelRestrictionTransport(account),
			Model:     model,
		})
	}
	decision, err := admission.service.CheckModels(ctx, admission.userID, inputs)
	if err != nil {
		return nil, err
	}
	return decision, nil
}

// UserModelRestrictionModelsFromPayload returns the deduplicated model
// dependencies serialized in a final upstream JSON payload. Images Responses
// requests may contain both a driver model and one or more tool models.
func UserModelRestrictionModelsFromPayload(primary string, body []byte) []string {
	models := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(value, role string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := role + ":" + strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		models = append(models, value)
	}
	add(primary, "primary")
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return models
	}
	add(gjson.GetBytes(body, "model").String(), "primary")
	add(gjson.GetBytes(body, "session.model").String(), "primary")
	for _, value := range gjson.GetBytes(body, "tools.#.model").Array() {
		add(value.String(), "tool")
	}
	return models
}

func explicitRestrictionNamespaceForAccount(account *Account) string {
	if account == nil {
		return ""
	}
	switch account.Platform {
	case PlatformAnthropic:
		return "anthropic"
	case PlatformOpenAI:
		// A compatible custom endpoint does not prove the original provider.
		if account.Type == AccountTypeAPIKey {
			base := strings.TrimSpace(account.GetCredential("base_url"))
			if base != "" {
				parsed, err := url.Parse(base)
				if err != nil || !strings.EqualFold(parsed.Hostname(), "api.openai.com") {
					return ""
				}
			}
		}
		return "openai"
	case PlatformGemini:
		return "google"
	case PlatformGrok:
		return "xai"
	case PlatformDeepseek:
		return "deepseek"
	case PlatformKimi:
		return "moonshot"
	case PlatformZhipu:
		return "zhipu"
	case PlatformMiniMax:
		return "minimax"
	default:
		return ""
	}
}

func userModelRestrictionTransport(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(account.Platform))
}

func WriteUserModelRestrictionError(c *gin.Context, protocol UserModelRestrictionProtocol, err error) {
	if c == nil || err == nil {
		return
	}
	status := http.StatusServiceUnavailable
	message := "Model access policy is temporarily unavailable"
	code := "MODEL_ACCESS_POLICY_UNAVAILABLE"
	if errors.Is(err, ErrUserModelRestrictionDenied) {
		status = http.StatusForbidden
		message = "Model is not available for this account"
		code = "MODEL_ACCESS_DENIED"
	} else if errors.Is(err, ErrModelExecutionBudgetExceeded) {
		status = http.StatusTooManyRequests
		message = "Model execution limit exceeded"
		code = "MODEL_EXECUTION_LIMIT_EXCEEDED"
	} else if errors.Is(err, ErrUserModelRestrictionIdentityUnknown) {
		message = "Model identity could not be resolved"
		code = "MODEL_IDENTITY_UNRESOLVED"
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	switch protocol {
	case UserModelRestrictionProtocolAnthropic:
		c.AbortWithStatusJSON(status, gin.H{"type": "error", "error": gin.H{"type": "permission_error", "message": message}})
	case UserModelRestrictionProtocolGoogle:
		googleStatus := "UNAVAILABLE"
		switch status {
		case http.StatusForbidden:
			googleStatus = "PERMISSION_DENIED"
		case http.StatusTooManyRequests:
			googleStatus = "RESOURCE_EXHAUSTED"
		}
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": status, "message": message, "status": googleStatus, "details": []gin.H{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": code}}}})
	default:
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "permission_error", "code": code, "message": message}})
	}
}
