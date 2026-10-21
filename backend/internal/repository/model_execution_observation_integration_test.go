//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestObservationCapacityDetachesWithoutFakingTermination(t *testing.T) {
	ctx := context.Background()
	var uid int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',1,50) RETURNING id`, fmt.Sprintf("observe-detach-%d@test.invalid", time.Now().UnixNano())).Scan(&uid))
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid) })
	repo := &distillationTelemetryRepository{db: integrationDB}
	rdb := testRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900).(*concurrencyCache)
	op := fmt.Sprintf("observe-%d", uid)
	require.NoError(t, repo.CreateExecutionAttempt(ctx, service.ExecutionAttempt{UserID: uid, OperationID: op, AttemptID: op, AccountID: 99, Provider: "openai", OwnerEpoch: 1, Targets: []string{"openai:gpt-5"}, FundsState: "none"}))
	require.NoError(t, repo.TransitionExecutionAttempt(ctx, op, "unknown", 1))
	_, err := integrationDB.ExecContext(ctx, `UPDATE model_execution_attempts SET enforces_limits=FALSE WHERE attempt_id=$1`, op)
	require.NoError(t, err)
	_, err = cache.ReserveModelExecution(ctx, service.ModelExecutionReservation{UserID: uid, OperationID: op, Targets: []string{"openai:gpt-5"}})
	require.NoError(t, err)
	require.NoError(t, cache.RestoreExecutionProviderAttempt(ctx, 99, "openai", op))
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(99), redis.Z{Score: float64(time.Now().Unix()), Member: "actual-live-request"}).Err())
	for range 2 {
		require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	}
	require.Equal(t, "0", rdb.HGet(ctx, modelExecutionStateKey(uid), "running").Val())
	require.False(t, rdb.HExists(ctx, modelExecutionOpsKey(uid), op).Val())
	require.Equal(t, []string{"actual-live-request"}, rdb.ZRange(ctx, accountSlotKey(99), 0, -1).Val())
	require.False(t, rdb.SIsMember(ctx, "model_execution:provider:openai", op).Val())
	var state string
	var detached bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state,observation_detached_at IS NOT NULL FROM model_execution_attempts WHERE attempt_id=$1`, op).Scan(&state, &detached))
	require.Equal(t, "unknown", state)
	require.True(t, detached)
}

func TestObservationIsolationMigrationPreservesExplicitCapsAndAsyncHolds(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var uid int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',1,50) RETURNING id`, fmt.Sprintf("observe-migration-%d@test.invalid", time.Now().UnixNano())).Scan(&uid))
	_, err = tx.ExecContext(ctx, `INSERT INTO user_model_restriction_versions(user_id,revision) VALUES($1,1);`, uid)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO user_model_restriction_rules(user_id,revision,namespace,canonical_model,action,limit_value) VALUES($1,1,'openai','gpt-5','cap_concurrency',2)`, uid)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO model_execution_attempts(user_id,operation_id,attempt_id,policy_revision,targets,hold_id) VALUES
($1,'observed','observed',0,'["openai:gpt-5"]',''),
($1,'capped','capped',1,'["openai:gpt-5"]',''),
($1,'uncapped','uncapped',1,'["openai:other"]',''),
($1,'async','async',0,'["openai:gpt-5"]','batch-funds-hold'),
($1,'no-evidence','no-evidence',0,'["openai:gpt-5"]','')`, uid)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO distillation_risk_attempts(attempt_id,user_id,operation_id,provider,model,observed_concurrency,started_at,status)
SELECT gen_random_uuid(),user_id,operation_id,'openai','gpt-5',1,NOW(),'active'
FROM model_execution_attempts WHERE user_id=$1 AND attempt_id<>'no-evidence'`, uid)
	require.NoError(t, err)
	sql, err := migrations.FS.ReadFile("261_observation_execution_isolation.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(sql))
	require.NoError(t, err)
	for attempt, expected := range map[string]bool{"observed": false, "uncapped": false, "capped": true, "async": true, "no-evidence": true} {
		var enforced bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT enforces_limits FROM model_execution_attempts WHERE user_id=$1 AND attempt_id=$2`, uid, attempt).Scan(&enforced))
		require.Equal(t, expected, enforced, attempt)
	}
}
