package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type distillationRiskRepository struct{ db *sql.DB }

func NewDistillationRiskRepository(db *sql.DB) service.DistillationRiskRepository {
	return &distillationRiskRepository{db: db}
}

func (r *distillationRiskRepository) GetConfig(ctx context.Context) (*service.DistillationRiskConfig, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrDistillationUnavailable
	}
	var c service.DistillationRiskConfig
	var updatedBy sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT mode, observation_started_at, scoring_config, rule_version, revision, updated_by, updated_at FROM distillation_risk_configs WHERE id=1`).Scan(
		&c.Mode, &c.ObservationStartedAt, &c.ScoringConfig, &c.RuleVersion, &c.Revision, &updatedBy, &c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("read distillation config: %w", err)
	}
	if updatedBy.Valid {
		c.UpdatedBy = &updatedBy.Int64
	}
	return &c, nil
}

func (r *distillationRiskRepository) UpdateConfig(ctx context.Context, mode string, scoring json.RawMessage, expectedRevision, actorID int64, now time.Time) (*service.DistillationRiskConfig, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var c service.DistillationRiskConfig
	var updatedBy sql.NullInt64
	err = tx.QueryRowContext(ctx, `UPDATE distillation_risk_configs
SET mode=$1::varchar, scoring_config=$2::jsonb,
    rule_version=rule_version + CASE WHEN scoring_config <> $2::jsonb THEN 1 ELSE 0 END,
	observation_started_at=CASE WHEN mode='off' AND $1::text='observe' THEN $4 ELSE observation_started_at END,
    revision=revision+1, updated_by=$3, updated_at=$4
WHERE id=1 AND revision=$5
RETURNING mode, observation_started_at, scoring_config, rule_version, revision, updated_by, updated_at`,
		mode, []byte(scoring), actorID, now, expectedRevision).Scan(
		&c.Mode, &c.ObservationStartedAt, &c.ScoringConfig, &c.RuleVersion, &c.Revision, &updatedBy, &c.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationRevisionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update distillation config: %w", err)
	}
	if updatedBy.Valid {
		c.UpdatedBy = &updatedBy.Int64
	}
	actor := actorID
	if err := insertDistillationEvent(ctx, tx, 0, "config_updated", map[string]any{"mode": c.Mode, "rule_version": c.RuleVersion, "revision": c.Revision}, &actor, nil, now); err != nil {
		return nil, err
	}
	if c.Mode != service.DistillationModeEnforce {
		if _, err := tx.ExecContext(ctx, `UPDATE distillation_risk_restrictions SET revoked_at=$1,revision=revision+1,updated_at=$1 WHERE source='automatic' AND revoked_at IS NULL AND expires_at>$1`, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *distillationRiskRepository) ListSealedAggregates(ctx context.Context, ruleVersion int64, now time.Time, limit int) ([]service.DistillationWindowAggregate, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	// The lookback bounds recovery work after downtime. Event-time buckets and
	// the NOT EXISTS guard make repeated worker runs idempotent.
	rows, err := r.db.QueryContext(ctx, `WITH base AS (
  SELECT user_id,
         date_bin(INTERVAL '30 minutes', created_at, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
         CASE WHEN COALESCE(image_count,0) > 0 OR COALESCE(video_count,0) > 0 OR billing_mode IN ('image','video') THEN 'async_batch' ELSE 'interactive' END AS cohort,
         created_at,
         LAG(created_at) OVER (PARTITION BY user_id, date_bin(INTERVAL '30 minutes', created_at, TIMESTAMPTZ '1970-01-01 00:00:00+00'), CASE WHEN COALESCE(image_count,0) > 0 OR COALESCE(video_count,0) > 0 OR billing_mode IN ('image','video') THEN 'async_batch' ELSE 'interactive' END ORDER BY created_at, id) AS previous_at,
         input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
         image_count, video_count, model, upstream_model, account_id
  FROM usage_logs
  WHERE created_at >= COALESCE((SELECT MIN(created_at) FROM usage_logs u0 WHERE NOT EXISTS (SELECT 1 FROM distillation_risk_windows w0 WHERE w0.user_id=u0.user_id AND w0.window_start=date_bin(INTERVAL '30 minutes',u0.created_at,TIMESTAMPTZ '1970-01-01 00:00:00+00') AND w0.cohort=CASE WHEN COALESCE(u0.image_count,0) > 0 OR COALESCE(u0.video_count,0) > 0 OR u0.billing_mode IN ('image','video') THEN 'async_batch' ELSE 'interactive' END AND w0.rule_version=$2)), $1)
    AND created_at < $1
), aggregate AS (
  SELECT user_id, bucket, cohort,
         COUNT(*)::bigint AS request_count,
         SUM(1 + GREATEST(image_count, 0) + GREATEST(video_count, 0))::bigint AS workload_units,
         SUM(GREATEST(input_tokens, 0) + GREATEST(output_tokens, 0) + GREATEST(cache_creation_tokens, 0) + GREATEST(cache_read_tokens, 0))::bigint AS total_tokens,
         SUM(GREATEST(input_tokens, 0) + GREATEST(cache_creation_tokens, 0) + GREATEST(cache_read_tokens, 0))::bigint AS input_tokens,
         SUM(GREATEST(output_tokens, 0))::bigint AS output_tokens,
         COUNT(DISTINCT COALESCE(NULLIF(upstream_model, ''), model))::bigint AS distinct_models,
         COUNT(DISTINCT account_id)::bigint AS distinct_accounts,
         CASE WHEN COUNT(previous_at) >= 2 AND AVG(EXTRACT(EPOCH FROM created_at - previous_at)) > 0 THEN STDDEV_POP(EXTRACT(EPOCH FROM created_at - previous_at)) / AVG(EXTRACT(EPOCH FROM created_at - previous_at)) ELSE NULL END AS interarrival_cv,
         COUNT(previous_at)::bigint AS interarrival_count
  FROM base GROUP BY user_id, bucket, cohort
), raw_attempts AS (
  SELECT a.*, date_bin(INTERVAL '30 minutes', a.started_at, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
         COALESCE(NULLIF(a.operation_id,''), a.attempt_id::text) AS logical_id
  FROM distillation_risk_attempts a WHERE a.started_at >= $1::timestamptz - INTERVAL '31 days' AND a.started_at < $1
), logical_attempts AS (
  SELECT DISTINCT ON (user_id, bucket, cohort, logical_id) *
  FROM raw_attempts ORDER BY user_id, bucket, cohort, logical_id, started_at DESC
), rate_buckets AS (
  SELECT user_id, bucket, cohort, FLOOR(EXTRACT(EPOCH FROM (started_at - bucket))/10)::int AS slot,
         COUNT(*)::bigint AS operation_count, MAX(configured_rpm)::bigint AS configured_rpm
  FROM logical_attempts GROUP BY user_id, bucket, cohort, FLOOR(EXTRACT(EPOCH FROM (started_at - bucket))/10)::int
), rate_summary AS (
  SELECT user_id,bucket,cohort,
         AVG(CASE WHEN operation_count >= CASE WHEN configured_rpm > 0 THEN GREATEST(10, CEIL(0.8 * configured_rpm::double precision / 6)) ELSE 20 END THEN 1.0 ELSE 0.0 END) AS request_rate_saturation
  FROM rate_buckets GROUP BY user_id,bucket,cohort
), token_samples AS (
  SELECT user_id,bucket,cohort,
    regexp_replace(model, '-[0-9]{4}-[0-9]{2}-[0-9]{2}$', '') AS family,
    SUM(GREATEST(input_tokens,0)+GREATEST(output_tokens,0)) AS tokens
  FROM logical_attempts
  WHERE telemetry_complete AND status='completed' AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL
  GROUP BY user_id,bucket,cohort,regexp_replace(model, '-[0-9]{4}-[0-9]{2}-[0-9]{2}$', '')
), baselines AS (
  SELECT target.user_id,target.bucket,target.cohort,
    MAX(b.p99) FILTER (WHERE b.samples >= 100) AS p99,
    MAX(b.samples) FILTER (WHERE b.samples >= 100) AS samples,
    BOOL_OR(b.samples >= 100 AND target.tokens >= 1500000 AND target.tokens > b.p99) AS family_hit
  FROM token_samples target
  CROSS JOIN LATERAL (
    SELECT percentile_cont(0.99) WITHIN GROUP (ORDER BY h.tokens) AS p99, COUNT(*) AS samples
    FROM token_samples h WHERE h.cohort=target.cohort AND h.family=target.family
      AND h.bucket < target.bucket AND h.bucket >= target.bucket - INTERVAL '30 days'
  ) b GROUP BY target.user_id,target.bucket,target.cohort
), concurrency_events AS (
  -- Include operations already running at the bucket boundary; grouping by
  -- their start bucket would undercount long requests in later windows.
  SELECT a.user_id, a.bucket, a.cohort, GREATEST(r.started_at,a.bucket) AS at, 1 AS delta, r.configured_concurrency
  FROM aggregate a JOIN raw_attempts r ON r.user_id=a.user_id AND r.cohort=a.cohort
    AND r.started_at<a.bucket+INTERVAL '30 minutes' AND COALESCE(r.completed_at,a.bucket+INTERVAL '30 minutes')>a.bucket
  UNION ALL
  SELECT a.user_id, a.bucket, a.cohort, LEAST(COALESCE(r.completed_at,a.bucket+INTERVAL '30 minutes'),a.bucket+INTERVAL '30 minutes'), -1, r.configured_concurrency
  FROM aggregate a JOIN raw_attempts r ON r.user_id=a.user_id AND r.cohort=a.cohort
    AND r.started_at<a.bucket+INTERVAL '30 minutes' AND COALESCE(r.completed_at,a.bucket+INTERVAL '30 minutes')>a.bucket
), concurrency_steps AS (
  SELECT user_id,bucket,cohort,at,
    LEAD(at) OVER (PARTITION BY user_id,bucket,cohort ORDER BY at) AS next_at,
    SUM(SUM(delta)) OVER (PARTITION BY user_id,bucket,cohort ORDER BY at) AS running,
    MAX(MAX(configured_concurrency)) OVER (PARTITION BY user_id,bucket,cohort) AS cap
  FROM concurrency_events GROUP BY user_id,bucket,cohort,at
), concurrency_summary AS (
  SELECT user_id,bucket,cohort,
    SUM(CASE WHEN running >= CASE WHEN cap > 0 THEN GREATEST(4,CEIL(0.8*cap)) ELSE 8 END THEN EXTRACT(EPOCH FROM next_at-at) ELSE 0 END)
    / NULLIF(SUM(CASE WHEN running > 0 THEN EXTRACT(EPOCH FROM next_at-at) ELSE 0 END),0) AS saturation
  FROM concurrency_steps GROUP BY user_id,bucket,cohort
), telemetry AS (
  SELECT l.user_id, l.bucket, l.cohort,
         CASE WHEN COUNT(l.prompt_hmac) > 0 THEN (COUNT(l.prompt_hmac) - COUNT(DISTINCT l.prompt_hmac))::double precision / COUNT(l.prompt_hmac) ELSE NULL END AS prompt_repeat_ratio,
         COUNT(*)::bigint AS operation_count, COUNT(l.prompt_hmac)::bigint AS fingerprint_count,
         COUNT(DISTINCT l.provider)::bigint AS distinct_providers,
         COUNT(l.previous_at)::bigint AS interarrival_count,
         CASE WHEN COUNT(l.previous_at) >= 2 AND AVG(EXTRACT(EPOCH FROM l.started_at - l.previous_at)) > 0 THEN STDDEV_POP(EXTRACT(EPOCH FROM l.started_at - l.previous_at)) / AVG(EXTRACT(EPOCH FROM l.started_at - l.previous_at)) ELSE NULL END AS interarrival_cv,
         BOOL_AND(l.telemetry_complete AND l.status <> 'active' AND l.completed_at <= l.bucket + INTERVAL '40 minutes') AS telemetry_complete
  FROM (SELECT x.*, LAG(x.started_at) OVER (PARTITION BY x.user_id,x.bucket,x.cohort ORDER BY x.started_at,x.attempt_id) AS previous_at FROM logical_attempts x) l
  GROUP BY l.user_id, l.bucket, l.cohort
)
SELECT a.user_id, a.bucket, a.cohort, a.request_count, a.workload_units, a.total_tokens,
       a.input_tokens, a.output_tokens, a.distinct_models, a.distinct_accounts, COALESCE(t.interarrival_cv,a.interarrival_cv),
       cs.saturation::double precision AS concurrency_saturation, t.prompt_repeat_ratio, rs.request_rate_saturation,
       t.distinct_providers, COALESCE(t.interarrival_count,a.interarrival_count),
       (t.telemetry_complete IS TRUE AND t.operation_count >= a.request_count AND t.fingerprint_count=t.operation_count
        AND NOT EXISTS (SELECT 1 FROM distillation_risk_telemetry_gaps g WHERE g.user_id=a.user_id AND g.window_start=a.bucket)
        -- Retry deduplication is for signals, never for completeness: an
        -- earlier unresolved attempt still invalidates this evidence window.
        AND NOT EXISTS (SELECT 1 FROM raw_attempts r WHERE r.user_id=a.user_id AND r.cohort=a.cohort
          AND r.started_at<a.bucket+INTERVAL '30 minutes' AND COALESCE(r.completed_at,a.bucket+INTERVAL '40 minutes')>a.bucket
          AND (NOT r.telemetry_complete OR r.status='active' OR r.completed_at IS NULL OR r.completed_at>a.bucket+INTERVAL '40 minutes'))
       ) AS telemetry_complete, b.p99, b.samples, COALESCE(b.family_hit,false)
FROM aggregate a LEFT JOIN baselines b ON b.user_id=a.user_id AND b.bucket=a.bucket AND b.cohort=a.cohort LEFT JOIN concurrency_summary cs ON cs.user_id=a.user_id AND cs.bucket=a.bucket AND cs.cohort=a.cohort LEFT JOIN telemetry t ON t.user_id=a.user_id AND t.bucket=a.bucket AND t.cohort=a.cohort
LEFT JOIN rate_summary rs ON rs.user_id=a.user_id AND rs.bucket=a.bucket AND rs.cohort=a.cohort
WHERE a.bucket + INTERVAL '40 minutes' <= $1
  AND NOT EXISTS (
    SELECT 1 FROM distillation_risk_windows w
    WHERE w.user_id=a.user_id AND w.window_start=a.bucket AND w.cohort=a.cohort AND w.rule_version=$2
  )
	ORDER BY a.bucket, a.user_id, a.cohort
LIMIT $3`, now.UTC(), ruleVersion, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.DistillationWindowAggregate, 0)
	for rows.Next() {
		var a service.DistillationWindowAggregate
		var cv, concurrency, repeat, rate, p99 sql.NullFloat64
		var samples sql.NullInt64
		var providers, intervals sql.NullInt64
		if err := rows.Scan(&a.UserID, &a.WindowStart, &a.Cohort, &a.RequestCount, &a.WorkloadUnits, &a.TotalTokens, &a.InputTokens, &a.OutputTokens, &a.DistinctModels, &a.DistinctAccounts, &cv, &concurrency, &repeat, &rate, &providers, &intervals, &a.TelemetryComplete, &p99, &samples, &a.TokenFamilyThroughputHit); err != nil {
			return nil, err
		}
		if cv.Valid {
			a.InterarrivalCV = &cv.Float64
		}
		if concurrency.Valid {
			a.ConcurrencySaturation = &concurrency.Float64
			a.ConcurrencyDurationEligible = true
		}
		if repeat.Valid {
			a.PromptRepeatRatio = &repeat.Float64
		}
		if rate.Valid {
			a.RequestRateSaturation = &rate.Float64
		}
		if providers.Valid {
			a.DistinctProviders = providers.Int64
		}
		if intervals.Valid {
			a.InterarrivalCount = intervals.Int64
		}
		if p99.Valid && samples.Valid && samples.Int64 >= 100 {
			a.TokenCohortP99 = &p99.Float64
			a.TokenBaselineSamples = samples.Int64
		}
		a.PromptRepeatEligible = a.PromptRepeatRatio != nil
		a.StrongSignal = (a.RequestRateSaturation != nil && *a.RequestRateSaturation >= 0.2) || (a.ConcurrencySaturation != nil && *a.ConcurrencySaturation >= 0.5) || a.TokenFamilyThroughputHit
		result = append(result, a)
	}
	return result, rows.Err()
}

func (r *distillationRiskRepository) RecordWindow(ctx context.Context, e service.DistillationWindowEvaluation, _ string, now time.Time) error {
	if e.Cohort == "" {
		e.Cohort = "interactive"
	}
	signals, err := json.Marshal(e.Signals)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var currentMode string
	var currentRuleVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT mode,rule_version FROM distillation_risk_configs WHERE id=1 FOR UPDATE`).Scan(&currentMode, &currentRuleVersion); err != nil {
		return err
	}
	if currentRuleVersion != e.RuleVersion {
		return tx.Commit()
	}
	mode := currentMode
	result, err := tx.ExecContext(ctx, `INSERT INTO distillation_risk_windows
(user_id,window_start,cohort,window_end,seal_at,rule_version,workload_units,request_count,total_tokens,input_tokens,output_tokens,signals,score,status,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14,$15)
ON CONFLICT (user_id,window_start,cohort,rule_version) DO NOTHING`,
		e.UserID, e.WindowStart, e.Cohort, e.WindowEnd, e.SealAt, e.RuleVersion, e.WorkloadUnits, e.RequestCount,
		e.TotalTokens, e.InputTokens, e.OutputTokens, signals, e.Score, e.Status, now)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 0 {
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if e.Status != "scored" || e.Score == nil {
		return tx.Commit()
	}
	if mode == service.DistillationModeOff {
		return tx.Commit()
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO distillation_risk_cases(user_id,state,review_status,revision,created_at,updated_at)
VALUES($1,'clear','none',1,$2,$2) ON CONFLICT (user_id) DO NOTHING`, e.UserID, now)
	if err != nil {
		return err
	}
	current, err := scanDistillationCase(tx.QueryRowContext(ctx, `SELECT user_id,state,review_status,suspected_streak,limited_streak,recovery_streak,last_eligible_window_end,last_score,revision,created_at,updated_at FROM distillation_risk_cases WHERE user_id=$1 FOR UPDATE`, e.UserID))
	if err != nil {
		return err
	}
	// Each cohort has its own evidence sequence. The user case is a projection
	// of these states, never a source of consecutive-window evidence.
	_, err = tx.ExecContext(ctx, `INSERT INTO distillation_risk_cohort_progress(user_id,cohort,created_at,updated_at) VALUES($1,$2,$3,$3) ON CONFLICT DO NOTHING`, e.UserID, e.Cohort, now)
	if err != nil {
		return err
	}
	userCurrent := current
	current, err = scanDistillationCase(tx.QueryRowContext(ctx, `SELECT user_id,state,review_status,suspected_streak,limited_streak,recovery_streak,last_eligible_window_end,last_score,revision,created_at,updated_at FROM distillation_risk_cohort_progress WHERE user_id=$1 AND cohort=$2 FOR UPDATE`, e.UserID, e.Cohort))
	if err != nil {
		return err
	}
	// After an automatic restriction ends, a new restriction requires three
	// fresh windows. A historical high streak cannot immediately restart it.
	var expiredAt sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT MAX(expires_at) FROM distillation_risk_restrictions WHERE user_id=$1 AND source='automatic' AND expires_at<=$2`, e.UserID, now).Scan(&expiredAt); err != nil {
		return err
	}
	if expiredAt.Valid && (current.LastEligibleWindowEnd == nil || !current.LastEligibleWindowEnd.After(expiredAt.Time)) {
		current.SuspectedStreak, current.LimitedStreak = 0, 0
	}
	if current.LastEligibleWindowEnd != nil && !e.WindowStart.Equal(*current.LastEligibleWindowEnd) {
		if !e.WindowEnd.After(*current.LastEligibleWindowEnd) {
			return tx.Commit()
		}
		current.SuspectedStreak, current.LimitedStreak, current.RecoveryStreak = 0, 0, 0
	}
	completeEvidence := service.DistillationEvidenceComplete(e.Signals)
	next := *current
	if completeEvidence {
		next = service.AdvanceDistillationCase(*current, *e.Score, mode, e.WindowEnd, now)
		if expiredAt.Valid && e.WindowStart.Before(expiredAt.Time) {
			// Only whole windows after the previous restriction ended can
			// establish a new sanction chain; low evidence still recovers.
			next.SuspectedStreak, next.LimitedStreak = 0, 0
		}
	} else {
		// Partial evidence may begin observation, but it cannot advance a
		// sanction or recovery sequence.
		next.SuspectedStreak, next.LimitedStreak, next.RecoveryStreak = 0, 0, 0
		if *e.Score >= 60 && current.State == service.DistillationStateClear {
			next.State = service.DistillationStateObserving
		}
		next.LastEligibleWindowEnd = &e.WindowEnd
		next.LastScore = e.Score
		next.UpdatedAt = now
	}
	_, err = tx.ExecContext(ctx, `UPDATE distillation_risk_cohort_progress SET state=$3,review_status=$4,suspected_streak=$5,limited_streak=$6,recovery_streak=$7,last_eligible_window_end=$8,last_score=$9,revision=revision+1,updated_at=$10 WHERE user_id=$1 AND cohort=$2`,
		e.UserID, e.Cohort, next.State, next.ReviewStatus, next.SuspectedStreak, next.LimitedStreak, next.RecoveryStreak, e.WindowEnd, *e.Score, now)
	if err != nil {
		return err
	}
	cohortNext := next
	// The most severe cohort determines the case state. A low batch window
	// therefore cannot clear an interactive restriction (or vice versa).
	err = tx.QueryRowContext(ctx, `SELECT state,suspected_streak,limited_streak,recovery_streak,last_eligible_window_end,last_score FROM distillation_risk_cohort_progress WHERE user_id=$1 ORDER BY CASE state WHEN 'limited' THEN 3 WHEN 'suspected' THEN 2 WHEN 'observing' THEN 1 ELSE 0 END DESC, updated_at DESC LIMIT 1`, e.UserID).Scan(&next.State, &next.SuspectedStreak, &next.LimitedStreak, &next.RecoveryStreak, &next.LastEligibleWindowEnd, &next.LastScore)
	if err != nil {
		return err
	}
	if next.State != service.DistillationStateClear && next.ReviewStatus == "none" {
		next.ReviewStatus = userCurrent.ReviewStatus
	}
	_, err = tx.ExecContext(ctx, `UPDATE distillation_risk_cases SET state=$2,review_status=$3,suspected_streak=$4,limited_streak=$5,recovery_streak=$6,last_eligible_window_end=$7,last_score=$8,revision=revision+1,updated_at=$9 WHERE user_id=$1`,
		e.UserID, next.State, next.ReviewStatus, next.SuspectedStreak, next.LimitedStreak, next.RecoveryStreak, next.LastEligibleWindowEnd, next.LastScore, now)
	if err != nil {
		return err
	}
	current = userCurrent
	if current.State != next.State {
		if err := insertDistillationEvent(ctx, tx, e.UserID, "state_changed", map[string]any{"from": current.State, "to": next.State, "score": *e.Score, "window_start": e.WindowStart}, nil, nil, now); err != nil {
			return err
		}
	}

	if next.State == service.DistillationStateClear && current.State != service.DistillationStateClear {
		if _, err := tx.ExecContext(ctx, `UPDATE distillation_risk_restrictions SET revoked_at=$2,revision=revision+1,updated_at=$2 WHERE user_id=$1 AND source='automatic' AND revoked_at IS NULL AND expires_at>$2`, e.UserID, now); err != nil {
			return err
		}
	}
	// Evaluation only occurs after seal+allowed lateness. A sealed window is
	// eligible for a new automatic action; late arrivals are excluded by the
	// aggregate query and recorded as audit gaps.
	if completeEvidence && mode == service.DistillationModeEnforce && cohortNext.LimitedStreak >= 3 && *e.Score >= 85 {
		exempt, err := activeDistillationExemption(ctx, tx, e.UserID, now)
		if err != nil {
			return err
		}
		if !exempt {
			coolingDown, err := automaticDistillationCooldown(ctx, tx, e.UserID, now)
			if err != nil {
				return err
			}
			if !coolingDown {
				if err := upsertAutomaticRestriction(ctx, tx, e.UserID, now); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

type distillationQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanDistillationCase(row *sql.Row) (*service.DistillationCase, error) {
	var c service.DistillationCase
	var lastEnd sql.NullTime
	var lastScore sql.NullInt64
	if err := row.Scan(&c.UserID, &c.State, &c.ReviewStatus, &c.SuspectedStreak, &c.LimitedStreak, &c.RecoveryStreak, &lastEnd, &lastScore, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if lastEnd.Valid {
		c.LastEligibleWindowEnd = &lastEnd.Time
	}
	if lastScore.Valid {
		v := int(lastScore.Int64)
		c.LastScore = &v
	}
	return &c, nil
}

func activeDistillationExemption(ctx context.Context, q distillationQueryRower, userID int64, now time.Time) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM distillation_risk_exemptions WHERE user_id=$1 AND revoked_at IS NULL AND starts_at<=$2 AND expires_at>$2)`, userID, now).Scan(&exists)
	return exists, err
}

func automaticDistillationCooldown(ctx context.Context, q distillationQueryRower, userID int64, now time.Time) (bool, error) {
	var active bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM distillation_risk_events
WHERE user_id=$1 AND event_type IN ('manual_dismiss','manual_revoke') AND created_at > $2::timestamptz - INTERVAL '24 hours'
)`, userID, now).Scan(&active)
	return active, err
}

func upsertAutomaticRestriction(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	var id int64
	var continuousStart, expires time.Time
	err := tx.QueryRowContext(ctx, `SELECT id,continuous_started_at,expires_at FROM distillation_risk_restrictions WHERE user_id=$1 AND source='automatic' AND revoked_at IS NULL AND expires_at>=$2 ORDER BY continuous_started_at DESC LIMIT 1 FOR UPDATE`, userID, now).Scan(&id, &continuousStart, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO distillation_risk_restrictions(user_id,source,concurrency_cap,rpm_cap,continuous_started_at,starts_at,expires_at,reason,created_at,updated_at) VALUES($1,'automatic',2,30,$2,$2,$3,'three consecutive eligible windows scored at least 85',$2,$2)`, userID, now, now.Add(service.DistillationRestrictionTTL))
		if err == nil {
			err = insertDistillationEvent(ctx, tx, userID, "automatic_restriction_created", map[string]any{"concurrency_cap": 2, "rpm_cap": 30, "expires_at": now.Add(service.DistillationRestrictionTTL)}, nil, nil, now)
		}
		return err
	}
	if err != nil {
		return err
	}
	maximum := continuousStart.Add(service.DistillationMaxContinuous)
	if !now.Before(maximum) {
		return nil
	}
	desired := now.Add(service.DistillationRestrictionTTL)
	if desired.After(maximum) {
		desired = maximum
	}
	if !desired.After(expires) {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE distillation_risk_restrictions SET expires_at=$2,revision=revision+1,updated_at=$3 WHERE id=$1`, id, desired, now)
	if err == nil {
		err = insertDistillationEvent(ctx, tx, userID, "automatic_restriction_renewed", map[string]any{"restriction_id": id, "expires_at": desired}, nil, nil, now)
	}
	return err
}

func insertDistillationEvent(ctx context.Context, tx *sql.Tx, userID int64, eventType string, details any, actorID *int64, key *string, now time.Time) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var user any
	if userID > 0 {
		user = userID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO distillation_risk_events(user_id,event_type,details,actor_id,idempotency_key,created_at) VALUES($1,$2,$3::jsonb,$4,$5,$6)`, user, eventType, payload, actorID, key, now)
	return err
}

func (r *distillationRiskRepository) GetActiveRestriction(ctx context.Context, userID int64, now time.Time) (*service.DistillationRestriction, error) {
	// Multiple manual/automatic records combine using the strictest cap.
	var x service.DistillationRestriction
	var count int64
	var continuousStart, startsAt, expiresAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(id),0),COALESCE(MIN(concurrency_cap),0),COALESCE(MIN(rpm_cap),0),MIN(continuous_started_at),MIN(starts_at),MAX(expires_at)
FROM distillation_risk_restrictions
WHERE user_id=$1 AND revoked_at IS NULL AND starts_at<=$2 AND expires_at>$2
	AND (source='manual' OR NOT EXISTS(SELECT 1 FROM distillation_risk_exemptions e WHERE e.user_id=$1 AND e.revoked_at IS NULL AND e.starts_at<=$2 AND e.expires_at>$2))
  AND (source='manual' OR EXISTS(SELECT 1 FROM distillation_risk_configs WHERE id=1 AND mode='enforce'))`, userID, now).Scan(
		&count, &x.ID, &x.ConcurrencyCap, &x.RPMCap, &continuousStart, &startsAt, &expiresAt)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	x.ContinuousStartedAt, x.StartsAt, x.ExpiresAt = continuousStart.Time, startsAt.Time, expiresAt.Time
	x.UserID, x.Source, x.Revision = userID, "combined", 1
	return &x, nil
}

func (r *distillationRiskRepository) Overview(ctx context.Context, now time.Time) (*service.DistillationOverview, error) {
	cfg, err := r.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	o := &service.DistillationOverview{Mode: cfg.Mode, StateCounts: map[string]int64{}}
	rows, err := r.db.QueryContext(ctx, `SELECT state,COUNT(*) FROM distillation_risk_cases GROUP BY state`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			_ = rows.Close()
			return nil, err
		}
		o.StateCounts[state] = count
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(DISTINCT user_id) FROM distillation_risk_restrictions WHERE revoked_at IS NULL AND starts_at<=$1 AND expires_at>$1),
 (SELECT COUNT(DISTINCT user_id) FROM distillation_risk_exemptions WHERE revoked_at IS NULL AND starts_at<=$1 AND expires_at>$1),
 (SELECT COUNT(*) FROM distillation_risk_cases WHERE review_status='pending'),
 (SELECT MAX(created_at) FROM distillation_risk_windows)`, now).Scan(&o.ActiveRestrictions, &o.ActiveExemptions, &o.PendingReviews, &o.LastProcessedAt)
	if err != nil {
		return nil, err
	}
	o.AsOf = &now
	o.TelemetryStatus = "unavailable"
	var heartbeat time.Time
	var degraded bool
	healthErr := r.db.QueryRowContext(ctx, `SELECT heartbeat_at,degraded FROM distillation_worker_health WHERE id=1`).Scan(&heartbeat, &degraded)
	if healthErr == nil && !degraded && !heartbeat.Before(now.Add(-90*time.Second)) {
		o.TelemetryStatus = "healthy"
	}

	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM distillation_risk_telemetry_gaps WHERE last_seen_at>$1::timestamptz-INTERVAL '90 seconds'`, now).Scan(&o.TelemetryGaps); err != nil {
		return nil, err
	}
	if cfg.Mode == service.DistillationModeOff {
		o.TelemetryStatus = "off"
	} else if o.TelemetryGaps > 0 {
		o.TelemetryStatus = "degraded"
	}
	return o, err
}

func (r *distillationRiskRepository) ListCases(ctx context.Context, opts service.DistillationListOptions) (*service.DistillationCaseList, error) {
	where, args := distillationCaseWhere(opts)
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM distillation_risk_cases c `+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	fetchSize := opts.PageSize
	offset := (opts.Page - 1) * opts.PageSize
	if opts.Cursor {
		fetchSize++
		offset = 0
	}
	args = append(args, fetchSize, offset)
	rows, err := r.db.QueryContext(ctx, `SELECT user_id,state,review_status,suspected_streak,limited_streak,recovery_streak,last_eligible_window_end,last_score,revision,created_at,updated_at FROM distillation_risk_cases c `+where+fmt.Sprintf(" ORDER BY user_id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := &service.DistillationCaseList{Items: []service.DistillationCase{}, Total: total}
	for rows.Next() {
		var c service.DistillationCase
		var end sql.NullTime
		var score sql.NullInt64
		if err := rows.Scan(&c.UserID, &c.State, &c.ReviewStatus, &c.SuspectedStreak, &c.LimitedStreak, &c.RecoveryStreak, &end, &score, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if end.Valid {
			c.LastEligibleWindowEnd = &end.Time
		}
		if score.Valid {
			v := int(score.Int64)
			c.LastScore = &v
		}
		result.Items = append(result.Items, c)
	}
	if opts.Cursor && len(result.Items) > opts.PageSize {
		result.Items = result.Items[:opts.PageSize]
		id := result.Items[len(result.Items)-1].UserID
		result.NextBeforeID = &id
	}
	return result, rows.Err()
}

func distillationCaseWhere(opts service.DistillationListOptions) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	if opts.Cursor && opts.BeforeID > 0 {
		args = append(args, opts.BeforeID)
		parts = append(parts, fmt.Sprintf("c.user_id<$%d", len(args)))
	}
	if opts.UserID > 0 {
		args = append(args, opts.UserID)
		parts = append(parts, fmt.Sprintf("c.user_id=$%d", len(args)))
	}
	if opts.State != "" {
		args = append(args, opts.State)
		parts = append(parts, fmt.Sprintf("c.state=$%d", len(args)))
	}
	return "WHERE " + strings.Join(parts, " AND "), args
}

func (r *distillationRiskRepository) GetCase(ctx context.Context, userID int64, now time.Time) (*service.DistillationCaseDetail, error) {
	c, err := scanDistillationCase(r.db.QueryRowContext(ctx, `SELECT user_id,state,review_status,suspected_streak,limited_streak,recovery_streak,last_eligible_window_end,last_score,revision,created_at,updated_at FROM distillation_risk_cases WHERE user_id=$1`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &service.DistillationCaseDetail{Case: c}
	d.ActiveRestriction, err = r.GetActiveRestriction(ctx, userID, now)
	if err != nil {
		return nil, err
	}
	var x service.DistillationExemption
	err = r.db.QueryRowContext(ctx, `SELECT id,user_id,starts_at,expires_at,reason,revision,revoked_at,created_by,created_at,updated_at FROM distillation_risk_exemptions WHERE user_id=$1 AND revoked_at IS NULL AND starts_at<=$2 AND expires_at>$2 ORDER BY expires_at DESC LIMIT 1`, userID, now).Scan(&x.ID, &x.UserID, &x.StartsAt, &x.ExpiresAt, &x.Reason, &x.Revision, &x.RevokedAt, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt)
	if err == nil {
		d.ActiveExemption = &x
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return d, nil
}

func (r *distillationRiskRepository) ListWindows(ctx context.Context, userID int64, limit int) ([]service.DistillationWindowEvaluation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT user_id,window_start,cohort,window_end,seal_at,rule_version,workload_units,request_count,total_tokens,input_tokens,output_tokens,signals,score,status FROM distillation_risk_windows WHERE user_id=$1 ORDER BY window_start DESC,cohort LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.DistillationWindowEvaluation{}
	for rows.Next() {
		var e service.DistillationWindowEvaluation
		var signals []byte
		var score sql.NullInt64
		if err := rows.Scan(&e.UserID, &e.WindowStart, &e.Cohort, &e.WindowEnd, &e.SealAt, &e.RuleVersion, &e.WorkloadUnits, &e.RequestCount, &e.TotalTokens, &e.InputTokens, &e.OutputTokens, &signals, &score, &e.Status); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(signals, &e.Signals); err != nil {
			return nil, err
		}
		if score.Valid {
			v := int(score.Int64)
			e.Score = &v
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (r *distillationRiskRepository) ListEvents(ctx context.Context, opts service.DistillationListOptions) (*service.DistillationEventList, error) {
	where := "WHERE 1=1"
	args := []any{}
	if opts.Cursor && opts.BeforeID > 0 {
		args = append(args, opts.BeforeID)
		where += fmt.Sprintf(" AND id<$%d", len(args))
	}
	if opts.UserID > 0 {
		args = append(args, opts.UserID)
		where += fmt.Sprintf(" AND user_id=$%d", len(args))
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM distillation_risk_events `+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	fetchSize := opts.PageSize
	offset := (opts.Page - 1) * opts.PageSize
	if opts.Cursor {
		fetchSize++
		offset = 0
	}
	args = append(args, fetchSize, offset)
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,event_type,details,actor_id,idempotency_key,created_at FROM distillation_risk_events `+where+fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	res := &service.DistillationEventList{Items: []service.DistillationEvent{}, Total: total}
	for rows.Next() {
		var e service.DistillationEvent
		var uid, actor sql.NullInt64
		var key sql.NullString
		if err := rows.Scan(&e.ID, &uid, &e.EventType, &e.Details, &actor, &key, &e.CreatedAt); err != nil {
			return nil, err
		}
		if uid.Valid {
			e.UserID = &uid.Int64
		}
		if actor.Valid {
			e.ActorID = &actor.Int64
		}
		if key.Valid {
			e.IdempotencyKey = &key.String
		}
		res.Items = append(res.Items, e)
	}
	if opts.Cursor && len(res.Items) > opts.PageSize {
		res.Items = res.Items[:opts.PageSize]
		id := res.Items[len(res.Items)-1].ID
		res.NextBeforeID = &id
	}
	return res, rows.Err()
}

func (r *distillationRiskRepository) ListExemptions(ctx context.Context, opts service.DistillationListOptions, now time.Time) (*service.DistillationExemptionList, error) {
	where := "WHERE revoked_at IS NULL AND expires_at>$1"
	args := []any{now}
	if opts.Cursor && opts.BeforeID > 0 {
		args = append(args, opts.BeforeID)
		where += fmt.Sprintf(" AND id<$%d", len(args))
	}
	if opts.UserID > 0 {
		args = append(args, opts.UserID)
		where += fmt.Sprintf(" AND user_id=$%d", len(args))
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM distillation_risk_exemptions `+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	fetchSize := opts.PageSize
	offset := (opts.Page - 1) * opts.PageSize
	if opts.Cursor {
		fetchSize++
		offset = 0
	}
	args = append(args, fetchSize, offset)
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,starts_at,expires_at,reason,revision,revoked_at,created_by,created_at,updated_at FROM distillation_risk_exemptions `+where+fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	res := &service.DistillationExemptionList{Items: []service.DistillationExemption{}, Total: total}
	for rows.Next() {
		var x service.DistillationExemption
		if err := rows.Scan(&x.ID, &x.UserID, &x.StartsAt, &x.ExpiresAt, &x.Reason, &x.Revision, &x.RevokedAt, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		res.Items = append(res.Items, x)
	}
	if opts.Cursor && len(res.Items) > opts.PageSize {
		res.Items = res.Items[:opts.PageSize]
		id := res.Items[len(res.Items)-1].ID
		res.NextBeforeID = &id
	}
	return res, rows.Err()
}

func (r *distillationRiskRepository) CreateExemption(ctx context.Context, m service.DistillationExemptionMutation) (*service.DistillationExemption, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	fingerprint := distillationMutationFingerprint(m.UserID, m.ActorID, m.StartsAt, m.ExpiresAt, m.Reason)
	if existing, found, err := existingExemptionByKey(ctx, tx, m.IdempotencyKey, "exemption_created", m.UserID, m.ActorID, fingerprint); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}
	var x service.DistillationExemption
	err = tx.QueryRowContext(ctx, `INSERT INTO distillation_risk_exemptions(user_id,starts_at,expires_at,reason,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,NOW(),NOW()) RETURNING id,user_id,starts_at,expires_at,reason,revision,revoked_at,created_by,created_at,updated_at`, m.UserID, m.StartsAt, m.ExpiresAt, m.Reason, m.ActorID).Scan(&x.ID, &x.UserID, &x.StartsAt, &x.ExpiresAt, &x.Reason, &x.Revision, &x.RevokedAt, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt)
	if err != nil {
		return nil, err
	}
	actor, key := m.ActorID, m.IdempotencyKey
	if err = insertDistillationEvent(ctx, tx, m.UserID, "exemption_created", map[string]any{"exemption_id": x.ID, "request_hash": fingerprint}, &actor, &key, time.Now().UTC()); err != nil {
		return nil, mapIdempotencyError(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE distillation_risk_restrictions SET revoked_at=NOW(),revision=revision+1,updated_at=NOW() WHERE user_id=$1 AND source='automatic' AND revoked_at IS NULL AND expires_at>NOW() AND $2<=NOW() AND $3>NOW()`, m.UserID, m.StartsAt, m.ExpiresAt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &x, nil
}

func existingExemptionByKey(ctx context.Context, tx *sql.Tx, key, eventType string, userID, actorID int64, fingerprint string) (*service.DistillationExemption, bool, error) {
	var gotType string
	var id int64
	var gotUser, gotActor sql.NullInt64
	var gotHash string
	err := tx.QueryRowContext(ctx, `SELECT event_type,user_id,actor_id,COALESCE((details->>'exemption_id')::bigint,0),COALESCE(details->>'request_hash','') FROM distillation_risk_events WHERE idempotency_key=$1`, key).Scan(&gotType, &gotUser, &gotActor, &id, &gotHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if gotType != eventType || id <= 0 || !gotUser.Valid || gotUser.Int64 != userID || !gotActor.Valid || gotActor.Int64 != actorID || gotHash != fingerprint {
		return nil, false, service.ErrDistillationIdempotencyReuse
	}
	var x service.DistillationExemption
	err = tx.QueryRowContext(ctx, `SELECT id,user_id,starts_at,expires_at,reason,revision,revoked_at,created_by,created_at,updated_at FROM distillation_risk_exemptions WHERE id=$1`, id).Scan(&x.ID, &x.UserID, &x.StartsAt, &x.ExpiresAt, &x.Reason, &x.Revision, &x.RevokedAt, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt)
	return &x, true, err
}

func (r *distillationRiskRepository) RevokeExemption(ctx context.Context, m service.DistillationExemptionMutation) (*service.DistillationExemption, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	fingerprint := distillationMutationFingerprint(m.ID, m.ExpectedRevision, m.ActorID)
	// User ID is discovered from the exemption for a new mutation. Replays
	// validate the actor and request hash before returning any record.
	if existing, found, err := existingExemptionByKeyAnyUser(ctx, tx, m.IdempotencyKey, "exemption_revoked", m.ActorID, fingerprint); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}
	var x service.DistillationExemption
	err = tx.QueryRowContext(ctx, `UPDATE distillation_risk_exemptions SET revoked_at=NOW(),revision=revision+1,updated_at=NOW() WHERE id=$1 AND revision=$2 AND revoked_at IS NULL RETURNING id,user_id,starts_at,expires_at,reason,revision,revoked_at,created_by,created_at,updated_at`, m.ID, m.ExpectedRevision).Scan(&x.ID, &x.UserID, &x.StartsAt, &x.ExpiresAt, &x.Reason, &x.Revision, &x.RevokedAt, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationRevisionConflict
	}
	if err != nil {
		return nil, err
	}
	actor, key := m.ActorID, m.IdempotencyKey
	if err = insertDistillationEvent(ctx, tx, x.UserID, "exemption_revoked", map[string]any{"exemption_id": x.ID, "request_hash": fingerprint}, &actor, &key, time.Now().UTC()); err != nil {
		return nil, mapIdempotencyError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &x, nil
}

func (r *distillationRiskRepository) ApplyAction(ctx context.Context, a service.DistillationAction, now time.Time) (*service.DistillationCaseDetail, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	fingerprint := distillationMutationFingerprint(a.UserID, a.Type, a.ExpectedRevision, a.Reason, a.ActorID, a.ExpiresAt)
	var existingType, existingHash string
	var existingUser, existingActor sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT event_type,user_id,actor_id,COALESCE(details->>'request_hash','') FROM distillation_risk_events WHERE idempotency_key=$1`, a.IdempotencyKey).Scan(&existingType, &existingUser, &existingActor, &existingHash)
	if err == nil {
		if existingType != "manual_"+a.Type || !existingUser.Valid || existingUser.Int64 != a.UserID || !existingActor.Valid || existingActor.Int64 != a.ActorID || existingHash != fingerprint {
			return nil, service.ErrDistillationIdempotencyReuse
		}
		_ = tx.Rollback()
		return r.GetCase(ctx, a.UserID, now)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	c, err := scanDistillationCase(tx.QueryRowContext(ctx, `SELECT user_id,state,review_status,suspected_streak,limited_streak,recovery_streak,last_eligible_window_end,last_score,revision,created_at,updated_at FROM distillation_risk_cases WHERE user_id=$1 FOR UPDATE`, a.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDistillationNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.Revision != a.ExpectedRevision {
		return nil, service.ErrDistillationRevisionConflict
	}
	state, review := c.State, c.ReviewStatus
	switch a.Type {
	case "in_review":
		review = "in_review"
	case "confirm":
		review = "confirmed"
	case "dismiss":
		state, review = service.DistillationStateClear, "dismissed"
	case "restrict":
		state, review = service.DistillationStateLimited, "confirmed"
	case "revoke":
		state = service.DistillationStateClear
	}
	if a.Type == "dismiss" || a.Type == "revoke" {
		if _, err = tx.ExecContext(ctx, `UPDATE distillation_risk_cohort_progress SET state='clear',review_status='none',suspected_streak=0,limited_streak=0,recovery_streak=0,updated_at=$2 WHERE user_id=$1`, a.UserID, now); err != nil {
			return nil, err
		}
		c.SuspectedStreak, c.LimitedStreak, c.RecoveryStreak = 0, 0, 0
	}
	res, err := tx.ExecContext(ctx, `UPDATE distillation_risk_cases SET state=$2,review_status=$3,suspected_streak=$4,limited_streak=$5,recovery_streak=$6,revision=revision+1,updated_at=$7 WHERE user_id=$1 AND revision=$8`, a.UserID, state, review, c.SuspectedStreak, c.LimitedStreak, c.RecoveryStreak, now, a.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected != 1 {
		return nil, service.ErrDistillationRevisionConflict
	}
	switch a.Type {
	case "restrict":
		_, err = tx.ExecContext(ctx, `INSERT INTO distillation_risk_restrictions(user_id,source,concurrency_cap,rpm_cap,continuous_started_at,starts_at,expires_at,reason,created_by,created_at,updated_at) VALUES($1,'manual',2,30,$2,$2,$3,$4,$5,$2,$2)`, a.UserID, now, a.ExpiresAt, a.Reason, a.ActorID)
	case "revoke", "dismiss":
		_, err = tx.ExecContext(ctx, `UPDATE distillation_risk_restrictions SET revoked_at=$2,revision=revision+1,updated_at=$2 WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>$2 AND ($3='revoke' OR source='automatic')`, a.UserID, now, a.Type)
	}
	if err != nil {
		return nil, err
	}
	actor, key := a.ActorID, a.IdempotencyKey
	if err = insertDistillationEvent(ctx, tx, a.UserID, "manual_"+a.Type, map[string]any{"reason": a.Reason, "request_hash": fingerprint}, &actor, &key, now); err != nil {
		return nil, mapIdempotencyError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCase(ctx, a.UserID, now)
}

func mapIdempotencyError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return service.ErrDistillationIdempotencyReuse
	}
	return err
}

func distillationMutationFingerprint(values ...any) string {
	payload, _ := json.Marshal(values)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:])
}

func existingExemptionByKeyAnyUser(ctx context.Context, tx *sql.Tx, key, eventType string, actorID int64, fingerprint string) (*service.DistillationExemption, bool, error) {
	var gotType, gotHash string
	var gotActor sql.NullInt64
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT event_type,actor_id,COALESCE((details->>'exemption_id')::bigint,0),COALESCE(details->>'request_hash','') FROM distillation_risk_events WHERE idempotency_key=$1`, key).Scan(&gotType, &gotActor, &id, &gotHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if gotType != eventType || !gotActor.Valid || gotActor.Int64 != actorID || id <= 0 || gotHash != fingerprint {
		return nil, false, service.ErrDistillationIdempotencyReuse
	}
	var x service.DistillationExemption
	err = tx.QueryRowContext(ctx, `SELECT id,user_id,starts_at,expires_at,reason,revision,revoked_at,created_by,created_at,updated_at FROM distillation_risk_exemptions WHERE id=$1`, id).Scan(&x.ID, &x.UserID, &x.StartsAt, &x.ExpiresAt, &x.Reason, &x.Revision, &x.RevokedAt, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt)
	return &x, true, err
}

func (r *distillationRiskRepository) RecordWorkerHealth(ctx context.Context, at time.Time, workerErr error) error {
	message := ""
	if workerErr != nil {
		message = workerErr.Error()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO distillation_worker_health(id,heartbeat_at,last_success,last_error,degraded) VALUES(1,$1,CASE WHEN $2='' THEN $1::timestamptz ELSE NULL END,$2,$3) ON CONFLICT(id) DO UPDATE SET heartbeat_at=EXCLUDED.heartbeat_at,last_success=COALESCE(EXCLUDED.last_success,distillation_worker_health.last_success),last_error=EXCLUDED.last_error,degraded=EXCLUDED.degraded`, at, message, workerErr != nil)
	return err
}
