# Nexara

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8.svg)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](docker-compose.yml)

**Centralized management platform for Proxmox VE and PBS — like vCenter for Proxmox.**

Free, open-source, Docker-deployable. Manage multiple Proxmox clusters from a single pane of glass with real-time dashboards, granular RBAC, automated operations, and enterprise security features.

## Features

### Infrastructure Management
- **Multi-cluster management** — manage unlimited Proxmox VE clusters from one UI
- **Real-time dashboards** — live CPU, memory, disk, and network metrics via WebSocket
- **VM/CT lifecycle** — create, start, stop, shutdown, reboot, suspend, resume, destroy
- **Snapshots & clones** — create, rollback, and delete snapshots; full and linked clones
- **Live migration** — intra-cluster and cross-cluster VM/CT migration with pre-flight checks
- **Disk management** — resize, move, attach, detach disks; change CD/DVD media
- **Template management** — convert VMs/CTs to templates, clone from templates
- **Resource pools** — organize VMs/CTs into pools with bulk management

### Consoles
- **VNC console** — browser-based graphical console via noVNC
- **Serial console** — xterm.js terminal for headless VMs and containers
- **Node shell** — direct shell access to Proxmox nodes

### Storage & Backup
- **Storage management** — view, create, and manage Proxmox storage across clusters
- **PBS integration** — full Proxmox Backup Server management with datastore monitoring
- **Backup jobs** — scheduled backups with retention policies
- **Restore** — restore VMs/CTs from PBS snapshots to any cluster/node
- **Backup coverage** — dashboard showing which VMs have recent backups
- **Ceph monitoring** — OSD, pool, monitor, and CephFS status with historical metrics

### Networking & Security
- **Firewall management** — cluster and VM-level rules, aliases, IP sets, security groups
- **Firewall templates** — create reusable rule sets and apply across clusters
- **SDN management** — zones, VNets, subnets, controllers, IPAMs, DNS
- **Network interfaces** — create, edit, delete bridges and bonds with apply/revert
- **ACME certificates** — Let's Encrypt certificate management for Proxmox nodes

### Automation
- **DRS (Distributed Resource Scheduler)** — automatic VM workload balancing with affinity/anti-affinity rules
- **Scheduled tasks** — cron-based snapshots, backups, and reboots
- **Rolling updates** — automated node-by-node upgrades with drain/upgrade/reboot/restore pipeline
- **Alert engine** — threshold-based alerts with escalation chains and 7 notification channels
- **CVE scanning** — automated vulnerability scanning via Debian Security Tracker
- **Replication** — manage and monitor ZFS replication jobs

### Enterprise Features
- **RBAC** — granular role-based access control with built-in and custom roles
- **LDAP/AD** — Active Directory and LDAP authentication with JIT provisioning and group-to-role mapping
- **OIDC/SSO** — single sign-on with any OIDC provider (Google, Okta, Keycloak, etc.)
- **Two-factor authentication** — TOTP-based 2FA with recovery codes
- **Audit logging** — comprehensive audit trail for all actions with syslog forwarding
- **Reports** — scheduled HTML/CSV reports for capacity planning and compliance
- **HA management** — high availability resources, groups, and rules

### User Experience
- **Topology visualization** — interactive infrastructure map with React Flow
- **Global search** — find VMs, nodes, and clusters instantly
- **Dark/light/system theme** — with 9 accent color presets
- **Custom branding** — upload logo, favicon, and set application title
- **Localization** — i18n framework with language selector

## Quick Start

```bash
# 1. Clone and configure
git clone https://gitea.crjlab.net/bigjakk/nexara.git && cd nexara
./scripts/setup-env.sh   # generates .env with secure random secrets

# 2. Start
docker compose up -d

# 3. Open http://localhost — create your admin account, add a Proxmox cluster
```

Or use the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/bigjakk/nexara/master/scripts/install.sh | bash
```

### Production Deployment

For production, use pre-built images from the container registry instead of building locally:

```bash
# Pull a specific release
NEXARA_VERSION=0.1.0 docker compose -f docker-compose.prod.yml up -d
```

See the [Installation Guide](docs/installation.md) for detailed setup, configuration, and troubleshooting.

## First Login

1. Open `http://localhost` in your browser
2. Create your admin account on the registration page (first user = admin)
3. Click **Add Cluster** on the dashboard
4. Enter your Proxmox VE API URL and API token
5. Inventory and metrics sync within seconds

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌────────────────┐
│   Caddy      │────▶│  React SPA   │     │  PostgreSQL    │
│   (port 80)  │     │  (nginx)     │     │  + TimescaleDB │
│              │     └──────────────┘     └────────────────┘
│              │                                 ▲
│              │────▶ API Server (Go) ───────────┤
│              │                                 │
│              │────▶ WebSocket Server (Go) ─────┤
└─────────────┘                                  │
                     Collector (Go) ─────────────┤
                     Scheduler (Go) ─────────────┘
                                                 │
                     Redis ◀─────────────────────┘
```

| Service | Port | Description |
|---------|------|-------------|
| Caddy proxy | 80, 443 | Reverse proxy with automatic HTTPS |
| API server | 8080 | REST API (200+ endpoints) |
| WebSocket server | 8081 | Real-time metrics and event streaming |
| Frontend | 3000 | React SPA served by nginx |
| PostgreSQL | 5432 | Primary database with TimescaleDB for time-series |
| Redis | 6379 | Pub/sub, caching, session management |
| Collector | — | Syncs Proxmox inventory and metrics |
| Scheduler | — | DRS, alerts, CVE scans, scheduled tasks |

## Configuration

All configuration is via environment variables in `.env`. See [`.env.example`](.env.example) for defaults.

| Variable | Required | Description |
|----------|----------|-------------|
| `JWT_SECRET` | Yes | Secret for signing auth tokens |
| `ENCRYPTION_KEY` | Yes | 32-byte hex key for encrypting secrets at rest |
| `POSTGRES_PASSWORD` | Yes | Database password |
| `NEXARA_DOMAIN` | No | Domain for Caddy auto-HTTPS (default: `localhost`) |
| `COLLECT_INTERVAL` | No | Inventory sync interval (default: `30s`) |
| `LOG_LEVEL` | No | Log verbosity: `debug`, `info`, `warn`, `error` |

Full configuration reference in the [Installation Guide](docs/installation.md#configuration-reference).

## Tech Stack

- **Backend:** Go 1.24, Fiber v3, sqlc + pgx, gorilla/websocket
- **Frontend:** React 19, TypeScript 5, Vite 6, Shadcn/ui, TanStack Query/Table, Zustand, Recharts, xterm.js, noVNC, React Flow
- **Database:** PostgreSQL 16 + TimescaleDB
- **Cache:** Redis 7 (Valkey compatible)
- **Proxy:** Caddy 2 (automatic HTTPS)

## Documentation

| Document | Description |
|----------|-------------|
| [Installation Guide](docs/installation.md) | Setup, configuration, backup, troubleshooting |
| [Admin Guide](docs/admin-guide.md) | Cluster, user, RBAC, auth, alert, and backup management |
| [API Reference](docs/api-reference.md) | REST API endpoint catalog with examples |
| [Contributing](docs/contributing.md) | Development setup, conventions, PR process |
| [Security Policy](SECURITY.md) | Vulnerability reporting and security features |

## CI/CD

Nexara uses Gitea Actions for continuous integration and releases.

- **CI** — every push and PR runs Go lint/test/build, frontend typecheck/lint/test/build, and Docker build validation
- **Release** — pushing a `v*` tag builds and pushes all Docker images to the Gitea container registry and creates a release with auto-generated notes

```bash
# Tag a release
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

## Contributing

We welcome contributions! See [docs/contributing.md](docs/contributing.md) for development setup and guidelines.

```bash
make build          # Build all Go binaries
make test           # Run tests
make lint           # Run linters
make generate       # Regenerate sqlc code
make docker-up      # Start full stack
```

## License

Nexara is licensed under the [GNU Affero General Public License v3.0](LICENSE).
