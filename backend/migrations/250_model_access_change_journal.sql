-- A transactional publication journal. Admission reads primary SQL snapshots;
-- this journal lets audit/invalidation consumers replay committed changes by ID.
CREATE TABLE IF NOT EXISTS model_access_change_journal (
    id BIGSERIAL PRIMARY KEY,
    aggregate_type VARCHAR(32) NOT NULL CHECK (aggregate_type IN ('user_policy', 'identity_catalog')),
    aggregate_id BIGINT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision >= 1),
    actor_id BIGINT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (aggregate_type, aggregate_id, revision)
);

CREATE OR REPLACE FUNCTION journal_user_model_policy_change() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.current_revision > 0 AND (TG_OP = 'INSERT' OR NEW.current_revision IS DISTINCT FROM OLD.current_revision) THEN
        INSERT INTO model_access_change_journal (aggregate_type, aggregate_id, revision, actor_id)
        VALUES ('user_policy', NEW.user_id, NEW.current_revision, NEW.updated_by);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_user_model_policy_change ON user_model_restriction_heads;
CREATE TRIGGER trg_user_model_policy_change
AFTER INSERT OR UPDATE ON user_model_restriction_heads
FOR EACH ROW EXECUTE FUNCTION journal_user_model_policy_change();

CREATE OR REPLACE FUNCTION journal_model_identity_catalog_change() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.revision IS DISTINCT FROM OLD.revision THEN
        INSERT INTO model_access_change_journal (aggregate_type, aggregate_id, revision, actor_id)
        VALUES ('identity_catalog', NEW.id, NEW.revision, NEW.updated_by);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_model_identity_catalog_change ON model_identity_catalog_heads;
CREATE TRIGGER trg_model_identity_catalog_change
AFTER INSERT OR UPDATE ON model_identity_catalog_heads
FOR EACH ROW EXECUTE FUNCTION journal_model_identity_catalog_change();
