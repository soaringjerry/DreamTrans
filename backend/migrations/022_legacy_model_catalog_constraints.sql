-- Repair pre-release model catalog tables that existed before migration 017.
-- CREATE TABLE IF NOT EXISTS preserved those installations' surrogate primary
-- keys, but it could not add the composite arbiters used by the application's
-- ON CONFLICT clauses. Merge duplicate logical rows before adding the missing
-- unique indexes. Legacy primary keys and columns are deliberately retained so
-- unknown external references are not invalidated.

-- Serialize the complete repair with the still-running previous application.
-- Without the table locks, a legacy writer could insert another duplicate
-- between the merge snapshot and CREATE UNIQUE INDEX.
LOCK TABLE provider_cost_rates IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE provider_cost_rates_022_merged
ON COMMIT DROP
AS
SELECT
  rates.provider,
  rates.sku,
  rates.service,
  rates.unit_type,
  (ARRAY_AGG(
    rates.ctid
    ORDER BY rates.updated_at DESC NULLS LAST,
             rates.effective_at DESC NULLS LAST,
             rates.created_at DESC NULLS LAST,
             rates.ctid DESC
  ))[1] AS keep_ctid,
  MIN(rates.created_at) AS first_created_at
FROM provider_cost_rates AS rates
GROUP BY rates.provider, rates.sku, rates.service, rates.unit_type
HAVING COUNT(*) > 1;

DELETE FROM provider_cost_rates AS duplicate
USING provider_cost_rates_022_merged AS merged
WHERE duplicate.provider IS NOT DISTINCT FROM merged.provider
  AND duplicate.sku IS NOT DISTINCT FROM merged.sku
  AND duplicate.service IS NOT DISTINCT FROM merged.service
  AND duplicate.unit_type IS NOT DISTINCT FROM merged.unit_type
  AND duplicate.ctid <> merged.keep_ctid;

-- Preserve the first-seen timestamp while retaining every other value from the
-- most recently updated physical row. In particular, do not OR is_active or
-- is_builtin: doing so could revive a deliberately disabled/custom rate.
UPDATE provider_cost_rates AS retained
SET created_at = COALESCE(merged.first_created_at, retained.created_at)
FROM provider_cost_rates_022_merged AS merged
WHERE retained.ctid = merged.keep_ctid;

DO $provider_cost_rates_identity$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_index AS index_record
    WHERE index_record.indrelid = 'provider_cost_rates'::regclass
      AND index_record.indisunique
      AND index_record.indisvalid
      AND index_record.indisready
      AND index_record.indpred IS NULL
      AND index_record.indexprs IS NULL
      AND index_record.indnkeyatts = 4
      AND (
        SELECT ARRAY_AGG(attribute.attname::TEXT ORDER BY key_column.ordinality)
        FROM UNNEST(index_record.indkey::SMALLINT[])
          WITH ORDINALITY AS key_column(attnum, ordinality)
        JOIN pg_attribute AS attribute
          ON attribute.attrelid = index_record.indrelid
         AND attribute.attnum = key_column.attnum
        WHERE key_column.ordinality <= index_record.indnkeyatts
      ) = ARRAY['provider', 'sku', 'service', 'unit_type']::TEXT[]
  ) THEN
    CREATE UNIQUE INDEX idx_provider_cost_rates_022_identity
      ON provider_cost_rates(provider, sku, service, unit_type);
  END IF;
END
$provider_cost_rates_identity$;

LOCK TABLE model_policies IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE model_policies_022_merged
ON COMMIT DROP
AS
SELECT
  policies.purpose,
  policies.model_id,
  (ARRAY_AGG(
    policies.ctid
    ORDER BY policies.updated_at DESC NULLS LAST,
             policies.ctid DESC
  ))[1] AS keep_ctid
FROM model_policies AS policies
GROUP BY policies.purpose, policies.model_id
HAVING COUNT(*) > 1;

-- Approval/default flags are administrator policy, so the newest complete row
-- wins. Combining booleans from older rows could silently resurrect a revoked
-- model.
DELETE FROM model_policies AS duplicate
USING model_policies_022_merged AS merged
WHERE duplicate.purpose IS NOT DISTINCT FROM merged.purpose
  AND duplicate.model_id IS NOT DISTINCT FROM merged.model_id
  AND duplicate.ctid <> merged.keep_ctid;

-- A legacy table without the partial unique index may contain several approved
-- defaults for one purpose. Keep the latest one and retain all other policies
-- as approved non-default choices.
WITH ranked_defaults AS (
  SELECT
    policies.ctid,
    ROW_NUMBER() OVER (
      PARTITION BY policies.purpose
      ORDER BY policies.updated_at DESC NULLS LAST,
               policies.model_id,
               policies.ctid DESC
    ) AS default_rank
  FROM model_policies AS policies
  WHERE policies.is_default = TRUE
    AND policies.is_approved = TRUE
)
UPDATE model_policies AS policies
SET is_default = FALSE
FROM ranked_defaults
WHERE policies.ctid = ranked_defaults.ctid
  AND ranked_defaults.default_rank > 1;

DO $model_policies_identity$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_index AS index_record
    WHERE index_record.indrelid = 'model_policies'::regclass
      AND index_record.indisunique
      AND index_record.indisvalid
      AND index_record.indisready
      AND index_record.indpred IS NULL
      AND index_record.indexprs IS NULL
      AND index_record.indnkeyatts = 2
      AND (
        SELECT ARRAY_AGG(attribute.attname::TEXT ORDER BY key_column.ordinality)
        FROM UNNEST(index_record.indkey::SMALLINT[])
          WITH ORDINALITY AS key_column(attnum, ordinality)
        JOIN pg_attribute AS attribute
          ON attribute.attrelid = index_record.indrelid
         AND attribute.attnum = key_column.attnum
        WHERE key_column.ordinality <= index_record.indnkeyatts
      ) = ARRAY['purpose', 'model_id']::TEXT[]
  ) THEN
    CREATE UNIQUE INDEX idx_model_policies_022_identity
      ON model_policies(purpose, model_id);
  END IF;
END
$model_policies_identity$;

-- Use a migration-owned name when the invariant is absent rather than relying
-- on 017's historical index name. Some legacy databases already have an
-- unrelated/non-unique index with that name. Fresh databases retain 017's
-- valid index without adding a redundant copy.
DO $model_policies_one_default$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_index AS index_record
    WHERE index_record.indrelid = 'model_policies'::regclass
      AND index_record.indisunique
      AND index_record.indisvalid
      AND index_record.indisready
      AND index_record.indexprs IS NULL
      AND index_record.indnkeyatts = 1
      AND (
        SELECT ARRAY_AGG(attribute.attname::TEXT ORDER BY key_column.ordinality)
        FROM UNNEST(index_record.indkey::SMALLINT[])
          WITH ORDINALITY AS key_column(attnum, ordinality)
        JOIN pg_attribute AS attribute
          ON attribute.attrelid = index_record.indrelid
         AND attribute.attnum = key_column.attnum
        WHERE key_column.ordinality <= index_record.indnkeyatts
      ) = ARRAY['purpose']::TEXT[]
      AND PG_GET_EXPR(index_record.indpred, index_record.indrelid) =
        '((is_default = true) AND (is_approved = true))'
  ) THEN
    CREATE UNIQUE INDEX idx_model_policies_022_one_default
      ON model_policies(purpose)
      WHERE is_default = TRUE AND is_approved = TRUE;
  END IF;
END
$model_policies_one_default$;

LOCK TABLE user_model_preferences IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE user_model_preferences_022_merged
ON COMMIT DROP
AS
SELECT
  preferences.user_id,
  (ARRAY_AGG(
    preferences.ctid
    ORDER BY preferences.updated_at DESC NULLS LAST,
             preferences.ctid DESC
  ))[1] AS keep_ctid
FROM user_model_preferences AS preferences
GROUP BY preferences.user_id
HAVING COUNT(*) > 1;

-- SavePreferences writes all three selections as one snapshot. Retaining the
-- newest row therefore preserves explicit NULLs ("use the default") instead
-- of reviving an older preference by merging fields independently.
DELETE FROM user_model_preferences AS duplicate
USING user_model_preferences_022_merged AS merged
WHERE duplicate.user_id IS NOT DISTINCT FROM merged.user_id
  AND duplicate.ctid <> merged.keep_ctid;

DO $user_model_preferences_identity$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_index AS index_record
    WHERE index_record.indrelid = 'user_model_preferences'::regclass
      AND index_record.indisunique
      AND index_record.indisvalid
      AND index_record.indisready
      AND index_record.indpred IS NULL
      AND index_record.indexprs IS NULL
      AND index_record.indnkeyatts = 1
      AND (
        SELECT ARRAY_AGG(attribute.attname::TEXT ORDER BY key_column.ordinality)
        FROM UNNEST(index_record.indkey::SMALLINT[])
          WITH ORDINALITY AS key_column(attnum, ordinality)
        JOIN pg_attribute AS attribute
          ON attribute.attrelid = index_record.indrelid
         AND attribute.attnum = key_column.attnum
        WHERE key_column.ordinality <= index_record.indnkeyatts
      ) = ARRAY['user_id']::TEXT[]
  ) THEN
    CREATE UNIQUE INDEX idx_user_model_preferences_022_user
      ON user_model_preferences(user_id);
  END IF;
END
$user_model_preferences_identity$;
