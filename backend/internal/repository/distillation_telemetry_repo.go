package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"time"
)

type distillationTelemetryRepository struct{ db *sql.DB }

func NewDistillationTelemetryRepository(db *sql.DB) service.DistillationTelemetryRepository {
	return &distillationTelemetryRepository{db: db}
}

func (r *distillationTelemetryRepository) CreateAttempt(ctx context.Context, a service.DistillationAttempt) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO distillation_risk_attempts(attempt_id,user_id,api_key_id,operation_id,provider,model,prompt_hmac,hmac_day,configured_concurrency,configured_rpm,observed_concurrency,cohort,started_at,status) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13,'active')`, a.AttemptID, a.UserID, a.APIKeyID, a.OperationID, a.Provider, a.Model, nullableBytes(a.PromptHMAC), a.HMACDay, a.ConfiguredConcurrency, a.ConfiguredRPM, a.ObservedConcurrency, a.Cohort, a.StartedAt)
	return err
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (r *distillationTelemetryRepository) CompleteAttempt(ctx context.Context, c service.DistillationAttemptCompletion) error {
	res, err := r.db.ExecContext(ctx, `UPDATE distillation_risk_attempts SET completed_at=$2,input_tokens=$3,output_tokens=$4,status=$5,telemetry_complete=$6,updated_at=NOW() WHERE attempt_id=$1 AND status='active'`, c.AttemptID, c.CompletedAt, c.InputTokens, c.OutputTokens, c.Status, c.TelemetryComplete)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return service.ErrDistillationAttemptConflict
	}
	return nil
}

func (r *distillationTelemetryRepository) MarkTelemetryGap(ctx context.Context, userID int64, window time.Time, reason string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO distillation_risk_telemetry_gaps(user_id,window_start,reason) VALUES($1,$2,$3) ON CONFLICT(user_id,window_start,reason) DO UPDATE SET last_seen_at=NOW(),occurrences=distillation_risk_telemetry_gaps.occurrences+1`, userID, service.DistillationWindowStart(window), reason)
	return err
}

func (r *distillationTelemetryRepository) CreateOperationIntent(ctx context.Context, i service.DistillationOperationIntent) (*service.DistillationOperationIntent, error) {
	targets, err := json.Marshal(i.Targets)
	if err != nil {
		return nil, err
	}
	var got service.DistillationOperationIntent
	var raw []byte
	err = r.db.QueryRowContext(ctx, `INSERT INTO distillation_risk_operation_intents(operation_id,user_id,kind,state,targets) VALUES($1,$2,$3,'reserved',$4::jsonb) ON CONFLICT(operation_id) DO UPDATE SET operation_id=EXCLUDED.operation_id WHERE distillation_risk_operation_intents.user_id=EXCLUDED.user_id AND distillation_risk_operation_intents.kind=EXCLUDED.kind AND distillation_risk_operation_intents.targets=EXCLUDED.targets RETURNING operation_id,user_id,kind,state,targets,revision,created_at,updated_at`, i.OperationID, i.UserID, i.Kind, targets).Scan(&got.OperationID, &got.UserID, &got.Kind, &got.State, &raw, &got.Revision, &got.CreatedAt, &got.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationOperationConflict
	}
	if err != nil {
		var p *pq.Error
		if errors.As(err, &p) && p.Code == "23505" {
			return nil, service.ErrDistillationOperationConflict
		}
		return nil, err
	}
	if err = json.Unmarshal(raw, &got.Targets); err != nil {
		return nil, err
	}
	return &got, nil
}

func (r *distillationTelemetryRepository) TransitionOperationIntent(ctx context.Context, id string, expected int64, state string, now time.Time) (*service.DistillationOperationIntent, error) {
	var got service.DistillationOperationIntent
	var raw []byte
	err := r.db.QueryRowContext(ctx, `UPDATE distillation_risk_operation_intents SET state=$3,revision=revision+1,updated_at=$4,completed_at=CASE WHEN $3 IN('completed','cancelled') THEN $4 ELSE completed_at END WHERE operation_id=$1 AND revision=$2 RETURNING operation_id,user_id,kind,state,targets,revision,created_at,updated_at`, id, expected, state, now).Scan(&got.OperationID, &got.UserID, &got.Kind, &got.State, &raw, &got.Revision, &got.CreatedAt, &got.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationOperationConflict
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &got.Targets); err != nil {
		return nil, err
	}
	return &got, nil
}

func (r *distillationTelemetryRepository) UpdateOperationIntentTargets(ctx context.Context, id string, expected int64, targets []string, now time.Time) (*service.DistillationOperationIntent, error) {
	rawTargets, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}
	var got service.DistillationOperationIntent
	var raw []byte
	err = r.db.QueryRowContext(ctx, `UPDATE distillation_risk_operation_intents SET targets=$3::jsonb,revision=revision+1,updated_at=$4 WHERE operation_id=$1 AND revision=$2 AND state IN('reserved','running','reconcile_required') RETURNING operation_id,user_id,kind,state,targets,revision,created_at,updated_at`, id, expected, rawTargets, now).Scan(&got.OperationID, &got.UserID, &got.Kind, &got.State, &raw, &got.Revision, &got.CreatedAt, &got.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationOperationConflict
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &got.Targets); err != nil {
		return nil, err
	}
	return &got, nil
}

func (r *distillationTelemetryRepository) GetOperationIntent(ctx context.Context, id string) (*service.DistillationOperationIntent, error) {
	var got service.DistillationOperationIntent
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT operation_id,user_id,kind,state,targets,revision,created_at,updated_at FROM distillation_risk_operation_intents WHERE operation_id=$1`, id).Scan(&got.OperationID, &got.UserID, &got.Kind, &got.State, &raw, &got.Revision, &got.CreatedAt, &got.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationAttemptNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &got.Targets); err != nil {
		return nil, err
	}
	return &got, nil
}

func (r *distillationTelemetryRepository) ReconcileActiveOperations(ctx context.Context, limit int) error {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	_, err := r.db.ExecContext(ctx, `WITH candidates AS (SELECT operation_id FROM distillation_risk_operation_intents WHERE state IN('reserved','running') AND updated_at < NOW()-INTERVAL '24 hours' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE distillation_risk_operation_intents i SET state='reconcile_required',revision=revision+1,updated_at=NOW() FROM candidates c WHERE i.operation_id=c.operation_id`, limit)
	return err
}

func (r *distillationTelemetryRepository) CleanupDailyHMAC(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE distillation_risk_attempts SET prompt_hmac=NULL,hmac_day=NULL,updated_at=NOW() WHERE hmac_day <= $1::date - 7`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("cleanup distillation hmac: %w", err)
	}
	return res.RowsAffected()
}
