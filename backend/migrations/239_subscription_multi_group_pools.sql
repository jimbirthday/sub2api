-- Shared subscription pools may authorize several subscription groups.
-- Existing rows remain valid: group_id is the legacy anchor and group_ids is
-- backfilled with that anchor when the migration runs.
ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS group_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);

UPDATE user_subscriptions
SET group_ids = jsonb_build_array(group_id)
WHERE group_ids = '[]'::jsonb OR group_ids IS NULL;

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS group_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);

UPDATE subscription_plans
SET group_ids = jsonb_build_array(group_id)
WHERE group_ids = '[]'::jsonb OR group_ids IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_group_ids_gin
    ON user_subscriptions USING GIN (group_ids);
CREATE INDEX IF NOT EXISTS idx_subscription_plans_group_ids_gin
    ON subscription_plans USING GIN (group_ids);
