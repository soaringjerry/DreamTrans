#!/usr/bin/env bash
set -Eeuo pipefail

: "${PGHOST:?PGHOST must be set}"
: "${PGPORT:=5432}"
: "${PGDATABASE:?PGDATABASE must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
migration="$repo_root/backend/migrations/022_legacy_model_catalog_constraints.sql"
test_schema="dreamtrans_migration_022_test"
psql_args=(-X -v ON_ERROR_STOP=1)

cleanup() {
  psql "${psql_args[@]}" -c \
    "DROP SCHEMA IF EXISTS $test_schema CASCADE;" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

# Recreate the three pre-release shapes: a surrogate primary key, no logical
# unique arbiter, duplicate logical rows, and (for policies) an unrelated index
# occupying migration 017's historical partial-index name.
psql "${psql_args[@]}" <<SQL >/dev/null
CREATE SCHEMA $test_schema;
SET search_path TO $test_schema, public;

CREATE TABLE provider_cost_rates (
  legacy_id BIGSERIAL PRIMARY KEY,
  id UUID NOT NULL,
  provider VARCHAR(60) NOT NULL,
  sku VARCHAR(200) NOT NULL,
  service VARCHAR(50) NOT NULL,
  unit_type VARCHAR(30) NOT NULL,
  cost_per_unit_usd DECIMAL(24,12) NOT NULL,
  catalog_version VARCHAR(40) NOT NULL,
  source_url TEXT NOT NULL DEFAULT '',
  effective_at TIMESTAMP WITH TIME ZONE,
  is_builtin BOOLEAN NOT NULL DEFAULT TRUE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

INSERT INTO provider_cost_rates (
  id, provider, sku, service, unit_type, cost_per_unit_usd,
  catalog_version, source_url, effective_at, is_builtin, is_active,
  created_at, updated_at
)
VALUES
  (
    '00000000-0000-0000-0000-000000000101',
    'legacy-provider', 'legacy-summary', 'llm', 'input_token', 0.10,
    'old', 'https://old.example/rate', '2026-01-01T00:00:00Z', TRUE, TRUE,
    '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z'
  ),
  (
    '00000000-0000-0000-0000-000000000102',
    'legacy-provider', 'legacy-summary', 'llm', 'input_token', 0.20,
    'new', 'https://new.example/rate', '2026-02-01T00:00:00Z', FALSE, FALSE,
    '2026-02-01T00:00:00Z', '2026-02-02T00:00:00Z'
  ),
  (
    '00000000-0000-0000-0000-000000000103',
    'legacy-provider', 'unrelated-model', 'llm', 'output_token', 0.30,
    'keep', 'https://keep.example/rate', '2026-03-01T00:00:00Z', FALSE, TRUE,
    '2026-03-01T00:00:00Z', '2026-03-02T00:00:00Z'
  );

CREATE TABLE model_policies (
  legacy_id BIGSERIAL PRIMARY KEY,
  purpose VARCHAR(30) NOT NULL,
  model_id VARCHAR(200) NOT NULL,
  is_approved BOOLEAN NOT NULL DEFAULT FALSE,
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  cost_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_by UUID
);

-- This is deliberately the wrong definition. 017's IF NOT EXISTS would leave
-- it in place, so 022 must install an independently named correct invariant.
CREATE INDEX idx_model_policy_one_default ON model_policies(model_id);

INSERT INTO model_policies (
  purpose, model_id, is_approved, is_default, cost_confirmed, updated_at
)
VALUES
  ('summary', 'legacy-summary', TRUE, TRUE, TRUE, '2026-01-01T00:00:00Z'),
  ('summary', 'legacy-summary', FALSE, FALSE, FALSE, '2026-02-01T00:00:00Z'),
  ('chat', 'legacy-chat-a', TRUE, TRUE, TRUE, '2026-03-01T00:00:00Z'),
  ('chat', 'legacy-chat-b', TRUE, TRUE, TRUE, '2026-04-01T00:00:00Z'),
  ('translation', 'unrelated-model', TRUE, FALSE, TRUE, '2026-05-01T00:00:00Z');

CREATE TABLE user_model_preferences (
  legacy_id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL,
  translation_model VARCHAR(200),
  summary_model VARCHAR(200),
  chat_model VARCHAR(200),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

INSERT INTO user_model_preferences (
  user_id, translation_model, summary_model, chat_model, updated_at
)
VALUES
  (
    '00000000-0000-0000-0000-000000000201',
    'old-translation', 'old-summary', 'old-chat', '2026-01-01T00:00:00Z'
  ),
  (
    '00000000-0000-0000-0000-000000000201',
    'new-translation', NULL, 'new-chat', '2026-02-01T00:00:00Z'
  ),
  (
    '00000000-0000-0000-0000-000000000202',
    'keep-translation', 'keep-summary', 'keep-chat', '2026-03-01T00:00:00Z'
  );
SQL

apply_migration() {
  {
    printf 'SET search_path TO %s, public;\nBEGIN;\n' "$test_schema"
    cat "$migration"
    printf '\nCOMMIT;\n'
  } | psql "${psql_args[@]}" >/dev/null
}

# The body itself remains safe if an operator reruns it manually or a migration
# record has to be reconstructed after recovery.
apply_migration
apply_migration

psql "${psql_args[@]}" <<SQL >/dev/null
SET search_path TO $test_schema, public;

DO \$verify\$
DECLARE
  table_name TEXT;
  primary_key_columns TEXT;
  duplicate_count BIGINT;
  default_count BIGINT;
  retained_rate provider_cost_rates%ROWTYPE;
  retained_policy model_policies%ROWTYPE;
  retained_preferences user_model_preferences%ROWTYPE;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'provider_cost_rates', 'model_policies', 'user_model_preferences'
  ] LOOP
    SELECT STRING_AGG(attribute.attname, ',' ORDER BY key_column.ordinality)
      INTO primary_key_columns
    FROM pg_constraint AS constraint_record
    CROSS JOIN LATERAL UNNEST(constraint_record.conkey)
      WITH ORDINALITY AS key_column(attnum, ordinality)
    JOIN pg_attribute AS attribute
      ON attribute.attrelid = constraint_record.conrelid
     AND attribute.attnum = key_column.attnum
    WHERE constraint_record.conrelid = table_name::regclass
      AND constraint_record.contype = 'p';

    IF primary_key_columns IS DISTINCT FROM 'legacy_id' THEN
      RAISE EXCEPTION '% primary key changed to %', table_name, primary_key_columns;
    END IF;
  END LOOP;

  SELECT COUNT(*) INTO duplicate_count
  FROM provider_cost_rates
  WHERE provider = 'legacy-provider'
    AND sku = 'legacy-summary'
    AND service = 'llm'
    AND unit_type = 'input_token';
  IF duplicate_count <> 1 THEN
    RAISE EXCEPTION 'provider rate duplicate count is %', duplicate_count;
  END IF;

  SELECT * INTO retained_rate
  FROM provider_cost_rates
  WHERE provider = 'legacy-provider'
    AND sku = 'legacy-summary'
    AND service = 'llm'
    AND unit_type = 'input_token';
  IF retained_rate.legacy_id <> 2
     OR retained_rate.cost_per_unit_usd <> 0.20
     OR retained_rate.catalog_version <> 'new'
     OR retained_rate.source_url <> 'https://new.example/rate'
     OR retained_rate.is_builtin <> FALSE
     OR retained_rate.is_active <> FALSE
     OR retained_rate.created_at <> '2026-01-01T00:00:00Z'::timestamptz THEN
    RAISE EXCEPTION 'provider rate winner was not preserved: %', ROW_TO_JSON(retained_rate);
  END IF;
  IF (SELECT COUNT(*) FROM provider_cost_rates) <> 2 THEN
    RAISE EXCEPTION 'unrelated provider cost row was lost';
  END IF;

  SELECT COUNT(*) INTO duplicate_count
  FROM model_policies
  WHERE purpose = 'summary' AND model_id = 'legacy-summary';
  IF duplicate_count <> 1 THEN
    RAISE EXCEPTION 'model policy duplicate count is %', duplicate_count;
  END IF;
  SELECT * INTO retained_policy
  FROM model_policies
  WHERE purpose = 'summary' AND model_id = 'legacy-summary';
  IF retained_policy.legacy_id <> 2
     OR retained_policy.is_approved <> FALSE
     OR retained_policy.is_default <> FALSE
     OR retained_policy.cost_confirmed <> FALSE THEN
    RAISE EXCEPTION 'latest policy intent was not preserved: %', ROW_TO_JSON(retained_policy);
  END IF;

  SELECT COUNT(*) INTO default_count
  FROM model_policies
  WHERE purpose = 'chat' AND is_approved = TRUE AND is_default = TRUE;
  IF default_count <> 1 OR NOT EXISTS (
    SELECT 1 FROM model_policies
    WHERE purpose = 'chat' AND model_id = 'legacy-chat-b' AND is_default = TRUE
  ) THEN
    RAISE EXCEPTION 'legacy default policies were not normalized';
  END IF;
  IF (SELECT COUNT(*) FROM model_policies) <> 4 THEN
    RAISE EXCEPTION 'unrelated model policy was lost';
  END IF;

  SELECT COUNT(*) INTO duplicate_count
  FROM user_model_preferences
  WHERE user_id = '00000000-0000-0000-0000-000000000201';
  IF duplicate_count <> 1 THEN
    RAISE EXCEPTION 'user preference duplicate count is %', duplicate_count;
  END IF;
  SELECT * INTO retained_preferences
  FROM user_model_preferences
  WHERE user_id = '00000000-0000-0000-0000-000000000201';
  IF retained_preferences.legacy_id <> 2
     OR retained_preferences.translation_model <> 'new-translation'
     OR retained_preferences.summary_model IS NOT NULL
     OR retained_preferences.chat_model <> 'new-chat' THEN
    RAISE EXCEPTION 'latest preference snapshot was not preserved: %',
      ROW_TO_JSON(retained_preferences);
  END IF;
  IF (SELECT COUNT(*) FROM user_model_preferences) <> 2 THEN
    RAISE EXCEPTION 'unrelated user preference was lost';
  END IF;
END
\$verify\$;

-- Exercise the exact conflict targets used by production code. Successful
-- inference proves that a usable non-partial unique arbiter now exists.
INSERT INTO provider_cost_rates (
  id, provider, sku, service, unit_type, cost_per_unit_usd, catalog_version
) VALUES (
  '00000000-0000-0000-0000-000000000104',
  'legacy-provider', 'legacy-summary', 'llm', 'input_token', 0.40, 'upsert'
)
ON CONFLICT (provider, sku, service, unit_type) DO UPDATE
SET cost_per_unit_usd = EXCLUDED.cost_per_unit_usd;

INSERT INTO model_policies (
  purpose, model_id, is_approved, is_default, cost_confirmed
) VALUES ('summary', 'legacy-summary', TRUE, FALSE, TRUE)
ON CONFLICT (purpose, model_id) DO UPDATE
SET is_approved = EXCLUDED.is_approved;

INSERT INTO user_model_preferences (
  user_id, translation_model, summary_model, chat_model
) VALUES (
  '00000000-0000-0000-0000-000000000201',
  'upsert-translation', 'upsert-summary', 'upsert-chat'
)
ON CONFLICT (user_id) DO UPDATE
SET summary_model = EXCLUDED.summary_model;

DO \$one_default\$
BEGIN
  BEGIN
    INSERT INTO model_policies (
      purpose, model_id, is_approved, is_default, cost_confirmed
    ) VALUES ('chat', 'forbidden-second-default', TRUE, TRUE, TRUE);
    RAISE EXCEPTION 'partial one-default unique index did not reject a conflict';
  EXCEPTION
    WHEN unique_violation THEN NULL;
  END;
END
\$one_default\$;
SQL

echo "Legacy model catalog constraint migration checks passed."
