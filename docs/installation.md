# Installation Guide

## Prerequisites

- **Docker** 20.10+ and **Docker Compose** v2+
- **2 CPU cores** and **2 GB RAM** minimum (4 GB recommended)
- **10 GB disk** for the application and database
- A **Proxmox VE** cluster (7.x, 8.x, or 9.x — including 9.2) with an API token
- Ports **80** and **443** available (or configure alternatives)
- A modern browser — **Chrome/Edge 111+, Firefox 128+, or Safari 16.4+**. Very old browsers that can't run the app at all show an upgrade notice instead of a blank page.

## Quick Install

Run the install script for an automated setup:

```bash
curl -fsSL https://raw.githubusercontent.com/bigjakk/Nexara/master/scripts/install.sh | bash
```

The script will:
1. Check prerequisites (Docker, Docker Compose, openssl, git)
2. Clone the repository
3. Generate secure secrets
4. Build and start all services
5. Wait for health checks and print the access URL

For manual setup, follow the steps below.

## Manual Install

### 1. Clone the Repository

```bash
git clone https://github.com/bigjakk/Nexara.git
cd Nexara
```

### 2. Configure Environment

```bash
cp .env.example .env
```

Set a database password:

```bash
sed -i "s/changeme/$(openssl rand -base64 16 | tr -d '=/+')/" .env
```

**Secrets (JWT_SECRET, ENCRYPTION_KEY) are auto-generated on first start** and persisted to the data volume at `/data/nexara/.secrets.json`. No manual secret generation is needed. If you prefer to manage secrets externally (e.g., via a secrets manager), you can still set them as env vars — they take precedence over the auto-generated file.

### 3. Start the Stack

```bash
docker compose up -d
```

The database schema is applied automatically on first startup. All 3 services will start in dependency order with health checks.

### 4. Verify

```bash
# Check all services are running
docker compose ps

# Check API health (the compose file maps host port 80 → container 8080;
# substitute your own host port if you've changed the mapping)
curl http://localhost/healthz
```

> The container's built-in health check runs `/nexara healthcheck` (a CLI subcommand) inside the container — that's what `docker compose ps` reports. The `/healthz` HTTP endpoint is the equivalent for manual checks and external monitors.

## Configuration Reference

All configuration is via environment variables in `.env`:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `POSTGRES_USER` | No | `nexara` | PostgreSQL username |
| `POSTGRES_PASSWORD` | **Yes** | — | PostgreSQL password (change from default) |
| `POSTGRES_DB` | No | `nexara` | PostgreSQL database name |
| `DATABASE_URL` | No | auto | Full PostgreSQL connection string |
| `REDIS_URL` | No | `redis://nexara-redis:6379/0` | Redis connection string |
| `API_PORT` | No | `8080` | API server listen port |
| `JWT_SECRET` | No | auto-generated | Secret for signing JWT tokens (min 16 chars) |
| `ENCRYPTION_KEY` | No | auto-generated | 32-byte hex key for AES-256-GCM encryption of secrets at rest |
| `METRICS_COLLECT_INTERVAL` | No | `30s` (Docker deployments set `10s`) | How often metrics are collected from Proxmox |
| `SNAPSHOT_SYNC_INTERVAL` | No | `5m` | How often the guest snapshot inventory is collected (feeds the central Snapshots page and `snapshot_age_days` alerts). One Proxmox listing per guest per pass, so it runs well below the metrics cadence; floored at `60s`, `0` disables collection |
| `TASK_HISTORY_RETENTION` | No | `24h` | How long finished Proxmox task records are kept before the retention sweep deletes them. Go duration in hours (`168h` = 7 days); running tasks are never removed. |
| `LOG_LEVEL` | No | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `PUID` | No | `1000` | User ID for the container process and data directory |
| `PGID` | No | `1000` | Group ID for the container process and data directory |
| `DATA_DIR` | No | Docker volumes | Host path that relocates **all** persistent state (PostgreSQL, Redis, app data) from named volumes to `db/`/`redis/`/`data/` subdirectories (e.g. an NFS mount) |
| `TRUSTED_PROXIES` | **Yes for production behind a reverse proxy** | empty | Comma-separated IPs/CIDRs whose `X-Forwarded-For` is honored. Without it the rate limiters can't tell clients apart behind nginx/Traefik/Caddy. Examples: `127.0.0.1`, `10.0.0.0/8,172.16.0.0/12`. Leave empty when Nexara is exposed directly. |
| `PROXY_HEADER` | No | `X-Forwarded-For` | Header consulted for the client IP when the remote is on `TRUSTED_PROXIES`. Override only for non-standard upstreams. |
| `WS_ALLOWED_ORIGINS` | **Recommended for production** | empty (allow all) | Comma-separated exact `Origin` values accepted on WebSocket upgrades (`/ws`, `/ws/console`, `/ws/vnc`), e.g. `https://nexara.example.com`. Empty or `*` keeps the permissive default (fine for labs, warned at startup). |
| `SECURE_COOKIES` | No | `auto` | `Secure` attribute on the refresh-token cookie: `auto` (set when the request is detected as HTTPS), `always` (recommended behind a TLS-terminating proxy), `never` (intentional plain-HTTP lab only). |
| `HSTS_MAX_AGE` | No | `0` (disabled) | `Strict-Transport-Security` max-age in seconds (e.g. `31536000`). Enable only on HTTPS with a trusted certificate — with a self-signed cert it makes certificate errors unbypassable. |

## First-Time Setup

### 1. Create Your Admin Account

Open `http://localhost` (or your configured domain) in a browser. On first run, you'll be redirected to the registration page. The first user created automatically receives the **Admin** role.

### 2. Add a Proxmox Cluster

1. From the dashboard, click **Add Cluster**
2. Enter a display name for the cluster
3. Enter the API URL: `https://your-proxmox-host:8006`
4. Enter a Proxmox API token (format: `user@realm!tokenid=secret-value`)
5. If using a self-signed certificate, click **Fetch Fingerprint** and accept it
6. Click **Save**

The collector begins syncing inventory and metrics within seconds. You'll see nodes, VMs, and containers appear on the dashboard.

### 3. Install on Your Phone (Optional)

The UI is fully responsive and ships a PWA manifest: open Nexara in your phone's browser and choose **Add to Home Screen** (or **Install app**) to get a standalone app-style window. All features — including consoles — work on mobile.

### Creating a Proxmox API Token

On your Proxmox host:

```bash
# Create an API token for an existing user
pveum user token add root@pam nexara --privsep 0

# Or create a dedicated user first
pveum user add nexara@pve
pveum aclmod / -user nexara@pve -role PVEAdmin
pveum user token add nexara@pve api --privsep 0
```

Copy the token value — it is only shown once. The format for Nexara is:
```
user@realm!tokenid=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

## Services

| Service | Container | Port | Description |
|---------|-----------|------|-------------|
| Nexara | `nexara` | 8080 (mapped to 80) | Unified: API + WebSocket + frontend + collector + scheduler |
| PostgreSQL | `nexara-db` | 5432 | Primary database + TimescaleDB |
| Redis | `nexara-redis` | 6379 | Pub/sub, caching, session store |

## Updating

```bash
cd nexara

# Pull latest changes
git pull

# Rebuild and restart (migrations run automatically)
docker compose build
docker compose up -d
```

Database migrations are applied automatically when the API server starts. There is no need to run them manually.

### Recovering from a failed upgrade

If a migration fails partway through an upgrade, the schema is marked
**dirty** and every subsequent container start refuses to boot (the logs show
`failed to ensure database schema` and the container restart-loops). The
recovery tooling ships inside the image — no extra tools needed:

```bash
# 1. See where the schema is stuck (prints applied version, dirty flag,
#    and the version this image expects)
docker compose run --rm nexara migrate status

# 2. Take a database backup before touching anything
docker compose exec nexara-db pg_dump -U nexara -Fc nexara > nexara-backup.dump

# 3. Clear the dirty flag. Check what the failed migration's .up.sql does
#    (the status output names the version; the SQL is in the repo's
#    migrations/ directory), then record the version that is actually
#    fully applied — usually the one BEFORE the failure:
docker compose run --rm nexara migrate force <version>

# 4. Retry the pending migrations, or just restart:
docker compose run --rm nexara migrate up
docker compose up -d
```

If the migration fails again the error is real — restore the backup, pin the
previous image version (`NEXARA_VERSION`), and report the migration error.

`nexara migrate down <n>` rolls back the last `n` migrations (running their
`.down.sql`). Down migrations can drop data — only use it as part of a
deliberate rollback to a matching older image, and always back up first.

## Backup & Restore

### Database Backup

```bash
# Dump the database
docker exec nexara-db pg_dump -U nexara nexara > nexara-backup-$(date +%Y%m%d).sql

# Or use compressed format
docker exec nexara-db pg_dump -U nexara -Fc nexara > nexara-backup-$(date +%Y%m%d).dump
```

### Database Restore

```bash
# Stop the application (keep DB running)
docker compose stop nexara

# Restore from SQL dump
docker exec -i nexara-db psql -U nexara nexara < nexara-backup-20240101.sql

# Or from compressed dump
docker exec -i nexara-db pg_restore -U nexara -d nexara --clean nexara-backup-20240101.dump

# Restart
docker compose up -d
```

### Volume Backup

Nexara stores state in **three** volumes: `nexara-db-data` (PostgreSQL), `nexara-redis-data` (Redis), and `nexara-data` (secrets, branding, uploads). For a full cold backup:

```bash
# Stop all services
docker compose down

# Back up all three Docker volumes into one archive
docker run --rm \
  -v nexara-db-data:/vol/db -v nexara-redis-data:/vol/redis -v nexara-data:/vol/data \
  -v $(pwd):/backup alpine \
  tar czf /backup/nexara-volumes-$(date +%Y%m%d).tar.gz /vol

# Restart
docker compose up -d
```

> If you set `DATA_DIR` in `.env`, there are no named volumes — all state lives in `db/`, `redis/`, and `data/` subdirectories under that path, so back up that directory instead.

## Troubleshooting

### Services won't start

```bash
# Check container logs
docker compose logs nexara
docker compose logs nexara-db

# Verify all containers are running
docker compose ps
```

### "JWT_SECRET must be set" or "ENCRYPTION_KEY must be set"

Secrets are auto-generated on first start and persisted to `DATA_DIR/.secrets.json`. If you see this error, check that the data volume is writable. If you prefer to manage secrets externally, set `JWT_SECRET` and `ENCRYPTION_KEY` in `.env` — they take precedence over the auto-generated file.

### Database connection refused

- Ensure `nexara-db` is healthy: `docker compose ps nexara-db`
- Check that `DATABASE_URL` in `.env` matches the PostgreSQL credentials
- The API server waits for the database health check — if the DB is slow to start, the API will retry

### Port conflicts

If ports 80, 5432, or 6379 are already in use:

1. Edit `docker-compose.yml` to change the host port mappings
2. For the Nexara service, change `"80:8080"` to e.g. `"8443:8080"`
3. Internal service-to-service communication uses container names, not host ports

### Frontend shows blank page

- Check the Nexara container logs: `docker compose logs nexara`
- Verify the container is healthy: `docker compose ps`

### Proxmox connection fails

- Verify the API URL is reachable from the Docker host
- Check that the API token has sufficient privileges
- For self-signed certificates, use the **Fetch Fingerprint** button when adding the cluster
- Check logs: `docker compose logs nexara`

### Reset admin password

If you lose access to your admin account:

```bash
# Connect to the database
docker exec -it nexara-db psql -U nexara nexara

# Delete every login-capable user; keep the seeded system actor that
# audit-log entries and DRS / scheduler tasks attribute to. Removing
# that row would break those references on the first system action.
DELETE FROM users WHERE id != '00000000-0000-0000-0000-000000000001';
```

Then visit the web UI — `/api/v1/auth/setup-status` will report
`needs_setup: true` again and the registration page will appear so you
can create a new admin account.
