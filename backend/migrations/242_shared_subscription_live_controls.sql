-- Live shared-plan entitlements, per-subscription quota overrides and checkout replacement.
ALTER TABLE shared_subscriptions
 ADD COLUMN quota_overrides JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE shared_subscription_order_items
 ADD COLUMN replace_subscription_id BIGINT REFERENCES shared_subscriptions(id);

CREATE TABLE shared_subscription_quota_adjustments (
 id BIGSERIAL PRIMARY KEY,
 subscription_id BIGINT NOT NULL REFERENCES shared_subscriptions(id),
 operator_user_id BIGINT NOT NULL REFERENCES users(id),
 reason TEXT NOT NULL,
 before_state JSONB NOT NULL,
 after_state JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX shared_subscription_quota_adjustments_subscription_time
 ON shared_subscription_quota_adjustments(subscription_id, created_at DESC);
