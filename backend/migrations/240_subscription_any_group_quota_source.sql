-- Existing subscriptions retain their original group-based limit semantics.
ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS quota_source VARCHAR(20) NOT NULL DEFAULT 'legacy_group';
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS quota_source VARCHAR(20) NOT NULL DEFAULT 'legacy_group';

-- Buying a new independent pool must not rewrite an existing legacy subscription.
DROP INDEX IF EXISTS user_subscriptions_user_group_unique_active;
CREATE UNIQUE INDEX IF NOT EXISTS user_subscriptions_user_group_source_unique_active
 ON user_subscriptions(user_id, group_id, quota_source) WHERE deleted_at IS NULL;
