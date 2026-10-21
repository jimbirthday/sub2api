package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

func (r *distillationTelemetryRepository) CreateExecutionAttempt(ctx context.Context, a service.ExecutionAttempt) error {
	raw, err := json.Marshal(a.Targets)
	if err != nil {
		return err
	}
	// The operation advisory lock serializes cross-instance attempts. Expired
	// ownership is uncertainty, never permission to submit the request again.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, a.OperationID); err != nil {
		return err
	}
	var unknown bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_execution_attempts WHERE operation_id=$1 AND (state='unknown' OR (state='running' AND updated_at<NOW()-INTERVAL '60 seconds') OR owner_epoch<>$2))`, a.OperationID, a.OwnerEpoch).Scan(&unknown)
	if err != nil {
		return err
	}
	if unknown {
		return service.ErrModelExecutionRevisionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO model_execution_attempts(operation_id,user_id,attempt_id,account_id,provider,owner_epoch,policy_revision,catalog_revision,owner_id,state,payload_hash,targets,funds_state,hold_id) VALUES($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8,$9,'running',$10,$11::jsonb,$12,$13)`, a.OperationID, a.UserID, a.AttemptID, a.AccountID, a.Provider, a.OwnerEpoch, a.PolicyRevision, a.CatalogRevision, a.OwnerID, a.PayloadHash, raw, a.FundsState, a.HoldID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *distillationTelemetryRepository) TransitionExecutionAttempt(ctx context.Context, id, state string, epoch int64) error {
	if state != "running" && state != "completed" && state != "unknown" {
		return service.ErrModelExecutionRevisionConflict
	}
	res, err := r.db.ExecContext(ctx, `UPDATE model_execution_attempts SET state=$2,updated_at=NOW() WHERE attempt_id=$1 AND owner_epoch=$3 AND (state='running' OR (state='unknown' AND $2='completed'))`, id, state, epoch)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return service.ErrModelExecutionRevisionConflict
	}
	return nil
}
func (r *distillationTelemetryRepository) ReconcileExecutionAttempts(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE model_execution_attempts SET state='unknown',updated_at=NOW() WHERE state='running' AND updated_at<$1`, before)
	return err
}

// RecoverExecutionBudgets restores conservative per-operation occupancy. Multiple
// attempts never multiply the user slot; their dependency union is retained.
func (r *distillationTelemetryRepository) RecoverExecutionBudgets(ctx context.Context, cache service.ModelExecutionBudgetCache) error {
	if err := r.detachObservationCapacity(ctx, cache); err != nil {
		return err
	}
	if err := r.completeTerminalBatchAttempts(ctx); err != nil {
		return err
	}
	if err := r.ReconcileExecutionAttempts(ctx, time.Now().Add(-60*time.Second)); err != nil {
		return err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT user_id,operation_id,COALESCE(jsonb_agg(DISTINCT target) FILTER(WHERE target IS NOT NULL),'[]'::jsonb),bool_or(state='unknown') FROM model_execution_attempts LEFT JOIN LATERAL jsonb_array_elements_text(targets) AS target ON TRUE WHERE enforces_limits AND state IN('running','unknown') GROUP BY user_id,operation_id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var uid int64
		var op string
		var raw []byte
		var unknown bool
		if err = rows.Scan(&uid, &op, &raw, &unknown); err != nil {
			return err
		}
		var targets []string
		if err = json.Unmarshal(raw, &targets); err != nil {
			return err
		}
		lease, e := cache.ReserveModelExecution(ctx, service.ModelExecutionReservation{UserID: uid, OperationID: op, Targets: targets, PayloadHash: "recovered"})
		if e == service.ErrModelExecutionRevisionConflict {
			// A stale durable attempt can outlive a successful in-process
			// retarget. Recover its dependency union and fence the existing
			// owner instead of silently retaining only the current targets.
			if unknown {
				restorer, ok := cache.(interface {
					RestoreUnknownModelExecution(context.Context, int64, string, []string) error
				})
				if !ok {
					return service.ErrModelExecutionBudgetUnavailable
				}
				if err = restorer.RestoreUnknownModelExecution(ctx, uid, op, targets); err != nil {
					return err
				}
			}
			continue
		}
		if e != nil {
			return e
		}
		// A rebuilt lease has no living fenced owner. It is always conservative.
		if err = cache.MarkModelExecutionUnknown(ctx, uid, op, lease.Revision, lease.Epoch); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()
	if err := r.recoverProviderAttempts(ctx, cache); err != nil {
		return err
	}
	if err := r.reclaimCompletedOperations(ctx, cache); err != nil {
		return err
	}
	return r.recoverBatchOutstanding(ctx, cache)
}

// Counts come from committed terminal item records. Submit completion alone
// cannot release asynchronous work; retries reconcile the absolute count.
func (r *distillationTelemetryRepository) recoverBatchOutstanding(ctx context.Context, cache service.ModelExecutionBudgetCache) error {
	syncer, ok := cache.(interface {
		SyncRecoveredBatchOutstanding(context.Context, int64, string, int) error
	})
	if !ok {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.user_id,a.operation_id,SUM(GREATEST(j.item_count-items.terminal,items.pending)) FROM (SELECT DISTINCT user_id,operation_id,hold_id FROM model_execution_attempts WHERE hold_id<>'' AND state IN ('running','unknown')) a JOIN batch_image_jobs j ON j.hold_id=a.hold_id CROSS JOIN LATERAL (SELECT COUNT(*) FILTER (WHERE i.status IN ('success','failed','cancelled')) AS terminal,COUNT(*) FILTER (WHERE i.status NOT IN ('success','failed','cancelled')) AS pending FROM batch_image_items i WHERE i.job_id=j.batch_id) items GROUP BY a.user_id,a.operation_id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var uid int64
		var op string
		var pending int
		if err = rows.Scan(&uid, &op, &pending); err != nil {
			return err
		}
		if err = syncer.SyncRecoveredBatchOutstanding(ctx, uid, op, pending); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *distillationTelemetryRepository) recoverProviderAttempts(ctx context.Context, cache service.ModelExecutionBudgetCache) error {
	restore, ok := cache.(interface {
		RestoreExecutionProviderAttempt(context.Context, int64, string, string) error
	})
	if !ok {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(account_id,0),COALESCE(provider,''),attempt_id FROM model_execution_attempts WHERE enforces_limits AND state='unknown'`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var aid int64
		var provider, id string
		if err = rows.Scan(&aid, &provider, &id); err != nil {
			return err
		}
		if err = restore.RestoreExecutionProviderAttempt(ctx, aid, provider, id); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Detach only the former observation limiter's Redis occupancy. In particular,
// this does not assert remote termination, change attempt state, or refund money.
func (r *distillationTelemetryRepository) detachObservationCapacity(ctx context.Context, cache service.ModelExecutionBudgetCache) error {
	cleaner, ok := cache.(interface {
		ReleaseExecutionProviderAttempt(context.Context, int64, string, string) error
		ReclaimExecutionOperation(context.Context, int64, string) error
	})
	if !ok {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,a.user_id,a.operation_id,COALESCE(a.account_id,0),COALESCE(a.provider,''),a.attempt_id,
NOT EXISTS(SELECT 1 FROM model_execution_attempts enforced WHERE enforced.operation_id=a.operation_id AND enforced.enforces_limits) AS detach_operation
FROM model_execution_attempts a WHERE NOT a.enforces_limits AND a.observation_detached_at IS NULL AND a.state IN('running','unknown') ORDER BY a.id LIMIT 500`)
	if err != nil {
		return err
	}
	type observation struct {
		id, userID, accountID        int64
		operation, provider, attempt string
		detachOperation              bool
	}
	var pending []observation
	for rows.Next() {
		var a observation
		if err = rows.Scan(&a.id, &a.userID, &a.operation, &a.accountID, &a.provider, &a.attempt, &a.detachOperation); err != nil {
			_ = rows.Close()
			return err
		}
		pending = append(pending, a)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, a := range pending {
		if err = cleaner.ReleaseExecutionProviderAttempt(ctx, a.accountID, a.provider, a.attempt); err != nil {
			return err
		}
		if a.detachOperation {
			if err = cleaner.ReclaimExecutionOperation(ctx, a.userID, a.operation); err != nil {
				return err
			}
		}
		if _, err = r.db.ExecContext(ctx, `UPDATE model_execution_attempts SET observation_detached_at=NOW() WHERE id=$1 AND NOT enforces_limits`, a.id); err != nil {
			return err
		}
	}
	return nil
}

// The database transaction is the authoritative remote termination proof for a
// batch: terminal job plus every expected item durably terminal. Redis cleanup
// can be retried after a crash without refunding or submitting anything.
func (r *distillationTelemetryRepository) completeTerminalBatchAttempts(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `UPDATE model_execution_attempts a SET state='completed',updated_at=NOW() FROM batch_image_jobs j WHERE a.hold_id<>'' AND a.hold_id=j.hold_id AND a.state IN('running','unknown') AND j.status IN('completed','failed','cancelled','output_deleted') AND (SELECT COUNT(*) FROM batch_image_items i WHERE i.job_id=j.batch_id)=j.item_count AND NOT EXISTS(SELECT 1 FROM batch_image_items i WHERE i.job_id=j.batch_id AND i.status NOT IN('success','failed','cancelled'))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *distillationTelemetryRepository) reclaimCompletedOperations(ctx context.Context, cache service.ModelExecutionBudgetCache) error {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT a.user_id,a.operation_id FROM model_execution_attempts a WHERE a.state='completed' AND a.reclaimed_at IS NULL AND NOT EXISTS(SELECT 1 FROM model_execution_attempts pending WHERE pending.operation_id=a.operation_id AND pending.state<>'completed') LIMIT 500`)
	if err != nil {
		return err
	}
	type operation struct {
		uid int64
		id  string
	}
	var ops []operation
	for rows.Next() {
		var op operation
		if err = rows.Scan(&op.uid, &op.id); err != nil {
			_ = rows.Close()
			return err
		}
		ops = append(ops, op)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, op := range ops {
		clear, ok := cache.(interface {
			ReleaseExecutionProviderAttempt(context.Context, int64, string, string) error
		})
		if !ok {
			continue
		}
		attempts, queryErr := r.db.QueryContext(ctx, `SELECT COALESCE(account_id,0),COALESCE(provider,''),attempt_id FROM model_execution_attempts WHERE operation_id=$1 AND state='completed' AND reclaimed_at IS NULL`, op.id)
		if queryErr != nil {
			return queryErr
		}
		for attempts.Next() {
			var aid int64
			var provider, id string
			if err = attempts.Scan(&aid, &provider, &id); err != nil {
				_ = attempts.Close()
				return err
			}
			if err = clear.ReleaseExecutionProviderAttempt(ctx, aid, provider, id); err != nil {
				_ = attempts.Close()
				return err
			}
		}
		if err = attempts.Err(); err != nil {
			_ = attempts.Close()
			return err
		}
		_ = attempts.Close()

		// Reserve is idempotent if already present; obtain current revision without
		// changing an existing lease through the optional repository-owned cleanup.
		if cleaner, ok := cache.(interface {
			ReclaimExecutionOperation(context.Context, int64, string) error
		}); ok {
			if err = cleaner.ReclaimExecutionOperation(ctx, op.uid, op.id); err != nil {
				return err
			}
		} else {
			continue
		}
		_, err = r.db.ExecContext(ctx, `UPDATE model_execution_attempts SET reclaimed_at=NOW() WHERE operation_id=$1 AND state='completed' AND reclaimed_at IS NULL`, op.id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *distillationTelemetryRepository) ConfirmExecutionOperationTerminated(ctx context.Context, userID int64, operationID string) error {
	if userID <= 0 || operationID == "" {
		return service.ErrModelExecutionRevisionConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, operationID); err != nil {
		return err
	}
	var owned bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_execution_attempts WHERE operation_id=$1 AND user_id=$2) AND NOT EXISTS(SELECT 1 FROM model_execution_attempts WHERE operation_id=$1 AND user_id<>$2)`, operationID, userID).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return service.ErrModelExecutionRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE model_execution_attempts SET state='completed',updated_at=NOW() WHERE operation_id=$1 AND user_id=$2 AND state IN('running','unknown')`, operationID, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE distillation_risk_operation_intents SET state='completed',revision=revision+1,completed_at=NOW(),updated_at=NOW() WHERE operation_id=$1 AND user_id=$2 AND state<>'completed'`, operationID, userID); err != nil {
		return err
	}
	return tx.Commit()
}
