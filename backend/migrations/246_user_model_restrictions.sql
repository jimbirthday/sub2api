CREATE TABLE IF NOT EXISTS user_model_restriction_heads (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    current_revision BIGINT NOT NULL DEFAULT 0 CHECK (current_revision >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by BIGINT NULL
);

CREATE TABLE IF NOT EXISTS user_model_restriction_versions (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL CHECK (revision >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by BIGINT NULL,
    PRIMARY KEY (user_id, revision)
);

CREATE TABLE IF NOT EXISTS user_model_restriction_rules (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL,
    namespace VARCHAR(64) NOT NULL,
    canonical_model VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_by BIGINT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (user_id, revision)
        REFERENCES user_model_restriction_versions(user_id, revision)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_model_restriction_rules_key
    ON user_model_restriction_rules (user_id, revision, namespace, canonical_model);

CREATE INDEX IF NOT EXISTS idx_user_model_restriction_rules_active
    ON user_model_restriction_rules (user_id, namespace, canonical_model)
    WHERE enabled = TRUE;
