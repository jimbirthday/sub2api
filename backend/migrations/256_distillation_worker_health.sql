CREATE TABLE IF NOT EXISTS distillation_worker_health (
    id INTEGER PRIMARY KEY CHECK (id=1),
    heartbeat_at TIMESTAMPTZ NOT NULL,
    last_success TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '',
    degraded BOOLEAN NOT NULL DEFAULT TRUE
);
