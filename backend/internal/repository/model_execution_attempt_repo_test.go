package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestExecutionAttemptUnknownRejectsProviderResubmission(t *testing.T) {
	db, m, e := sqlmock.New()
	require.NoError(t, e)
	defer func() { _ = db.Close() }()
	r := &distillationTelemetryRepository{db: db}
	m.ExpectBegin()
	m.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("op").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT EXISTS").WithArgs("op", int64(3)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	m.ExpectRollback()
	require.ErrorIs(t, r.CreateExecutionAttempt(context.Background(), service.ExecutionAttempt{OperationID: "op", OwnerEpoch: 3}), service.ErrModelExecutionRevisionConflict)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestExecutionAttemptHeartbeatRequiresCurrentEpochAndRunningState(t *testing.T) {
	db, m, e := sqlmock.New()
	require.NoError(t, e)
	defer func() { _ = db.Close() }()
	r := &distillationTelemetryRepository{db: db}
	m.ExpectExec("UPDATE model_execution_attempts SET state").WithArgs("attempt", "running", int64(2)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, r.TransitionExecutionAttempt(context.Background(), "attempt", "running", 2), service.ErrModelExecutionRevisionConflict)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestExecutionAttemptRecoveryDeduplicatesOperationAndRetainsUnknown(t *testing.T) {
	db, m, e := sqlmock.New()
	require.NoError(t, e)
	defer func() { _ = db.Close() }()
	r := &distillationTelemetryRepository{db: db}
	cache, client := newModelExecutionBudgetCache(t)
	ctx := context.Background()
	m.ExpectQuery("SELECT a.id,a.user_id").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "operation_id", "account_id", "provider", "attempt_id", "detach_operation"}))
	m.ExpectBegin()
	m.ExpectExec("UPDATE model_execution_attempts a SET state='completed'").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectCommit()
	m.ExpectExec("UPDATE model_execution_attempts SET state='unknown'").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT user_id,operation_id").WillReturnRows(sqlmock.NewRows([]string{"user_id", "operation_id", "targets", "unknown"}).AddRow(42, "op", `["openai:gpt-5","google:gemini"]`, true))
	m.ExpectQuery("SELECT COALESCE\\(account_id,0\\)").WillReturnRows(sqlmock.NewRows([]string{"account_id", "provider", "attempt_id"}).AddRow(99, "openai", "attempt"))
	m.ExpectQuery("SELECT DISTINCT a.user_id,a.operation_id").WillReturnRows(sqlmock.NewRows([]string{"user_id", "operation_id"}))
	m.ExpectQuery("SELECT a.user_id,a.operation_id").WillReturnRows(sqlmock.NewRows([]string{"user_id", "operation_id", "count"}))
	require.NoError(t, r.RecoverExecutionBudgets(ctx, cache))
	require.Equal(t, "1", client.HGet(ctx, modelExecutionStateKey(42), "running").Val())
	// Recovery is idempotent and cannot expire unknown capacity merely with time.
	time.Sleep(time.Millisecond)
	_, err := cache.ReserveModelExecution(ctx, reservation("new"))
	require.ErrorIs(t, err, service.ErrModelExecutionBudgetExceeded)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestExecutionAttemptRecoveryFencesExistingLeaseAndRestoresUnknownTargets(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &distillationTelemetryRepository{db: db}
	cache, client := newModelExecutionBudgetCache(t)
	ctx := context.Background()
	current := reservation("op")
	current.Targets = []string{"google:gemini"}
	current.TargetLimits = nil
	lease, err := cache.ReserveModelExecution(ctx, current)
	require.NoError(t, err)
	for range 2 {
		m.ExpectQuery("SELECT a.id,a.user_id").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "operation_id", "account_id", "provider", "attempt_id", "detach_operation"}))
		m.ExpectBegin()
		m.ExpectExec("UPDATE model_execution_attempts a SET state='completed'").WillReturnResult(sqlmock.NewResult(0, 0))
		m.ExpectCommit()
		m.ExpectExec("UPDATE model_execution_attempts SET state='unknown'").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectQuery("SELECT user_id,operation_id").WillReturnRows(sqlmock.NewRows([]string{"user_id", "operation_id", "targets", "unknown"}).AddRow(42, "op", `["openai:gpt-5"]`, true))
		m.ExpectQuery("SELECT COALESCE\\(account_id,0\\)").WillReturnRows(sqlmock.NewRows([]string{"account_id", "provider", "attempt_id"}))
		m.ExpectQuery("SELECT DISTINCT a.user_id,a.operation_id").WillReturnRows(sqlmock.NewRows([]string{"user_id", "operation_id"}))
		m.ExpectQuery("SELECT a.user_id,a.operation_id").WillReturnRows(sqlmock.NewRows([]string{"user_id", "operation_id", "count"}))
		require.NoError(t, r.RecoverExecutionBudgets(ctx, cache))
	}
	require.Equal(t, "1", client.HGet(ctx, modelExecutionStateKey(42), "running").Val())
	require.Equal(t, "1", client.HGet(ctx, modelExecutionTargetsKey(42), "openai:gpt-5").Val())
	require.Equal(t, "1", client.HGet(ctx, modelExecutionTargetsKey(42), "google:gemini").Val())
	_, err = cache.ConfirmModelExecution(ctx, service.ModelExecutionConfirmation{UserID: 42, OperationID: "op", ExpectedRevision: lease.Revision, ExpectedEpoch: lease.Epoch, Targets: current.Targets, PolicyRevision: current.PolicyRevision, CatalogRevision: current.CatalogRevision, PayloadHash: current.PayloadHash})
	require.ErrorIs(t, err, service.ErrModelExecutionRevisionConflict)
	require.NoError(t, m.ExpectationsWereMet())
}
