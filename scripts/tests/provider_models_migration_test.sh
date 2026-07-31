#!/usr/bin/env bash
set -euo pipefail

: "${PGHOST:?PGHOST must be set}"
: "${PGPORT:=5432}"
: "${PGDATABASE:?PGDATABASE must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
export MIGRATIONS_DIR="${MIGRATIONS_DIR:-$repo_root/backend/migrations}"
psql_args=(-X -v ON_ERROR_STOP=1)

# Recreate the production drift: an older table used a surrogate primary key
# and accumulated duplicate provider/model rows with complementary catalog
# facts. Mark 021 pending so the bundled migration must repair it.
psql "${psql_args[@]}" <<'SQL'
ALTER TABLE provider_models
  DROP CONSTRAINT provider_models_pkey;
ALTER TABLE provider_models
  ADD COLUMN legacy_id BIGSERIAL;
ALTER TABLE provider_models
  ADD CONSTRAINT provider_models_pkey PRIMARY KEY (legacy_id);

INSERT INTO provider_models (
  provider,
  model_id,
  source,
  provider_available,
  metadata,
  first_seen_at,
  last_seen_at
)
VALUES
  (
    'openai-compatible',
    'chatgpt-image-latest',
    'builtin',
    FALSE,
    '{"builtin":true,"winner":"old"}'::jsonb,
    '2026-01-01T00:00:00Z',
    '2026-01-02T00:00:00Z'
  ),
  (
    'openai-compatible',
    'chatgpt-image-latest',
    'provider',
    TRUE,
    '{"provider":true,"winner":"new"}'::jsonb,
    '2026-01-03T00:00:00Z',
    '2026-01-04T00:00:00Z'
  );

DELETE FROM schema_migrations
WHERE version = '021_provider_models_primary_key.sql';
SQL

bash "$repo_root/scripts/migrate.sh"

repair_valid="$(
  psql "${psql_args[@]}" -Atc "
    WITH primary_key_columns AS (
      SELECT string_agg(attribute.attname, ',' ORDER BY key_column.ordinality) AS names
      FROM pg_constraint AS constraint_record
      CROSS JOIN LATERAL unnest(constraint_record.conkey)
        WITH ORDINALITY AS key_column(attnum, ordinality)
      JOIN pg_attribute AS attribute
        ON attribute.attrelid = constraint_record.conrelid
       AND attribute.attnum = key_column.attnum
      WHERE constraint_record.conrelid = 'provider_models'::regclass
        AND constraint_record.contype = 'p'
    )
    SELECT COUNT(*) = 1
       AND BOOL_AND(models.source = 'builtin+provider')
       AND BOOL_AND(models.provider_available)
       AND BOOL_AND(models.metadata @> '{\"builtin\":true,\"provider\":true,\"winner\":\"new\"}'::jsonb)
       AND BOOL_AND(models.first_seen_at = '2026-01-01T00:00:00Z'::timestamptz)
       AND BOOL_AND(models.last_seen_at = '2026-01-04T00:00:00Z'::timestamptz)
       AND BOOL_AND(models.legacy_id IS NOT NULL)
       AND (SELECT names = 'provider,model_id' FROM primary_key_columns)
    FROM provider_models AS models
    WHERE models.provider = 'openai-compatible'
      AND models.model_id = 'chatgpt-image-latest';
  "
)"
if [[ "$repair_valid" != "t" ]]; then
  echo "021 did not merge legacy provider model duplicates correctly" >&2
  exit 1
fi

# The first 021 revision was already published. A database where it succeeded
# must accept the exact historical checksum and advance it to the expanded
# migration checksum without attempting to rerun the body.
psql "${psql_args[@]}" -c "
  UPDATE schema_migrations
  SET checksum = '7fa7f919b164148b15d76158fd8bde5e2e19cefddbab5d8e75af45696e574f6b'
  WHERE version = '021_provider_models_primary_key.sql';
" >/dev/null
bash "$repo_root/scripts/migrate.sh"

read -r bundled_checksum _ < <(
  sha256sum "$MIGRATIONS_DIR/021_provider_models_primary_key.sql"
)
recorded_checksum="$(
  psql "${psql_args[@]}" -Atc "
    SELECT checksum
    FROM schema_migrations
    WHERE version = '021_provider_models_primary_key.sql';
  "
)"
if [[ "$recorded_checksum" != "$bundled_checksum" ]]; then
  echo "021 historical checksum was not advanced to the bundled checksum" >&2
  exit 1
fi

psql "${psql_args[@]}" <<'SQL' >/dev/null
DELETE FROM provider_models
WHERE provider = 'openai-compatible'
  AND model_id = 'chatgpt-image-latest';
ALTER TABLE provider_models DROP COLUMN legacy_id;
SQL

echo "Provider model legacy duplicate migration checks passed."
