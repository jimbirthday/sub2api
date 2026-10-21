ALTER TABLE distillation_risk_windows
    ADD COLUMN IF NOT EXISTS cohort VARCHAR(16) NOT NULL DEFAULT 'interactive'
    CHECK (cohort IN ('interactive', 'async_batch'));

ALTER TABLE distillation_risk_windows
    DROP CONSTRAINT IF EXISTS distillation_risk_windows_user_id_window_start_rule_version_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_distillation_risk_windows_user_cohort_rule
    ON distillation_risk_windows(user_id, window_start, cohort, rule_version);
