//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserModelRestrictionRepositoryConcurrentCAS(t *testing.T) {
	ctx := context.Background()
	user, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("restriction-cas-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx) })
	repo := NewUserModelRestrictionRepository(integrationDB)
	actor := user.ID
	race := func(revision int64) {
		t.Helper()
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := repo.ReplaceSnapshot(ctx, user.ID, revision, &actor, []service.UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-5.4", Enabled: true}})
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		success, conflicts := 0, 0
		for err := range errs {
			if err == nil {
				success++
			} else if errors.Is(err, service.ErrUserModelRestrictionRevisionConflict) {
				conflicts++
			} else {
				t.Fatalf("unexpected writer error: %v", err)
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, conflicts)
	}
	// 第一轮同时建 head；第二轮在既有 head 上竞争同一 revision。
	race(0)
	race(1)
	first, err := repo.GetVersion(ctx, user.ID, 1)
	require.NoError(t, err)
	clear, err := repo.ReplaceSnapshot(ctx, user.ID, 2, &actor, []service.UserModelRestrictionRule{})
	require.NoError(t, err)
	require.Equal(t, int64(3), clear.Revision)
	require.Empty(t, clear.Rules)
	again, err := repo.GetVersion(ctx, user.ID, 1)
	require.NoError(t, err)
	require.Equal(t, first, again)
	history, err := repo.ListVersions(ctx, user.ID, 0, 2)
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	require.Equal(t, int64(3), history.Items[0].Revision)
	require.Zero(t, history.Items[0].RuleCount)
	tail, err := repo.ListVersions(ctx, user.ID, *history.NextBeforeRevision, 2)
	require.NoError(t, err)
	require.Len(t, tail.Items, 1)
	require.Equal(t, int64(1), tail.Items[0].Revision)
}

func TestModelIdentityCatalogAdmissionAndCAS(t *testing.T) {
	ctx := context.Background()
	user, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("identity-catalog-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx) })
	repo := NewUserModelRestrictionRepository(integrationDB)
	identityRepo := repo.(service.ModelIdentityRepository)
	svc := service.NewUserModelRestrictionService(repo)
	cat, err := svc.Catalog(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cat.Models), 70)
	actor := user.ID
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformAntigravity, Model: "unregistered"}})
	require.NoError(t, err)
	_, err = svc.Replace(ctx, user.ID, 0, &actor, []service.UserModelRestrictionRule{{Namespace: "anthropic", CanonicalModel: "claude-sonnet-4-5-20250929", Enabled: true}})
	require.NoError(t, err)
	decision, err := svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformAntigravity, Model: "claude-sonnet-4-5"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionDenied)
	require.Equal(t, int64(1), decision.PolicyRevision)
	require.Equal(t, cat.Revision, decision.CatalogRevision)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Namespace: "openai", Transport: "openai", Model: "claude-sonnet-4-5"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionIdentityUnknown)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformAntigravity, Model: "vendor-custom"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionIdentityUnknown)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformAntigravity, Model: "gpt-5.4"}, {Transport: service.PlatformAntigravity, Model: "claude-sonnet-4-5"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionDenied)
	// 无模糊子串或计费normalize，即使看起来像已知模型也不可推断。
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Namespace: "openai", Model: "vendor-gpt-5.4-extra"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionIdentityUnknown)
	catalogModels := append(append([]service.ModelIdentityCatalogEntry{}, cat.Models...), service.ModelIdentityCatalogEntry{Namespace: "custom:acme", CanonicalModel: "private-model", DisplayName: "Private", Source: "admin:test"})
	catalogAliases := append(append([]service.ModelIdentityAlias{}, cat.Aliases...), service.ModelIdentityAlias{Namespace: "custom:acme", Transport: service.PlatformAntigravity, Alias: "private-alias", CanonicalModel: "private-model"})
	updated, err := svc.ReplaceCatalog(ctx, cat.Revision, actor, catalogModels, catalogAliases)
	require.NoError(t, err)
	require.Equal(t, cat.Revision+1, updated.Revision)
	// 后续目录写入不覆盖旧版本。
	var oldCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_identity_catalog_models WHERE revision=$1`, cat.Revision).Scan(&oldCount))
	require.Equal(t, len(cat.Models), oldCount)
	_, err = svc.ReplaceCatalog(ctx, cat.Revision, actor, catalogModels, catalogAliases)
	require.ErrorIs(t, err, service.ErrUserModelRestrictionRevisionConflict)
	decision, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformAntigravity, Model: "private-alias"}})
	require.NoError(t, err)
	require.Equal(t, "custom:acme", decision.Identities[0].Namespace)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformOpenAI, Model: "private-alias"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionIdentityUnknown)
	// 两个原厂登记同一未限定别名必须产生歧义，而不是按字符串猜厂商。
	ambiguousModels := append(catalogModels, service.ModelIdentityCatalogEntry{Namespace: "custom:other", CanonicalModel: "private-model", Source: "admin:test"})
	ambiguousAliases := append(catalogAliases, service.ModelIdentityAlias{Namespace: "custom:other", Alias: "private-alias", CanonicalModel: "private-model"})
	ambiguous, err := svc.ReplaceCatalog(ctx, updated.Revision, actor, ambiguousModels, ambiguousAliases)
	require.NoError(t, err)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Transport: service.PlatformAntigravity, Model: "private-alias"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionIdentityUnknown)
	// 策略有效性使用数据库时间；全部过期后未知模型不阻塞。
	expired := time.Now().Add(-time.Hour)
	_, err = svc.Replace(ctx, user.ID, 1, &actor, []service.UserModelRestrictionRule{{Namespace: "anthropic", CanonicalModel: "claude-sonnet-4-5-20250929", Enabled: true, ExpiresAt: &expired}})
	require.NoError(t, err)
	result, err := identityRepo.GetModelIdentityAdmission(ctx, user.ID, []service.ModelIdentityInput{{Model: "unknown"}})
	require.NoError(t, err)
	require.Empty(t, result.ActiveRules)
	require.Equal(t, ambiguous.Revision, result.CatalogRevision)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Model: "unknown"}})
	require.NoError(t, err)
	// 两个目录writer使用同一revision时只能一个成功。
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := svc.ReplaceCatalog(ctx, ambiguous.Revision, actor, cat.Models, cat.Aliases)
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, service.ErrUserModelRestrictionRevisionConflict) {
			conflicts++
		} else {
			t.Fatalf("catalog writer: %v", e)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
	_, err = svc.Replace(ctx, user.ID, 2, &actor, []service.UserModelRestrictionRule{{Namespace: "custom:missing", CanonicalModel: "not-registered", Enabled: true}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionInvalid)
	// 模拟目录上线前的旧别名规则：只允许精确目录映射，不能漏拦。
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_model_restriction_rules SET canonical_model='claude-sonnet-4-5', expires_at=NULL WHERE user_id=$1 AND revision=2`, user.ID)
	require.NoError(t, err)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Model: "claude-sonnet-4-5-20250929"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionDenied)
	// 无法解析的历史禁用身份必须fail closed，不能变成永不匹配的规则。
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_model_restriction_rules SET canonical_model='missing-legacy-model' WHERE user_id=$1 AND revision=2`, user.ID)
	require.NoError(t, err)
	_, err = svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Model: "gpt-5.4"}})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionIdentityUnknown)
	// Restore current catalog through normal versioned update; historical test versions remain immutable.
	_, err = svc.ReplaceCatalog(ctx, ambiguous.Revision+1, actor, cat.Models, cat.Aliases)
	require.NoError(t, err)
}

func TestUserModelPolicyActionsHistoryAndJournal(t *testing.T) {
	ctx := context.Background()
	user, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("policy-actions-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx) })
	repo := NewUserModelRestrictionRepository(integrationDB)
	svc := service.NewUserModelRestrictionService(repo)
	cap := 2
	future := time.Now().Add(time.Hour)
	snapshot, err := svc.Replace(ctx, user.ID, 0, &user.ID, []service.UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-5.4", Enabled: true, Action: "cap_concurrency", LimitValue: &cap}, {Namespace: "openai", CanonicalModel: "gpt-5.4", Enabled: true, StartsAt: &future}})
	require.NoError(t, err)
	require.Len(t, snapshot.Rules, 2)
	decision, err := svc.CheckModels(ctx, user.ID, []service.ModelIdentityInput{{Namespace: "openai", Model: "gpt-5.4"}})
	require.NoError(t, err)
	require.Equal(t, 2, decision.ModelCaps["openai:gpt-5.4"].Concurrency)
	require.NoError(t, svc.CheckRequest(ctx, user.ID, "", "gpt-5.4", "", time.Now()))
	next, err := svc.Replace(ctx, user.ID, 1, &user.ID, snapshot.Rules)
	require.NoError(t, err)
	require.Equal(t, snapshot.Rules[0].RuleID, next.Rules[0].RuleID)
	list, err := svc.ListCurrent(ctx, service.ModelRestrictionListOptions{UserID: user.ID, Limit: 1, State: "active"})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Nil(t, list.NextBeforeID)
	var journalCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_access_change_journal WHERE aggregate_type='user_policy' AND aggregate_id=$1`, user.ID).Scan(&journalCount))
	require.Equal(t, 2, journalCount)
	_, err = svc.Replace(ctx, user.ID, 0, &user.ID, []service.UserModelRestrictionRule{})
	require.ErrorIs(t, err, service.ErrUserModelRestrictionRevisionConflict)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_access_change_journal WHERE aggregate_type='user_policy' AND aggregate_id=$1`, user.ID).Scan(&journalCount))
	require.Equal(t, 2, journalCount)
}
