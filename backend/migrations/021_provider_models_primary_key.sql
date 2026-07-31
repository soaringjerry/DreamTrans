-- Some pre-release installations created provider_models with a surrogate or
-- otherwise incompatible primary key. Migration 017 intentionally uses a
-- provider-scoped key, but its CREATE TABLE IF NOT EXISTS could not repair an
-- already-existing table. Those installations can contain more than one row
-- for the same provider/model, so merge the catalog facts before enforcing the
-- intended key.

-- The previous application remains online while the installer runs release
-- migrations. Prevent a concurrent catalog refresh from inserting another
-- duplicate between the merge snapshot and the primary-key replacement.
LOCK TABLE provider_models IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE provider_models_021_merged
ON COMMIT DROP
AS
SELECT
  models.provider,
  models.model_id,
  (ARRAY_AGG(
    models.ctid
    ORDER BY models.last_seen_at DESC NULLS LAST,
             models.first_seen_at DESC NULLS LAST,
             models.ctid DESC
  ))[1] AS keep_ctid,
  CASE
    WHEN BOOL_OR(models.source IN ('builtin', 'builtin+provider'))
     AND BOOL_OR(models.source IN ('provider', 'builtin+provider'))
      THEN 'builtin+provider'
    WHEN BOOL_OR(models.source IN ('provider', 'builtin+provider'))
      THEN 'provider'
    WHEN BOOL_OR(models.source IN ('builtin', 'builtin+provider'))
      THEN 'builtin'
    ELSE COALESCE(MAX(NULLIF(BTRIM(models.source), '')), 'provider')
  END AS source,
  BOOL_OR(COALESCE(models.provider_available, FALSE)) AS provider_available,
  COALESCE((
    SELECT JSONB_OBJECT_AGG(
      metadata_entry.key,
      metadata_entry.value
      ORDER BY candidate.last_seen_at NULLS FIRST,
               candidate.first_seen_at NULLS FIRST,
               candidate.ctid
    )
    FROM provider_models AS candidate
    CROSS JOIN LATERAL JSONB_EACH(
      COALESCE(candidate.metadata, '{}'::jsonb)
    ) AS metadata_entry
    WHERE candidate.provider = models.provider
      AND candidate.model_id = models.model_id
  ), '{}'::jsonb) AS metadata,
  COALESCE(MIN(models.first_seen_at), CURRENT_TIMESTAMP) AS first_seen_at,
  COALESCE(MAX(models.last_seen_at), CURRENT_TIMESTAMP) AS last_seen_at
FROM provider_models AS models
GROUP BY models.provider, models.model_id
HAVING COUNT(*) > 1;

-- Keep the most recently observed physical row so legacy-only columns (for
-- example a surrogate id) are preserved. Merge the canonical catalog fields
-- into it only after the redundant rows have been removed.
DELETE FROM provider_models AS duplicate
USING provider_models_021_merged AS merged
WHERE duplicate.provider = merged.provider
  AND duplicate.model_id = merged.model_id
  AND duplicate.ctid <> merged.keep_ctid;

UPDATE provider_models AS retained
SET source = merged.source,
    provider_available = merged.provider_available,
    metadata = merged.metadata,
    first_seen_at = merged.first_seen_at,
    last_seen_at = merged.last_seen_at
FROM provider_models_021_merged AS merged
WHERE retained.provider = merged.provider
  AND retained.model_id = merged.model_id;

ALTER TABLE provider_models
  DROP CONSTRAINT IF EXISTS provider_models_pkey;

ALTER TABLE provider_models
  ADD CONSTRAINT provider_models_pkey PRIMARY KEY (provider, model_id);
