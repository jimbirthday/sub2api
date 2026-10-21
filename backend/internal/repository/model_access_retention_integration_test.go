//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestModelAccessRetentionPreservesCurrentAndActive(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.AddDate(-2, 0, 0)
	u, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("retention-%d@example.com", now.UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(u.ID).Exec(ctx) })
	svc := service.NewUserModelRestrictionService(NewUserModelRestrictionRepository(integrationDB))
	_, err = svc.Replace(ctx, u.ID, 0, &u.ID, []service.UserModelRestrictionRule{})
	require.NoError(t, err)
	_, err = svc.Replace(ctx, u.ID, 1, &u.ID, []service.UserModelRestrictionRule{})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_model_restriction_versions SET created_at=$1 WHERE user_id=$2`, old, u.ID)
	require.NoError(t, err)
	active, done := uuid.New(), uuid.New()
	for _, a := range []struct {
		id        uuid.UUID
		status    string
		completed any
	}{{active, "active", nil}, {done, "completed", old.Add(time.Minute)}} {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO distillation_risk_attempts(attempt_id,user_id,provider,model,observed_concurrency,started_at,completed_at,status) VALUES($1,$2,'openai','gpt-5.4',1,$3,$4,$5)`, a.id, u.ID, old, a.completed, a.status)
		require.NoError(t, err)
	}
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO model_access_decisions(user_id,policy_revision,catalog_revision,decision,created_at) VALUES($1,1,1,'allowed',$2)`, u.ID, old)
	require.NoError(t, err)
	repo := &distillationTelemetryRepository{db: integrationDB}
	require.NoError(t, repo.CleanupModelAccessHistory(ctx, now))
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM user_model_restriction_versions WHERE user_id=$1`, u.ID).Scan(&n))
	require.Equal(t, 1, n)
	v, err := svc.GetVersion(ctx, u.ID, 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, v.Revision)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM distillation_risk_attempts WHERE attempt_id=$1`, active).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM distillation_risk_attempts WHERE attempt_id=$1`, done).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM model_access_decisions WHERE user_id=$1`, u.ID).Scan(&n))
	require.Zero(t, n)
}

func TestDistillationHMACSevenDayBoundary(t *testing.T) {
	ctx := context.Background()
	now := service.DistillationWindowStart(time.Now().UTC())
	u, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("hmac-retention-%d@example.com", now.UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, integrationEntClient.User.DeleteOneID(u.ID).Exec(ctx)) })
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for i, days := range []int{7, 6} {
		at := now.AddDate(0, 0, -days)
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO distillation_risk_attempts(attempt_id,user_id,provider,model,observed_concurrency,started_at,status,prompt_hmac,hmac_day) VALUES($1,$2,'openai','gpt-5.4',1,$3::timestamptz,'active',$4,($3::timestamptz)::date)`, ids[i], u.ID, at, make([]byte, 32))
		require.NoError(t, err)
	}
	repo := &distillationTelemetryRepository{db: integrationDB}
	_, err = repo.CleanupDailyHMAC(ctx, now)
	require.NoError(t, err)
	for i, id := range ids {
		var hasHMAC bool
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT prompt_hmac IS NOT NULL FROM distillation_risk_attempts WHERE attempt_id=$1`, id).Scan(&hasHMAC))
		require.Equal(t, i == 1, hasHMAC)
	}
}
