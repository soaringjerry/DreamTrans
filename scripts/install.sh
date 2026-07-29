#!/bin/bash
set -Ee
umask 077

# ============================================================================
# DreamTrans One-Click Installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash
#
# Or with options:
#   curl -fsSL ... | bash -s -- --port 8080
# ============================================================================

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Default values
INSTALL_DIR="${INSTALL_DIR:-$HOME/dreamtrans}"
PORT="${PORT:-16002}"
BIND_ADDRESS="${BIND_ADDRESS:-127.0.0.1}"
RAG_MAX_DB_MB="${RAG_MAX_DB_MB:-102400}"
if [[ -n "${IMAGE_TAG:-}" ]]; then
    IMAGE_TAG_EXPLICIT="true"
else
    IMAGE_TAG="latest"
    IMAGE_TAG_EXPLICIT="false"
fi
INSTALL_SENTINEL=".dreamtrans-install"
INSTALL_SENTINEL_VERSION="dreamtrans-install-v1"

# Print banner
print_banner() {
    echo -e "${CYAN}"
    echo "╔═══════════════════════════════════════════════════════════════╗"
    echo "║                                                               ║"
    echo "║       ██████╗ ██████╗ ███████╗ █████╗ ███╗   ███╗            ║"
    echo "║       ██╔══██╗██╔══██╗██╔════╝██╔══██╗████╗ ████║            ║"
    echo "║       ██║  ██║██████╔╝█████╗  ███████║██╔████╔██║            ║"
    echo "║       ██║  ██║██╔══██╗██╔══╝  ██╔══██║██║╚██╔╝██║            ║"
    echo "║       ██████╔╝██║  ██║███████╗██║  ██║██║ ╚═╝ ██║            ║"
    echo "║       ╚═════╝ ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝     ╚═╝            ║"
    echo "║                      TRANS                                    ║"
    echo "║                                                               ║"
    echo "║          Real-time Transcription & Translation                ║"
    echo "║                                                               ║"
    echo "╚═══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

# Print info message
info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

# Print success message
success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# Print warning message
warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# Print error message
error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Read from terminal (works with curl pipe)
read_input() {
    local prompt="$1"
    local var_name="$2"
    local default="$3"
    local secret="$4"

    if [[ "$secret" == "true" ]]; then
        echo -ne "$prompt" >/dev/tty
        read -rs "$var_name" </dev/tty
        echo "" >/dev/tty
    else
        echo -ne "$prompt" >/dev/tty
        read -r "$var_name" </dev/tty
    fi

    # Use default if empty
    if [[ -z "${!var_name}" && -n "$default" ]]; then
        printf -v "$var_name" '%s' "$default"
    fi
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

normalize_install_dir() {
    if ! command_exists realpath; then
        error "realpath is required to validate --dir safely"
        return 1
    fi

    if [[ -z "$INSTALL_DIR" || "$INSTALL_DIR" == *$'\n'* || "$INSTALL_DIR" == *$'\r'* ]]; then
        error "Installation directory is empty or contains a newline"
        return 1
    fi

    local normalized_dir
    local normalized_home
    normalized_dir="$(realpath -m -- "$INSTALL_DIR")" || {
        error "Unable to normalize installation directory: $INSTALL_DIR"
        return 1
    }
    normalized_home="$(realpath -m -- "${HOME:?HOME must be set}")"

    if [[ -z "$normalized_dir" || "$normalized_dir" != /* ||
          "$normalized_dir" == "/" || "$normalized_dir" == "$normalized_home" ||
          "$normalized_dir" == *$'\n'* || "$normalized_dir" == *$'\r'* ]]; then
        error "Unsafe installation directory: $normalized_dir"
        echo "  --dir must resolve to a dedicated absolute directory, never / or HOME."
        return 1
    fi

    INSTALL_DIR="$normalized_dir"
}

validate_deployment_options() {
    if [[ ! "$PORT" =~ ^[0-9]+$ || ${#PORT} -gt 5 ]] ||
       ((10#$PORT < 1 || 10#$PORT > 65535)); then
        error "PORT must be an integer between 1 and 65535"
        return 1
    fi
    PORT="$((10#$PORT))"
    if [[ ! "$IMAGE_TAG" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
        error "IMAGE_TAG is not a valid OCI tag"
        return 1
    fi
    if [[ ! "$BIND_ADDRESS" =~ ^([A-Za-z0-9][A-Za-z0-9._-]*|\[[0-9A-Fa-f:]+\])$ ]]; then
        error "BIND_ADDRESS must be an IPv4 address, hostname, or bracketed IPv6 address"
        return 1
    fi
    if [[ ! "$RAG_MAX_DB_MB" =~ ^(-1|[0-9]+)$ ]]; then
        error "RAG_MAX_DB_MB must be a non-negative MiB value, or -1 to disable the limit"
        return 1
    fi
}

write_install_sentinel() {
    local sentinel_path="$INSTALL_DIR/$INSTALL_SENTINEL"
    local temporary_path
    temporary_path="$(mktemp "$INSTALL_DIR/.dreamtrans-install.tmp.XXXXXX")"
    printf '%s\npath=%s\n' "$INSTALL_SENTINEL_VERSION" "$INSTALL_DIR" > "$temporary_path"
    chmod 600 "$temporary_path"
    mv -f -- "$temporary_path" "$sentinel_path"
}

has_valid_install_sentinel() {
    local sentinel_path="$INSTALL_DIR/$INSTALL_SENTINEL"
    [[ -f "$sentinel_path" && ! -L "$sentinel_path" && -O "$sentinel_path" ]] || return 1
    [[ "$(sed -n '1p' "$sentinel_path")" == "$INSTALL_SENTINEL_VERSION" ]] || return 1
    [[ "$(sed -n '2p' "$sentinel_path")" == "path=$INSTALL_DIR" ]]
}

looks_like_legacy_installation() {
    local env_file="$INSTALL_DIR/.env"
    local compose_file="$INSTALL_DIR/docker-compose.yml"

    [[ -d "$INSTALL_DIR" && ! -L "$INSTALL_DIR" && -O "$INSTALL_DIR" &&
       -f "$env_file" && ! -L "$env_file" && -O "$env_file" &&
       -f "$compose_file" && ! -L "$compose_file" && -O "$compose_file" ]] || return 1
    grep -q '^# Generated by install\.sh$' "$compose_file" || return 1
    grep -q '^  db:$' "$compose_file" || return 1
    grep -q '^  app:$' "$compose_file" || return 1
    grep -Eq '^[[:space:]]*image:[[:space:]]*ghcr\.io/soaringjerry/dreamtrans:' \
        "$compose_file" || return 1
    (cd "$INSTALL_DIR" && $COMPOSE_CMD config --quiet >/dev/null 2>&1)
}

validate_fresh_install_target() {
    [[ -e "$INSTALL_DIR" ]] || return 0
    if [[ ! -d "$INSTALL_DIR" || -L "$INSTALL_DIR" || ! -O "$INSTALL_DIR" ]]; then
        error "Installation target must be a directory owned by the current user: $INSTALL_DIR"
        return 1
    fi
    if find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
        if has_valid_install_sentinel; then
            error "DreamTrans is already installed at $INSTALL_DIR"
            echo "  Use --update to preserve its database and credentials, or --uninstall first."
        else
            error "Refusing to install into a non-empty directory without a valid DreamTrans marker:"
            echo "  $INSTALL_DIR"
        fi
        return 1
    fi
}

require_installation() {
    local allow_legacy="${1:-false}"
    if [[ ! -d "$INSTALL_DIR" || -L "$INSTALL_DIR" || ! -O "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        return 1
    fi
    if has_valid_install_sentinel; then
        if [[ ! -f "$INSTALL_DIR/.env" || -L "$INSTALL_DIR/.env" ||
              ! -O "$INSTALL_DIR/.env" ||
              ! -f "$INSTALL_DIR/docker-compose.yml" ||
              -L "$INSTALL_DIR/docker-compose.yml" ||
              ! -O "$INSTALL_DIR/docker-compose.yml" ]]; then
            error "DreamTrans installation files are missing, symlinked, or owned by another user"
            return 1
        fi
        return 0
    fi
    if [[ "$allow_legacy" == "true" ]] && looks_like_legacy_installation; then
        warn "Adopting a verified legacy DreamTrans installation and adding its safety marker"
        write_install_sentinel
        return 0
    fi
    error "Valid DreamTrans installation marker not found at $INSTALL_DIR/$INSTALL_SENTINEL"
    echo "  Refusing to manage this directory. Run --update once to adopt a verified legacy installation."
    return 1
}

# Check prerequisites
check_prerequisites() {
    info "Checking prerequisites..."

    local required_command
    for required_command in docker realpath; do
        if ! command_exists "$required_command"; then
            error "$required_command is required"
            exit 1
        fi
    done

    # Check if docker compose is available (v2 or v1)
    if docker compose version >/dev/null 2>&1; then
        COMPOSE_CMD="docker compose"
    elif command_exists docker-compose; then
        COMPOSE_CMD="docker-compose"
    else
        error "Docker Compose is not installed. Please install Docker Compose."
        echo "  Visit: https://docs.docker.com/compose/install/"
        exit 1
    fi

    success "Prerequisites check passed"
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --port)
                [[ $# -ge 2 && -n "$2" ]] || { error "--port requires a value"; exit 1; }
                PORT="$2"
                shift 2
                ;;
            --dir)
                [[ $# -ge 2 && -n "$2" ]] || { error "--dir requires a value"; exit 1; }
                INSTALL_DIR="$2"
                shift 2
                ;;
            --tag)
                [[ $# -ge 2 && -n "$2" ]] || { error "--tag requires a value"; exit 1; }
                IMAGE_TAG="$2"
                IMAGE_TAG_EXPLICIT="true"
                shift 2
                ;;
            --sm-key)
                [[ $# -ge 2 && -n "$2" ]] || { error "--sm-key requires a value"; exit 1; }
                SM_API_KEY="$2"
                shift 2
                ;;
            --openai-key)
                [[ $# -ge 2 ]] || { error "--openai-key requires a value"; exit 1; }
                OPENAI_API_KEY="$2"
                shift 2
                ;;
            --admin-email)
                [[ $# -ge 2 && -n "$2" ]] || { error "--admin-email requires a value"; exit 1; }
                ADMIN_EMAIL="$2"
                shift 2
                ;;
            --admin-password)
                [[ $# -ge 2 && -n "$2" ]] || { error "--admin-password requires a value"; exit 1; }
                ADMIN_PASSWORD="$2"
                shift 2
                ;;
            --update)
                UPDATE_MODE="true"
                shift
                ;;
            --stop)
                STOP_MODE="true"
                shift
                ;;
            --start)
                START_MODE="true"
                shift
                ;;
            --restart)
                RESTART_MODE="true"
                shift
                ;;
            --status)
                STATUS_MODE="true"
                shift
                ;;
            --logs)
                LOGS_MODE="true"
                shift
                ;;
            --init-db)
                INIT_DB_MODE="true"
                shift
                ;;
            --migrate)
                MIGRATE_MODE="true"
                shift
                ;;
            --uninstall)
                UNINSTALL_MODE="true"
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            *)
                error "Unknown option: $1"
                echo "  Run with --help to see supported commands."
                exit 1
                ;;
        esac
    done
}

validate_command_selection() {
    local selected=0
    local mode
    for mode in \
        "${UPDATE_MODE:-false}" "${STOP_MODE:-false}" "${START_MODE:-false}" \
        "${RESTART_MODE:-false}" "${STATUS_MODE:-false}" "${LOGS_MODE:-false}" \
        "${INIT_DB_MODE:-false}" "${MIGRATE_MODE:-false}" "${UNINSTALL_MODE:-false}"; do
        [[ "$mode" == "true" ]] && selected=$((selected + 1))
    done
    if ((selected > 1)); then
        error "Choose only one command mode per invocation"
        return 1
    fi
}

# Show help
show_help() {
    echo "DreamTrans Installer & Manager"
    echo ""
    echo "Usage:"
    echo "  curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash"
    echo "  curl -fsSL ... | bash -s -- [COMMAND] [OPTIONS]"
    echo ""
    echo "Commands:"
    echo "  (default)         Install DreamTrans (interactive)"
    echo "  --update          Pull latest image, run migrations, and restart"
    echo "  --stop            Stop services"
    echo "  --start           Start services"
    echo "  --restart         Restart services"
    echo "  --status          Show service status"
    echo "  --logs            Show logs (follow mode)"
    echo "  --init-db         Initialize database schema"
    echo "  --migrate         Run database migrations only"
    echo "  --uninstall       Remove DreamTrans and all data"
    echo ""
    echo "Options:"
    echo "  --port PORT       Set the port (default: 16002)"
    echo "  --dir DIR         Set installation directory (default: ~/dreamtrans)"
    echo "  --tag TAG         Docker image tag (default: latest)"
    echo "  --sm-key KEY      Speechmatics API key (skip prompt)"
    echo "  --openai-key KEY  OpenAI API key (skip prompt)"
    echo "  --admin-email     Bootstrap administrator email"
    echo "  --admin-password  Bootstrap password (environment variable is safer)"
    echo "  -h, --help        Show this help message"
    echo ""
    echo "Security:"
    echo "  Values passed with --sm-key, --openai-key, or --admin-password may be"
    echo "  recorded in shell history. Interactive secret prompts are safer."
    echo ""
    echo "Examples:"
    echo "  # Install"
    echo "  curl -fsSL ... | bash"
    echo ""
    echo "  # Install with custom port"
    echo "  curl -fsSL ... | bash -s -- --port 8080"
    echo ""
    echo "  # Non-interactive install"
    echo "  curl -fsSL ... | bash -s -- --sm-key YOUR_KEY --openai-key YOUR_KEY \\"
    echo "    --admin-email you@example.com --admin-password A_STRONG_PASSWORD"
    echo ""
    echo "  # Update"
    echo "  curl -fsSL ... | bash -s -- --update"
    echo ""
    echo "  # Stop/Start/Restart"
    echo "  curl -fsSL ... | bash -s -- --stop"
    echo "  curl -fsSL ... | bash -s -- --start"
    echo "  curl -fsSL ... | bash -s -- --restart"
    echo ""
    echo "  # View logs"
    echo "  curl -fsSL ... | bash -s -- --logs"
}

# Generate random string
random_string() {
    local length=${1:-32}
    local byte_count=$(((length + 1) / 2))
    local random_hex
    random_hex="$(od -An -N "$byte_count" -tx1 /dev/urandom)"
    random_hex="${random_hex//[[:space:]]/}"
    printf '%s' "${random_hex:0:length}"
}

prompt_admin_credentials() {
    if [[ -z "$ADMIN_EMAIL" ]]; then
        echo "" >/dev/tty
        echo -e "${CYAN}Administrator email${NC} (required for the admin console):" >/dev/tty
        read_input "  ADMIN_EMAIL: " ADMIN_EMAIL "" false
    fi
    if [[ ! "$ADMIN_EMAIL" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]]; then
        error "A valid ADMIN_EMAIL is required"
        return 1
    fi

    if [[ -z "$ADMIN_PASSWORD" ]]; then
        echo "" >/dev/tty
        echo -e "${CYAN}Administrator password${NC}:" >/dev/tty
        read_input "  ADMIN_PASSWORD (press Enter to generate): " ADMIN_PASSWORD "" true
        if [[ -z "$ADMIN_PASSWORD" ]]; then
            ADMIN_PASSWORD="$(random_string 32)"
            ADMIN_PASSWORD_GENERATED="true"
        fi
    fi
    if [[ ${#ADMIN_PASSWORD} -lt 16 ]]; then
        error "ADMIN_PASSWORD must be at least 16 characters"
        return 1
    fi
    if [[ ! "$ADMIN_PASSWORD" =~ ^[A-Za-z0-9._~!@%+=:,/-]+$ ]]; then
        error "ADMIN_PASSWORD contains characters that cannot be stored safely in .env"
        echo "  Use letters, numbers, or: . _ ~ ! @ % + = : , / -"
        return 1
    fi
}

set_env_value() {
    local key="$1"
    local value="$2"
    local env_file="$INSTALL_DIR/.env"

    if grep -q "^${key}=" "$env_file"; then
        sed -i "s|^${key}=.*$|${key}=${value}|" "$env_file"
    else
        printf '\n%s=%s\n' "$key" "$value" >> "$env_file"
    fi
}

unset_env_value() {
    local key="$1"
    local env_file="$INSTALL_DIR/.env"
    sed -i "/^${key}=/d" "$env_file"
}

read_env_value() {
    local key="$1"
    local env_file="$INSTALL_DIR/.env"
    grep "^${key}=" "$env_file" 2>/dev/null | tail -n 1 | cut -d= -f2-
}

sync_image_tag_for_update() {
    local persisted_tag
    persisted_tag="$(read_env_value "IMAGE_TAG")"

    if [[ "$IMAGE_TAG_EXPLICIT" == "true" ]]; then
        :
    elif [[ -n "$persisted_tag" ]]; then
        IMAGE_TAG="$persisted_tag"
    else
        # Legacy installer files embedded the selected tag directly in Compose.
        local legacy_tag
        legacy_tag="$(sed -n \
            's|^[[:space:]]*image:[[:space:]]*ghcr.io/soaringjerry/dreamtrans:\([A-Za-z0-9_.-]*\)[[:space:]]*$|\1|p' \
            "$INSTALL_DIR/docker-compose.yml" | head -n 1)"
        if [[ -n "$legacy_tag" ]]; then
            IMAGE_TAG="$legacy_tag"
        fi
    fi

    validate_deployment_options
    # Process environment overrides Compose's .env file for this update only.
    # The selected tag is persisted after migrations and readiness succeed.
    export IMAGE_TAG
}

compose_app_image_ref() {
    local compose_config
    compose_config="$($COMPOSE_CMD config)"
    printf '%s\n' "$compose_config" | awk '
        /^  app:$/ { in_app = 1; next }
        in_app && /^    image:/ {
            sub(/^[[:space:]]*image:[[:space:]]*/, "")
            print
            exit
        }
        in_app && /^  [^ ]/ { exit }
    '
}

resolve_app_image_identity() {
    APP_IMAGE_REF="$(compose_app_image_ref)"
    if [[ -z "$APP_IMAGE_REF" ]]; then
        error "Unable to resolve the DreamTrans app image from Docker Compose"
        return 1
    fi

    APP_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$APP_IMAGE_REF" 2>/dev/null)" || {
        error "Pulled app image is unavailable locally: $APP_IMAGE_REF"
        return 1
    }
    if [[ ! "$APP_IMAGE_ID" =~ ^sha256:[0-9a-f]{64}$ ]]; then
        error "Docker returned an invalid immutable image ID"
        return 1
    fi

    APP_IMAGE_REVISION="$(docker image inspect \
        --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' \
        "$APP_IMAGE_ID" 2>/dev/null || true)"
    if [[ "$APP_IMAGE_REVISION" =~ ^[0-9a-fA-F]{40,64}$ ]]; then
        APP_IMAGE_REVISION="${APP_IMAGE_REVISION,,}"
        set_env_value "DREAMTRANS_IMAGE_REVISION" "$APP_IMAGE_REVISION"
        info "Release revision: $APP_IMAGE_REVISION"
    else
        APP_IMAGE_REVISION=""
        unset_env_value "DREAMTRANS_IMAGE_REVISION"
        warn "Image has no valid OCI revision label; using its immutable image ID for asset extraction"
    fi
    set_env_value "DREAMTRANS_IMAGE_ID" "$APP_IMAGE_ID"
}

extract_release_migration_assets() {
    local staging_dir
    local asset_container
    local previous_migrations=""
    staging_dir="$(mktemp -d "$INSTALL_DIR/.migration-assets.XXXXXX")"
    mkdir -p "$staging_dir/migrations"

    asset_container="$(docker create "$APP_IMAGE_ID")" || {
        rm -rf -- "$staging_dir"
        error "Unable to create migration asset container from $APP_IMAGE_ID"
        return 1
    }
    if ! docker cp \
        "$asset_container:/usr/share/dreamtrans/migrations/." \
        "$staging_dir/migrations" ||
       ! docker cp \
        "$asset_container:/usr/share/dreamtrans/migrate.sh" \
        "$staging_dir/migrate.sh"; then
        docker rm -f "$asset_container" >/dev/null 2>&1 || true
        rm -rf -- "$staging_dir"
        error "The selected image does not contain its matching migration bundle"
        echo "  Refusing to download mutable migrations from a Git branch."
        return 1
    fi
    docker rm "$asset_container" >/dev/null 2>&1 || \
        warn "Could not remove temporary asset container $asset_container"

    if [[ ! -f "$staging_dir/migrate.sh" || -L "$staging_dir/migrate.sh" ]] ||
       find "$staging_dir/migrations" -type l -print -quit | grep -q . ||
       ! find "$staging_dir/migrations" -maxdepth 1 -type f \
            -name '[0-9][0-9][0-9]_*.sql' -print -quit | grep -q .; then
        rm -rf -- "$staging_dir"
        error "Image migration bundle is missing or unsafe"
        return 1
    fi

    # docker cp preserves image modes and umask 077 makes staging directories
    # private. Normalize explicitly so the non-root postgres migration service
    # can traverse and read every release asset.
    find "$staging_dir/migrations" -type d -exec chmod 0755 {} +
    find "$staging_dir/migrations" -type f -exec chmod 0444 {} +
    chmod 0555 "$staging_dir/migrate.sh"

    local pending_runner
    pending_runner="$(mktemp "$INSTALL_DIR/.migrate.sh.new.XXXXXX")"
    if ! install -m 0555 "$staging_dir/migrate.sh" "$pending_runner"; then
        rm -f -- "$pending_runner"
        rm -rf -- "$staging_dir"
        error "Unable to stage the release migration runner"
        return 1
    fi

    if [[ -e "$INSTALL_DIR/migrations" ]]; then
        previous_migrations="$(mktemp -d "$INSTALL_DIR/.migrations.previous.XXXXXX")"
        rmdir -- "$previous_migrations"
        mv -- "$INSTALL_DIR/migrations" "$previous_migrations"
    fi
    if ! mv -- "$staging_dir/migrations" "$INSTALL_DIR/migrations" ||
       ! mv -f -- "$pending_runner" "$INSTALL_DIR/migrate.sh"; then
        rm -f -- "$pending_runner"
        rm -rf -- "$INSTALL_DIR/migrations"
        if [[ -n "$previous_migrations" && -e "$previous_migrations" ]]; then
            mv -- "$previous_migrations" "$INSTALL_DIR/migrations"
        fi
        rm -rf -- "$staging_dir"
        error "Unable to install release migration bundle"
        return 1
    fi
    if [[ -n "$previous_migrations" ]]; then
        chmod -R u+w "$previous_migrations" 2>/dev/null || true
        rm -rf -- "$previous_migrations"
    fi
    rm -rf -- "$staging_dir"
    success "Migration assets extracted from immutable image $APP_IMAGE_ID"
}

begin_update_transaction() {
    UPDATE_BACKUP_DIR="$(mktemp -d "$INSTALL_DIR/.update-backup.XXXXXX")"
    UPDATE_HAD_MIGRATIONS="false"
    UPDATE_HAD_RUNNER="false"
    UPDATE_APP_RECREATE_ATTEMPTED="false"
    PREVIOUS_APP_IMAGE_REF=""
    PREVIOUS_APP_IMAGE_ID=""
    PREVIOUS_APP_WAS_RUNNING="false"
    PREVIOUS_IMAGE_TAG_ENV="$(read_env_value "IMAGE_TAG")"
    if ! cp -p -- "$INSTALL_DIR/.env" "$UPDATE_BACKUP_DIR/.env" ||
       ! cp -p -- "$INSTALL_DIR/docker-compose.yml" "$UPDATE_BACKUP_DIR/docker-compose.yml"; then
        rm -rf -- "$UPDATE_BACKUP_DIR"
        UPDATE_BACKUP_DIR=""
        error "Unable to create update configuration backup"
        return 1
    fi
    if [[ -d "$INSTALL_DIR/migrations" ]]; then
        if ! cp -a -- "$INSTALL_DIR/migrations" "$UPDATE_BACKUP_DIR/migrations"; then
            chmod -R u+w "$UPDATE_BACKUP_DIR" 2>/dev/null || true
            rm -rf -- "$UPDATE_BACKUP_DIR"
            UPDATE_BACKUP_DIR=""
            error "Unable to back up existing migration assets"
            return 1
        fi
        UPDATE_HAD_MIGRATIONS="true"
    fi
    if [[ -f "$INSTALL_DIR/migrate.sh" && ! -L "$INSTALL_DIR/migrate.sh" ]]; then
        if ! cp -p -- "$INSTALL_DIR/migrate.sh" "$UPDATE_BACKUP_DIR/migrate.sh"; then
            chmod -R u+w "$UPDATE_BACKUP_DIR" 2>/dev/null || true
            rm -rf -- "$UPDATE_BACKUP_DIR"
            UPDATE_BACKUP_DIR=""
            error "Unable to back up the existing migration runner"
            return 1
        fi
        UPDATE_HAD_RUNNER="true"
    fi

    # Ignore an inherited/CLI IMAGE_TAG while resolving the deployment that is
    # currently configured in its own .env file.
    PREVIOUS_APP_IMAGE_REF="$(unset IMAGE_TAG; compose_app_image_ref 2>/dev/null || true)"
    local previous_app_container_id
    previous_app_container_id="$(compose_service_container_id app)"
    if [[ -n "$previous_app_container_id" ]]; then
        PREVIOUS_APP_IMAGE_ID="$(docker inspect --format '{{.Image}}' \
            "$previous_app_container_id" 2>/dev/null || true)"
        if [[ "$(docker inspect --format '{{.State.Status}}' \
            "$previous_app_container_id" 2>/dev/null || true)" == "running" ]]; then
            PREVIOUS_APP_WAS_RUNNING="true"
        fi
    elif [[ -n "$PREVIOUS_APP_IMAGE_REF" ]]; then
        PREVIOUS_APP_IMAGE_ID="$(docker image inspect --format '{{.Id}}' \
            "$PREVIOUS_APP_IMAGE_REF" 2>/dev/null || true)"
    fi
    if [[ ! "$PREVIOUS_APP_IMAGE_ID" =~ ^sha256:[0-9a-f]{64}$ ]]; then
        PREVIOUS_APP_IMAGE_ID=""
    fi
}

rollback_update_files() {
    trap - ERR INT TERM
    local restore_failed="false"
    if [[ -n "${UPDATE_BACKUP_DIR:-}" && -d "$UPDATE_BACKUP_DIR" ]]; then
        cp -p -- "$UPDATE_BACKUP_DIR/.env" "$INSTALL_DIR/.env" 2>/dev/null ||
            restore_failed="true"
        cp -p -- "$UPDATE_BACKUP_DIR/docker-compose.yml" \
            "$INSTALL_DIR/docker-compose.yml" 2>/dev/null || restore_failed="true"
        rm -rf -- "$INSTALL_DIR/migrations" 2>/dev/null || restore_failed="true"
        rm -f -- "$INSTALL_DIR/migrate.sh" 2>/dev/null || restore_failed="true"
        if [[ "${UPDATE_HAD_MIGRATIONS:-false}" == "true" ]]; then
            mv -- "$UPDATE_BACKUP_DIR/migrations" "$INSTALL_DIR/migrations" \
                2>/dev/null || restore_failed="true"
        fi
        if [[ "${UPDATE_HAD_RUNNER:-false}" == "true" ]]; then
            mv -- "$UPDATE_BACKUP_DIR/migrate.sh" "$INSTALL_DIR/migrate.sh" \
                2>/dev/null || restore_failed="true"
        fi
        if [[ "$restore_failed" == "true" ]]; then
            error "Automatic update rollback was incomplete"
            echo "  Recovery backup preserved at: $UPDATE_BACKUP_DIR"
            return 1
        fi
        chmod -R u+w "$UPDATE_BACKUP_DIR" 2>/dev/null || true
        rm -rf -- "$UPDATE_BACKUP_DIR" 2>/dev/null || {
            error "Restored deployment files, but could not remove backup: $UPDATE_BACKUP_DIR"
            return 1
        }
        UPDATE_BACKUP_DIR=""
    fi
}

restore_previous_app_image() {
    if [[ -z "${PREVIOUS_APP_IMAGE_ID:-}" ||
          -z "${PREVIOUS_APP_IMAGE_REF:-}" ||
          "$PREVIOUS_APP_IMAGE_REF" == *@* ||
          "$PREVIOUS_APP_IMAGE_REF" == sha256:* ]]; then
        return 0
    fi
    if ! docker image inspect "$PREVIOUS_APP_IMAGE_ID" >/dev/null 2>&1 ||
       ! docker tag "$PREVIOUS_APP_IMAGE_ID" "$PREVIOUS_APP_IMAGE_REF"; then
        error "Unable to restore the previous app image tag"
        return 1
    fi
}

rollback_update_deployment() {
    local rollback_failed="false"
    rollback_update_files || rollback_failed="true"
    if [[ -n "${PREVIOUS_IMAGE_TAG_ENV:-}" ]]; then
        IMAGE_TAG="$PREVIOUS_IMAGE_TAG_ENV"
        export IMAGE_TAG
    else
        unset IMAGE_TAG
    fi
    restore_previous_app_image || rollback_failed="true"

    if [[ "${UPDATE_APP_RECREATE_ATTEMPTED:-false}" == "true" &&
          "${PREVIOUS_APP_WAS_RUNNING:-false}" == "true" &&
          -n "${PREVIOUS_APP_IMAGE_ID:-}" &&
          "$rollback_failed" == "false" ]]; then
        APP_IMAGE_ID="$PREVIOUS_APP_IMAGE_ID"
        if ! $COMPOSE_CMD up -d --force-recreate app; then
            error "Previous application container could not be restored automatically"
            rollback_failed="true"
        else
            local restored_container_id
            local restored_image_id
            restored_container_id="$(compose_service_container_id app)"
            restored_image_id="$(docker inspect --format '{{.Image}}' \
                "$restored_container_id" 2>/dev/null || true)"
            if [[ "$restored_image_id" != "$PREVIOUS_APP_IMAGE_ID" ]]; then
                error "Restored application container does not use the previous image"
                rollback_failed="true"
            fi
        fi
    fi

    if [[ "$rollback_failed" == "true" ]]; then
        error "Update rollback needs manual recovery"
        return 1
    fi
    warn "Update failed; restored the previous image, configuration, and migration assets"
}

rollback_update_on_error() {
    local status="${1:-1}"
    rollback_update_deployment || true
    exit "$status"
}

commit_update_transaction() {
    chmod -R u+w "$UPDATE_BACKUP_DIR" 2>/dev/null || true
    rm -rf -- "$UPDATE_BACKUP_DIR" || return 1
    UPDATE_BACKUP_DIR=""
}

prepare_release_migrations() {
    resolve_app_image_identity || return 1
    extract_release_migration_assets || return 1
}

ensure_jwt_secrets_for_update() {
    local env_file="$INSTALL_DIR/.env"
    if [[ ! -f "$env_file" ]]; then
        error "Environment file not found at $env_file"
        return 1
    fi
    local access_secret
    local refresh_secret
    access_secret="$(read_env_value "JWT_SECRET")"
    refresh_secret="$(read_env_value "JWT_REFRESH_SECRET")"

    if [[ ${#access_secret} -lt 32 ]]; then
        access_secret="$(random_string 64)"
        set_env_value "JWT_SECRET" "$access_secret"
        warn "Generated a new JWT_SECRET; existing access tokens will be invalidated"
    fi
    if [[ ${#refresh_secret} -lt 32 || "$refresh_secret" == "$access_secret" ]]; then
        refresh_secret="$(random_string 64)"
        set_env_value "JWT_REFRESH_SECRET" "$refresh_secret"
        warn "Generated a new independent JWT_REFRESH_SECRET; existing refresh tokens will be invalidated"
    fi
    chmod 600 "$env_file"
}

configure_admin_for_update() {
    local env_file="$INSTALL_DIR/.env"
    if [[ ! -f "$env_file" ]]; then
        error "Environment file not found at $env_file"
        return 1
    fi

    if [[ -z "$ADMIN_EMAIL" ]]; then
        ADMIN_EMAIL="$(grep '^ADMIN_EMAIL=' "$env_file" 2>/dev/null | tail -n 1 | cut -d= -f2-)"
    fi
    if [[ -z "$ADMIN_PASSWORD" ]]; then
        ADMIN_PASSWORD="$(grep '^ADMIN_PASSWORD=' "$env_file" 2>/dev/null | tail -n 1 | cut -d= -f2-)"
    fi

    if [[ -z "$ADMIN_EMAIL" && -z "$ADMIN_PASSWORD" ]]; then
        # Existing deployments with a secured active administrator do not need
        # a bootstrap password added retroactively. The known legacy bcrypt
        # hash is excluded because migration 003 will disable it.
        local has_secured_admin
        local db_container_id
        db_container_id="$(compose_service_container_id db)"
        if [[ -z "$db_container_id" ]]; then
            error "Unable to resolve the database container for this Compose project"
            return 1
        fi
        has_secured_admin="$(docker exec -i "$db_container_id" /bin/sh -c '
            exec psql -v ON_ERROR_STOP=1 \
                -U "${POSTGRES_USER:-dreamtrans}" \
                -d "${POSTGRES_DB:-dreamtrans}" -At
        ' 2>/dev/null <<'SQL' | tr -d '[:space:]'
SELECT CASE WHEN EXISTS (
    SELECT 1 FROM users
    WHERE role = 'super_admin'
      AND is_active = true
      AND password_hash <> '$2a$10$DEoAtxRrvaAbHFrSSgw3uu.rhEuoc3UJr2ctVDEooZv96sRC.7Eie'
) THEN 1 ELSE 0 END;
SQL
)"
        if [[ "$has_secured_admin" == "1" ]]; then
            return 0
        fi
    fi

    prompt_admin_credentials
    set_env_value "ADMIN_EMAIL" "$ADMIN_EMAIL"
    set_env_value "ADMIN_PASSWORD" "$ADMIN_PASSWORD"
    chmod 600 "$env_file"
    ADMIN_CREDENTIALS_ADDED="true"
}

harden_existing_compose() {
    local compose_file="$INSTALL_DIR/docker-compose.yml"
    local env_file="$INSTALL_DIR/.env"
    local published_port

    published_port="$(sed -nE \
        's/^[[:space:]]*-[[:space:]]*"([^"]*:)?([0-9]+):8080"[[:space:]]*$/\2/p' \
        "$compose_file" | head -n 1)"
    if [[ -n "$published_port" ]]; then
        set_env_value "PORT" "$published_port"
    fi

    # Preserve existing values while migrating legacy variable names.
    sed -i 's/^OPENAI_BASE=/OPENAI_API_BASE=/' "$env_file"
    sed -i "/^version: ['\"]3\.[0-9]['\"]$/d" "$compose_file"
    sed -i 's|OPENAI_BASE|OPENAI_API_BASE|g' "$compose_file"
    sed -i 's|^\([[:space:]]*image:[[:space:]]*ghcr.io/soaringjerry/dreamtrans:\).*$|\1${IMAGE_TAG:-latest}|' "$compose_file"
    if ! grep -q '^BIND_ADDRESS=' "$env_file"; then
        set_env_value "BIND_ADDRESS" "127.0.0.1"
    fi
    if ! grep -q 'BIND_ADDRESS.*:.*:8080' "$compose_file"; then
        sed -i 's|^\([[:space:]]*- "\)\([0-9][0-9]*:8080"\)$|\1${BIND_ADDRESS:-127.0.0.1}:\2|' "$compose_file"
    fi

    # Remove known insecure fallbacks from installer-generated compose files.
    sed -i 's|${POSTGRES_PASSWORD:-dreamtrans}|${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}|g' "$compose_file"
    sed -i 's|${SM_API_KEY}|${SM_API_KEY:?SM_API_KEY must be set}|g' "$compose_file"
    sed -i 's|^[[:space:]]*- DATABASE_URL=.*|      - DATABASE_URL=postgres://${POSTGRES_USER:-dreamtrans}:${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}@db:5432/${POSTGRES_DB:-dreamtrans}?sslmode=disable|' "$compose_file"
    sed -i 's|${JWT_SECRET}|${JWT_SECRET:?JWT_SECRET must be set}|g' "$compose_file"
    sed -i 's|${JWT_REFRESH_SECRET:-}|${JWT_REFRESH_SECRET:?JWT_REFRESH_SECRET must be set}|g' "$compose_file"

    if ! grep -q 'ADMIN_EMAIL=' "$compose_file"; then
        if ! grep -q 'JWT_REFRESH_SECRET=' "$compose_file"; then
            error "Cannot safely add bootstrap administrator variables to $compose_file"
            return 1
        fi
        sed -i '/JWT_REFRESH_SECRET=/a\
      - ADMIN_EMAIL=${ADMIN_EMAIL:-}\
      - ADMIN_PASSWORD=${ADMIN_PASSWORD:-}' "$compose_file"
    fi

    if ! grep -q 'DREAMTRANS_API_KEY=' "$compose_file"; then
        sed -i '/ADMIN_PASSWORD=/a\
      - DREAMTRANS_API_KEY=${DREAMTRANS_API_KEY:-}\
      - DREAMTRANS_ADMIN_API_KEY=${DREAMTRANS_ADMIN_API_KEY:-}\
      - ALLOW_ANONYMOUS_API=${ALLOW_ANONYMOUS_API:-false}\
      - ALLOW_WEBSOCKET_QUERY_TOKEN=${ALLOW_WEBSOCKET_QUERY_TOKEN:-false}\
      - API_RATE_LIMIT_PER_MINUTE=${API_RATE_LIMIT_PER_MINUTE:-120}\
      - WEBSOCKET_MAX_CONNECTIONS=${WEBSOCKET_MAX_CONNECTIONS:-256}\
      - WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=${WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL:-4}\
      - REGISTRATION_ENABLED=${REGISTRATION_ENABLED:-false}\
      - REGISTRATION_INVITE_CODE=${REGISTRATION_INVITE_CODE:-}\
      - CORS_ALLOWED_ORIGINS=${CORS_ALLOWED_ORIGINS:-}' "$compose_file"
    fi

    if ! grep -q 'ALLOW_WEBSOCKET_QUERY_TOKEN=' "$compose_file"; then
        sed -i '/ALLOW_ANONYMOUS_API=/a\
      - ALLOW_WEBSOCKET_QUERY_TOKEN=${ALLOW_WEBSOCKET_QUERY_TOKEN:-false}' "$compose_file"
    fi
    if ! grep -q 'WEBSOCKET_MAX_CONNECTIONS=' "$compose_file"; then
        sed -i '/API_RATE_LIMIT_PER_MINUTE=/a\
      - WEBSOCKET_MAX_CONNECTIONS=${WEBSOCKET_MAX_CONNECTIONS:-256}\
      - WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=${WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL:-4}' "$compose_file"
    fi
    if ! grep -q 'WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=' "$compose_file"; then
        sed -i '/WEBSOCKET_MAX_CONNECTIONS=/a\
      - WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=${WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL:-4}' "$compose_file"
    fi

    if ! grep -q '^RAG_MAX_DB_MB=' "$env_file"; then
        set_env_value "RAG_MAX_DB_MB" "$RAG_MAX_DB_MB"
    fi
    if ! grep -q 'RAG_MAX_DB_MB=' "$compose_file"; then
        if grep -q 'ALLOW_USER_API_KEY=' "$compose_file"; then
            sed -i '/ALLOW_USER_API_KEY=/a\
      - RAG_MAX_DB_MB=${RAG_MAX_DB_MB:-102400}' "$compose_file"
        elif grep -q 'CORS_ALLOWED_ORIGINS=' "$compose_file"; then
            sed -i '/CORS_ALLOWED_ORIGINS=/a\
      - RAG_MAX_DB_MB=${RAG_MAX_DB_MB:-102400}' "$compose_file"
        else
            error "Cannot safely add RAG_MAX_DB_MB to $compose_file"
            return 1
        fi
    fi

    # Installer releases before the migration runner relied on
    # docker-entrypoint-initdb.d, which does nothing for an existing volume.
    if ! grep -q '^  migrate:' "$compose_file"; then
        sed -i '/^  app:/i\
  migrate:\
    image: postgres:16-alpine\
    user: postgres\
    read_only: true\
    cap_drop:\
      - ALL\
    security_opt:\
      - no-new-privileges:true\
    environment:\
      PGHOST: db\
      PGPORT: "5432"\
      PGDATABASE: ${POSTGRES_DB:-dreamtrans}\
      PGUSER: ${POSTGRES_USER:-dreamtrans}\
      PGPASSWORD: ${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}\
      MIGRATIONS_DIR: /migrations\
    volumes:\
      - ./migrations:/migrations:ro\
      - ./migrate.sh:/migration-tools/migrate.sh:ro\
    entrypoint:\
      - /bin/sh\
      - /migration-tools/migrate.sh\
    depends_on:\
      db:\
        condition: service_healthy\
    restart: "no"\
' "$compose_file"
    fi
    if ! grep -q 'condition: service_completed_successfully' "$compose_file"; then
        sed -i '/^  app:/,/^volumes:/ {
            /condition: service_healthy/a\
      migrate:\
        condition: service_completed_successfully
        }' "$compose_file"
    fi
    if ! sed -n '/^  app:/,/^volumes:/p' "$compose_file" | grep -q '^    healthcheck:'; then
        sed -i '/^  app:/,/^volumes:/ {
            /^    volumes:/i\
    healthcheck:\
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/readyz"]\
      interval: 10s\
      timeout: 3s\
      start_period: 10s\
      retries: 12
        }' "$compose_file"
    fi
    if ! sed -n '/^  app:/,/^volumes:/p' "$compose_file" | grep -q '^    stop_grace_period:'; then
        sed -i '/^  app:/,/^volumes:/ {
            /^    volumes:/i\
    stop_grace_period: 30s
        }' "$compose_file"
    fi

    if ! $COMPOSE_CMD config --quiet; then
        error "Hardened Docker Compose configuration is invalid"
        return 1
    fi
}

# Prompt for API keys
prompt_api_keys() {
    echo "" >/dev/tty
    info "Please provide your API keys:"
    echo "" >/dev/tty

    # Speechmatics API Key
    if [[ -z "$SM_API_KEY" ]]; then
        echo -e "${CYAN}Speechmatics API Key${NC} (required for transcription):" >/dev/tty
        echo "  Get yours at: https://www.speechmatics.com/" >/dev/tty
        read_input "  SM_API_KEY: " SM_API_KEY "" true
        if [[ -z "$SM_API_KEY" ]]; then
            error "Speechmatics API Key is required!"
            exit 1
        fi
    fi

    # OpenAI API Key
    if [[ -z "$OPENAI_API_KEY" ]]; then
        echo "" >/dev/tty
        echo -e "${CYAN}OpenAI API Key${NC} (optional, for translation/chat):" >/dev/tty
        echo "  Get yours at: https://platform.openai.com/api-keys" >/dev/tty
        read_input "  OPENAI_API_KEY (press Enter to skip): " OPENAI_API_KEY "" true
    fi

    # OpenAI Base URL
    if [[ -z "$OPENAI_API_BASE" ]]; then
        echo "" >/dev/tty
        echo -e "${CYAN}OpenAI Base URL${NC} (optional, for custom endpoints):" >/dev/tty
        read_input "  OPENAI_API_BASE (press Enter for default): " OPENAI_API_BASE "https://api.openai.com/v1" false
    fi

    # Bootstrap administrator. There is deliberately no known default account.
    prompt_admin_credentials

    local secret_value
    for secret_value in "$SM_API_KEY" "${OPENAI_API_KEY:-}" "${OPENAI_API_BASE:-}"; do
        if [[ "$secret_value" == *$'\n'* || "$secret_value" == *$'\r'* ]]; then
            error "API configuration values must not contain newlines"
            exit 1
        fi
    done
}

# Create directory structure
create_directories() {
    info "Creating directory structure..."
    validate_fresh_install_target
    mkdir -p "$INSTALL_DIR"
    if [[ -L "$INSTALL_DIR" || ! -O "$INSTALL_DIR" ]]; then
        error "Installation directory ownership changed while installing"
        return 1
    fi
    mkdir -p "$INSTALL_DIR/data"
    mkdir -p "$INSTALL_DIR/migrations"
    write_install_sentinel
    success "Directories created at $INSTALL_DIR"
}

# Generate .env file
generate_env_file() {
    info "Generating environment configuration..."

    local jwt_secret
    local jwt_refresh_secret
    local pg_password
    jwt_secret="$(random_string 64)"
    jwt_refresh_secret="$(random_string 64)"
    pg_password="$(random_string 32)"

    cat > "$INSTALL_DIR/.env" << EOF
# DreamTrans Configuration
# Generated on $(date)

# === Required ===
SM_API_KEY=${SM_API_KEY}

# === OpenAI (optional) ===
OPENAI_API_KEY=${OPENAI_API_KEY:-}
OPENAI_API_BASE=${OPENAI_API_BASE:-https://api.openai.com/v1}

# === Server ===
PORT=${PORT}
BIND_ADDRESS=${BIND_ADDRESS}
IMAGE_TAG=${IMAGE_TAG}

# === PostgreSQL ===
POSTGRES_DB=dreamtrans
POSTGRES_USER=dreamtrans
POSTGRES_PASSWORD=${pg_password}

# === JWT Authentication ===
JWT_SECRET=${jwt_secret}
JWT_REFRESH_SECRET=${jwt_refresh_secret}

# === Bootstrap Administrator ===
ADMIN_EMAIL=${ADMIN_EMAIL}
ADMIN_PASSWORD=${ADMIN_PASSWORD}

# === Authentication and Registration ===
DREAMTRANS_API_KEY=
DREAMTRANS_ADMIN_API_KEY=
ALLOW_ANONYMOUS_API=false
ALLOW_WEBSOCKET_QUERY_TOKEN=false
API_RATE_LIMIT_PER_MINUTE=120
WEBSOCKET_MAX_CONNECTIONS=256
WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=4
REGISTRATION_ENABLED=false
REGISTRATION_INVITE_CODE=
CORS_ALLOWED_ORIGINS=

# === System Settings ===
ALLOW_USER_API_KEY=false
RAG_MAX_DB_MB=${RAG_MAX_DB_MB}
EOF

    chmod 600 "$INSTALL_DIR/.env"
    success "Environment file created"
}

# Generate docker-compose.yml
generate_compose_file() {
    info "Generating Docker Compose configuration..."

    cat > "$INSTALL_DIR/docker-compose.yml" << EOF
# DreamTrans Docker Compose
# Generated by install.sh
#
# Access:
#   - Classic UI: http://localhost:${PORT}
#   - Pro UI:     http://localhost:${PORT}/pro
#

services:
  db:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: \${POSTGRES_USER:-dreamtrans}
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}
      POSTGRES_DB: \${POSTGRES_DB:-dreamtrans}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U \${POSTGRES_USER:-dreamtrans} -d \${POSTGRES_DB:-dreamtrans}"]
      interval: 5s
      timeout: 5s
      retries: 5

  migrate:
    image: postgres:16-alpine
    user: postgres
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      PGHOST: db
      PGPORT: "5432"
      PGDATABASE: \${POSTGRES_DB:-dreamtrans}
      PGUSER: \${POSTGRES_USER:-dreamtrans}
      PGPASSWORD: \${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}
      MIGRATIONS_DIR: /migrations
    volumes:
      - ./migrations:/migrations:ro
      - ./migrate.sh:/migration-tools/migrate.sh:ro
    entrypoint:
      - /bin/sh
      - /migration-tools/migrate.sh
    depends_on:
      db:
        condition: service_healthy
    restart: "no"

  app:
    image: ghcr.io/soaringjerry/dreamtrans:\${IMAGE_TAG:-latest}
    restart: unless-stopped
    ports:
      - "\${BIND_ADDRESS:-127.0.0.1}:\${PORT:-16002}:8080"
    environment:
      - SM_API_KEY=\${SM_API_KEY:?SM_API_KEY must be set}
      - OPENAI_API_KEY=\${OPENAI_API_KEY:-}
      - OPENAI_API_BASE=\${OPENAI_API_BASE:-https://api.openai.com/v1}
      - DATABASE_URL=postgres://\${POSTGRES_USER:-dreamtrans}:\${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}@db:5432/\${POSTGRES_DB:-dreamtrans}?sslmode=disable
      - JWT_SECRET=\${JWT_SECRET:?JWT_SECRET must be set}
      - JWT_REFRESH_SECRET=\${JWT_REFRESH_SECRET:?JWT_REFRESH_SECRET must be set}
      - ADMIN_EMAIL=\${ADMIN_EMAIL:?ADMIN_EMAIL must be set}
      - ADMIN_PASSWORD=\${ADMIN_PASSWORD:?ADMIN_PASSWORD must be set}
      - DREAMTRANS_API_KEY=\${DREAMTRANS_API_KEY:-}
      - DREAMTRANS_ADMIN_API_KEY=\${DREAMTRANS_ADMIN_API_KEY:-}
      - ALLOW_ANONYMOUS_API=\${ALLOW_ANONYMOUS_API:-false}
      - ALLOW_WEBSOCKET_QUERY_TOKEN=\${ALLOW_WEBSOCKET_QUERY_TOKEN:-false}
      - API_RATE_LIMIT_PER_MINUTE=\${API_RATE_LIMIT_PER_MINUTE:-120}
      - WEBSOCKET_MAX_CONNECTIONS=\${WEBSOCKET_MAX_CONNECTIONS:-256}
      - WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=\${WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL:-4}
      - REGISTRATION_ENABLED=\${REGISTRATION_ENABLED:-false}
      - REGISTRATION_INVITE_CODE=\${REGISTRATION_INVITE_CODE:-}
      - CORS_ALLOWED_ORIGINS=\${CORS_ALLOWED_ORIGINS:-}
      - ALLOW_USER_API_KEY=\${ALLOW_USER_API_KEY:-false}
      - RAG_MAX_DB_MB=\${RAG_MAX_DB_MB:-102400}
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/readyz"]
      interval: 10s
      timeout: 3s
      start_period: 10s
      retries: 12
    stop_grace_period: 30s
    volumes:
      - appdata:/app/data
    depends_on:
      db:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully

volumes:
  pgdata:
  appdata:
EOF

    success "Docker Compose file created"
}

compose_service_container_id() {
    $COMPOSE_CMD ps -q "$1" 2>/dev/null | sed -n '1p'
}

# Wait for PostgreSQL to be ready
wait_for_db() {
    local max_attempts=30
    local attempt=0
    local db_container_id
    while true; do
        db_container_id="$(compose_service_container_id db)"
        if [[ -n "$db_container_id" ]] &&
           docker exec "$db_container_id" /bin/sh -c '
               exec pg_isready \
                   -U "${POSTGRES_USER:-dreamtrans}" \
                   -d "${POSTGRES_DB:-dreamtrans}"
           ' >/dev/null 2>&1; then
            return 0
        fi
        attempt=$((attempt + 1))
        if [[ $attempt -ge $max_attempts ]]; then
            error "PostgreSQL failed to start for this Compose project"
            return 1
        fi
        sleep 1
    done
}

# Run all database migrations
run_migrations() {
    info "Running database migrations..."

    wait_for_db || return 1
    local db_container_id
    db_container_id="$(compose_service_container_id db)"
    if [[ -z "$db_container_id" ]]; then
        error "Unable to resolve the database container for this Compose project"
        return 1
    fi

    local migrations_dir="$INSTALL_DIR/migrations"
    if [[ ! -x "$INSTALL_DIR/migrate.sh" ]] ||
       ! find "$migrations_dir" -maxdepth 1 -type f \
            -name '[0-9][0-9][0-9]_*.sql' -print -quit | grep -q .; then
        error "Matching migration assets are not installed"
        echo "  Pull the selected app image and extract its release bundle first."
        return 1
    fi

    local container_migrations="/tmp/dreamtrans-migrations-$$"
    docker exec "$db_container_id" mkdir -p "$container_migrations"
    docker cp "$migrations_dir/." "$db_container_id:$container_migrations"

    if ! docker exec -i -e MIGRATIONS_DIR="$container_migrations" "$db_container_id" /bin/sh -c '
        export PGHOST=127.0.0.1
        export PGPORT=5432
        export PGDATABASE="${POSTGRES_DB:-dreamtrans}"
        export PGUSER="${POSTGRES_USER:-dreamtrans}"
        export PGPASSWORD="${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}"
        exec /bin/sh
    ' < "$INSTALL_DIR/migrate.sh"; then
        docker exec "$db_container_id" rm -rf -- "$container_migrations" >/dev/null 2>&1 || true
        error "Database migration failed; the failing version was rolled back and not marked as applied"
        return 1
    fi
    docker exec "$db_container_id" rm -rf -- "$container_migrations" >/dev/null 2>&1 || true

    success "Database migrations complete"
}

# Initialize database schema (alias for backwards compatibility)
init_database() {
    run_migrations
}

wait_for_app_ready() {
    local max_attempts=75
    local attempt=0
    local state
    local health
    local app_container_id
    local running_image_id
    local expected_image_id="${APP_IMAGE_ID:-}"

    if [[ -z "$expected_image_id" ]]; then
        local expected_image_ref
        expected_image_ref="$(compose_app_image_ref)"
        expected_image_id="$(docker image inspect --format '{{.Id}}' \
            "$expected_image_ref" 2>/dev/null || true)"
    fi

    info "Waiting for DreamTrans readiness..."
    while ((attempt < max_attempts)); do
        app_container_id="$(compose_service_container_id app)"
        state="$(docker inspect --format '{{.State.Status}}' "$app_container_id" 2>/dev/null || true)"
        health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' \
            "$app_container_id" 2>/dev/null || true)"
        if [[ "$state" == "running" && "$health" == "healthy" ]]; then
            running_image_id="$(docker inspect --format '{{.Image}}' \
                "$app_container_id" 2>/dev/null || true)"
            if [[ -n "$expected_image_id" && "$running_image_id" != "$expected_image_id" ]]; then
                error "Ready container image does not match the selected release"
                echo "  Expected: $expected_image_id"
                echo "  Running:  ${running_image_id:-unknown}"
                return 1
            fi
            success "DreamTrans readiness check passed"
            return 0
        fi
        if [[ "$state" == "exited" || "$state" == "dead" ]]; then
            error "DreamTrans stopped before becoming ready (status: $state)"
            $COMPOSE_CMD logs --tail=100 app 2>/dev/null || true
            return 1
        fi
        attempt=$((attempt + 1))
        sleep 2
    done

    error "DreamTrans did not become ready (container: ${state:-missing}, health: ${health:-missing})"
    $COMPOSE_CMD logs --tail=100 app 2>/dev/null || true
    return 1
}

# Start services
start_services() {
    info "Starting DreamTrans..."
    cd "$INSTALL_DIR"

    # Pull latest image
    info "Pulling latest image..."
    $COMPOSE_CMD pull || return 1
    prepare_release_migrations || return 1

    # Bring up PostgreSQL first. The application requires the schema (including
    # the bootstrap-admin tables) to exist before its first start.
    $COMPOSE_CMD up -d db || return 1
    init_database || return 1

    # Start the application only after every migration committed successfully.
    $COMPOSE_CMD up -d app || return 1

    wait_for_app_ready || return 1
    success "DreamTrans is running!"
}

# Update installation
update_installation() {
    info "Updating DreamTrans..."

    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"
    begin_update_transaction
    trap 'rollback_update_on_error "$?"' ERR
    trap 'rollback_update_on_error 130' INT
    trap 'rollback_update_on_error 143' TERM

    # New releases require separate access/refresh signing keys. Repair legacy
    # installer environments before Compose evaluates required variables.
    ensure_jwt_secrets_for_update || { rollback_update_deployment; return 1; }
    sync_image_tag_for_update || { rollback_update_deployment; return 1; }
    harden_existing_compose || { rollback_update_deployment; return 1; }

    # Pull latest image
    info "Pulling latest image..."
    $COMPOSE_CMD pull || { rollback_update_deployment; return 1; }
    prepare_release_migrations || { rollback_update_deployment; return 1; }

    # Apply migrations before recreating the application container, so a failed
    # migration leaves the currently running version untouched.
    $COMPOSE_CMD up -d db || { rollback_update_deployment; return 1; }
    wait_for_db || { rollback_update_deployment; return 1; }
    configure_admin_for_update || { rollback_update_deployment; return 1; }
    run_migrations || { rollback_update_deployment; return 1; }

    info "Restarting application..."
    UPDATE_APP_RECREATE_ATTEMPTED="true"
    $COMPOSE_CMD up -d app || { rollback_update_deployment; return 1; }

    wait_for_app_ready || { rollback_update_deployment; return 1; }
    set_env_value "IMAGE_TAG" "$IMAGE_TAG" || { rollback_update_deployment; return 1; }
    chmod 600 "$INSTALL_DIR/.env" || { rollback_update_deployment; return 1; }
    commit_update_transaction || { rollback_update_deployment; return 1; }
    trap - ERR INT TERM

    if [[ "$ADMIN_CREDENTIALS_ADDED" == "true" ]]; then
        warn "A bootstrap administrator was added during this security update:"
        echo "  Email: $ADMIN_EMAIL"
        if [[ "$ADMIN_PASSWORD_GENERATED" == "true" ]]; then
            echo "  Password: $ADMIN_PASSWORD"
            echo "  This generated password is shown only once; store it securely."
        else
            echo "  Password: (the password supplied during the update)"
        fi
    fi

    success "DreamTrans updated successfully!"
}

# Stop services
stop_services() {
    info "Stopping DreamTrans..."

    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"
    $COMPOSE_CMD down

    success "DreamTrans stopped"
}

# Start services
start_services_only() {
    info "Starting DreamTrans..."

    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"
    $COMPOSE_CMD up -d

    wait_for_app_ready
    success "DreamTrans started"
    show_access_info
}

# Restart services
restart_services() {
    info "Restarting DreamTrans..."

    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"
    $COMPOSE_CMD restart

    wait_for_app_ready
    success "DreamTrans restarted"
    show_access_info
}

# Show status
show_status() {
    info "DreamTrans Status:"

    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"
    echo ""
    $COMPOSE_CMD ps
    echo ""
}

# Show logs
show_logs() {
    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"
    $COMPOSE_CMD logs -f
}

# Show access info
show_access_info() {
    # Try to get PORT from .env file
    if [[ -f "$INSTALL_DIR/.env" ]]; then
        local env_port=$(grep "^PORT=" "$INSTALL_DIR/.env" 2>/dev/null | cut -d= -f2)
        if [[ -n "$env_port" ]]; then
            PORT="$env_port"
        fi
    fi
    # Prefer Docker's resolved published port; this handles bind addresses,
    # variable interpolation, and legacy installer files.
    if [[ -f "$INSTALL_DIR/docker-compose.yml" ]]; then
        local published_endpoint
        local compose_port
        published_endpoint="$($COMPOSE_CMD port app 8080 2>/dev/null | head -n 1 || true)"
        compose_port="${published_endpoint##*:}"
        if [[ "$compose_port" =~ ^[0-9]+$ ]]; then
            PORT="$compose_port"
        fi
    fi
    echo ""
    echo -e "  ${CYAN}Classic UI:${NC}  http://localhost:${PORT}"
    echo -e "  ${CYAN}Pro UI:${NC}      http://localhost:${PORT}/pro"
    echo ""
}

# Uninstall
uninstall() {
    require_installation "false"
    warn "This will remove DreamTrans and all its data!"
    warn "Uninstall target: $INSTALL_DIR"
    read_input "Are you sure? (yes/no): " confirm "" false

    if [[ "$confirm" != "yes" ]]; then
        info "Uninstall cancelled"
        exit 0
    fi

    info "Stopping and removing containers..."
    cd "$INSTALL_DIR"
    if ! $COMPOSE_CMD down -v --remove-orphans; then
        error "Docker Compose cleanup failed; configuration and data were preserved"
        return 1
    fi

    # Revalidate immediately before deletion. Only installer-owned paths are
    # removed; an accidental --dir pointing at an existing project can never
    # erase unknown files.
    normalize_install_dir
    require_installation "false"
    info "Removing installer-owned files..."
    rm -f -- \
        "$INSTALL_DIR/.env" \
        "$INSTALL_DIR/docker-compose.yml" \
        "$INSTALL_DIR/migrate.sh" \
        "$INSTALL_DIR/$INSTALL_SENTINEL"
    rm -rf --one-file-system -- \
        "$INSTALL_DIR/migrations" \
        "$INSTALL_DIR/data"
    find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 \
        \( -name '.migration-assets.*' -o -name '.migrations.previous.*' -o \
           -name '.update-backup.*' -o -name '.migrate.sh.new.*' \) \
        -exec rm -rf --one-file-system -- {} +

    if rmdir -- "$INSTALL_DIR" 2>/dev/null; then
        success "DreamTrans and its installation directory have been removed"
    else
        success "DreamTrans has been uninstalled"
        warn "Unknown files remain in $INSTALL_DIR, so the directory was preserved"
    fi
}

# Print completion message
print_completion() {
    local quoted_install_dir
    printf -v quoted_install_dir '%q' "$INSTALL_DIR"
    echo "" >/dev/tty
    echo -e "${GREEN}╔═══════════════════════════════════════════════════════════════╗${NC}" >/dev/tty
    echo -e "${GREEN}║                                                               ║${NC}" >/dev/tty
    echo -e "${GREEN}║              DreamTrans installed successfully!              ║${NC}" >/dev/tty
    echo -e "${GREEN}║                                                               ║${NC}" >/dev/tty
    echo -e "${GREEN}╚═══════════════════════════════════════════════════════════════╝${NC}" >/dev/tty
    echo "" >/dev/tty
    echo -e "  ${CYAN}Classic UI:${NC}     http://localhost:${PORT}" >/dev/tty
    echo -e "  ${CYAN}Pro UI:${NC}         http://localhost:${PORT}/pro" >/dev/tty
    echo -e "  ${CYAN}Install Dir:${NC}    $INSTALL_DIR" >/dev/tty
    echo "" >/dev/tty
    echo -e "  ${YELLOW}Useful Commands:${NC}" >/dev/tty
    echo "    cd -- $quoted_install_dir" >/dev/tty
    echo "    $COMPOSE_CMD logs -f        # View logs" >/dev/tty
    echo "    $COMPOSE_CMD restart        # Restart services" >/dev/tty
    echo "    $COMPOSE_CMD down           # Stop services" >/dev/tty
    echo "" >/dev/tty
    echo -e "  ${YELLOW}Update:${NC}" >/dev/tty
    echo "    curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash -s -- --update --dir $quoted_install_dir" >/dev/tty
    echo "" >/dev/tty
    echo -e "  ${YELLOW}Administrator:${NC}" >/dev/tty
    echo "    Email:    ${ADMIN_EMAIL}" >/dev/tty
    if [[ "$ADMIN_PASSWORD_GENERATED" == "true" ]]; then
        echo "    Password: ${ADMIN_PASSWORD}" >/dev/tty
        echo "    This generated password is shown only once; store it securely." >/dev/tty
    else
        echo "    Password: (the password supplied during installation)" >/dev/tty
    fi
    echo "" >/dev/tty
    echo -e "  ${YELLOW}Notes:${NC}" >/dev/tty
    echo "    - Both Classic (/) and Pro (/pro) UIs are available" >/dev/tty
    echo "    - Sign in at /pro with the administrator credentials above" >/dev/tty
    echo "    - Self-registration is disabled by default" >/dev/tty
    echo "" >/dev/tty
}

# Main function
main() {
    print_banner
    parse_args "$@"
    validate_command_selection
    normalize_install_dir
    validate_deployment_options

    # Handle special modes
    if [[ "$UNINSTALL_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        uninstall
        exit 0
    fi

    if [[ "$STOP_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        stop_services
        exit 0
    fi

    if [[ "$START_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        start_services_only
        exit 0
    fi

    if [[ "$RESTART_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        restart_services
        exit 0
    fi

    if [[ "$STATUS_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        show_status
        exit 0
    fi

    if [[ "$LOGS_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        show_logs
        exit 0
    fi

    if [[ "$INIT_DB_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        cd "$INSTALL_DIR"
        prepare_release_migrations
        $COMPOSE_CMD up -d db
        init_database
        exit 0
    fi

    if [[ "$MIGRATE_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "false"
        cd "$INSTALL_DIR"
        prepare_release_migrations
        $COMPOSE_CMD up -d db
        run_migrations
        exit 0
    fi

    if [[ "$UPDATE_MODE" == "true" ]]; then
        check_prerequisites
        require_installation "true"
        update_installation
        show_access_info
        exit 0
    fi

    # Normal installation
    check_prerequisites
    validate_fresh_install_target

    prompt_api_keys
    create_directories
    generate_env_file
    generate_compose_file
    start_services
    print_completion
}

# Run main function
main "$@"
