CREATE TABLE IF NOT EXISTS distillation_risk_configs (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    mode VARCHAR(16) NOT NULL DEFAULT 'observe' CHECK (mode IN ('off', 'observe', 'enforce')),
    observation_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    scoring_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    rule_version BIGINT NOT NULL DEFAULT 1 CHECK (rule_version >= 1),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    updated_by BIGINT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO distillation_risk_configs (id, mode)
VALUES (1, 'observe')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS distillation_risk_windows (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    seal_at TIMESTAMPTZ NOT NULL,
    rule_version BIGINT NOT NULL,
    workload_units BIGINT NOT NULL CHECK (workload_units >= 0),
    request_count BIGINT NOT NULL CHECK (request_count >= 0),
    total_tokens BIGINT NOT NULL CHECK (total_tokens >= 0),
    input_tokens BIGINT NOT NULL CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL CHECK (output_tokens >= 0),
    signals JSONB NOT NULL,
    score INTEGER NULL CHECK (score IS NULL OR (score >= 0 AND score <= 100)),
    status VARCHAR(32) NOT NULL CHECK (status IN ('insufficient_data', 'scored', 'telemetry_failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (window_end = window_start + INTERVAL '30 minutes'),
    CHECK (seal_at = window_end + INTERVAL '10 minutes'),
    UNIQUE (user_id, window_start, rule_version)
);

CREATE INDEX IF NOT EXISTS idx_distillation_risk_windows_user_time
    ON distillation_risk_windows (user_id, window_start DESC);
CREATE INDEX IF NOT EXISTS idx_distillation_risk_windows_time
    ON distillation_risk_windows (window_start DESC);

CREATE TABLE IF NOT EXISTS distillation_risk_cases (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state VARCHAR(24) NOT NULL DEFAULT 'clear' CHECK (state IN ('clear', 'observing', 'suspected', 'limited')),
    review_status VARCHAR(24) NOT NULL DEFAULT 'none' CHECK (review_status IN ('none', 'pending', 'confirmed', 'dismissed')),
    suspected_streak INTEGER NOT NULL DEFAULT 0 CHECK (suspected_streak >= 0),
    limited_streak INTEGER NOT NULL DEFAULT 0 CHECK (limited_streak >= 0),
    recovery_streak INTEGER NOT NULL DEFAULT 0 CHECK (recovery_streak >= 0),
    last_eligible_window_end TIMESTAMPTZ NULL,
    last_score INTEGER NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_distillation_risk_cases_state
    ON distillation_risk_cases (state, updated_at DESC);

CREATE TABLE IF NOT EXISTS distillation_risk_restrictions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source VARCHAR(16) NOT NULL CHECK (source IN ('automatic', 'manual')),
    concurrency_cap INTEGER NOT NULL CHECK (concurrency_cap > 0),
    rpm_cap INTEGER NOT NULL CHECK (rpm_cap > 0),
    continuous_started_at TIMESTAMPTZ NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    reason TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_by BIGINT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at > starts_at),
    CHECK (source <> 'automatic' OR expires_at <= continuous_started_at + INTERVAL '6 hours')
);

CREATE INDEX IF NOT EXISTS idx_distillation_risk_restrictions_active
    ON distillation_risk_restrictions (user_id, expires_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS distillation_risk_exemptions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    revoked_at TIMESTAMPTZ NULL,
    created_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at > starts_at),
    CHECK (expires_at <= starts_at + INTERVAL '30 days')
);

CREATE INDEX IF NOT EXISTS idx_distillation_risk_exemptions_active
    ON distillation_risk_exemptions (user_id, expires_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS distillation_risk_events (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    event_type VARCHAR(64) NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    actor_id BIGINT NULL,
    idempotency_key VARCHAR(128) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_distillation_risk_events_idempotency
    ON distillation_risk_events (idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_distillation_risk_events_time
    ON distillation_risk_events (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_distillation_risk_events_user_time
    ON distillation_risk_events (user_id, created_at DESC, id DESC);
