#!/bin/bash
set -e

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
PORT="${PORT:-8080}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
CONTAINER_NAME="dreamtrans"
DB_CONTAINER_NAME="dreamtrans-db"

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
        eval "$var_name='$default'"
    fi
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check prerequisites
check_prerequisites() {
    info "Checking prerequisites..."

    if ! command_exists docker; then
        error "Docker is not installed. Please install Docker first."
        echo "  Visit: https://docs.docker.com/get-docker/"
        exit 1
    fi

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
                PORT="$2"
                shift 2
                ;;
            --dir)
                INSTALL_DIR="$2"
                shift 2
                ;;
            --tag)
                IMAGE_TAG="$2"
                shift 2
                ;;
            --sm-key)
                SM_API_KEY="$2"
                shift 2
                ;;
            --openai-key)
                OPENAI_API_KEY="$2"
                shift 2
                ;;
            --update)
                UPDATE_MODE="true"
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
                warn "Unknown option: $1"
                shift
                ;;
        esac
    done
}

# Show help
show_help() {
    echo "DreamTrans Installer"
    echo ""
    echo "Usage:"
    echo "  curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash"
    echo "  curl -fsSL ... | bash -s -- [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --port PORT       Set the port (default: 8080)"
    echo "  --dir DIR         Set installation directory (default: ~/dreamtrans)"
    echo "  --tag TAG         Docker image tag (default: latest)"
    echo "  --sm-key KEY      Speechmatics API key (skip prompt)"
    echo "  --openai-key KEY  OpenAI API key (skip prompt)"
    echo "  --update          Update existing installation"
    echo "  --uninstall       Remove DreamTrans"
    echo "  -h, --help        Show this help message"
    echo ""
    echo "Examples:"
    echo "  # Interactive installation"
    echo "  curl -fsSL ... | bash"
    echo ""
    echo "  # Non-interactive with API keys"
    echo "  curl -fsSL ... | bash -s -- --sm-key YOUR_KEY --openai-key YOUR_KEY"
    echo ""
    echo "  # Custom port"
    echo "  curl -fsSL ... | bash -s -- --port 3000"
    echo ""
    echo "  # Update existing installation"
    echo "  curl -fsSL ... | bash -s -- --update"
}

# Generate random string
random_string() {
    local length=${1:-32}
    cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w "$length" | head -n 1
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
        read_input "  SM_API_KEY: " SM_API_KEY "" false
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
        read_input "  OPENAI_API_KEY (press Enter to skip): " OPENAI_API_KEY "" false
    fi

    # OpenAI Base URL
    if [[ -z "$OPENAI_BASE" ]]; then
        echo "" >/dev/tty
        echo -e "${CYAN}OpenAI Base URL${NC} (optional, for custom endpoints):" >/dev/tty
        read_input "  OPENAI_BASE (press Enter for default): " OPENAI_BASE "https://api.openai.com/v1" false
    fi
}

# Create directory structure
create_directories() {
    info "Creating directory structure..."
    mkdir -p "$INSTALL_DIR"
    mkdir -p "$INSTALL_DIR/data"
    success "Directories created at $INSTALL_DIR"
}

# Generate .env file
generate_env_file() {
    info "Generating environment configuration..."

    local jwt_secret=$(random_string 48)
    local jwt_refresh_secret=$(random_string 48)
    local pg_password=$(random_string 24)

    cat > "$INSTALL_DIR/.env" << EOF
# DreamTrans Configuration
# Generated on $(date)

# === Required ===
SM_API_KEY=${SM_API_KEY}

# === OpenAI (optional) ===
OPENAI_API_KEY=${OPENAI_API_KEY:-}
OPENAI_BASE=${OPENAI_BASE:-https://api.openai.com/v1}

# === Server ===
PORT=8080

# === PostgreSQL ===
POSTGRES_USER=dreamtrans
POSTGRES_PASSWORD=${pg_password}
DATABASE_URL=postgres://dreamtrans:${pg_password}@db:5432/dreamtrans?sslmode=disable

# === JWT Authentication ===
JWT_SECRET=${jwt_secret}
JWT_REFRESH_SECRET=${jwt_refresh_secret}

# === System Settings ===
ALLOW_USER_API_KEY=false
EOF

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
version: '3.8'

services:
  db:
    image: postgres:16-alpine
    container_name: ${DB_CONTAINER_NAME}
    restart: unless-stopped
    environment:
      POSTGRES_USER: \${POSTGRES_USER:-dreamtrans}
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD:-dreamtrans}
      POSTGRES_DB: dreamtrans
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U dreamtrans -d dreamtrans"]
      interval: 5s
      timeout: 5s
      retries: 5

  app:
    image: ghcr.io/soaringjerry/dreamtrans:${IMAGE_TAG}
    container_name: ${CONTAINER_NAME}
    restart: unless-stopped
    ports:
      - "${PORT}:8080"
    environment:
      - SM_API_KEY=\${SM_API_KEY}
      - OPENAI_API_KEY=\${OPENAI_API_KEY:-}
      - OPENAI_BASE=\${OPENAI_BASE:-https://api.openai.com/v1}
      - DATABASE_URL=\${DATABASE_URL}
      - JWT_SECRET=\${JWT_SECRET}
      - JWT_REFRESH_SECRET=\${JWT_REFRESH_SECRET:-}
      - ALLOW_USER_API_KEY=\${ALLOW_USER_API_KEY:-false}
    volumes:
      - appdata:/app/data
    depends_on:
      db:
        condition: service_healthy

volumes:
  pgdata:
  appdata:
EOF

    success "Docker Compose file created"
}

# Start services
start_services() {
    info "Starting DreamTrans..."
    cd "$INSTALL_DIR"

    # Pull latest image
    info "Pulling latest image..."
    $COMPOSE_CMD pull

    # Start services
    $COMPOSE_CMD up -d

    # Wait for services to be ready
    info "Waiting for services to start..."
    sleep 8

    # Check if running
    if docker ps | grep -q "$CONTAINER_NAME"; then
        success "DreamTrans is running!"
    else
        error "Failed to start DreamTrans. Check logs with: cd $INSTALL_DIR && $COMPOSE_CMD logs"
        exit 1
    fi
}

# Update installation
update_installation() {
    info "Updating DreamTrans..."

    if [[ ! -d "$INSTALL_DIR" ]]; then
        error "Installation not found at $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"

    # Pull latest image
    info "Pulling latest image..."
    $COMPOSE_CMD pull

    # Restart services
    info "Restarting services..."
    $COMPOSE_CMD up -d

    success "DreamTrans updated successfully!"
}

# Uninstall
uninstall() {
    warn "This will remove DreamTrans and all its data!"
    read_input "Are you sure? (yes/no): " confirm "" false

    if [[ "$confirm" != "yes" ]]; then
        info "Uninstall cancelled"
        exit 0
    fi

    info "Stopping and removing containers..."
    cd "$INSTALL_DIR" 2>/dev/null && $COMPOSE_CMD down -v 2>/dev/null || true

    info "Removing installation directory..."
    rm -rf "$INSTALL_DIR"

    success "DreamTrans has been uninstalled"
}

# Print completion message
print_completion() {
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
    echo "    cd $INSTALL_DIR" >/dev/tty
    echo "    $COMPOSE_CMD logs -f        # View logs" >/dev/tty
    echo "    $COMPOSE_CMD restart        # Restart services" >/dev/tty
    echo "    $COMPOSE_CMD down           # Stop services" >/dev/tty
    echo "" >/dev/tty
    echo -e "  ${YELLOW}Update:${NC}" >/dev/tty
    echo "    curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash -s -- --update" >/dev/tty
    echo "" >/dev/tty
    echo -e "  ${YELLOW}Notes:${NC}" >/dev/tty
    echo "    - Both Classic (/) and Pro (/pro) UIs are available" >/dev/tty
    echo "    - Register at /pro to create an account" >/dev/tty
    echo "    - First registered user becomes admin" >/dev/tty
    echo "" >/dev/tty
}

# Main function
main() {
    print_banner
    parse_args "$@"

    # Handle special modes
    if [[ "$UNINSTALL_MODE" == "true" ]]; then
        check_prerequisites
        uninstall
        exit 0
    fi

    if [[ "$UPDATE_MODE" == "true" ]]; then
        check_prerequisites
        update_installation
        echo "" >/dev/tty
        success "Update complete!"
        echo -e "  ${CYAN}Classic UI:${NC}     http://localhost:${PORT}" >/dev/tty
        echo -e "  ${CYAN}Pro UI:${NC}         http://localhost:${PORT}/pro" >/dev/tty
        echo "" >/dev/tty
        exit 0
    fi

    # Normal installation
    check_prerequisites

    # Check if already installed
    if [[ -d "$INSTALL_DIR" && -f "$INSTALL_DIR/docker-compose.yml" ]]; then
        warn "DreamTrans is already installed at $INSTALL_DIR"
        read_input "Do you want to reinstall? This will overwrite config. (yes/no): " confirm "" false
        if [[ "$confirm" != "yes" ]]; then
            info "Use --update to update existing installation without changing config"
            exit 0
        fi
    fi

    prompt_api_keys
    create_directories
    generate_env_file
    generate_compose_file
    start_services
    print_completion
}

# Run main function
main "$@"
