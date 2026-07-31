-- Some pre-release installations created provider_models with a model_id-only
-- primary key. Migration 017 intentionally uses a provider-scoped key, but its
-- CREATE TABLE IF NOT EXISTS could not repair an already-existing table. The
-- stale key makes a valid provider refresh fail with provider_models_pkey even
-- though the refresh upsert targets (provider, model_id).

ALTER TABLE provider_models
  DROP CONSTRAINT IF EXISTS provider_models_pkey;

ALTER TABLE provider_models
  ADD CONSTRAINT provider_models_pkey PRIMARY KEY (provider, model_id);
