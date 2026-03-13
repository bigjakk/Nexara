#!/usr/bin/env bash
set -euo pipefail

# Nexara Install Script
# Usage: curl -fsSL https://raw.githubusercontent.com/bigjakk/Nexara/master/scripts/install.sh | bash

REPO="https://github.com/bigjakk/Nexara.git"
INSTALL_DIR="${NEXARA_DIR:-$HOME/nexara}"
BRANCH="${NEXARA_BRANCH:-master}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

echo ""
echo -e "${GREEN}╔══════════════════════════════════════╗${NC}"
echo -e "${GREEN}║         Nexara Installer             ║${NC}"
echo -e "${GREEN}║  Centralized Proxmox Management      ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════╝${NC}"
echo ""

# ── Detect privilege level ───────────────────────────────────────────────────

SUDO=""
if [ "$(id -u)" -eq 0 ]; then
    ok "Running as root"
elif command -v sudo &>/dev/null && sudo -n true 2>/dev/null; then
    SUDO="sudo"
    ok "sudo available (passwordless)"
elif command -v sudo &>/dev/null; then
    # sudo exists but needs a password — test it
    info "sudo requires a password. You may be prompted."
    if sudo true < /dev/tty; then
        SUDO="sudo"
        ok "sudo authenticated"
    else
        error "Failed to authenticate with sudo. Run as root or configure sudo."
    fi
else
    error "This script requires root privileges. Run with: sudo bash or install sudo."
fi

# ── Detect package manager ───────────────────────────────────────────────────

install_pkg() {
    if command -v apt-get &>/dev/null; then
        $SUDO apt-get update -qq && $SUDO apt-get install -y -qq "$@"
    elif command -v dnf &>/dev/null; then
        $SUDO dnf install -y "$@"
    elif command -v yum &>/dev/null; then
        $SUDO yum install -y "$@"
    elif command -v pacman &>/dev/null; then
        $SUDO pacman -S --noconfirm "$@"
    elif command -v zypper &>/dev/null; then
        $SUDO zypper install -y "$@"
    else
        error "Could not detect package manager. Please install '$*' manually."
    fi
}

install_docker() {
    info "Installing Docker via get.docker.com..."
    curl -fsSL https://get.docker.com | $SUDO sh
    $SUDO systemctl enable --now docker
    # Add current user to docker group (skip if already root)
    if [ "$(id -u)" -ne 0 ] && ! groups | grep -q docker; then
        $SUDO usermod -aG docker "$USER"
        warn "Added $USER to docker group. You may need to log out and back in."
    fi
}

prompt_install() {
    local cmd="$1"
    warn "'$cmd' is not installed."
    read -rp "  Install $cmd now? (Y/n): " answer < /dev/tty
    answer="${answer:-y}"
    if [[ "$answer" != [yY] ]]; then
        error "Cannot continue without '$cmd'. Please install it and try again."
    fi
}

# ── Check prerequisites ──────────────────────────────────────────────────────

info "Checking prerequisites..."

# Docker
if ! command -v docker &>/dev/null; then
    prompt_install docker
    install_docker
fi
ok "docker found: $(command -v docker)"

# openssl
if ! command -v openssl &>/dev/null; then
    prompt_install openssl
    install_pkg openssl
fi
ok "openssl found: $(command -v openssl)"

# git
if ! command -v git &>/dev/null; then
    prompt_install git
    install_pkg git
fi
ok "git found: $(command -v git)"

# curl (needed for health checks later)
if ! command -v curl &>/dev/null; then
    prompt_install curl
    install_pkg curl
fi
ok "curl found: $(command -v curl)"

# Check for docker compose (v2 plugin or standalone)
if docker compose version &>/dev/null; then
    COMPOSE_CMD="docker compose"
    ok "docker compose found (plugin)"
elif command -v docker-compose &>/dev/null; then
    COMPOSE_CMD="docker-compose"
    ok "docker-compose found (standalone)"
else
    warn "Docker Compose is not installed."
    read -rp "  Install docker-compose-plugin now? (Y/n): " answer < /dev/tty
    answer="${answer:-y}"
    if [[ "$answer" != [yY] ]]; then
        error "Cannot continue without Docker Compose. Please install it and try again."
    fi
    install_pkg docker-compose-plugin
    COMPOSE_CMD="docker compose"
    ok "docker compose installed"
fi

# Check Docker is running (try with $SUDO if direct access fails)
if ! docker info &>/dev/null; then
    if [ -n "$SUDO" ] && $SUDO docker info &>/dev/null; then
        # Docker works with sudo but not without — offer to fix
        warn "Docker requires sudo to run."
        read -rp "  Add $USER to the docker group for sudoless access? (Y/n): " answer < /dev/tty
        answer="${answer:-y}"
        if [[ "$answer" == [yY] ]]; then
            $SUDO usermod -aG docker "$USER"
            ok "Added $USER to docker group"
            warn "Group change takes effect on next login. Using sudo for this session."
        fi
        # Use sudo-wrapped docker for the rest of this run
        docker() { $SUDO "$(command -v docker)" "$@"; }
        export -f docker 2>/dev/null || true
    else
        warn "Docker daemon is not running."
        read -rp "  Start Docker now? (Y/n): " answer < /dev/tty
        answer="${answer:-y}"
        if [[ "$answer" != [yY] ]]; then
            error "Docker must be running to continue."
        fi
        $SUDO systemctl start docker
    fi
fi
ok "Docker daemon is running"

echo ""

# ── Clone repository ──────────────────────────────────────────────────────────

if [ -d "$INSTALL_DIR" ]; then
    warn "Directory $INSTALL_DIR already exists."
    read -rp "Overwrite? (y/N): " confirm < /dev/tty
    if [[ "$confirm" != [yY] ]]; then
        error "Aborted. Remove or rename the existing directory and try again."
    fi
    rm -rf "$INSTALL_DIR"
fi

info "Cloning Nexara to $INSTALL_DIR..."
git clone --depth 1 --branch "$BRANCH" "$REPO" "$INSTALL_DIR"
cd "$INSTALL_DIR"
ok "Repository cloned"

echo ""

# ── Generate configuration ───────────────────────────────────────────────────

info "Generating configuration..."
cp .env.example .env

# Generate a secure database password (secrets are auto-generated by the app)
POSTGRES_PASSWORD=$(openssl rand -base64 16 | tr -d '=/+')
sed -i "s|changeme|${POSTGRES_PASSWORD}|g" .env

ok "Database password generated and written to .env"
info "JWT_SECRET and ENCRYPTION_KEY will be auto-generated on first start"

echo ""

# ── Pull and start the stack ─────────────────────────────────────────────────

info "Pulling images and starting Nexara..."
$COMPOSE_CMD pull --quiet
$COMPOSE_CMD up -d

echo ""
info "Waiting for services to become healthy..."

# Wait for health check (up to 120 seconds)
# Port 80 is mapped to the unified nexara container
max_attempts=60
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if curl -sf http://localhost:80/healthz &>/dev/null; then
        break
    fi
    attempt=$((attempt + 1))
    sleep 2
done

if [ $attempt -ge $max_attempts ]; then
    warn "Nexara did not become healthy within 120 seconds."
    warn "Check logs with: $COMPOSE_CMD logs nexara"
else
    ok "Nexara is healthy"
fi

echo ""
echo -e "${GREEN}══════════════════════════════════════════${NC}"
echo -e "${GREEN}  Nexara is running!${NC}"
echo ""
echo -e "  Open ${BLUE}http://localhost${NC} in your browser"
echo ""
echo -e "  On first visit, you'll create your admin account."
echo -e "  Then add your Proxmox cluster to get started."
echo ""
echo -e "${GREEN}══════════════════════════════════════════${NC}"
echo ""
echo -e "  Useful commands:"
echo -e "    cd $INSTALL_DIR"
echo -e "    $COMPOSE_CMD logs -f          # View logs"
echo -e "    $COMPOSE_CMD down             # Stop"
echo -e "    $COMPOSE_CMD up -d            # Start"
echo -e "    $COMPOSE_CMD pull && $COMPOSE_CMD up -d  # Update"
echo ""
