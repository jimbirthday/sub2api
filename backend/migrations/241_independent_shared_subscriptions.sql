-- Close the experimental extension without changing legacy subscription rules.
-- 239/240 remain immutable because some installations have applied them.
UPDATE user_subscriptions SET deleted_at = COALESCE(deleted_at, NOW()) WHERE quota_source = 'pool';
UPDATE subscription_plans SET for_sale = FALSE WHERE quota_source = 'pool';
DROP INDEX IF EXISTS user_subscriptions_user_group_source_unique_active;
CREATE UNIQUE INDEX IF NOT EXISTS user_subscriptions_user_group_unique_active
 ON user_subscriptions(user_id, group_id) WHERE deleted_at IS NULL;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS group_ids, DROP COLUMN IF EXISTS quota_source,
 DROP COLUMN IF EXISTS daily_limit_usd, DROP COLUMN IF EXISTS weekly_limit_usd, DROP COLUMN IF EXISTS monthly_limit_usd;
ALTER TABLE subscription_plans DROP COLUMN IF EXISTS group_ids, DROP COLUMN IF EXISTS quota_source,
 DROP COLUMN IF EXISTS daily_limit_usd, DROP COLUMN IF EXISTS weekly_limit_usd, DROP COLUMN IF EXISTS monthly_limit_usd;

CREATE TABLE shared_subscription_plans (
 id BIGSERIAL PRIMARY KEY,
 definition JSONB NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 for_sale BOOLEAN NOT NULL DEFAULT TRUE,
 deleted_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE shared_subscription_plan_groups (
 plan_id BIGINT NOT NULL REFERENCES shared_subscription_plans(id),
 group_id BIGINT NOT NULL REFERENCES groups(id),
 PRIMARY KEY(plan_id, group_id)
);
CREATE TABLE shared_subscriptions (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 plan_id BIGINT NOT NULL REFERENCES shared_subscription_plans(id),
 snapshot JSONB NOT NULL,
 starts_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 status VARCHAR(20) NOT NULL DEFAULT 'active',
 generation INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CHECK(status IN ('active','paused','revoked'))
);
CREATE INDEX shared_subscription_user_active ON shared_subscriptions(user_id, expires_at, id) WHERE status = 'active';
CREATE TABLE shared_subscription_groups (
 subscription_id BIGINT NOT NULL REFERENCES shared_subscriptions(id),
 group_id BIGINT NOT NULL REFERENCES groups(id),
 PRIMARY KEY(subscription_id, group_id)
);
CREATE INDEX shared_subscription_group_members ON shared_subscription_groups(group_id, subscription_id);
CREATE TABLE shared_subscription_windows (
 subscription_id BIGINT NOT NULL REFERENCES shared_subscriptions(id),
 generation INTEGER NOT NULL,
 kind VARCHAR(10) NOT NULL CHECK(kind IN ('daily','weekly','monthly')),
 starts_at TIMESTAMPTZ NOT NULL,
 used NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK(used >= 0),
 reserved NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK(reserved >= 0),
 PRIMARY KEY(subscription_id, generation, kind, starts_at)
);
CREATE TABLE shared_subscription_order_items (
 order_id BIGINT PRIMARY KEY REFERENCES payment_orders(id),
 snapshot JSONB NOT NULL,
 renew_subscription_id BIGINT REFERENCES shared_subscriptions(id),
 subscription_id BIGINT REFERENCES shared_subscriptions(id),
 duration_days INTEGER NOT NULL CHECK(duration_days > 0),
 refunded BOOLEAN NOT NULL DEFAULT FALSE,
 fulfilled_at TIMESTAMPTZ
);
CREATE TABLE shared_subscription_settlements (
 request_id TEXT NOT NULL,
 api_key_id BIGINT NOT NULL,
 user_id BIGINT NOT NULL REFERENCES users(id),
 group_id BIGINT NOT NULL,
 cost NUMERIC(20,8) NOT NULL,
 subscription_cost NUMERIC(20,8) NOT NULL,
 balance_cost NUMERIC(20,8) NOT NULL,
 billing_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(request_id, api_key_id),
 CHECK(cost = subscription_cost + balance_cost)
);
CREATE INDEX shared_settlement_user_time ON shared_subscription_settlements(user_id, created_at DESC);
CREATE TABLE shared_subscription_debits (
 request_id TEXT NOT NULL,
 api_key_id BIGINT NOT NULL,
 subscription_id BIGINT NOT NULL REFERENCES shared_subscriptions(id),
 amount NUMERIC(20,8) NOT NULL CHECK(amount >= 0),
 PRIMARY KEY(request_id, api_key_id, subscription_id)
);
CREATE TABLE shared_subscription_holds (
 batch_id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL,
 funding JSONB NOT NULL,
 allocations JSONB NOT NULL,
 wallet_amount NUMERIC(20,8) NOT NULL,
 total_amount NUMERIC(20,8) NOT NULL,
 status VARCHAR(20) NOT NULL DEFAULT 'held' CHECK(status IN ('held','captured','released')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Persist final immutable commands before executing any monetary effects.
CREATE TABLE shared_subscription_billing_jobs (
 request_id TEXT NOT NULL,
 api_key_id BIGINT NOT NULL,
 fingerprint TEXT NOT NULL,
 command JSONB NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 completed_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(request_id, api_key_id)
);
CREATE INDEX shared_billing_pending ON shared_subscription_billing_jobs(created_at) WHERE completed_at IS NULL;
-- Batch-image jobs need their original authorization even after restart/key edits.
ALTER TABLE batch_image_jobs ADD COLUMN shared_funding JSONB;
CREATE TABLE shared_subscription_grants (
 user_id BIGINT NOT NULL REFERENCES users(id),
 request_id TEXT NOT NULL,
 plan_id BIGINT NOT NULL REFERENCES shared_subscription_plans(id),
 subscription_id BIGINT REFERENCES shared_subscriptions(id),
 PRIMARY KEY(user_id, request_id)
);
