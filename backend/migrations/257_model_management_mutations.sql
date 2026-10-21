CREATE TABLE model_management_mutations (
 actor_id BIGINT NOT NULL,
 idempotency_key VARCHAR(200) NOT NULL,
 request_hash VARCHAR(64) NOT NULL,
 resource VARCHAR(100) NOT NULL,
 reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
 result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(actor_id,idempotency_key)
);
