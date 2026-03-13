# Installation Guide

## Prerequisites

- **Docker** 20.10+ and **Docker Compose** v2+
- **2 CPU cores** and **2 GB RAM** minimum (4 GB recommended)
- **10 GB disk** for the application and database
- A **Proxmox VE** cluster (7.x or 8.x) with an API token
- Ports **80** and **443** available (or configure alternatives)

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

# Check API health
curl http://localhost:8080/healthz
```

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
| `METRICS_COLLECT_INTERVAL` | No | `10s` | How often metrics are collected from Proxmox |
| `LOG_LEVEL` | No | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

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

For a full backup including Redis data:

```bash
# Stop all services
docker compose down

# Back up Docker volumes
docker run --rm -v nexara-db-data:/data -v $(pwd):/backup alpine \
  tar czf /backup/nexara-volumes-$(date +%Y%m%d).tar.gz /data

# Restart
docker compose up -d
```

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

You need to generate secrets before starting. See [Configure Environment](#2-configure-environment) above.

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

# Delete all users to re-trigger the registration page
DELETE FROM users;
```

Then visit the web UI to create a new admin account.
