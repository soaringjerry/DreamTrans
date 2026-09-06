#!/usr/bin/env bash
# Exercises scripts/backup.sh with a fake docker so no database or bucket is
# needed: the dry run must describe the plan, a real run must dump, encrypt,
# upload both files, prune, and rotate local copies.
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
FIXTURE="$(mktemp -d /tmp/dreamtrans-backup-test.XXXXXX)"
trap 'rm -rf -- "$FIXTURE"' EXIT

export INSTALL_DIR="$FIXTURE/install"
mkdir -p "$INSTALL_DIR"
printf '%s\n' 'POSTGRES_USER=dreamtrans' 'POSTGRES_DB=dreamtrans' 'POSTGRES_PASSWORD=pw' \
    'R2_ACCOUNT_ID=acct' 'R2_ACCESS_KEY_ID=key' 'R2_SECRET_ACCESS_KEY=secret' \
    'R2_BUCKET=bucket' 'BACKUP_PASSPHRASE=sixteen-characters-long' 'BACKUP_RETENTION_DAYS=14' \
    > "$INSTALL_DIR/.env"
printf 'services: {}\n' > "$INSTALL_DIR/docker-compose.yml"

# Fake docker: `compose exec ... db <cmd>` echoes a payload so the pipeline
# produces bytes; `run ... rclone ...` records the rclone invocation.
DOCKER_LOG="$FIXTURE/docker.log"
cat > "$FIXTURE/docker" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$1" in
    compose)
        if [[ "$2" == "version" ]]; then exit 0; fi
        # stdin may carry a tar stream for the config archive; drain it.
        cat >/dev/null
        printf 'encrypted-bytes'
        ;;
    run) exit 0 ;;
esac
MOCK
chmod +x "$FIXTURE/docker"
export DOCKER="$FIXTURE/docker" DOCKER_LOG

# Missing settings are refused before anything runs.
sed -i '/^R2_BUCKET=/d' "$INSTALL_DIR/.env"
if bash "$REPO_ROOT/scripts/backup.sh" --dry-run >"$FIXTURE/missing.log" 2>&1; then
    echo "backup ran without R2_BUCKET" >&2; exit 1
fi
grep -q 'R2_BUCKET is not set' "$FIXTURE/missing.log"
printf 'R2_BUCKET=bucket\n' >> "$INSTALL_DIR/.env"

# Dry run explains the plan and touches nothing.
bash "$REPO_ROOT/scripts/backup.sh" --dry-run > "$FIXTURE/dry.log"
grep -q 'would upload both to r2:bucket/dreamtrans/' "$FIXTURE/dry.log"
grep -q 'older than 14 days' "$FIXTURE/dry.log"
test ! -e "$DOCKER_LOG"

# A real run produces both encrypted artefacts and uploads them.
bash "$REPO_ROOT/scripts/backup.sh" > "$FIXTURE/run.log"
grep -q 'backup complete' "$FIXTURE/run.log"
ls "$INSTALL_DIR"/backups/dreamtrans-*.dump.enc >/dev/null
ls "$INSTALL_DIR"/backups/dreamtrans-*.config.tar.enc >/dev/null
grep -q 'pg_dump -U' "$DOCKER_LOG"
grep -q 'openssl enc -aes-256-cbc' "$DOCKER_LOG"
grep -q 'RCLONE_CONFIG_R2_ENDPOINT=https://acct.r2.cloudflarestorage.com' "$DOCKER_LOG"
grep -q 'copyto /backups/dreamtrans-.*\.dump\.enc r2:bucket/dreamtrans/' "$DOCKER_LOG"
grep -q 'copyto /backups/dreamtrans-.*\.config\.tar\.enc r2:bucket/dreamtrans/' "$DOCKER_LOG"
grep -q 'delete r2:bucket/dreamtrans --min-age 14d' "$DOCKER_LOG"
if grep -q 'sixteen-characters-long' "$DOCKER_LOG"; then
    echo "passphrase leaked into a docker argument" >&2; exit 1
fi

# Local rotation keeps only the newest BACKUP_LOCAL_KEEP dumps.
for i in 1 2 3; do
    touch -d "-$((i * 60)) minutes" "$INSTALL_DIR/backups/dreamtrans-old$i.dump.enc"
done
BACKUP_LOCAL_KEEP=2 bash "$REPO_ROOT/scripts/backup.sh" >/dev/null
count="$(ls "$INSTALL_DIR"/backups/dreamtrans-*.dump.enc | wc -l)"
if [[ "$count" -ne 2 ]]; then
    echo "expected 2 local dumps after rotation, found $count" >&2; exit 1
fi

echo "Backup script checks passed"
