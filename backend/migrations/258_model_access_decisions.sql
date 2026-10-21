CREATE TABLE model_access_decisions (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    operation_id VARCHAR(128) NOT NULL DEFAULT '',
    user_id BIGINT NOT NULL,
    policy_revision BIGINT NOT NULL,
    catalog_revision BIGINT NOT NULL,
    decision VARCHAR(24) NOT NULL CHECK (decision IN ('allowed','denied','unresolved','unavailable')),
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    model_identities JSONB NOT NULL DEFAULT '[]'::jsonb,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_model_access_decisions_created ON model_access_decisions(created_at);
CREATE INDEX idx_model_access_decisions_user_created ON model_access_decisions(user_id,created_at DESC);
