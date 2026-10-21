package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

type identityContextRepo struct {
	service.UserModelRestrictionRepository
	received []service.ModelIdentityInput
}

func (r *identityContextRepo) GetModelIdentityCatalog(context.Context) (*service.ModelIdentityCatalog, error) {
	return nil, nil
}
func (r *identityContextRepo) ReplaceModelIdentityCatalog(context.Context, int64, int64, []service.ModelIdentityCatalogEntry, []service.ModelIdentityAlias) (*service.ModelIdentityCatalog, error) {
	return nil, nil
}
func (r *identityContextRepo) GetModelIdentityAdmission(_ context.Context, _ int64, inputs []service.ModelIdentityInput) (*service.ModelIdentityAdmissionSnapshot, error) {
	r.received = inputs
	candidates := make([][]service.CanonicalModelIdentity, len(inputs))
	return &service.ModelIdentityAdmissionSnapshot{Candidates: candidates}, nil
}
func TestModelIdentityRouteContextReachesFinalAdmission(t *testing.T) {
	for path, want := range map[string]string{"/v1/responses": "responses", "/responses": "responses", "/backend-api/codex/responses": "responses", "/v1/live": "responses", "/backend-api/codex/realtime/calls": "responses", "/v1/chat/completions": "chat_completions", "/v1/messages": "messages", "/v1/messages/count_tokens": "count_tokens", "/v1/embeddings": "embeddings", "/v1/images/generations/async": "images", "/v1beta/models/*modelAction": "gemini", "/v1beta/cachedContents": "cached_content"} {
		t.Run(path, func(t *testing.T) {
			require.Equal(t, want, modelIdentityProtocolForRoute(path))
			r := &identityContextRepo{}
			ctx := service.WithUserModelRestrictionAdmission(context.Background(), service.NewUserModelRestrictionService(r), 42)
			ctx = service.WithModelIdentityContext(ctx, modelIdentityProtocolForRoute(path), "primary", "")
			require.NoError(t, service.EnforceUserModelRestrictionWithoutBudget(ctx, &service.Account{Platform: service.PlatformOpenAI}, "alias", "tool"))
			require.Len(t, r.received, 2)
			require.Equal(t, want, r.received[0].Protocol)
			require.Equal(t, "primary", r.received[0].Role)
			require.Equal(t, "tool", r.received[1].Role)
		})
	}
}
