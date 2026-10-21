package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type changingFinalPolicyRepo struct {
	UserModelRestrictionRepository
	ModelIdentityRepository
	calls  int
	models [][]ModelIdentityInput
}

func (r *changingFinalPolicyRepo) GetModelIdentityAdmission(_ context.Context, _ int64, inputs []ModelIdentityInput) (*ModelIdentityAdmissionSnapshot, error) {
	r.calls++
	r.models = append(r.models, append([]ModelIdentityInput(nil), inputs...))
	snapshot := &ModelIdentityAdmissionSnapshot{PolicyRevision: int64(r.calls), CatalogRevision: 1, CheckedAt: time.Now()}
	for range inputs {
		snapshot.Candidates = append(snapshot.Candidates, []CanonicalModelIdentity{{Namespace: "openai", CanonicalModel: "gpt-driver"}})
	}
	if r.calls > 1 {
		snapshot.ActiveRules = []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-driver", Action: "deny_model", Enabled: true}}
	}
	return snapshot, nil
}

func TestOpenAIResponsesRechecksMappedModelImmediatelyBeforeHTTPWrite(t *testing.T) {
	body := []byte(`{"model":"public-alias","stream":false,"input":"hello"}`)
	upstream := &httpUpstreamRecorder{}
	repo := &changingFinalPolicyRepo{}
	ctx := WithUserModelRestrictionAdmission(context.Background(), NewUserModelRestrictionService(repo), 42)
	c := newOpenAIRejectedFieldTestContext(body)
	c.Request = c.Request.WithContext(ctx)
	account := newOpenAIRejectedFieldTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"public-alias": "gpt-driver"}
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	_, err := newOpenAIRejectedFieldTestService(upstream).Forward(ctx, c, account, body)
	require.ErrorIs(t, err, ErrUserModelRestrictionDenied)
	require.GreaterOrEqual(t, repo.calls, 2, "early preflight cannot authorize a later network write")
	require.Equal(t, "gpt-driver", repo.models[len(repo.models)-1][0].Model)
	require.Empty(t, upstream.requests, "policy changed after preflight; no provider request may be sent")
}
