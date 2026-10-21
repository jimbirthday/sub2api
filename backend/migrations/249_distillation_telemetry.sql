CREATE TABLE IF NOT EXISTS distillation_risk_attempts (
    attempt_id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT NULL REFERENCES api_keys(id) ON DELETE SET NULL,
    operation_id VARCHAR(128) NULL,
    provider VARCHAR(64) NOT NULL,
    model VARCHAR(255) NOT NULL,
    prompt_hmac BYTEA NULL CHECK (prompt_hmac IS NULL OR octet_length(prompt_hmac) = 32),
    hmac_day DATE NULL,
    configured_concurrency INTEGER NOT NULL DEFAULT 0 CHECK (configured_concurrency >= 0),
    configured_rpm INTEGER NOT NULL DEFAULT 0 CHECK (configured_rpm >= 0),
    observed_concurrency INTEGER NOT NULL CHECK (observed_concurrency >= 1),
    cohort VARCHAR(16) NOT NULL DEFAULT 'interactive' CHECK (cohort IN ('interactive', 'async_batch')),
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL,
    input_tokens BIGINT NULL CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens BIGINT NULL CHECK (output_tokens IS NULL OR output_tokens >= 0),
    status VARCHAR(16) NOT NULL CHECK (status IN ('active', 'completed', 'failed', 'abandoned')),
    telemetry_complete BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((prompt_hmac IS NULL) = (hmac_day IS NULL)),
    CHECK (completed_at IS NULL OR completed_at >= started_at)
);

ALTER TABLE distillation_risk_attempts ADD COLUMN IF NOT EXISTS configured_rpm INTEGER NOT NULL DEFAULT 0 CHECK (configured_rpm >= 0);
ALTER TABLE distillation_risk_attempts ADD COLUMN IF NOT EXISTS cohort VARCHAR(16) NOT NULL DEFAULT 'interactive' CHECK (cohort IN ('interactive', 'async_batch'));

CREATE INDEX IF NOT EXISTS idx_distillation_attempts_user_started
    ON distillation_risk_attempts (user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_distillation_attempts_active_user
    ON distillation_risk_attempts (user_id, started_at)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_distillation_attempts_prompt_repeat
    ON distillation_risk_attempts (user_id, hmac_day, prompt_hmac)
    WHERE prompt_hmac IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_distillation_attempts_operation
    ON distillation_risk_attempts (user_id, operation_id)
    WHERE operation_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS distillation_risk_telemetry_gaps (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    window_start TIMESTAMPTZ NOT NULL,
    reason VARCHAR(64) NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    occurrences BIGINT NOT NULL DEFAULT 1 CHECK (occurrences >= 1),
    UNIQUE (user_id, window_start, reason),
    CHECK (window_start = date_bin(INTERVAL '30 minutes', window_start, TIMESTAMPTZ '1970-01-01 00:00:00+00'))
);

CREATE INDEX IF NOT EXISTS idx_distillation_telemetry_gaps_window
    ON distillation_risk_telemetry_gaps (window_start DESC, user_id);

-- Operation intents survive process crashes and are reconciled explicitly.
-- They have no automatic expiry because expiry could silently over-allocate work.
CREATE TABLE IF NOT EXISTS distillation_risk_operation_intents (
    operation_id VARCHAR(128) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind VARCHAR(32) NOT NULL,
    state VARCHAR(24) NOT NULL CHECK (state IN ('reserved', 'running', 'completed', 'cancelled', 'reconcile_required')),
    targets JSONB NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_distillation_operation_intents_user_state
    ON distillation_risk_operation_intents (user_id, state, created_at);
