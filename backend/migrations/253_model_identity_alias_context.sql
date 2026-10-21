ALTER TABLE model_identity_catalog_aliases
 ADD COLUMN IF NOT EXISTS alias_id UUID NOT NULL DEFAULT gen_random_uuid(),
 ADD COLUMN IF NOT EXISTS protocol VARCHAR(64) NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS role VARCHAR(32) NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS provider_context VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE model_identity_catalog_aliases DROP CONSTRAINT IF EXISTS model_identity_catalog_aliases_pkey;
ALTER TABLE model_identity_catalog_aliases ADD PRIMARY KEY(revision,namespace,transport,protocol,role,provider_context,alias);
CREATE UNIQUE INDEX IF NOT EXISTS uq_model_identity_alias_id ON model_identity_catalog_aliases(revision,alias_id);
