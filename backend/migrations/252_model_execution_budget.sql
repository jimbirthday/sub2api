-- Durable execution ownership for provider admission and conservative recovery.
CREATE TABLE IF NOT EXISTS model_execution_attempts (
  id BIGSERIAL PRIMARY KEY,
  operation_id TEXT NOT NULL,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  attempt_id TEXT NOT NULL,
  account_id BIGINT,
  provider TEXT,
  owner_epoch BIGINT NOT NULL DEFAULT 1,
  policy_revision BIGINT NOT NULL DEFAULT 0,
  catalog_revision BIGINT NOT NULL DEFAULT 0,
  owner_id TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'intent',
  payload_hash TEXT NOT NULL DEFAULT '',
  targets JSONB NOT NULL DEFAULT '[]'::jsonb,
  funds_state TEXT NOT NULL DEFAULT 'none',
  hold_id TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(operation_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS model_execution_attempts_recovery_idx
  ON model_execution_attempts(state, updated_at);
