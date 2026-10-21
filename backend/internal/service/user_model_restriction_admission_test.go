package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWithUserModelRestrictionAdmissionWithoutBindingIsNoop(t *testing.T) {
	require.NoError(t, EnforceUserModelRestriction(context.Background(), &Account{Platform: PlatformOpenAI}, "gpt-driver"))
}

func TestWriteUserModelRestrictionUnavailableReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	WriteUserModelRestrictionError(c, UserModelRestrictionProtocolOpenAI, ErrUserModelRestrictionUnavailable)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"error":{"type":"permission_error","code":"MODEL_ACCESS_POLICY_UNAVAILABLE","message":"Model access policy is temporarily unavailable"}}`, recorder.Body.String())
}

func TestUserModelRestrictionModelsFromPayloadIncludesToolModels(t *testing.T) {
	models := UserModelRestrictionModelsFromPayload("gpt-driver", []byte(`{"model":"gpt-driver","tools":[{"type":"image_generation","model":"gemini-image"},{"model":"gemini-image"}]}`))
	require.Equal(t, []string{"gpt-driver", "gemini-image"}, models)
}

func TestEnforceUserModelRestrictionOnlyAssignsAccountOriginToPrimaryModel(t *testing.T) {
	repo := &restrictionServiceRepoStub{}
	repo.admission = &ModelIdentityAdmissionSnapshot{
		Candidates: [][]CanonicalModelIdentity{
			{{Namespace: "openai", CanonicalModel: "gpt-driver"}},
			{{Namespace: "google", CanonicalModel: "gemini-image"}},
		},
	}
	svc := NewUserModelRestrictionService(repo)
	ctx := WithUserModelRestrictionAdmission(context.Background(), svc, 42)
	require.NoError(t, EnforceUserModelRestriction(ctx, &Account{Platform: PlatformOpenAI}, "gpt-driver", "gemini-image"))
	require.Len(t, repo.admissionInputs, 2)
	require.Equal(t, "openai", repo.admissionInputs[0].Namespace)
	require.Empty(t, repo.admissionInputs[1].Namespace)
}

func TestCarryDistillationTelemetryCarriesFinalPolicyContext(t *testing.T) {
	repo := &restrictionServiceRepoStub{admission: &ModelIdentityAdmissionSnapshot{ActiveRules: []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-denied", Enabled: true}}, Candidates: [][]CanonicalModelIdentity{{{Namespace: "openai", CanonicalModel: "gpt-denied"}}}}}
	svc := NewUserModelRestrictionService(repo)
	parent := WithUserModelRestrictionAdmission(context.Background(), svc, 42)
	parent = WithModelIdentityContext(parent, "responses", "primary", "")
	detached := CarryDistillationTelemetry(parent, context.Background())
	err := EnforceUserModelRestrictionWithoutBudget(detached, &Account{Platform: PlatformOpenAI}, "gpt-denied")
	require.ErrorIs(t, err, ErrUserModelRestrictionDenied)
	require.Equal(t, "responses", repo.admissionInputs[0].Protocol)
}

func TestToolModelWithSameNameAsPrimaryRetainsItsIdentityRole(t *testing.T) {
	models := UserModelRestrictionModelsFromPayload("shared-name", []byte(`{"model":"shared-name","tools":[{"model":"shared-name"}]}`))
	require.Equal(t, []string{"shared-name", "shared-name"}, models)
	repo := &restrictionServiceRepoStub{admission: &ModelIdentityAdmissionSnapshot{
		ActiveRules: []UserModelRestrictionRule{{Namespace: "google", CanonicalModel: "tool-model", Enabled: true}},
		Candidates: [][]CanonicalModelIdentity{
			{{Namespace: "openai", CanonicalModel: "driver-model"}},
			{{Namespace: "google", CanonicalModel: "tool-model"}},
		},
	}}
	ctx := WithUserModelRestrictionAdmission(context.Background(), NewUserModelRestrictionService(repo), 42)
	err := EnforceUserModelRestrictionWithoutBudget(ctx, &Account{Platform: PlatformOpenAI}, models...)
	require.ErrorIs(t, err, ErrUserModelRestrictionDenied)
	require.Len(t, repo.admissionInputs, 2)
	require.Equal(t, "primary", repo.admissionInputs[0].Role)
	require.Equal(t, "tool", repo.admissionInputs[1].Role)
}
