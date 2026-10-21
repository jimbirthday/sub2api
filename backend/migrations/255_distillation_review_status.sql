ALTER TABLE distillation_risk_cases DROP CONSTRAINT IF EXISTS distillation_risk_cases_review_status_check;
ALTER TABLE distillation_risk_cases ADD CONSTRAINT distillation_risk_cases_review_status_check
    CHECK (review_status IN ('none','pending','in_review','confirmed','dismissed'));
