#!/usr/bin/env bash
set -euo pipefail

# Nexara Install Script
# Usage: curl -fsSL https://raw.githubusercontent.com/nexara/nexara/master/scripts/install.sh | bash

REPO="https://github.com/nexara/nexara.git"
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

# ── Check prerequisites ──────────────────────────────────────────────────────

check_command() {
    if ! command -v "$1" &>/dev/null; then
        error "'$1' is required but not installed. Please install it first."
    fi
    ok "$1 found: $(command -v "$1")"
}

info "Checking prerequisites..."
check_command docker
check_command openssl
check_command git

# Check for docker compose (v2 plugin or standalone)
if docker compose version &>/dev/null; then
    COMPOSE_CMD="docker compose"
    ok "docker compose found (plugin)"
elif command -v docker-compose &>/dev/null; then
    COMPOSE_CMD="docker-compose"
    ok "docker-compose found (standalone)"
else
    error "Docker Compose is required. Install it with: https://docs.docker.com/compose/install/"
fi

# Check Docker is running
if ! docker info &>/dev/null; then
    error "Docker is not running. Start Docker and try again."
fi
ok "Docker daemon is running"

echo ""

# ── Clone repository ──────────────────────────────────────────────────────────

if [ -d "$INSTALL_DIR" ]; then
    warn "Directory $INSTALL_DIR already exists."
    read -rp "Overwrite? (y/N): " confirm
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

# Generate cryptographically secure secrets
JWT_SECRET=$(openssl rand -base64 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -base64 16 | tr -d '=/+')

sed -i "s|change-this-to-a-secure-random-string|${JWT_SECRET}|" .env
sed -i "s|change-this-to-a-32-byte-hex-key|${ENCRYPTION_KEY}|" .env
sed -i "s|changeme|${POSTGRES_PASSWORD}|g" .env

ok "Secrets generated and written to .env"

# Ask for domain
read -rp "Enter your domain (or press Enter for 'localhost'): " domain
domain="${domain:-localhost}"
sed -i "s|NEXARA_DOMAIN=localhost|NEXARA_DOMAIN=${domain}|" .env
ok "Domain set to: $domain"

echo ""

# ── Start the stack ───────────────────────────────────────────────────────────

info "Building and starting Nexara..."
$COMPOSE_CMD build --quiet
$COMPOSE_CMD up -d

echo ""
info "Waiting for services to become healthy..."

# Wait for API health check (up to 120 seconds)
max_attempts=60
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if curl -sf http://localhost:8080/healthz &>/dev/null; then
        break
    fi
    attempt=$((attempt + 1))
    sleep 2
done

if [ $attempt -ge $max_attempts ]; then
    warn "API server did not become healthy within 120 seconds."
    warn "Check logs with: $COMPOSE_CMD logs nexara-api"
else
    ok "API server is healthy"
fi

# Check frontend
if curl -sf http://localhost:80 &>/dev/null || curl -sf http://localhost:3000 &>/dev/null; then
    ok "Frontend is reachable"
fi

echo ""
echo -e "${GREEN}══════════════════════════════════════════${NC}"
echo -e "${GREEN}  Nexara is running!${NC}"
echo ""
if [ "$domain" = "localhost" ]; then
    echo -e "  Open ${BLUE}http://localhost${NC} in your browser"
else
    echo -e "  Open ${BLUE}https://${domain}${NC} in your browser"
fi
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
