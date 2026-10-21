ALTER TABLE user_model_restriction_rules
    ADD COLUMN IF NOT EXISTS rule_id UUID NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN IF NOT EXISTS action VARCHAR(24) NOT NULL DEFAULT 'deny_model',
    ADD COLUMN IF NOT EXISTS limit_value INTEGER NULL,
    ADD COLUMN IF NOT EXISTS starts_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'manual';
ALTER TABLE user_model_restriction_rules ADD CONSTRAINT ck_user_model_policy_action
    CHECK ((action='deny_model' AND limit_value IS NULL) OR (action IN ('cap_concurrency','cap_rpm') AND limit_value>0));
ALTER TABLE user_model_restriction_rules ADD CONSTRAINT ck_user_model_policy_source CHECK (source IN ('manual','behavior'));
ALTER TABLE user_model_restriction_rules ADD CONSTRAINT ck_user_model_policy_period CHECK (starts_at IS NULL OR expires_at IS NULL OR expires_at>starts_at);
DROP INDEX IF EXISTS uq_user_model_restriction_rules_key;
CREATE UNIQUE INDEX uq_user_model_restriction_rules_key ON user_model_restriction_rules(user_id,revision,namespace,canonical_model,action,source);
CREATE UNIQUE INDEX uq_user_model_restriction_rule_identity ON user_model_restriction_rules(user_id,revision,rule_id);
ALTER TABLE user_model_restriction_versions ADD COLUMN IF NOT EXISTS parent_revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS catalog_revision BIGINT NULL,
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'manual';
UPDATE user_model_restriction_versions SET parent_revision=revision-1;
