package repository

import (
	"context"
	"fmt"
	"time"
)

// CleanupModelAccessHistory bounds each DELETE and never removes live policy
// heads or unresolved work. HMAC material has its separate seven-day cleanup.
func (r *distillationTelemetryRepository) CleanupModelAccessHistory(ctx context.Context, now time.Time) error {
	targets := []struct {
		name  string
		query string
		days  int
	}{
		{"decisions", `DELETE FROM model_access_decisions WHERE id IN (SELECT id FROM model_access_decisions WHERE created_at < $1 ORDER BY id LIMIT 1000)`, 180},
		{"events", `DELETE FROM distillation_risk_events WHERE id IN (SELECT id FROM distillation_risk_events WHERE created_at < $1 ORDER BY id LIMIT 1000)`, 180},
		{"windows", `DELETE FROM distillation_risk_windows WHERE id IN (SELECT id FROM distillation_risk_windows WHERE window_end < $1 ORDER BY id LIMIT 1000)`, 90},
		{"gaps", `DELETE FROM distillation_risk_telemetry_gaps WHERE id IN (SELECT id FROM distillation_risk_telemetry_gaps WHERE window_start < $1 ORDER BY id LIMIT 1000)`, 90},
		{"attempts", `DELETE FROM distillation_risk_attempts WHERE attempt_id IN (SELECT attempt_id FROM distillation_risk_attempts WHERE completed_at < $1 AND status IN ('completed','failed','abandoned') ORDER BY completed_at LIMIT 1000)`, 90},
		{"policy_versions", `DELETE FROM user_model_restriction_versions WHERE (user_id,revision) IN (SELECT v.user_id,v.revision FROM user_model_restriction_versions v WHERE v.created_at < $1 AND NOT EXISTS (SELECT 1 FROM user_model_restriction_heads h WHERE h.user_id=v.user_id AND h.current_revision=v.revision) ORDER BY v.created_at LIMIT 1000)`, 365},
	}
	for _, target := range targets {
		for {
			result, err := r.db.ExecContext(ctx, target.query, now.UTC().AddDate(0, 0, -target.days))
			if err != nil {
				return fmt.Errorf("cleanup model access %s: %w", target.name, err)
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count < 1000 {
				break
			}
		}
	}
	return nil
}
