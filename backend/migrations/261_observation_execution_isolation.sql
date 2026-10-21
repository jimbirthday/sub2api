-- Earlier builds created hard execution leases for ordinary observed traffic.
-- Keep their audit state, but detach capacity that had no explicit model cap.
-- Existing asynchronous jobs with a funds hold retain durable protection.
ALTER TABLE model_execution_attempts
    ADD COLUMN IF NOT EXISTS enforces_limits BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS observation_detached_at TIMESTAMPTZ;

UPDATE model_execution_attempts a
SET enforces_limits = FALSE
WHERE a.hold_id = ''
  AND (a.policy_revision = 0 OR EXISTS (
    SELECT 1 FROM user_model_restriction_versions v
    WHERE v.user_id = a.user_id AND v.revision = a.policy_revision
  ))
  AND EXISTS (
    SELECT 1 FROM distillation_risk_attempts t
    WHERE t.user_id = a.user_id AND t.operation_id = a.operation_id
      AND t.cohort = 'interactive'
  )
  AND NOT EXISTS (
    SELECT 1 FROM user_model_restriction_rules r
    WHERE r.user_id = a.user_id AND r.revision = a.policy_revision
      AND r.enabled AND r.action IN ('cap_concurrency', 'cap_rpm')
      AND r.limit_value > 0
      AND (r.starts_at IS NULL OR r.starts_at <= a.created_at)
      AND (r.expires_at IS NULL OR r.expires_at > a.created_at)
      AND a.targets ? (r.namespace || ':' || r.canonical_model)
  );

CREATE INDEX IF NOT EXISTS model_execution_observation_detach_idx
    ON model_execution_attempts(id)
    WHERE NOT enforces_limits AND observation_detached_at IS NULL
      AND state IN ('running', 'unknown');
