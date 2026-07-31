#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
MIGRATIONS_DIR="$REPO_ROOT/backend/migrations"
MIGRATION_RUNNER="$REPO_ROOT/scripts/migrate.sh"

expected_latest_prefix="$(
    tr -d '\r' < "$MIGRATION_RUNNER" |
        sed -n 's/^expected_latest_prefix=\([0-9][0-9][0-9]\)$/\1/p'
)"
if [[ ! "$expected_latest_prefix" =~ ^[0-9]{3}$ ]]; then
    echo "scripts/migrate.sh must declare one three-digit expected_latest_prefix" >&2
    exit 1
fi

expected_sequence=1
latest_prefix=""
found_migration="false"
for migration in "$MIGRATIONS_DIR"/[0-9][0-9][0-9]_*.sql; do
    [[ -f "$migration" ]] || continue
    found_migration="true"
    filename="${migration##*/}"
    prefix="${filename%%_*}"
    expected_prefix="$(printf '%03d' "$expected_sequence")"
    if [[ "$prefix" != "$expected_prefix" ]]; then
        echo "Migration sequence is incomplete: expected $expected_prefix, found $prefix" >&2
        exit 1
    fi
    latest_prefix="$prefix"
    expected_sequence=$((expected_sequence + 1))
done

if [[ "$found_migration" != "true" ]]; then
    echo "No migration files found in $MIGRATIONS_DIR" >&2
    exit 1
fi
if [[ "$latest_prefix" != "$expected_latest_prefix" ]]; then
    echo "Migration marker mismatch: scripts/migrate.sh expects $expected_latest_prefix, latest file is $latest_prefix" >&2
    exit 1
fi

printf 'Migration bundle is contiguous through %s.\n' "$latest_prefix"
