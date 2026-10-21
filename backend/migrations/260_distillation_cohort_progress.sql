-- Cohort evidence must never be combined into one consecutive-window chain.
-- Start conservatively with no inherited streak: historical user-level streaks
-- cannot prove which cohort supplied each window.
CREATE TABLE distillation_risk_cohort_progress (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cohort VARCHAR(16) NOT NULL CHECK (cohort IN ('interactive','async_batch')),
    state VARCHAR(16) NOT NULL DEFAULT 'clear' CHECK (state IN ('clear','observing','suspected','limited')),
    review_status VARCHAR(16) NOT NULL DEFAULT 'none',
    suspected_streak INTEGER NOT NULL DEFAULT 0,
    limited_streak INTEGER NOT NULL DEFAULT 0,
    recovery_streak INTEGER NOT NULL DEFAULT 0,
    last_eligible_window_end TIMESTAMPTZ,
    last_score INTEGER,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,cohort)
);
