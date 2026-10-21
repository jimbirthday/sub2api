//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestModelExecutionAttemptRealRecovery(t *testing.T) {
	ctx := context.Background()
	var uid int64
	err := integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',1,1) RETURNING id`, fmt.Sprintf("execution-%d@test.invalid", time.Now().UnixNano())).Scan(&uid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid) })
	repo := &distillationTelemetryRepository{db: integrationDB}
	rdb := testRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900).(*concurrencyCache)
	op := fmt.Sprintf("op-%d", uid)
	a := service.ExecutionAttempt{UserID: uid, OperationID: op, AttemptID: op + "-a", OwnerEpoch: 1, Targets: []string{"openai:gpt-5"}, PayloadHash: "wire", FundsState: "none"}
	require.NoError(t, repo.CreateExecutionAttempt(ctx, a))
	var hash, state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT payload_hash,state FROM model_execution_attempts WHERE attempt_id=$1`, a.AttemptID).Scan(&hash, &state))
	require.Equal(t, "wire", hash)
	require.Equal(t, "running", state)
	require.ErrorIs(t, repo.TransitionExecutionAttempt(ctx, a.AttemptID, "running", 2), service.ErrModelExecutionRevisionConflict)
	require.NoError(t, repo.TransitionExecutionAttempt(ctx, a.AttemptID, "running", 1))
	_, err = integrationDB.ExecContext(ctx, `UPDATE model_execution_attempts SET updated_at=NOW()-INTERVAL '120 seconds' WHERE attempt_id=$1`, a.AttemptID)
	require.NoError(t, err)
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.Equal(t, "1", rdb.HGet(ctx, modelExecutionStateKey(uid), "running").Val())
	a.AttemptID += "-retry"
	require.ErrorIs(t, repo.CreateExecutionAttempt(ctx, a), service.ErrModelExecutionRevisionConflict)
	_, err = cache.ReserveModelExecution(ctx, service.ModelExecutionReservation{UserID: uid, OperationID: "blocked", Targets: a.Targets, UserLimit: 1})
	require.ErrorIs(t, err, service.ErrModelExecutionBudgetExceeded)
}

func TestModelExecutionBatchItemsAndProviderTerminalRecovery(t *testing.T) {
	ctx := context.Background()
	var uid int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',1,1) RETURNING id`, fmt.Sprintf("batch-recovery-%d@test.invalid", time.Now().UnixNano())).Scan(&uid))
	batch := fmt.Sprintf("batch-%d", uid)
	op := fmt.Sprintf("batch-op-%d", uid)
	hold := "hold-" + batch
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM batch_image_jobs WHERE batch_id=$1`, batch)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid)
	})
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO batch_image_jobs(batch_id,user_id,provider,model,item_count,hold_id) VALUES($1,$2,'openai','gpt-image',2,$3)`, batch, uid, hold)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO batch_image_items(job_id,custom_id,status) VALUES($1,'a','pending'),($1,'b','success')`, batch)
	require.NoError(t, err)
	repo := &distillationTelemetryRepository{db: integrationDB}
	rdb := testRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900).(*concurrencyCache)
	a := service.ExecutionAttempt{OperationID: op, AttemptID: op + "-attempt", UserID: uid, AccountID: 99, Provider: "openai", OwnerEpoch: 1, Targets: []string{"openai:gpt-image"}, HoldID: hold, FundsState: "reserved"}
	require.NoError(t, repo.CreateExecutionAttempt(ctx, a))
	// Missing item records still represent outstanding submitted work.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM batch_image_items WHERE job_id=$1 AND custom_id='a'`, batch)
	require.NoError(t, err)
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.Equal(t, "1", rdb.HGet(ctx, modelExecutionStateKey(uid), "batch").Val())
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO batch_image_items(job_id,custom_id,status) VALUES($1,'a','pending')`, batch)
	require.NoError(t, err)
	// A live owner already holds its account slot. Recovery must not add an
	// intent slot for a healthy running attempt.
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(99), redis.Z{Score: float64(time.Now().Unix()), Member: "live-owner"}).Err())
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.EqualValues(t, 1, rdb.ZCard(ctx, accountSlotKey(99)).Val())
	require.NoError(t, rdb.ZRem(ctx, accountSlotKey(99), "live-owner").Err())
	require.NoError(t, repo.TransitionExecutionAttempt(ctx, a.AttemptID, "unknown", 1))
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.Equal(t, "1", rdb.HGet(ctx, modelExecutionStateKey(uid), "batch").Val())
	require.EqualValues(t, 1, rdb.ZCard(ctx, accountSlotKey(99)).Val())
	require.EqualValues(t, 1, rdb.SCard(ctx, "model_execution:provider:openai").Val())
	_, err = integrationDB.ExecContext(ctx, `UPDATE batch_image_items SET status='failed' WHERE job_id=$1 AND custom_id='a'`, batch)
	require.NoError(t, err)
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.Equal(t, "0", rdb.HGet(ctx, modelExecutionStateKey(uid), "batch").Val())
	_, err = integrationDB.ExecContext(ctx, `UPDATE batch_image_jobs SET status='completed' WHERE batch_id=$1`, batch)
	require.NoError(t, err)
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
	require.Zero(t, rdb.ZCard(ctx, accountSlotKey(99)).Val())
	require.Zero(t, rdb.SCard(ctx, "model_execution:provider:openai").Val())
	require.Equal(t, "0", rdb.HGet(ctx, modelExecutionStateKey(uid), "running").Val())
	var reclaimed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT reclaimed_at IS NOT NULL FROM model_execution_attempts WHERE attempt_id=$1`, a.AttemptID).Scan(&reclaimed))
	require.True(t, reclaimed)
	require.NoError(t, repo.RecoverExecutionBudgets(ctx, cache))
}

func TestConfirmedLiveTerminationUsesDurableOwner(t *testing.T) {
	ctx := context.Background()
	var uid int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',1,1) RETURNING id`, fmt.Sprintf("live-execution-%d@test.invalid", time.Now().UnixNano())).Scan(&uid))
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid) })
	repo := &distillationTelemetryRepository{db: integrationDB}
	op := fmt.Sprintf("live-op-%d", uid)
	require.NoError(t, repo.CreateExecutionAttempt(ctx, service.ExecutionAttempt{UserID: uid, OperationID: op, AttemptID: op + "-a", OwnerEpoch: 1, Targets: []string{"openai:gpt-live"}, FundsState: "none"}))
	require.NoError(t, repo.TransitionExecutionAttempt(ctx, op+"-a", "unknown", 1))
	require.ErrorIs(t, repo.ConfirmExecutionOperationTerminated(ctx, uid+1, op), service.ErrModelExecutionRevisionConflict)
	require.NoError(t, repo.ConfirmExecutionOperationTerminated(ctx, uid, op))
	require.NoError(t, repo.ConfirmExecutionOperationTerminated(ctx, uid, op))
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM model_execution_attempts WHERE attempt_id=$1`, op+"-a").Scan(&state))
	require.Equal(t, "completed", state)
}
