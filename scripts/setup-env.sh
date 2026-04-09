#!/usr/bin/env bash
#
# Generate .env from .env.example with cryptographically secure secrets.
# Safe to run multiple times — skips if .env already exists (use -f to force).
#
# Usage:
#   ./scripts/setup-env.sh          # create .env if missing
#   ./scripts/setup-env.sh -f       # overwrite existing .env

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"
EXAMPLE_FILE="$PROJECT_ROOT/.env.example"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[+]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*"; exit 1; }

# Check for openssl
command -v openssl &>/dev/null || error "openssl is required but not found"

# Check for .env.example
[ -f "$EXAMPLE_FILE" ] || error ".env.example not found at $EXAMPLE_FILE"

# Handle existing .env
FORCE=false
[ "${1:-}" = "-f" ] && FORCE=true

if [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
    warn ".env already exists. Use -f to overwrite."
    exit 0
fi

# Copy template
cp "$EXAMPLE_FILE" "$ENV_FILE"

# Generate secrets
JWT_SECRET=$(openssl rand -base64 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -base64 16 | tr -d '=/+')

# Replace the postgres placeholder password
sed -i "s|changeme|${POSTGRES_PASSWORD}|g" "$ENV_FILE"

# JWT_SECRET and ENCRYPTION_KEY are commented out in .env.example
# (auto-generated at runtime if omitted). Append concrete values so the
# generated .env is fully self-contained.
echo "" >> "$ENV_FILE"
echo "# ---- Generated secrets (created by setup-env.sh) ----" >> "$ENV_FILE"
echo "JWT_SECRET=${JWT_SECRET}" >> "$ENV_FILE"
echo "ENCRYPTION_KEY=${ENCRYPTION_KEY}" >> "$ENV_FILE"

info "Generated .env with secure secrets"
info "  JWT_SECRET:      ${JWT_SECRET:0:8}..."
info "  ENCRYPTION_KEY:  ${ENCRYPTION_KEY:0:8}..."
info "  POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:0:4}..."
echo ""
info "Review and edit .env if needed, then run:"
echo "  docker compose up -d"
