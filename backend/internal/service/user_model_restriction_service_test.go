package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type restrictionServiceRepoStub struct {
	UserModelRestrictionRepository
	calls           int
	rules           []UserModelRestrictionRule
	err             error
	admission       *ModelIdentityAdmissionSnapshot
	admissionInputs []ModelIdentityInput
	admissionUserID int64
}

func (r *restrictionServiceRepoStub) ReplaceSnapshot(_ context.Context, user, revision int64, by *int64, rules []UserModelRestrictionRule) (*UserModelRestrictionSnapshot, error) {
	r.calls++
	r.rules = rules
	return &UserModelRestrictionSnapshot{UserID: user, Revision: revision + 1, UpdatedBy: by, Rules: rules}, r.err
}
func (r *restrictionServiceRepoStub) GetSnapshot(context.Context, int64) (*UserModelRestrictionSnapshot, error) {
	return &UserModelRestrictionSnapshot{Rules: r.rules}, r.err
}
func (r *restrictionServiceRepoStub) GetVersion(context.Context, int64, int64) (*UserModelRestrictionSnapshot, error) {
	return nil, r.err
}
func (r *restrictionServiceRepoStub) ListVersions(context.Context, int64, int64, int) (*UserModelRestrictionHistory, error) {
	return nil, r.err
}

func TestUserModelRestrictionServiceValidation(t *testing.T) {
	actor := int64(9)
	good := UserModelRestrictionRule{Namespace: "openai", CanonicalModel: "gpt-x", Enabled: true}
	for _, tc := range []struct {
		name     string
		revision int64
		rules    []UserModelRestrictionRule
	}{
		{"nil", 0, nil}, {"negative revision", -1, []UserModelRestrictionRule{}}, {"overflow", math.MaxInt64, []UserModelRestrictionRule{}},
		{"duplicate", 0, []UserModelRestrictionRule{good, {Namespace: " OPENAI ", CanonicalModel: "GPT-X"}}},
		{"blank namespace", 0, []UserModelRestrictionRule{{Namespace: " ", CanonicalModel: "x"}}},
		{"long model", 0, []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: strings.Repeat("x", 256)}}},
		{"long namespace", 0, []UserModelRestrictionRule{{Namespace: strings.Repeat("x", 65), CanonicalModel: "x"}}},
		{"control", 0, []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "x\ny"}}},
		{"long reason", 0, []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "x", Reason: strings.Repeat("界", 2001)}}},
		{"too many", 0, make([]UserModelRestrictionRule, 1001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &restrictionServiceRepoStub{}
			_, err := NewUserModelRestrictionService(repo).Replace(context.Background(), 1, tc.revision, &actor, tc.rules)
			require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
			require.Zero(t, repo.calls)
		})
	}
	repo := &restrictionServiceRepoStub{}
	input := []UserModelRestrictionRule{{Namespace: " OpenAI ", CanonicalModel: " GPT-X ", CreatedBy: new(int64)}}
	snapshot, err := NewUserModelRestrictionService(repo).Replace(context.Background(), 1, 0, &actor, input)
	require.NoError(t, err)
	require.Equal(t, int64(1), snapshot.Revision)
	require.Equal(t, "openai", repo.rules[0].Namespace)
	require.Equal(t, "gpt-x", repo.rules[0].CanonicalModel)
	require.Equal(t, actor, *repo.rules[0].CreatedBy)
	require.Equal(t, " OpenAI ", input[0].Namespace)
}

func TestUserModelRestrictionServiceErrorMappingAndHistory(t *testing.T) {
	actor := int64(9)
	ctx := context.Background()
	for _, tc := range []struct{ source, want error }{{ErrUserModelRestrictionRevisionConflict, ErrUserModelRestrictionRevisionConflict}, {errors.New("database offline"), ErrUserModelRestrictionUnavailable}} {
		repo := &restrictionServiceRepoStub{err: tc.source}
		_, err := NewUserModelRestrictionService(repo).Replace(ctx, 1, 0, &actor, []UserModelRestrictionRule{})
		require.ErrorIs(t, err, tc.want)
	}
	svc := NewUserModelRestrictionService(&restrictionServiceRepoStub{err: ErrUserModelRestrictionNotFound})
	_, err := svc.GetVersion(ctx, 1, 2)
	require.ErrorIs(t, err, ErrUserModelRestrictionNotFound)
	_, err = svc.GetVersion(ctx, 1, 0)
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	_, err = svc.History(ctx, 1, -1, 20)
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	_, err = svc.History(ctx, 1, 0, 101)
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
}

func TestUserModelRestrictionServiceExpiredAndNamespace(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Second)
	svc := NewUserModelRestrictionService(&restrictionServiceRepoStub{rules: []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-x", Enabled: true, ExpiresAt: &expired}, {Namespace: "anthropic", CanonicalModel: "gpt-x", Enabled: true}}})
	allowed, err := svc.CheckCanonical(context.Background(), 1, "openai", "gpt-x", now)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = svc.CheckCanonical(context.Background(), 1, "anthropic", "gpt-x", now)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestUserModelRestrictionTransportNamespaceAndEarlyDenial(t *testing.T) {
	require.Empty(t, RestrictionNamespaceForPlatform(PlatformAntigravity))
	require.Empty(t, RestrictionNamespaceForPlatform(PlatformComposite))
	require.Equal(t, "google", RestrictionNamespaceForPlatform(PlatformGemini))
	require.Equal(t, "deepseek", RestrictionNamespaceForPlatform(PlatformDeepseek))
	now := time.Now()
	svc := NewUserModelRestrictionService(&restrictionServiceRepoStub{rules: []UserModelRestrictionRule{{Namespace: "anthropic", CanonicalModel: "claude-x", Enabled: true}}})
	for _, platform := range []string{PlatformAntigravity, PlatformComposite, PlatformOpenAI} {
		require.ErrorIs(t, svc.CheckRequest(context.Background(), 1, platform, "claude-x", "upstream-alias", now), ErrUserModelRestrictionDenied)
		require.ErrorIs(t, svc.CheckRequest(context.Background(), 1, platform, "client-alias", "claude-x", now), ErrUserModelRestrictionDenied)
	}
	require.NoError(t, svc.CheckRequest(context.Background(), 1, PlatformComposite, "unknown-client-alias", "", now))
	allowed, err := svc.CheckCanonical(context.Background(), 1, "", "claude-x", now)
	require.False(t, allowed)
	require.ErrorIs(t, err, ErrUserModelRestrictionIdentityUnknown)
	allowed, err = svc.CheckCanonical(context.Background(), 1, "openai", "claude-x", now)
	require.NoError(t, err)
	require.True(t, allowed)
}

func (r *restrictionServiceRepoStub) GetModelIdentityCatalog(context.Context) (*ModelIdentityCatalog, error) {
	return &ModelIdentityCatalog{Revision: 1, Models: []ModelIdentityCatalogEntry{}, Aliases: []ModelIdentityAlias{}}, r.err
}
func (r *restrictionServiceRepoStub) ReplaceModelIdentityCatalog(context.Context, int64, int64, []ModelIdentityCatalogEntry, []ModelIdentityAlias) (*ModelIdentityCatalog, error) {
	return nil, r.err
}
func (r *restrictionServiceRepoStub) GetModelIdentityAdmission(_ context.Context, userID int64, inputs []ModelIdentityInput) (*ModelIdentityAdmissionSnapshot, error) {
	r.admissionUserID = userID
	r.admissionInputs = append([]ModelIdentityInput(nil), inputs...)
	if r.err != nil {
		return nil, r.err
	}
	if r.admission != nil {
		result := *r.admission
		result.CheckedAt = time.Now()
		return &result, nil
	}
	now := time.Now()
	snapshot := &ModelIdentityAdmissionSnapshot{PolicyRevision: 1, CatalogRevision: 1, CheckedAt: now, Candidates: make([][]CanonicalModelIdentity, len(inputs))}
	for _, rule := range r.rules {
		if restrictionRuleActive(rule, now) {
			snapshot.ActiveRules = append(snapshot.ActiveRules, rule)
		}
	}
	// 固定测试目录，生产解析来自数据库，不从模型前缀猜测。
	catalog := []CanonicalModelIdentity{{Namespace: "openai", CanonicalModel: "gpt-driver"}, {Namespace: "google", CanonicalModel: "gemini-image"}, {Namespace: "openai", CanonicalModel: "gpt-x"}, {Namespace: "anthropic", CanonicalModel: "gpt-x"}, {Namespace: "anthropic", CanonicalModel: "claude-x"}, {Namespace: "openai", CanonicalModel: "claude-x"}}
	for i, input := range inputs {
		for _, identity := range catalog {
			if identity.CanonicalModel == input.Model && (input.Namespace == "" || input.Namespace == identity.Namespace) {
				snapshot.Candidates[i] = append(snapshot.Candidates[i], identity)
			}
		}
	}
	return snapshot, nil
}

func TestUserModelRestrictionFollowsUserAcrossAccountsAndTransports(t *testing.T) {
	repo := &restrictionServiceRepoStub{
		rules: []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-driver", Enabled: true}},
	}
	svc := NewUserModelRestrictionService(repo)
	for _, account := range []*Account{
		{ID: 101, Platform: PlatformOpenAI},
		{ID: 202, Platform: PlatformComposite},
	} {
		ctx := WithUserModelRestrictionAdmission(context.Background(), svc, 42)
		err := EnforceUserModelRestriction(ctx, account, "gpt-driver")
		require.ErrorIs(t, err, ErrUserModelRestrictionDenied)
		require.Equal(t, int64(42), repo.admissionUserID)
	}

	// A different authenticated user gets an independent policy snapshot. This
	// assertion locks the admission key to user identity rather than API key,
	// group, or selected upstream account.
	ctx := WithUserModelRestrictionAdmission(context.Background(), svc, 77)
	_ = EnforceUserModelRestriction(ctx, &Account{ID: 303, Platform: PlatformOpenAI}, "gpt-driver")
	require.Equal(t, int64(77), repo.admissionUserID)
}
