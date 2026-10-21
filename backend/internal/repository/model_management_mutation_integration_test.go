//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestModelManagementMutationDurableReplay(t *testing.T) {
	ctx := context.Background()
	user, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("management-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx) })
	repo := NewUserModelRestrictionRepository(integrationDB).(*userModelRestrictionRepository)
	svc := service.NewUserModelRestrictionService(repo)
	m := service.ModelManagementMutation{ActorID: user.ID, Key: "durable-policy", Resource: fmt.Sprintf("policy:%d", user.ID), Reason: "reviewed", Payload: json.RawMessage(`{"expected_revision":0}`)}
	calls := 0
	fn := func(ctx context.Context) (json.RawMessage, error) {
		calls++
		s, e := svc.Replace(ctx, user.ID, 0, &user.ID, []service.UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-5.4", Action: "deny_model", Reason: "reviewed", Enabled: true}})
		if e != nil {
			return nil, e
		}
		return json.Marshal(s)
	}
	first, err := svc.MutateManagement(ctx, m, fn)
	require.NoError(t, err)
	// A fresh repository/service must replay the original result, without touching revision.
	fresh := service.NewUserModelRestrictionService(NewUserModelRestrictionRepository(integrationDB))
	second, err := fresh.MutateManagement(ctx, m, fn)
	require.NoError(t, err)
	require.JSONEq(t, string(first), string(second))
	require.Equal(t, 1, calls)
	got, err := svc.Get(ctx, user.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Revision)
	m.Payload = json.RawMessage(`{"expected_revision":1}`)
	_, err = fresh.MutateManagement(ctx, m, fn)
	require.ErrorIs(t, err, service.ErrModelManagementIdempotencyConflict)
	m.Key = "rollback"
	_, err = svc.MutateManagement(ctx, m, func(ctx context.Context) (json.RawMessage, error) {
		_, e := svc.Replace(ctx, user.ID, 1, &user.ID, []service.UserModelRestrictionRule{})
		require.NoError(t, e)
		return nil, fmt.Errorf("injected failure")
	})
	require.Error(t, err)
	got, err = svc.Get(ctx, user.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Revision)
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM model_management_mutations WHERE actor_id=$1 AND idempotency_key='rollback'`, user.ID).Scan(&n))
	require.Zero(t, n)
}
