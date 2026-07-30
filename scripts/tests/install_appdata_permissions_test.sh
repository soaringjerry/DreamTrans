#!/bin/bash
set -Eeuo pipefail

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 IMAGE" >&2
    exit 2
fi

PERMISSION_TEST_IMAGE="$1"
export PERMISSION_TEST_IMAGE

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
FIXTURE_DIR="$(mktemp -d /tmp/dreamtrans-appdata-permissions.XXXXXX)"
export INSTALL_DIR="$FIXTURE_DIR"
export COMPOSE_PROJECT_NAME="dreamtrans-appdata-permissions-$$"
export COMPOSE_FILE="$FIXTURE_DIR/docker-compose.yml"

cleanup() {
    docker compose down -v --remove-orphans >/dev/null 2>&1 || true
    case "$FIXTURE_DIR" in
        /tmp/dreamtrans-appdata-permissions.*)
            rm -rf -- "$FIXTURE_DIR"
            ;;
    esac
}
trap cleanup EXIT

cat > "$COMPOSE_FILE" <<'YAML'
services:
  pg-probe:
    image: ${PERMISSION_TEST_IMAGE:?}
    pull_policy: never
    network_mode: none
    read_only: true
    volumes:
      - pgdata:/fixture-pgdata
  app:
    image: ${PERMISSION_TEST_IMAGE:?}
    pull_policy: never
    network_mode: none
    read_only: true
    volumes:
      - appdata:/app/data
volumes:
  pgdata:
  appdata:
YAML

# Reuse the production functions without executing install.sh's main entrypoint.
# shellcheck source=/dev/null
source <(sed '$d' "$REPO_ROOT/scripts/install.sh")
COMPOSE_CMD="docker compose"
APP_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$PERMISSION_TEST_IMAGE")"

docker compose config --quiet
printf '%s\n' 'IMAGE_TAG=permission-test' > "$INSTALL_DIR/.env"
docker compose create app >/dev/null
PREVIOUS_APP_CONTAINER_ID="$(docker compose ps -a -q app)"
PREVIOUS_APP_WAS_RUNNING="false"
PREVIOUS_APP_CONTAINER_CREATED_FOR_DISCOVERY="false"
UPDATE_APP_RECREATE_ATTEMPTED="false"
APP_DATA_PERMISSION_MIGRATION_ATTEMPTED="false"
APP_DATA_VOLUME_NAME=""
APP_DATA_PREVIOUS_OWNER=""

docker compose run -T --rm --no-deps --user 0:0 \
    --entrypoint /bin/sh app -ec '
        mkdir -p /app/data/nested
        printf %s legacy-config > /app/data/dreamtrans.config.json
        printf %s legacy-rag > /app/data/nested/rag.db
        chown -R 100:101 /app/data
        chmod 0700 /app/data /app/data/nested
        chmod 0600 \
            /app/data/dreamtrans.config.json \
            /app/data/nested/rag.db
    '
docker compose run -T --rm --no-deps --user 0:0 \
    --entrypoint /bin/sh pg-probe -ec '
        mkdir -p /fixture-pgdata/private
        printf %s pg-sentinel > /fixture-pgdata/private/PG_VERSION
        chown -R 70:70 /fixture-pgdata
        chmod 0710 /fixture-pgdata /fixture-pgdata/private
        chmod 0600 /fixture-pgdata/private/PG_VERSION
    '

snapshot_app() {
    docker compose run -T --rm --no-deps --user 0:0 \
        --entrypoint /bin/sh app -ec '
            stat -c "%u:%g:%a" \
                /app/data \
                /app/data/dreamtrans.config.json \
                /app/data/nested \
                /app/data/nested/rag.db
            sha256sum \
                /app/data/dreamtrans.config.json \
                /app/data/nested/rag.db
        '
}

snapshot_pg() {
    docker compose run -T --rm --no-deps --user 0:0 \
        --entrypoint /bin/sh pg-probe -ec '
            stat -c "%u:%g:%a" \
                /fixture-pgdata \
                /fixture-pgdata/private \
                /fixture-pgdata/private/PG_VERSION
            sha256sum /fixture-pgdata/private/PG_VERSION
        '
}

if docker compose run -T --rm --no-deps --user 10001:10001 \
    --entrypoint /bin/sh app -ec '
        test -r /app/data/dreamtrans.config.json
        touch /app/data/.must-not-succeed
    ' >/dev/null 2>&1; then
    echo "Legacy fixture is unexpectedly accessible to UID 10001" >&2
    exit 1
fi

app_before="$(snapshot_app)"
pg_before="$(snapshot_pg)"
repair_app_data_permissions_for_update
test "$APP_DATA_PREVIOUS_OWNER" = "100:101"
test "$APP_DATA_PERMISSION_MIGRATION_ATTEMPTED" = "true"
test "$pg_before" = "$(snapshot_pg)"

# Re-entering the repair within one update must preserve the original rollback
# owner instead of treating the already-migrated owner as the new baseline.
repair_app_data_permissions_for_update
test "$APP_DATA_PREVIOUS_OWNER" = "100:101"
test "$APP_DATA_PERMISSION_MIGRATION_ATTEMPTED" = "true"
test "$pg_before" = "$(snapshot_pg)"

docker compose run -T --rm --no-deps --user 10001:10001 \
    --entrypoint /bin/sh app -ec '
        test "$(cat /app/data/dreamtrans.config.json)" = legacy-config
        test "$(cat /app/data/nested/rag.db)" = legacy-rag
        test -w /app/data/dreamtrans.config.json
        test -w /app/data/nested/rag.db
        printf %s writable > /app/data/.uid-10001-probe
        rm /app/data/.uid-10001-probe
    '

restore_previous_app_data_permissions
test "$APP_DATA_PERMISSION_MIGRATION_ATTEMPTED" = "false"
test "$app_before" = "$(snapshot_app)"
test "$pg_before" = "$(snapshot_pg)"

# A failed update must restore an app container that was stopped before the
# update as a stopped container using the previous immutable image.
stopped_container_before="$(compose_service_container_id_any_state app)"
begin_update_transaction
test "$PREVIOUS_APP_CONTAINER_ID" = "$stopped_container_before"
test "$PREVIOUS_APP_WAS_RUNNING" = "false"
test "$PREVIOUS_APP_CONTAINER_CREATED_FOR_DISCOVERY" = "false"
previous_stopped_image_id="$PREVIOUS_APP_IMAGE_ID"
repair_app_data_permissions_for_update
test "$APP_DATA_PERMISSION_MIGRATION_ATTEMPTED" = "true"
docker compose up --no-start --no-deps --force-recreate app >/dev/null
replacement_stopped_container="$(compose_service_container_id_any_state app)"
test -n "$replacement_stopped_container"
test "$replacement_stopped_container" != "$stopped_container_before"

rollback_update_deployment
restored_stopped_container="$(compose_service_container_id_any_state app)"
test -n "$restored_stopped_container"
test "$restored_stopped_container" != "$replacement_stopped_container"
test "$(docker inspect --format '{{.Image}}' "$restored_stopped_container")" = \
    "$previous_stopped_image_id"
case "$(docker inspect --format '{{.State.Status}}' "$restored_stopped_container")" in
    running|restarting|paused)
        echo "Previously stopped app was restored in an active state" >&2
        exit 1
        ;;
esac
test "$app_before" = "$(snapshot_app)"
test "$pg_before" = "$(snapshot_pg)"

# The official stop command uses `compose down`, so updates must discover the
# retained named volume through a temporary stopped app container. Rollback
# must remove that container and restore the prior "no app container" state.
docker compose rm -f -s app >/dev/null
test -z "$(compose_service_container_id_any_state app)"
absent_app_before="$(snapshot_app)"
begin_update_transaction
test -z "$PREVIOUS_APP_CONTAINER_ID"
test "$PREVIOUS_APP_CONTAINER_CREATED_FOR_DISCOVERY" = "false"
ensure_app_container_for_update_discovery
test "$PREVIOUS_APP_CONTAINER_CREATED_FOR_DISCOVERY" = "true"
test -n "$PREVIOUS_APP_CONTAINER_ID"
test -n "$(compose_service_container_id_any_state app)"

rollback_update_deployment
test -z "$(compose_service_container_id_any_state app)"
test "$absent_app_before" = "$(snapshot_app)"
test "$pg_before" = "$(snapshot_pg)"

# Mixed ownership cannot be reversed to an exact prior state with one owner
# pair, so the production migration must fail closed without touching pgdata.
docker compose create app >/dev/null
PREVIOUS_APP_CONTAINER_ID="$(compose_service_container_id_any_state app)"
PREVIOUS_APP_WAS_RUNNING="false"
PREVIOUS_APP_CONTAINER_CREATED_FOR_DISCOVERY="false"
docker compose run -T --rm --no-deps --user 0:0 \
    --entrypoint /bin/sh app -ec '
        chown 0:0 /app/data/nested/rag.db
    '
APP_DATA_PERMISSION_MIGRATION_ATTEMPTED="false"
APP_DATA_VOLUME_NAME=""
APP_DATA_PREVIOUS_OWNER=""
if repair_app_data_permissions_for_update; then
    echo "Mixed ownership was unexpectedly accepted" >&2
    exit 1
fi
test "$APP_DATA_PERMISSION_MIGRATION_ATTEMPTED" = "false"
test "$pg_before" = "$(snapshot_pg)"

echo "Installer appdata permission migration checks passed"
