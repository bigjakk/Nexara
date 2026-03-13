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
git clone https://github.com/bigjakk/Nexara.git && cd Nexara
cp .env.example .env     # secrets auto-generated on first start

# 2. Start
docker compose up -d

# 3. Open http://localhost — create your admin account, add a Proxmox cluster
```

Or use the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/bigjakk/nexara/master/scripts/install.sh | bash
```

### Production Deployment

For production, use pre-built images from the container registry:

```bash
docker compose up -d
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
┌──────────────────────────────────────────┐
│  Nexara (single Go binary, port 8080)    │
│                                          │     ┌────────────────┐
│  ├── /api/v1/*    REST API               │────▶│  PostgreSQL    │
│  ├── /ws/*        WebSocket              │     │  + TimescaleDB │
│  ├── /*           Embedded React SPA     │     └────────────────┘
│  ├── Collector    goroutine              │
│  └── Scheduler    goroutine              │────▶ Redis
└──────────────────────────────────────────┘
```

| Service | Container | Port | Description |
|---------|-----------|------|-------------|
| Nexara | `nexara` | 8080 (mapped to 80) | Unified: API + WebSocket + frontend + collector + scheduler |
| PostgreSQL | `nexara-db` | 5432 | Primary database with TimescaleDB for time-series |
| Redis | `nexara-redis` | 6379 | Pub/sub, caching, session management |

## Using a Reverse Proxy

Nexara serves everything on a single port (8080), so reverse proxy configuration is straightforward — just proxy all traffic to the `nexara` container:

```nginx
# nginx example
location / {
    proxy_pass http://nexara:8080;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 86400s;
}
```

```yaml
# Traefik labels example
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.nexara.rule=Host(`nexara.example.com`)"
  - "traefik.http.services.nexara.loadbalancer.server.port=8080"
```

### Things to watch for

- **Upload size** — the ISO upload endpoint (`/api/v1/clusters/*/storage/*/upload`) accepts files up to 15GB. Configure your proxy's max body size accordingly.
- **WebSocket upgrades** — ensure your proxy passes `Upgrade` and `Connection` headers for `/ws` paths.
- **Security headers** — the Go server already sets security headers (X-Content-Type-Options, X-Frame-Options, etc.), but you may want to duplicate them at the proxy level.
- **Timeouts** — WebSocket connections are long-lived. Increase proxy read/write timeouts (e.g., nginx `proxy_read_timeout 86400s`).

## Configuration

All configuration is via environment variables in `.env`. See [`.env.example`](.env.example) for defaults.

| Variable | Required | Description |
|----------|----------|-------------|
| `JWT_SECRET` | No | Auto-generated on first start, persisted to data volume |
| `ENCRYPTION_KEY` | No | Auto-generated on first start, persisted to data volume |
| `POSTGRES_PASSWORD` | Yes | Database password |
| `API_PORT` | No | Server listen port (default: `8080`) |
| `COLLECT_INTERVAL` | No | Inventory sync interval (default: `30s`) |
| `LOG_LEVEL` | No | Log verbosity: `debug`, `info`, `warn`, `error` |

Full configuration reference in the [Installation Guide](docs/installation.md#configuration-reference).

## Tech Stack

- **Backend:** Go 1.24, Fiber v3, sqlc + pgx, gorilla/websocket
- **Frontend:** React 19, TypeScript 5, Vite 6, Shadcn/ui, TanStack Query/Table, Zustand, Recharts, xterm.js, noVNC, React Flow
- **Database:** PostgreSQL 16 + TimescaleDB
- **Cache:** Redis 7 (Valkey compatible)
- **Deploy:** Docker Compose (3 containers)

## Documentation

| Document | Description |
|----------|-------------|
| [Installation Guide](docs/installation.md) | Setup, configuration, backup, troubleshooting |
| [Admin Guide](docs/admin-guide.md) | Cluster, user, RBAC, auth, alert, and backup management |
| [API Reference](docs/api-reference.md) | REST API endpoint catalog with examples |
| [Contributing](docs/contributing.md) | Development setup, conventions, PR process |
| [Security Policy](SECURITY.md) | Vulnerability reporting and security features |

## CI/CD

Nexara uses GitHub Actions for continuous integration and releases.

- **CI** — every push and PR runs Go lint/test/build, frontend typecheck/lint/test/build, and Docker build validation
- **Release** — pushing a `v*` tag builds and pushes the Docker image to the GitHub Container Registry (ghcr.io) and creates a release with auto-generated notes

```bash
# Tag a release
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

## Contributing

We welcome contributions! See [docs/contributing.md](docs/contributing.md) for development setup and guidelines.

```bash
make build          # Build unified Go binary
make test           # Run tests
make lint           # Run linters
make generate       # Regenerate sqlc code
make docker-up      # Start full stack
```

## License

Nexara is licensed under the [GNU Affero General Public License v3.0](LICENSE).
