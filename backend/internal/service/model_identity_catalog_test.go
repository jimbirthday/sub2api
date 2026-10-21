package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeModelIdentityCatalogRequiresExactOriginsAndUniqueAliasScope(t *testing.T) {
	models := []ModelIdentityCatalogEntry{{Namespace: "openai", CanonicalModel: "gpt-5.4", DisplayName: "GPT 5.4", Source: "openai-default"}}
	aliases := []ModelIdentityAlias{{Namespace: "openai", Transport: "", Alias: "gpt-latest", CanonicalModel: "gpt-5.4"}}
	got, als, err := NormalizeModelIdentityCatalog(models, aliases)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", got[0].CanonicalModel)
	require.Equal(t, "gpt-latest", als[0].Alias)
	_, _, err = NormalizeModelIdentityCatalog(models, []ModelIdentityAlias{{Namespace: "openai", Alias: "gpt-latest", CanonicalModel: "gpt-5.4"}, {Namespace: "openai", Alias: "gpt-latest", CanonicalModel: "gpt-5.4"}})
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	_, _, err = NormalizeModelIdentityCatalog([]ModelIdentityCatalogEntry{{Namespace: "billing", CanonicalModel: "gpt-5.4", DisplayName: "x", Source: "billing"}}, nil)
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
}
func TestNormalizeModelIdentityCatalogAliasMustTargetRegisteredModel(t *testing.T) {
	_, _, err := NormalizeModelIdentityCatalog([]ModelIdentityCatalogEntry{{Namespace: "openai", CanonicalModel: "gpt-5.4", DisplayName: "x", Source: "seed"}}, []ModelIdentityAlias{{Namespace: "openai", Alias: "unknown", CanonicalModel: "gpt-5.5"}})
	require.Error(t, err)
}

func TestModelIdentityAliasContextAndStableIDs(t *testing.T) {
	models := []ModelIdentityCatalogEntry{{Namespace: "openai", CanonicalModel: "a", Source: "test"}, {Namespace: "openai", CanonicalModel: "b", Source: "test"}}
	aliases := []ModelIdentityAlias{{Namespace: "openai", Alias: "shared", CanonicalModel: "a", Protocol: "responses", Role: "primary"}, {Namespace: "openai", Alias: "shared", CanonicalModel: "b", Protocol: "chat_completions", Role: "primary"}}
	_, _, err := NormalizeModelIdentityCatalog(models, aliases)
	require.NoError(t, err)
	aliases[1].Protocol = "responses"
	_, _, err = NormalizeModelIdentityCatalog(models, aliases)
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	aliases[1].Protocol = "chat_completions"
	aliases[0].AliasID = "1bfc1709-2955-45b5-a08c-4c456c4b3008"
	aliases[1].AliasID = aliases[0].AliasID
	_, _, err = NormalizeModelIdentityCatalog(models, aliases)
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
}

func TestFilterModelsForListingUsesOneSnapshotAndHidesUnknownWithActiveRules(t *testing.T) {
	repo := &restrictionServiceRepoStub{
		rules: []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-driver", Enabled: true}},
	}
	svc := NewUserModelRestrictionService(repo)
	result, err := svc.FilterModelsForListing(context.Background(), 42, []ModelIdentityInput{
		{Namespace: "openai", Transport: "openai", Model: "gpt-driver"},
		{Namespace: "openai", Transport: "openai", Model: "unknown"},
		{Namespace: "google", Transport: "gemini", Model: "gemini-image"},
	})
	require.NoError(t, err)
	require.Equal(t, []bool{false, false, true}, result.Allowed)
	require.Len(t, repo.admissionInputs, 3)
}

func TestNormalizeModelIdentityCatalogRejectsShadowAndConflictingScope(t *testing.T) {
	models := []ModelIdentityCatalogEntry{{Namespace: "openai", CanonicalModel: "a", Source: "test"}, {Namespace: "openai", CanonicalModel: "b", Source: "test"}}
	_, _, err := NormalizeModelIdentityCatalog(models, []ModelIdentityAlias{{Namespace: "openai", Alias: "a", CanonicalModel: "b"}})
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	_, _, err = NormalizeModelIdentityCatalog(models, []ModelIdentityAlias{{Namespace: "openai", Alias: "name", CanonicalModel: "a"}, {Namespace: "openai", Transport: "openai", Alias: "name", CanonicalModel: "b"}})
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	_, _, err = NormalizeModelIdentityCatalog(models, []ModelIdentityAlias{{Namespace: "openai", Alias: "gpt-*", CanonicalModel: "a"}})
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
	_, _, err = NormalizeModelIdentityCatalog(models, []ModelIdentityAlias{{Namespace: "openai", Transport: "unknown", Alias: "name", CanonicalModel: "a"}})
	require.ErrorIs(t, err, ErrUserModelRestrictionInvalid)
}
