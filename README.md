# Nexara

Centralized management platform for Proxmox VE and PBS — like vCenter for Proxmox. Free and open-source.

## Features

- Multi-cluster Proxmox VE management from a single pane
- Real-time dashboards with live CPU, memory, disk, and network metrics
- VM/CT lifecycle management (start, stop, shutdown, reboot, suspend, resume)
- Floating VNC/serial console with power controls and keyboard macros
- Virtual media — mount and eject ISO images from the console toolbar
- Snapshots, clones, migrations (intra-cluster and cross-cluster)
- Distributed Resource Scheduler (DRS) for automatic workload balancing
- Ceph storage monitoring with OSD, pool, and cluster metrics
- Proxmox Backup Server integration with datastore and snapshot management
- Firewall rule management and templates
- SDN zone, VNet, and subnet management
- Scheduled tasks (snapshots, backups, reboots) with cron expressions
- Audit logging for all actions
- Dark mode with light/dark/system toggle

## Quick Start

### Prerequisites

- Docker and Docker Compose v2+
- A Proxmox VE cluster with an API token

### Deploy

```bash
git clone https://github.com/nexara/nexara.git
cd nexara

# Create your environment file
cp .env.example .env

# Generate secrets (required — the app will not start with placeholder values)
sed -i "s/change-this-to-a-secure-random-string/$(openssl rand -base64 32)/" .env
sed -i "s/change-this-to-a-32-byte-hex-key/$(openssl rand -hex 32)/" .env

# Start the stack
docker compose up -d
```

The database schema is created automatically on first startup.

### First Login

Open `http://localhost` in your browser. On first run you'll be redirected to the registration page to create your admin account.

After logging in, add your Proxmox cluster:
1. Go to the dashboard
2. Click **Add Cluster**
3. Enter the cluster name, API URL (e.g. `https://pve.example.com:8006`), and an API token
4. The collector will begin syncing inventory and metrics within seconds

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
| Caddy proxy | 80, 443 | Reverse proxy — the entry point |
| API server | 8080 | REST API |
| WebSocket server | 8081 | Live metrics and event streaming |
| Frontend | 3000 | React SPA served by nginx |
| PostgreSQL | 5432 | Primary database with TimescaleDB |
| Redis | 6379 | Pub/sub for real-time metrics |
| Collector | — | Syncs Proxmox inventory and metrics |
| Scheduler | — | Runs scheduled tasks and DRS |

## Configuration

All configuration is via environment variables in `.env`. See [`.env.example`](.env.example) for all options.

| Variable | Required | Description |
|----------|----------|-------------|
| `JWT_SECRET` | Yes | Secret for signing auth tokens (min 16 chars) |
| `ENCRYPTION_KEY` | Yes | 32-byte hex key for encrypting API tokens at rest |
| `POSTGRES_PASSWORD` | Yes | Database password |
| `NEXARA_DOMAIN` | No | Domain for Caddy (default: `localhost`) |
| `COLLECT_INTERVAL` | No | Metric collection interval (default: `30s`) |
| `LOG_LEVEL` | No | Log verbosity: `debug`, `info`, `warn`, `error` |

## Tech Stack

- **Backend:** Go, Fiber, sqlc, pgx, gorilla/websocket
- **Frontend:** React 19, TypeScript 5, Vite 6, Shadcn/ui, TanStack Query, Zustand, Recharts, xterm.js, noVNC
- **Database:** PostgreSQL 16 + TimescaleDB
- **Cache:** Redis 7
- **Proxy:** Caddy 2

## License

MIT
