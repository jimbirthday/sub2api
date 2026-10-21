ALTER TABLE model_execution_attempts ADD COLUMN IF NOT EXISTS reclaimed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS model_execution_attempts_unreclaimed_terminal_idx
 ON model_execution_attempts(id) WHERE state='completed' AND reclaimed_at IS NULL;
