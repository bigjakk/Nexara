#!/usr/bin/env bash
# test-api-key-permissions.sh
# Tests EVERY Nexara API endpoint with both an admin and a viewer API key
# to verify RBAC enforcement through API key authentication.
#
# Usage:
#   ./test-api-key-permissions.sh [ADMIN_KEY] [VIEWER_KEY]
#
# Defaults to environment variables NEXARA_ADMIN_KEY / NEXARA_VIEWER_KEY if args not provided.

set -uo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
ADMIN_KEY="${1:-${NEXARA_ADMIN_KEY:-}}"
VIEWER_KEY="${2:-${NEXARA_VIEWER_KEY:-}}"

if [ -z "$ADMIN_KEY" ] || [ -z "$VIEWER_KEY" ]; then
    echo "Usage: $0 <admin-api-key> <viewer-api-key>"
    echo "  or set NEXARA_ADMIN_KEY and NEXARA_VIEWER_KEY env vars"
    exit 1
fi

BASE="https://localhost"
CID="3ac12021-58f0-4fa6-855d-bd070a474647"

# Dummy IDs for sub-resource endpoints (will get 404 which is fine — not 403)
FAKE_UUID="00000000-0000-0000-0000-000000000000"
FAKE_UPID="UPID:node1:00001234:12345678:00000001:task:100:user@pam:"

# ---------------------------------------------------------------------------
# Counters
# ---------------------------------------------------------------------------
total=0
passed=0
failed=0

# ---------------------------------------------------------------------------
# Colors
# ---------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# ---------------------------------------------------------------------------
# Helper: test a single endpoint
# ---------------------------------------------------------------------------
test_endpoint() {
    local label="$1"
    local method="$2"
    local path="$3"
    local key="$4"
    local expected="$5"
    local body="${6:-}"

    if [ -n "$body" ]; then
        actual=$(curl -sk -o /dev/null -w "%{http_code}" -X "$method" \
            -H "Authorization: Bearer $key" \
            -H "Content-Type: application/json" \
            -d "$body" \
            "${BASE}${path}" 2>/dev/null)
    else
        actual=$(curl -sk -o /dev/null -w "%{http_code}" -X "$method" \
            -H "Authorization: Bearer $key" \
            "${BASE}${path}" 2>/dev/null)
    fi

    if [ "$actual" = "$expected" ]; then
        printf "  ${GREEN}PASS${NC}  %-7s %-65s => %s (expected %s)\n" "$method" "$path" "$actual" "$expected"
        ((passed++))
    else
        printf "  ${RED}FAIL${NC}  %-7s %-65s => %s (expected %s)  ${RED}[%s]${NC}\n" "$method" "$path" "$actual" "$expected" "$label"
        ((failed++))
    fi
    ((total++))
}

# ---------------------------------------------------------------------------
# Helper: test admin key — expect NOT 401 and NOT 403
# ---------------------------------------------------------------------------
test_admin() {
    local label="$1"
    local method="$2"
    local path="$3"
    local body="${4:-}"

    if [ -n "$body" ]; then
        actual=$(curl -sk -o /dev/null -w "%{http_code}" -X "$method" \
            -H "Authorization: Bearer $ADMIN_KEY" \
            -H "Content-Type: application/json" \
            -d "$body" \
            "${BASE}${path}" 2>/dev/null)
    else
        actual=$(curl -sk -o /dev/null -w "%{http_code}" -X "$method" \
            -H "Authorization: Bearer $ADMIN_KEY" \
            "${BASE}${path}" 2>/dev/null)
    fi

    if [ "$actual" != "401" ] && [ "$actual" != "403" ]; then
        printf "  ${GREEN}PASS${NC}  %-7s %-65s => %s (not 401/403)\n" "$method" "$path" "$actual"
        ((passed++))
    else
        printf "  ${RED}FAIL${NC}  %-7s %-65s => %s (expected NOT 401/403)  ${RED}[%s]${NC}\n" "$method" "$path" "$actual" "$label"
        ((failed++))
    fi
    ((total++))
}

# ---------------------------------------------------------------------------
# Connectivity check
# ---------------------------------------------------------------------------
echo ""
echo -e "${BOLD}===========================================================${NC}"
echo -e "${BOLD}  Nexara API Key RBAC Permission Test Suite${NC}"
echo -e "${BOLD}===========================================================${NC}"
echo ""
echo "  Target:     $BASE"
echo "  Cluster ID: $CID"
echo ""

http_code=$(curl -sk -o /dev/null -w "%{http_code}" "${BASE}/healthz" 2>/dev/null || true)
if [ "$http_code" != "200" ]; then
    echo -e "${RED}ERROR: Cannot reach ${BASE}/healthz (got $http_code). Is Nexara running?${NC}"
    exit 1
fi
echo -e "  ${GREEN}Nexara is reachable (healthz => 200)${NC}"
echo ""

# ===================================================================
# SECTION 1: PUBLIC ENDPOINTS (no auth needed)
# ===================================================================
echo -e "${CYAN}${BOLD}--- Public Endpoints (no auth required) ---${NC}"

test_endpoint "healthz"          GET "/healthz"                  "$VIEWER_KEY" "200"
test_endpoint "version"          GET "/api/v1/version"           "$VIEWER_KEY" "200"
test_endpoint "setup-status"     GET "/api/v1/auth/setup-status" "$VIEWER_KEY" "200"
test_endpoint "sso-status"       GET "/api/v1/auth/sso-status"   "$VIEWER_KEY" "200"

echo ""

# ===================================================================
# SECTION 2: AUTH ENDPOINTS (need auth, no specific RBAC perm)
# ===================================================================
echo -e "${CYAN}${BOLD}--- Auth / Self-Service Endpoints ---${NC}"

test_endpoint "logout"           POST "/api/v1/auth/logout"          "$VIEWER_KEY" "200"
test_endpoint "me"               GET  "/api/v1/auth/me"              "$VIEWER_KEY" "200"
test_endpoint "profile"          PUT  "/api/v1/auth/profile"         "$VIEWER_KEY" "200" '{"display_name":"Test"}'
test_endpoint "totp-status"      GET  "/api/v1/auth/totp/status"     "$VIEWER_KEY" "200"

echo ""

# ===================================================================
# SECTION 3: VIEWER KEY — READ ENDPOINTS (expect 200)
# ===================================================================
echo -e "${CYAN}${BOLD}--- Viewer Key: Read Endpoints (expect 200) ---${NC}"

# Clusters
test_endpoint "list-clusters"       GET "/api/v1/clusters"                            "$VIEWER_KEY" "200"
test_endpoint "get-cluster"         GET "/api/v1/clusters/${CID}"                     "$VIEWER_KEY" "200"

# Cluster sub-resources
test_endpoint "nodes"               GET "/api/v1/clusters/${CID}/nodes"               "$VIEWER_KEY" "200"
test_endpoint "vms"                 GET "/api/v1/clusters/${CID}/vms"                 "$VIEWER_KEY" "200"
test_endpoint "containers"          GET "/api/v1/clusters/${CID}/containers"          "$VIEWER_KEY" "200"
test_endpoint "storage"             GET "/api/v1/clusters/${CID}/storage"             "$VIEWER_KEY" "200"
test_endpoint "metrics"             GET "/api/v1/clusters/${CID}/metrics"             "$VIEWER_KEY" "200"
test_endpoint "networks"            GET "/api/v1/clusters/${CID}/networks"            "$VIEWER_KEY" "200"
test_endpoint "pools"               GET "/api/v1/clusters/${CID}/pools"               "$VIEWER_KEY" "200"

# Firewall
test_endpoint "fw-rules"            GET "/api/v1/clusters/${CID}/firewall/rules"      "$VIEWER_KEY" "200"
test_endpoint "fw-options"          GET "/api/v1/clusters/${CID}/firewall/options"     "$VIEWER_KEY" "200"
test_endpoint "fw-aliases"          GET "/api/v1/clusters/${CID}/firewall/aliases"     "$VIEWER_KEY" "200"
test_endpoint "fw-ipset"            GET "/api/v1/clusters/${CID}/firewall/ipset"       "$VIEWER_KEY" "200"
test_endpoint "fw-groups"           GET "/api/v1/clusters/${CID}/firewall/groups"      "$VIEWER_KEY" "200"
test_endpoint "fw-log"              GET "/api/v1/clusters/${CID}/firewall/log"         "$VIEWER_KEY" "200"

# SDN
test_endpoint "sdn-zones"           GET "/api/v1/clusters/${CID}/sdn/zones"           "$VIEWER_KEY" "200"
test_endpoint "sdn-vnets"           GET "/api/v1/clusters/${CID}/sdn/vnets"           "$VIEWER_KEY" "200"
test_endpoint "sdn-controllers"     GET "/api/v1/clusters/${CID}/sdn/controllers"     "$VIEWER_KEY" "200"
test_endpoint "sdn-ipams"           GET "/api/v1/clusters/${CID}/sdn/ipams"           "$VIEWER_KEY" "200"
test_endpoint "sdn-dns"             GET "/api/v1/clusters/${CID}/sdn/dns"             "$VIEWER_KEY" "200"

# CVE / Security
test_endpoint "cve-scans"           GET "/api/v1/clusters/${CID}/cve-scans"           "$VIEWER_KEY" "200"
test_endpoint "security-posture"    GET "/api/v1/clusters/${CID}/security-posture"    "$VIEWER_KEY" "200"
test_endpoint "cve-schedule"        GET "/api/v1/clusters/${CID}/cve-scan-schedule"   "$VIEWER_KEY" "200"

# DRS
test_endpoint "drs-config"          GET "/api/v1/clusters/${CID}/drs/config"          "$VIEWER_KEY" "200"
test_endpoint "drs-rules"           GET "/api/v1/clusters/${CID}/drs/rules"           "$VIEWER_KEY" "200"
test_endpoint "drs-history"         GET "/api/v1/clusters/${CID}/drs/history"         "$VIEWER_KEY" "200"
test_endpoint "drs-ha-rules"        GET "/api/v1/clusters/${CID}/drs/ha-rules"        "$VIEWER_KEY" "200"

# Rolling Updates
test_endpoint "rolling-updates"     GET "/api/v1/clusters/${CID}/rolling-updates"     "$VIEWER_KEY" "200"

# Cluster Options / Config
test_endpoint "options"              GET "/api/v1/clusters/${CID}/options"              "$VIEWER_KEY" "200"
test_endpoint "description"          GET "/api/v1/clusters/${CID}/description"          "$VIEWER_KEY" "200"
test_endpoint "tags"                 GET "/api/v1/clusters/${CID}/tags"                 "$VIEWER_KEY" "200"
test_endpoint "config"               GET "/api/v1/clusters/${CID}/config"               "$VIEWER_KEY" "200"
test_endpoint "config-join"          GET "/api/v1/clusters/${CID}/config/join"          "$VIEWER_KEY" "200"
test_endpoint "config-nodes"         GET "/api/v1/clusters/${CID}/config/nodes"         "$VIEWER_KEY" "200"

# HA
test_endpoint "ha-resources"         GET "/api/v1/clusters/${CID}/ha/resources"         "$VIEWER_KEY" "200"
test_endpoint "ha-groups"            GET "/api/v1/clusters/${CID}/ha/groups"            "$VIEWER_KEY" "200"
test_endpoint "ha-status"            GET "/api/v1/clusters/${CID}/ha/status"            "$VIEWER_KEY" "200"
test_endpoint "ha-rules"             GET "/api/v1/clusters/${CID}/ha/rules"             "$VIEWER_KEY" "200"

# Replication
test_endpoint "replication"          GET "/api/v1/clusters/${CID}/replication"          "$VIEWER_KEY" "200"

# ACME
test_endpoint "acme-accounts"        GET "/api/v1/clusters/${CID}/acme/accounts"        "$VIEWER_KEY" "200"
test_endpoint "acme-plugins"         GET "/api/v1/clusters/${CID}/acme/plugins"         "$VIEWER_KEY" "200"
test_endpoint "acme-challenge"       GET "/api/v1/clusters/${CID}/acme/challenge-schema" "$VIEWER_KEY" "200"
test_endpoint "acme-directories"     GET "/api/v1/clusters/${CID}/acme/directories"     "$VIEWER_KEY" "200"
test_endpoint "acme-tos"             GET "/api/v1/clusters/${CID}/acme/tos"             "$VIEWER_KEY" "200"

# Metric Servers
test_endpoint "metric-servers"       GET "/api/v1/clusters/${CID}/metric-servers"       "$VIEWER_KEY" "200"

# Migrations (cluster-scoped)
test_endpoint "cluster-migrations"   GET "/api/v1/clusters/${CID}/migrations"           "$VIEWER_KEY" "200"

# Backup Jobs
test_endpoint "backup-jobs"          GET "/api/v1/clusters/${CID}/backup-jobs"          "$VIEWER_KEY" "200"

# Schedules
test_endpoint "schedules"            GET "/api/v1/clusters/${CID}/schedules"            "$VIEWER_KEY" "200"

# Audit log (cluster-scoped)
test_endpoint "cluster-audit"        GET "/api/v1/clusters/${CID}/audit-log"            "$VIEWER_KEY" "200"

# Alerts (cluster-scoped)
test_endpoint "cluster-alerts"       GET "/api/v1/clusters/${CID}/alerts"               "$VIEWER_KEY" "200"
test_endpoint "cluster-alerts-count" GET "/api/v1/clusters/${CID}/alerts/count"         "$VIEWER_KEY" "200"

# Maintenance Windows
test_endpoint "maint-windows"        GET "/api/v1/clusters/${CID}/maintenance-windows"  "$VIEWER_KEY" "200"

# Ceph (may 500 if no ceph configured — accept 200 or 500)
ceph_code=$(curl -sk -o /dev/null -w "%{http_code}" -X GET \
    -H "Authorization: Bearer $VIEWER_KEY" \
    "${BASE}/api/v1/clusters/${CID}/ceph/status" 2>/dev/null)
if [ "$ceph_code" = "200" ] || [ "$ceph_code" = "500" ]; then
    printf "  ${GREEN}PASS${NC}  %-7s %-65s => %s (200 or 500 ok — ceph may not exist)\n" "GET" "/api/v1/clusters/${CID}/ceph/status" "$ceph_code"
    ((passed++))
else
    printf "  ${RED}FAIL${NC}  %-7s %-65s => %s (expected 200 or 500)\n" "GET" "/api/v1/clusters/${CID}/ceph/status" "$ceph_code"
    ((failed++))
fi
((total++))

# PBS
test_endpoint "pbs-servers"          GET "/api/v1/pbs-servers"                          "$VIEWER_KEY" "200"
test_endpoint "pbs-snapshots"        GET "/api/v1/pbs-snapshots"                        "$VIEWER_KEY" "200"
test_endpoint "backup-coverage"      GET "/api/v1/backup-coverage"                      "$VIEWER_KEY" "200"

# Firewall Templates
test_endpoint "fw-templates"         GET "/api/v1/firewall-templates"                   "$VIEWER_KEY" "200"

# Global Migrations
test_endpoint "migrations"           GET "/api/v1/migrations"                           "$VIEWER_KEY" "200"

# Alerts (global)
test_endpoint "alerts"               GET "/api/v1/alerts"                               "$VIEWER_KEY" "200"
test_endpoint "alerts-summary"       GET "/api/v1/alerts/summary"                       "$VIEWER_KEY" "200"

# Alert Rules
test_endpoint "alert-rules"          GET "/api/v1/alert-rules"                          "$VIEWER_KEY" "200"

# Notification Channels
test_endpoint "notif-channels"       GET "/api/v1/notification-channels"                "$VIEWER_KEY" "200"

# Reports
test_endpoint "report-schedules"     GET "/api/v1/reports/schedules"                    "$VIEWER_KEY" "200"
test_endpoint "report-runs"          GET "/api/v1/reports/runs"                         "$VIEWER_KEY" "200"

# RBAC
test_endpoint "rbac-roles"           GET "/api/v1/rbac/roles"                           "$VIEWER_KEY" "200"
test_endpoint "rbac-permissions"     GET "/api/v1/rbac/permissions"                     "$VIEWER_KEY" "200"
test_endpoint "rbac-my-perms"        GET "/api/v1/rbac/me/permissions"                  "$VIEWER_KEY" "200"

# Users
test_endpoint "users-list"           GET "/api/v1/users"                                "$VIEWER_KEY" "200"

# Tasks
test_endpoint "tasks"                GET "/api/v1/tasks"                                "$VIEWER_KEY" "200"

# Audit log (global)
test_endpoint "audit-log"            GET "/api/v1/audit-log"                            "$VIEWER_KEY" "200"
test_endpoint "audit-recent"         GET "/api/v1/audit-log/recent"                     "$VIEWER_KEY" "200"
test_endpoint "audit-actions"        GET "/api/v1/audit-log/actions"                    "$VIEWER_KEY" "200"
test_endpoint "audit-users"          GET "/api/v1/audit-log/users"                      "$VIEWER_KEY" "200"

# Search
test_endpoint "search"               GET "/api/v1/search?q=test"                        "$VIEWER_KEY" "200"

# Settings
test_endpoint "settings"             GET "/api/v1/settings"                             "$VIEWER_KEY" "200"
test_endpoint "settings-branding"    GET "/api/v1/settings/branding"                    "$VIEWER_KEY" "200"

# API Keys (self-service — viewer has manage:api_key)
test_endpoint "api-keys-list"        GET "/api/v1/api-keys"                             "$VIEWER_KEY" "200"

# API Docs
test_endpoint "api-docs"             GET "/api/v1/api-docs"                             "$VIEWER_KEY" "200"

echo ""

# ===================================================================
# SECTION 4: VIEWER KEY — WRITE ENDPOINTS (expect 403)
# ===================================================================
echo -e "${CYAN}${BOLD}--- Viewer Key: Write Endpoints (expect 403) ---${NC}"

# Cluster management
test_endpoint "create-cluster"       POST   "/api/v1/clusters"                          "$VIEWER_KEY" "403" '{"name":"test","host":"1.2.3.4","token_id":"t","token_secret":"s"}'
test_endpoint "update-cluster"       PUT    "/api/v1/clusters/${CID}"                   "$VIEWER_KEY" "403" '{"name":"renamed"}'
test_endpoint "delete-cluster"       DELETE "/api/v1/clusters/${CID}"                   "$VIEWER_KEY" "403"
test_endpoint "fetch-fingerprint"    POST   "/api/v1/clusters/fetch-fingerprint"        "$VIEWER_KEY" "403" '{"host":"1.2.3.4"}'

# VM write ops
test_endpoint "create-vm"            POST   "/api/v1/clusters/${CID}/vms"               "$VIEWER_KEY" "403" '{}'
test_endpoint "vm-action"            POST   "/api/v1/clusters/${CID}/vms/100/status"    "$VIEWER_KEY" "403" '{"action":"start"}'
test_endpoint "clone-vm"             POST   "/api/v1/clusters/${CID}/vms/100/clone"     "$VIEWER_KEY" "403" '{}'
test_endpoint "convert-vm-tmpl"      POST   "/api/v1/clusters/${CID}/vms/100/convert-to-template" "$VIEWER_KEY" "403"
test_endpoint "clone-to-tmpl"        POST   "/api/v1/clusters/${CID}/vms/100/clone-to-template"   "$VIEWER_KEY" "403" '{}'
test_endpoint "migrate-vm"           POST   "/api/v1/clusters/${CID}/vms/100/migrate"   "$VIEWER_KEY" "403" '{}'
test_endpoint "destroy-vm"           DELETE "/api/v1/clusters/${CID}/vms/100"            "$VIEWER_KEY" "403"
test_endpoint "create-vm-snap"       POST   "/api/v1/clusters/${CID}/vms/100/snapshots" "$VIEWER_KEY" "403" '{"name":"snap1"}'
test_endpoint "delete-vm-snap"       DELETE "/api/v1/clusters/${CID}/vms/100/snapshots/snap1"     "$VIEWER_KEY" "403"
test_endpoint "rollback-vm-snap"     POST   "/api/v1/clusters/${CID}/vms/100/snapshots/snap1/rollback" "$VIEWER_KEY" "403"
test_endpoint "set-vm-config"        PUT    "/api/v1/clusters/${CID}/vms/100/config"    "$VIEWER_KEY" "403" '{}'
test_endpoint "resize-vm-disk"       POST   "/api/v1/clusters/${CID}/vms/100/disks/resize"  "$VIEWER_KEY" "403" '{}'
test_endpoint "move-vm-disk"         POST   "/api/v1/clusters/${CID}/vms/100/disks/move"    "$VIEWER_KEY" "403" '{}'
test_endpoint "attach-vm-disk"       POST   "/api/v1/clusters/${CID}/vms/100/disks/attach"  "$VIEWER_KEY" "403" '{}'
test_endpoint "detach-vm-disk"       POST   "/api/v1/clusters/${CID}/vms/100/disks/detach"  "$VIEWER_KEY" "403" '{}'
test_endpoint "change-media"         POST   "/api/v1/clusters/${CID}/vms/100/media"     "$VIEWER_KEY" "403" '{}'
test_endpoint "set-vm-pool"          PUT    "/api/v1/clusters/${CID}/vms/100/pool"      "$VIEWER_KEY" "403" '{"pool":"testpool"}'

# Container write ops
test_endpoint "create-ct"            POST   "/api/v1/clusters/${CID}/containers"                "$VIEWER_KEY" "403" '{}'
test_endpoint "ct-action"            POST   "/api/v1/clusters/${CID}/containers/100/status"     "$VIEWER_KEY" "403" '{"action":"start"}'
test_endpoint "clone-ct"             POST   "/api/v1/clusters/${CID}/containers/100/clone"      "$VIEWER_KEY" "403" '{}'
test_endpoint "convert-ct-tmpl"      POST   "/api/v1/clusters/${CID}/containers/100/convert-to-template" "$VIEWER_KEY" "403"
test_endpoint "clone-ct-to-tmpl"     POST   "/api/v1/clusters/${CID}/containers/100/clone-to-template"   "$VIEWER_KEY" "403" '{}'
test_endpoint "migrate-ct"           POST   "/api/v1/clusters/${CID}/containers/100/migrate"    "$VIEWER_KEY" "403" '{}'
test_endpoint "destroy-ct"           DELETE "/api/v1/clusters/${CID}/containers/100"             "$VIEWER_KEY" "403"
test_endpoint "create-ct-snap"       POST   "/api/v1/clusters/${CID}/containers/100/snapshots"  "$VIEWER_KEY" "403" '{"name":"snap1"}'
test_endpoint "delete-ct-snap"       DELETE "/api/v1/clusters/${CID}/containers/100/snapshots/snap1"     "$VIEWER_KEY" "403"
test_endpoint "rollback-ct-snap"     POST   "/api/v1/clusters/${CID}/containers/100/snapshots/snap1/rollback" "$VIEWER_KEY" "403"
test_endpoint "set-ct-config"        PUT    "/api/v1/clusters/${CID}/containers/100/config"     "$VIEWER_KEY" "403" '{}'
test_endpoint "resize-ct-disk"       POST   "/api/v1/clusters/${CID}/containers/100/disks/resize" "$VIEWER_KEY" "403" '{}'
test_endpoint "move-ct-volume"       POST   "/api/v1/clusters/${CID}/containers/100/volumes/move" "$VIEWER_KEY" "403" '{}'

# Storage write ops
test_endpoint "create-storage"       POST   "/api/v1/clusters/${CID}/storage"           "$VIEWER_KEY" "403" '{}'
test_endpoint "update-storage"       PUT    "/api/v1/clusters/${CID}/storage/local"     "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-storage"       DELETE "/api/v1/clusters/${CID}/storage/local"     "$VIEWER_KEY" "403"

# Network write ops
test_endpoint "create-network"       POST   "/api/v1/clusters/${CID}/networks/node1"    "$VIEWER_KEY" "403" '{}'
test_endpoint "update-network"       PUT    "/api/v1/clusters/${CID}/networks/node1/vmbr0"  "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-network"       DELETE "/api/v1/clusters/${CID}/networks/node1/vmbr0"  "$VIEWER_KEY" "403"
test_endpoint "apply-network"        POST   "/api/v1/clusters/${CID}/networks/node1/apply"  "$VIEWER_KEY" "403"
test_endpoint "revert-network"       POST   "/api/v1/clusters/${CID}/networks/node1/revert" "$VIEWER_KEY" "403"

# Firewall write ops
test_endpoint "create-fw-rule"       POST   "/api/v1/clusters/${CID}/firewall/rules"    "$VIEWER_KEY" "403" '{}'
test_endpoint "update-fw-rule"       PUT    "/api/v1/clusters/${CID}/firewall/rules/0"  "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-fw-rule"       DELETE "/api/v1/clusters/${CID}/firewall/rules/0"  "$VIEWER_KEY" "403"
test_endpoint "set-fw-options"       PUT    "/api/v1/clusters/${CID}/firewall/options"   "$VIEWER_KEY" "403" '{}'
test_endpoint "create-fw-alias"      POST   "/api/v1/clusters/${CID}/firewall/aliases"  "$VIEWER_KEY" "403" '{}'
test_endpoint "update-fw-alias"      PUT    "/api/v1/clusters/${CID}/firewall/aliases/test" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-fw-alias"      DELETE "/api/v1/clusters/${CID}/firewall/aliases/test" "$VIEWER_KEY" "403"
test_endpoint "create-fw-ipset"      POST   "/api/v1/clusters/${CID}/firewall/ipset"    "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-fw-ipset"      DELETE "/api/v1/clusters/${CID}/firewall/ipset/test"   "$VIEWER_KEY" "403"
test_endpoint "create-fw-group"      POST   "/api/v1/clusters/${CID}/firewall/groups"   "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-fw-group"      DELETE "/api/v1/clusters/${CID}/firewall/groups/test"  "$VIEWER_KEY" "403"

# VM Firewall write ops
test_endpoint "create-vm-fw-rule"    POST   "/api/v1/clusters/${CID}/vms/100/firewall/rules"   "$VIEWER_KEY" "403" '{}'
test_endpoint "update-vm-fw-rule"    PUT    "/api/v1/clusters/${CID}/vms/100/firewall/rules/0" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-vm-fw-rule"    DELETE "/api/v1/clusters/${CID}/vms/100/firewall/rules/0" "$VIEWER_KEY" "403"

# SDN write ops
test_endpoint "create-sdn-zone"      POST   "/api/v1/clusters/${CID}/sdn/zones"         "$VIEWER_KEY" "403" '{}'
test_endpoint "update-sdn-zone"      PUT    "/api/v1/clusters/${CID}/sdn/zones/test"    "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-sdn-zone"      DELETE "/api/v1/clusters/${CID}/sdn/zones/test"    "$VIEWER_KEY" "403"
test_endpoint "create-sdn-vnet"      POST   "/api/v1/clusters/${CID}/sdn/vnets"         "$VIEWER_KEY" "403" '{}'
test_endpoint "update-sdn-vnet"      PUT    "/api/v1/clusters/${CID}/sdn/vnets/test"    "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-sdn-vnet"      DELETE "/api/v1/clusters/${CID}/sdn/vnets/test"    "$VIEWER_KEY" "403"
test_endpoint "create-sdn-subnet"    POST   "/api/v1/clusters/${CID}/sdn/vnets/test/subnets"       "$VIEWER_KEY" "403" '{}'
test_endpoint "update-sdn-subnet"    PUT    "/api/v1/clusters/${CID}/sdn/vnets/test/subnets/test"  "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-sdn-subnet"    DELETE "/api/v1/clusters/${CID}/sdn/vnets/test/subnets/test"  "$VIEWER_KEY" "403"
test_endpoint "apply-sdn"            PUT    "/api/v1/clusters/${CID}/sdn/apply"          "$VIEWER_KEY" "403"
test_endpoint "create-sdn-ctrl"      POST   "/api/v1/clusters/${CID}/sdn/controllers"   "$VIEWER_KEY" "403" '{}'
test_endpoint "update-sdn-ctrl"      PUT    "/api/v1/clusters/${CID}/sdn/controllers/test"  "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-sdn-ctrl"      DELETE "/api/v1/clusters/${CID}/sdn/controllers/test"  "$VIEWER_KEY" "403"
test_endpoint "create-sdn-ipam"      POST   "/api/v1/clusters/${CID}/sdn/ipams"         "$VIEWER_KEY" "403" '{}'
test_endpoint "update-sdn-ipam"      PUT    "/api/v1/clusters/${CID}/sdn/ipams/test"    "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-sdn-ipam"      DELETE "/api/v1/clusters/${CID}/sdn/ipams/test"    "$VIEWER_KEY" "403"
test_endpoint "create-sdn-dns"       POST   "/api/v1/clusters/${CID}/sdn/dns"           "$VIEWER_KEY" "403" '{}'
test_endpoint "update-sdn-dns"       PUT    "/api/v1/clusters/${CID}/sdn/dns/test"      "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-sdn-dns"       DELETE "/api/v1/clusters/${CID}/sdn/dns/test"      "$VIEWER_KEY" "403"

# Firewall Templates write ops
test_endpoint "create-fw-tmpl"       POST   "/api/v1/firewall-templates"                "$VIEWER_KEY" "403" '{}'
test_endpoint "update-fw-tmpl"       PUT    "/api/v1/firewall-templates/${FAKE_UUID}"   "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-fw-tmpl"       DELETE "/api/v1/firewall-templates/${FAKE_UUID}"   "$VIEWER_KEY" "403"
test_endpoint "apply-fw-tmpl"        POST   "/api/v1/clusters/${CID}/firewall-templates/${FAKE_UUID}/apply" "$VIEWER_KEY" "403" '{}'

# CVE write ops
test_endpoint "trigger-cve-scan"     POST   "/api/v1/clusters/${CID}/cve-scans"         "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-cve-scan"      DELETE "/api/v1/clusters/${CID}/cve-scans/${FAKE_UUID}" "$VIEWER_KEY" "403"
test_endpoint "update-cve-schedule"  PUT    "/api/v1/clusters/${CID}/cve-scan-schedule" "$VIEWER_KEY" "403" '{}'

# DRS write ops
test_endpoint "update-drs-config"    PUT    "/api/v1/clusters/${CID}/drs/config"        "$VIEWER_KEY" "403" '{}'
test_endpoint "create-drs-rule"      POST   "/api/v1/clusters/${CID}/drs/rules"         "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-drs-rule"      DELETE "/api/v1/clusters/${CID}/drs/rules/${FAKE_UUID}" "$VIEWER_KEY" "403"
test_endpoint "trigger-drs-eval"     POST   "/api/v1/clusters/${CID}/drs/evaluate"      "$VIEWER_KEY" "403"
test_endpoint "create-drs-ha-rule"   POST   "/api/v1/clusters/${CID}/drs/ha-rules"      "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-drs-ha-rule"   DELETE "/api/v1/clusters/${CID}/drs/ha-rules/test" "$VIEWER_KEY" "403"

# Rolling Updates write ops
test_endpoint "create-rolling"       POST   "/api/v1/clusters/${CID}/rolling-updates"   "$VIEWER_KEY" "403" '{}'
test_endpoint "start-rolling"        POST   "/api/v1/clusters/${CID}/rolling-updates/${FAKE_UUID}/start"  "$VIEWER_KEY" "403"
test_endpoint "cancel-rolling"       POST   "/api/v1/clusters/${CID}/rolling-updates/${FAKE_UUID}/cancel" "$VIEWER_KEY" "403"
test_endpoint "pause-rolling"        POST   "/api/v1/clusters/${CID}/rolling-updates/${FAKE_UUID}/pause"  "$VIEWER_KEY" "403"
test_endpoint "resume-rolling"       POST   "/api/v1/clusters/${CID}/rolling-updates/${FAKE_UUID}/resume" "$VIEWER_KEY" "403"
test_endpoint "preflight-ha"         POST   "/api/v1/clusters/${CID}/rolling-updates/preflight-ha"  "$VIEWER_KEY" "403" '{}'

# SSH Credentials (Admin-only: manage:ssh_credentials)
test_endpoint "get-ssh-creds"        GET    "/api/v1/clusters/${CID}/ssh-credentials"   "$VIEWER_KEY" "403"
test_endpoint "upsert-ssh-creds"     PUT    "/api/v1/clusters/${CID}/ssh-credentials"   "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-ssh-creds"     DELETE "/api/v1/clusters/${CID}/ssh-credentials"   "$VIEWER_KEY" "403"
test_endpoint "test-ssh-conn"        POST   "/api/v1/clusters/${CID}/ssh-credentials/test" "$VIEWER_KEY" "403" '{}'

# Cluster Options write ops
test_endpoint "update-options"       PUT    "/api/v1/clusters/${CID}/options"            "$VIEWER_KEY" "403" '{}'
test_endpoint "update-description"   PUT    "/api/v1/clusters/${CID}/description"        "$VIEWER_KEY" "403" '{"description":"test"}'
test_endpoint "update-tags"          PUT    "/api/v1/clusters/${CID}/tags"               "$VIEWER_KEY" "403" '{"tags":[]}'

# HA write ops
test_endpoint "create-ha-resource"   POST   "/api/v1/clusters/${CID}/ha/resources"      "$VIEWER_KEY" "403" '{}'
test_endpoint "update-ha-resource"   PUT    "/api/v1/clusters/${CID}/ha/resources/vm:100" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-ha-resource"   DELETE "/api/v1/clusters/${CID}/ha/resources/vm:100" "$VIEWER_KEY" "403"
test_endpoint "create-ha-group"      POST   "/api/v1/clusters/${CID}/ha/groups"         "$VIEWER_KEY" "403" '{}'
test_endpoint "update-ha-group"      PUT    "/api/v1/clusters/${CID}/ha/groups/test"    "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-ha-group"      DELETE "/api/v1/clusters/${CID}/ha/groups/test"    "$VIEWER_KEY" "403"
test_endpoint "create-ha-rule"       POST   "/api/v1/clusters/${CID}/ha/rules"          "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-ha-rule"       DELETE "/api/v1/clusters/${CID}/ha/rules/test"     "$VIEWER_KEY" "403"

# Pool write ops
test_endpoint "create-pool"          POST   "/api/v1/clusters/${CID}/pools"             "$VIEWER_KEY" "403" '{}'
test_endpoint "update-pool"          PUT    "/api/v1/clusters/${CID}/pools/test"        "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-pool"          DELETE "/api/v1/clusters/${CID}/pools/test"        "$VIEWER_KEY" "403"

# Replication write ops
test_endpoint "create-repl"          POST   "/api/v1/clusters/${CID}/replication"       "$VIEWER_KEY" "403" '{}'
test_endpoint "update-repl"          PUT    "/api/v1/clusters/${CID}/replication/test"  "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-repl"          DELETE "/api/v1/clusters/${CID}/replication/test"  "$VIEWER_KEY" "403"
test_endpoint "trigger-repl-sync"    POST   "/api/v1/clusters/${CID}/replication/test/trigger" "$VIEWER_KEY" "403"

# ACME write ops
test_endpoint "create-acme-account"  POST   "/api/v1/clusters/${CID}/acme/accounts"     "$VIEWER_KEY" "403" '{}'
test_endpoint "update-acme-account"  PUT    "/api/v1/clusters/${CID}/acme/accounts/test" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-acme-account"  DELETE "/api/v1/clusters/${CID}/acme/accounts/test" "$VIEWER_KEY" "403"
test_endpoint "create-acme-plugin"   POST   "/api/v1/clusters/${CID}/acme/plugins"      "$VIEWER_KEY" "403" '{}'
test_endpoint "update-acme-plugin"   PUT    "/api/v1/clusters/${CID}/acme/plugins/test" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-acme-plugin"   DELETE "/api/v1/clusters/${CID}/acme/plugins/test" "$VIEWER_KEY" "403"

# Metric Servers write ops
test_endpoint "create-metric-srv"    POST   "/api/v1/clusters/${CID}/metric-servers"       "$VIEWER_KEY" "403" '{}'
test_endpoint "update-metric-srv"    PUT    "/api/v1/clusters/${CID}/metric-servers/test"   "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-metric-srv"    DELETE "/api/v1/clusters/${CID}/metric-servers/test"   "$VIEWER_KEY" "403"

# Ceph write ops
test_endpoint "create-ceph-pool"     POST   "/api/v1/clusters/${CID}/ceph/pools"        "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-ceph-pool"     DELETE "/api/v1/clusters/${CID}/ceph/pools/test"   "$VIEWER_KEY" "403"

# Alert write ops
test_endpoint "ack-alert"            POST   "/api/v1/alerts/${FAKE_UUID}/acknowledge"   "$VIEWER_KEY" "403"
test_endpoint "resolve-alert"        POST   "/api/v1/alerts/${FAKE_UUID}/resolve"       "$VIEWER_KEY" "403"
test_endpoint "create-alert-rule"    POST   "/api/v1/alert-rules"                       "$VIEWER_KEY" "403" '{}'
test_endpoint "update-alert-rule"    PUT    "/api/v1/alert-rules/${FAKE_UUID}"           "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-alert-rule"    DELETE "/api/v1/alert-rules/${FAKE_UUID}"           "$VIEWER_KEY" "403"

# Notification Channels write ops
test_endpoint "create-notif-chan"    POST   "/api/v1/notification-channels"              "$VIEWER_KEY" "403" '{}'
test_endpoint "update-notif-chan"    PUT    "/api/v1/notification-channels/${FAKE_UUID}" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-notif-chan"    DELETE "/api/v1/notification-channels/${FAKE_UUID}" "$VIEWER_KEY" "403"
test_endpoint "test-notif-chan"      POST   "/api/v1/notification-channels/${FAKE_UUID}/test" "$VIEWER_KEY" "403"

# Maintenance Windows write ops
test_endpoint "create-maint-win"    POST   "/api/v1/clusters/${CID}/maintenance-windows"              "$VIEWER_KEY" "403" '{}'
test_endpoint "update-maint-win"    PUT    "/api/v1/clusters/${CID}/maintenance-windows/${FAKE_UUID}" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-maint-win"    DELETE "/api/v1/clusters/${CID}/maintenance-windows/${FAKE_UUID}" "$VIEWER_KEY" "403"

# PBS write ops
test_endpoint "create-pbs"          POST   "/api/v1/pbs-servers"                        "$VIEWER_KEY" "403" '{}'
test_endpoint "update-pbs"          PUT    "/api/v1/pbs-servers/${FAKE_UUID}"            "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-pbs"          DELETE "/api/v1/pbs-servers/${FAKE_UUID}"            "$VIEWER_KEY" "403"

# Backup write ops
test_endpoint "restore-backup"      POST   "/api/v1/clusters/${CID}/restore"            "$VIEWER_KEY" "403" '{}'
test_endpoint "trigger-backup"      POST   "/api/v1/clusters/${CID}/backup"             "$VIEWER_KEY" "403" '{}'
test_endpoint "create-backup-job"   POST   "/api/v1/clusters/${CID}/backup-jobs"        "$VIEWER_KEY" "403" '{}'
test_endpoint "update-backup-job"   PUT    "/api/v1/clusters/${CID}/backup-jobs/test"   "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-backup-job"   DELETE "/api/v1/clusters/${CID}/backup-jobs/test"   "$VIEWER_KEY" "403"
test_endpoint "run-backup-job"      POST   "/api/v1/clusters/${CID}/backup-jobs/test/run" "$VIEWER_KEY" "403"

# Schedule write ops
test_endpoint "create-schedule"     POST   "/api/v1/clusters/${CID}/schedules"          "$VIEWER_KEY" "403" '{}'
test_endpoint "update-schedule"     PUT    "/api/v1/clusters/${CID}/schedules/${FAKE_UUID}" "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-schedule"     DELETE "/api/v1/clusters/${CID}/schedules/${FAKE_UUID}" "$VIEWER_KEY" "403"

# Migration write ops
test_endpoint "create-migration"    POST   "/api/v1/migrations"                         "$VIEWER_KEY" "403" '{}'
test_endpoint "check-migration"     POST   "/api/v1/migrations/${FAKE_UUID}/check"      "$VIEWER_KEY" "403"
test_endpoint "execute-migration"   POST   "/api/v1/migrations/${FAKE_UUID}/execute"    "$VIEWER_KEY" "403"
test_endpoint "cancel-migration"    POST   "/api/v1/migrations/${FAKE_UUID}/cancel"     "$VIEWER_KEY" "403"

# Report write ops
test_endpoint "create-report-sched" POST   "/api/v1/reports/schedules"                  "$VIEWER_KEY" "403" '{}'
test_endpoint "update-report-sched" PUT    "/api/v1/reports/schedules/${FAKE_UUID}"      "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-report-sched" DELETE "/api/v1/reports/schedules/${FAKE_UUID}"      "$VIEWER_KEY" "403"
test_endpoint "generate-report"     POST   "/api/v1/reports/generate"                   "$VIEWER_KEY" "403" '{}'

# RBAC write ops
test_endpoint "create-role"         POST   "/api/v1/rbac/roles"                         "$VIEWER_KEY" "403" '{}'
test_endpoint "update-role"         PUT    "/api/v1/rbac/roles/${FAKE_UUID}"             "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-role"         DELETE "/api/v1/rbac/roles/${FAKE_UUID}"             "$VIEWER_KEY" "403"
test_endpoint "assign-user-role"    POST   "/api/v1/rbac/users/${FAKE_UUID}/roles"      "$VIEWER_KEY" "403" '{}'
test_endpoint "revoke-user-role"    DELETE "/api/v1/rbac/users/${FAKE_UUID}/roles/${FAKE_UUID}" "$VIEWER_KEY" "403"

# User management (manage:user — Admin only)
test_endpoint "update-user"         PUT    "/api/v1/users/${FAKE_UUID}"                 "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-user"         DELETE "/api/v1/users/${FAKE_UUID}"                 "$VIEWER_KEY" "403"
test_endpoint "admin-reset-totp"    DELETE "/api/v1/users/${FAKE_UUID}/totp"            "$VIEWER_KEY" "403"

# Settings write ops (manage:settings — Admin only)
test_endpoint "upsert-setting"      PUT    "/api/v1/settings/test-key"                  "$VIEWER_KEY" "403" '{"value":"x"}'
test_endpoint "delete-setting"      DELETE "/api/v1/settings/test-key"                  "$VIEWER_KEY" "403"

# Admin API keys (manage:user — Admin only)
test_endpoint "admin-list-keys"     GET    "/api/v1/admin/api-keys"                     "$VIEWER_KEY" "403"
test_endpoint "admin-revoke-key"    DELETE "/api/v1/admin/api-keys/${FAKE_UUID}"         "$VIEWER_KEY" "403"

# Audit syslog config (manage:audit — Admin only)
test_endpoint "update-syslog"       PUT    "/api/v1/audit-log/syslog-config"            "$VIEWER_KEY" "403" '{}'
test_endpoint "test-syslog"         POST   "/api/v1/audit-log/syslog-test"              "$VIEWER_KEY" "403" '{}'
test_endpoint "export-audit"        GET    "/api/v1/audit-log/export"                   "$VIEWER_KEY" "403"

# LDAP (manage:ldap — Admin only)
test_endpoint "list-ldap"           GET    "/api/v1/ldap/configs"                       "$VIEWER_KEY" "403"
test_endpoint "create-ldap"         POST   "/api/v1/ldap/configs"                       "$VIEWER_KEY" "403" '{}'
test_endpoint "update-ldap"         PUT    "/api/v1/ldap/configs/${FAKE_UUID}"           "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-ldap"         DELETE "/api/v1/ldap/configs/${FAKE_UUID}"           "$VIEWER_KEY" "403"
test_endpoint "test-ldap"           POST   "/api/v1/ldap/configs/${FAKE_UUID}/test"      "$VIEWER_KEY" "403"
test_endpoint "sync-ldap"           POST   "/api/v1/ldap/configs/${FAKE_UUID}/sync"      "$VIEWER_KEY" "403"

# OIDC (manage:oidc — Admin only)
test_endpoint "list-oidc"           GET    "/api/v1/oidc/configs"                       "$VIEWER_KEY" "403"
test_endpoint "create-oidc"         POST   "/api/v1/oidc/configs"                       "$VIEWER_KEY" "403" '{}'
test_endpoint "update-oidc"         PUT    "/api/v1/oidc/configs/${FAKE_UUID}"           "$VIEWER_KEY" "403" '{}'
test_endpoint "delete-oidc"         DELETE "/api/v1/oidc/configs/${FAKE_UUID}"           "$VIEWER_KEY" "403"
test_endpoint "test-oidc"           POST   "/api/v1/oidc/configs/${FAKE_UUID}/test"      "$VIEWER_KEY" "403"

# Task write ops
test_endpoint "create-task"         POST   "/api/v1/tasks"                              "$VIEWER_KEY" "403" '{}'
test_endpoint "update-task"         PUT    "/api/v1/tasks/test"                         "$VIEWER_KEY" "403" '{}'
test_endpoint "clear-tasks"         DELETE "/api/v1/tasks"                              "$VIEWER_KEY" "403"

echo ""

# ===================================================================
# SECTION 5: ADMIN KEY — ALL ENDPOINTS (expect NOT 401/403)
# ===================================================================
echo -e "${CYAN}${BOLD}--- Admin Key: All Endpoints (expect NOT 401/403) ---${NC}"

# Public
test_admin "healthz"              GET    "/healthz"
test_admin "version"              GET    "/api/v1/version"
test_admin "setup-status"         GET    "/api/v1/auth/setup-status"
test_admin "sso-status"           GET    "/api/v1/auth/sso-status"

# Auth self-service
test_admin "me"                   GET    "/api/v1/auth/me"
test_admin "profile"              PUT    "/api/v1/auth/profile"    '{"display_name":"Admin Test"}'
test_admin "totp-status"          GET    "/api/v1/auth/totp/status"

# Clusters
test_admin "list-clusters"        GET    "/api/v1/clusters"
test_admin "get-cluster"          GET    "/api/v1/clusters/${CID}"
test_admin "create-cluster"       POST   "/api/v1/clusters"        '{"name":"test","host":"1.2.3.4","token_id":"t","token_secret":"s"}'
test_admin "update-cluster"       PUT    "/api/v1/clusters/${CID}" '{"name":"renamed"}'
test_admin "fetch-fingerprint"    POST   "/api/v1/clusters/fetch-fingerprint" '{"host":"1.2.3.4"}'

# Cluster sub-resources READ
test_admin "nodes"                GET    "/api/v1/clusters/${CID}/nodes"
test_admin "vms"                  GET    "/api/v1/clusters/${CID}/vms"
test_admin "containers"           GET    "/api/v1/clusters/${CID}/containers"
test_admin "storage"              GET    "/api/v1/clusters/${CID}/storage"
test_admin "metrics"              GET    "/api/v1/clusters/${CID}/metrics"
test_admin "networks"             GET    "/api/v1/clusters/${CID}/networks"
test_admin "pools"                GET    "/api/v1/clusters/${CID}/pools"

# Firewall READ
test_admin "fw-rules"             GET    "/api/v1/clusters/${CID}/firewall/rules"
test_admin "fw-options"           GET    "/api/v1/clusters/${CID}/firewall/options"
test_admin "fw-aliases"           GET    "/api/v1/clusters/${CID}/firewall/aliases"
test_admin "fw-ipset"             GET    "/api/v1/clusters/${CID}/firewall/ipset"
test_admin "fw-groups"            GET    "/api/v1/clusters/${CID}/firewall/groups"
test_admin "fw-log"               GET    "/api/v1/clusters/${CID}/firewall/log"

# Firewall WRITE
test_admin "create-fw-rule"       POST   "/api/v1/clusters/${CID}/firewall/rules"    '{}'
test_admin "set-fw-options"       PUT    "/api/v1/clusters/${CID}/firewall/options"   '{}'
test_admin "create-fw-alias"      POST   "/api/v1/clusters/${CID}/firewall/aliases"  '{}'
test_admin "create-fw-ipset"      POST   "/api/v1/clusters/${CID}/firewall/ipset"    '{}'
test_admin "create-fw-group"      POST   "/api/v1/clusters/${CID}/firewall/groups"   '{}'

# SDN READ
test_admin "sdn-zones"            GET    "/api/v1/clusters/${CID}/sdn/zones"
test_admin "sdn-vnets"            GET    "/api/v1/clusters/${CID}/sdn/vnets"
test_admin "sdn-controllers"      GET    "/api/v1/clusters/${CID}/sdn/controllers"
test_admin "sdn-ipams"            GET    "/api/v1/clusters/${CID}/sdn/ipams"
test_admin "sdn-dns"              GET    "/api/v1/clusters/${CID}/sdn/dns"

# SDN WRITE
test_admin "create-sdn-zone"      POST   "/api/v1/clusters/${CID}/sdn/zones"        '{}'
test_admin "create-sdn-vnet"      POST   "/api/v1/clusters/${CID}/sdn/vnets"        '{}'
test_admin "create-sdn-ctrl"      POST   "/api/v1/clusters/${CID}/sdn/controllers"  '{}'
test_admin "create-sdn-ipam"      POST   "/api/v1/clusters/${CID}/sdn/ipams"        '{}'
test_admin "create-sdn-dns"       POST   "/api/v1/clusters/${CID}/sdn/dns"          '{}'

# Network WRITE
test_admin "create-network"       POST   "/api/v1/clusters/${CID}/networks/node1"    '{}'

# CVE
test_admin "cve-scans"            GET    "/api/v1/clusters/${CID}/cve-scans"
test_admin "security-posture"     GET    "/api/v1/clusters/${CID}/security-posture"
test_admin "cve-schedule"         GET    "/api/v1/clusters/${CID}/cve-scan-schedule"
test_admin "trigger-cve-scan"     POST   "/api/v1/clusters/${CID}/cve-scans"         '{}'
test_admin "update-cve-schedule"  PUT    "/api/v1/clusters/${CID}/cve-scan-schedule" '{}'

# DRS
test_admin "drs-config"           GET    "/api/v1/clusters/${CID}/drs/config"
test_admin "drs-rules"            GET    "/api/v1/clusters/${CID}/drs/rules"
test_admin "drs-history"          GET    "/api/v1/clusters/${CID}/drs/history"
test_admin "drs-ha-rules"         GET    "/api/v1/clusters/${CID}/drs/ha-rules"
test_admin "update-drs-config"    PUT    "/api/v1/clusters/${CID}/drs/config"        '{}'
test_admin "create-drs-rule"      POST   "/api/v1/clusters/${CID}/drs/rules"         '{}'
test_admin "trigger-drs-eval"     POST   "/api/v1/clusters/${CID}/drs/evaluate"
test_admin "create-drs-ha-rule"   POST   "/api/v1/clusters/${CID}/drs/ha-rules"      '{}'

# Rolling Updates
test_admin "rolling-updates"      GET    "/api/v1/clusters/${CID}/rolling-updates"
test_admin "create-rolling"       POST   "/api/v1/clusters/${CID}/rolling-updates"   '{}'
test_admin "preflight-ha"         POST   "/api/v1/clusters/${CID}/rolling-updates/preflight-ha" '{}'

# SSH Credentials
test_admin "get-ssh-creds"        GET    "/api/v1/clusters/${CID}/ssh-credentials"
test_admin "upsert-ssh-creds"     PUT    "/api/v1/clusters/${CID}/ssh-credentials"   '{"username":"root","auth_method":"password","password":"test"}'
test_admin "test-ssh-conn"        POST   "/api/v1/clusters/${CID}/ssh-credentials/test" '{}'

# Cluster Options
test_admin "options"              GET    "/api/v1/clusters/${CID}/options"
test_admin "description"          GET    "/api/v1/clusters/${CID}/description"
test_admin "tags"                 GET    "/api/v1/clusters/${CID}/tags"
test_admin "config"               GET    "/api/v1/clusters/${CID}/config"
test_admin "config-join"          GET    "/api/v1/clusters/${CID}/config/join"
test_admin "config-nodes"         GET    "/api/v1/clusters/${CID}/config/nodes"
test_admin "update-options"       PUT    "/api/v1/clusters/${CID}/options"            '{}'
test_admin "update-description"   PUT    "/api/v1/clusters/${CID}/description"        '{"description":"test"}'
test_admin "update-tags"          PUT    "/api/v1/clusters/${CID}/tags"               '{"tags":[]}'

# HA
test_admin "ha-resources"         GET    "/api/v1/clusters/${CID}/ha/resources"
test_admin "ha-groups"            GET    "/api/v1/clusters/${CID}/ha/groups"
test_admin "ha-status"            GET    "/api/v1/clusters/${CID}/ha/status"
test_admin "ha-rules"             GET    "/api/v1/clusters/${CID}/ha/rules"
test_admin "create-ha-resource"   POST   "/api/v1/clusters/${CID}/ha/resources"      '{}'
test_admin "create-ha-group"      POST   "/api/v1/clusters/${CID}/ha/groups"         '{}'
test_admin "create-ha-rule"       POST   "/api/v1/clusters/${CID}/ha/rules"          '{}'

# Pools
test_admin "create-pool"          POST   "/api/v1/clusters/${CID}/pools"             '{}'

# Replication
test_admin "replication"          GET    "/api/v1/clusters/${CID}/replication"
test_admin "create-repl"          POST   "/api/v1/clusters/${CID}/replication"       '{}'

# ACME
test_admin "acme-accounts"        GET    "/api/v1/clusters/${CID}/acme/accounts"
test_admin "acme-plugins"         GET    "/api/v1/clusters/${CID}/acme/plugins"
test_admin "acme-challenge"       GET    "/api/v1/clusters/${CID}/acme/challenge-schema"
test_admin "acme-directories"     GET    "/api/v1/clusters/${CID}/acme/directories"
test_admin "acme-tos"             GET    "/api/v1/clusters/${CID}/acme/tos"
test_admin "create-acme-account"  POST   "/api/v1/clusters/${CID}/acme/accounts"     '{}'
test_admin "create-acme-plugin"   POST   "/api/v1/clusters/${CID}/acme/plugins"      '{}'

# Metric Servers
test_admin "metric-servers"       GET    "/api/v1/clusters/${CID}/metric-servers"
test_admin "create-metric-srv"    POST   "/api/v1/clusters/${CID}/metric-servers"    '{}'

# Ceph
test_admin "ceph-pools"           POST   "/api/v1/clusters/${CID}/ceph/pools"        '{}'

# Cluster Migrations
test_admin "cluster-migrations"   GET    "/api/v1/clusters/${CID}/migrations"

# Backup Jobs
test_admin "backup-jobs"          GET    "/api/v1/clusters/${CID}/backup-jobs"
test_admin "create-backup-job"    POST   "/api/v1/clusters/${CID}/backup-jobs"       '{}'
test_admin "restore-backup"       POST   "/api/v1/clusters/${CID}/restore"           '{}'
test_admin "trigger-backup"       POST   "/api/v1/clusters/${CID}/backup"            '{}'

# Schedules
test_admin "schedules"            GET    "/api/v1/clusters/${CID}/schedules"
test_admin "create-schedule"      POST   "/api/v1/clusters/${CID}/schedules"         '{}'

# Audit log
test_admin "cluster-audit"        GET    "/api/v1/clusters/${CID}/audit-log"
test_admin "audit-log"            GET    "/api/v1/audit-log"
test_admin "audit-recent"         GET    "/api/v1/audit-log/recent"
test_admin "audit-actions"        GET    "/api/v1/audit-log/actions"
test_admin "audit-users"          GET    "/api/v1/audit-log/users"
test_admin "audit-export"         GET    "/api/v1/audit-log/export"
test_admin "syslog-config"        GET    "/api/v1/audit-log/syslog-config"
test_admin "update-syslog"        PUT    "/api/v1/audit-log/syslog-config"           '{}'
test_admin "test-syslog"          POST   "/api/v1/audit-log/syslog-test"             '{}'

# Alerts (cluster)
test_admin "cluster-alerts"       GET    "/api/v1/clusters/${CID}/alerts"
test_admin "cluster-alerts-count" GET    "/api/v1/clusters/${CID}/alerts/count"
test_admin "maint-windows"        GET    "/api/v1/clusters/${CID}/maintenance-windows"
test_admin "create-maint-win"     POST   "/api/v1/clusters/${CID}/maintenance-windows" '{}'

# Alerts (global)
test_admin "alerts"               GET    "/api/v1/alerts"
test_admin "alerts-summary"       GET    "/api/v1/alerts/summary"
test_admin "ack-alert"            POST   "/api/v1/alerts/${FAKE_UUID}/acknowledge"
test_admin "resolve-alert"        POST   "/api/v1/alerts/${FAKE_UUID}/resolve"

# Alert Rules
test_admin "alert-rules"          GET    "/api/v1/alert-rules"
test_admin "create-alert-rule"    POST   "/api/v1/alert-rules"                       '{}'

# Notification Channels
test_admin "notif-channels"       GET    "/api/v1/notification-channels"
test_admin "create-notif-chan"    POST   "/api/v1/notification-channels"              '{}'

# PBS
test_admin "pbs-servers"          GET    "/api/v1/pbs-servers"
test_admin "create-pbs"           POST   "/api/v1/pbs-servers"                       '{}'
test_admin "pbs-snapshots"        GET    "/api/v1/pbs-snapshots"
test_admin "backup-coverage"      GET    "/api/v1/backup-coverage"

# Firewall Templates
test_admin "fw-templates"         GET    "/api/v1/firewall-templates"
test_admin "create-fw-tmpl"       POST   "/api/v1/firewall-templates"                '{}'

# Global Migrations
test_admin "migrations"           GET    "/api/v1/migrations"
test_admin "create-migration"     POST   "/api/v1/migrations"                        '{}'

# Reports
test_admin "report-schedules"     GET    "/api/v1/reports/schedules"
test_admin "report-runs"          GET    "/api/v1/reports/runs"
test_admin "create-report-sched"  POST   "/api/v1/reports/schedules"                 '{}'
test_admin "generate-report"      POST   "/api/v1/reports/generate"                  '{}'

# RBAC
test_admin "rbac-roles"           GET    "/api/v1/rbac/roles"
test_admin "rbac-permissions"     GET    "/api/v1/rbac/permissions"
test_admin "rbac-my-perms"        GET    "/api/v1/rbac/me/permissions"
test_admin "create-role"          POST   "/api/v1/rbac/roles"                        '{}'

# Users
test_admin "users-list"           GET    "/api/v1/users"
test_admin "update-user"          PUT    "/api/v1/users/${FAKE_UUID}"                '{}'

# Tasks
test_admin "tasks"                GET    "/api/v1/tasks"
test_admin "create-task"          POST   "/api/v1/tasks"                             '{}'
test_admin "clear-tasks"          DELETE "/api/v1/tasks"

# Search
test_admin "search"               GET    "/api/v1/search?q=test"

# Settings
test_admin "settings"             GET    "/api/v1/settings"
test_admin "settings-branding"    GET    "/api/v1/settings/branding"
test_admin "upsert-setting"       PUT    "/api/v1/settings/test-key"                 '{"value":"x"}'

# API Keys
test_admin "api-keys-list"        GET    "/api/v1/api-keys"
test_admin "admin-list-keys"      GET    "/api/v1/admin/api-keys"

# API Docs
test_admin "api-docs"             GET    "/api/v1/api-docs"

# LDAP
test_admin "list-ldap"            GET    "/api/v1/ldap/configs"
test_admin "create-ldap"          POST   "/api/v1/ldap/configs"                      '{"name":"test","server_url":"ldap://localhost","bind_dn":"cn=admin","bind_password":"secret","search_base":"dc=test","user_filter":"(uid=%s)"}'

# OIDC
test_admin "list-oidc"            GET    "/api/v1/oidc/configs"
test_admin "create-oidc"          POST   "/api/v1/oidc/configs"                      '{"name":"test","issuer_url":"https://accounts.google.com","client_id":"id","client_secret":"secret"}'

echo ""

# ===================================================================
# SUMMARY
# ===================================================================
echo -e "${BOLD}===========================================================${NC}"
echo -e "${BOLD}  SUMMARY${NC}"
echo -e "${BOLD}===========================================================${NC}"
echo ""
echo -e "  Total tests:  ${BOLD}${total}${NC}"
echo -e "  Passed:       ${GREEN}${BOLD}${passed}${NC}"
echo -e "  Failed:       ${RED}${BOLD}${failed}${NC}"
echo ""

if [ "$failed" -eq 0 ]; then
    echo -e "  ${GREEN}${BOLD}ALL TESTS PASSED${NC}"
    echo ""
    exit 0
else
    pct=$((passed * 100 / total))
    echo -e "  ${YELLOW}Pass rate: ${pct}%${NC}"
    echo ""
    echo -e "  ${RED}Some tests failed. Review FAIL lines above for details.${NC}"
    echo ""
    exit 1
fi
