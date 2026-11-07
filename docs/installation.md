# Installation Guide

## Prerequisites

- **Docker** 20.10+ and **Docker Compose** v2+
- An **x86-64 (amd64)** host — the published image is amd64-only; there is no arm64 build yet
- **2 CPU cores** and **2 GB RAM** minimum (4 GB recommended)
- **10 GB disk** for the application and database
- A **Proxmox VE** cluster (7.x, 8.x, or 9.x — including 9.2). Nexara can create its own API token from a privileged login, or you can paste one you made
- Port **80** available on the host (or change the `80:8080` mapping in `docker-compose.yml`). Nexara serves plain HTTP on a single port — put your own reverse proxy in front of it for TLS.
- A modern browser — **Chrome/Edge 111+, Firefox 128+, or Safari 16.4+**. Very old browsers that can't run the app at all show an upgrade notice instead of a blank page.

## Quick Install

Run the install script for an automated setup:

```bash
curl -fsSL https://raw.githubusercontent.com/bigjakk/Nexara/master/scripts/install.sh | bash
```

The script needs **root or sudo** (it can install missing dependencies, including Docker itself) and is interactive — run it on a host where you can answer prompts. It will:
1. Check prerequisites (Docker, Docker Compose, openssl, git, curl) and offer to install any that are missing
2. Clone the repository
3. Generate secure secrets
4. Pull the prebuilt image and start all services
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

All configuration is via environment variables.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `NEXARA_VERSION` | No | `latest` | Image tag the compose file pulls (`ghcr.io/bigjakk/nexara:<tag>`). Leave unset to track the newest release; pin it (e.g. `1.17.0` — the published image tag drops the leading `v` from the git tag) to control when you upgrade. An older image will not start on a schema a newer one has migrated, so rolling back also means restoring the backup from before the upgrade |
| `POSTGRES_USER` | No | `nexara` | PostgreSQL username |
| `POSTGRES_PASSWORD` | **Yes** | — | PostgreSQL password (change from default) |
| `POSTGRES_DB` | No | `nexara` | PostgreSQL database name |
| `DATABASE_URL` | No | auto | Full PostgreSQL connection string |
| `REDIS_URL` | No | `redis://nexara-redis:6379/0` | Redis connection string |
| `API_PORT` | No | `8080` | API server listen port |
| `JWT_SECRET` | No | auto-generated | Secret for signing JWT tokens (min 16 chars) |
| `ENCRYPTION_KEY` | No | auto-generated | 32-byte hex key for AES-256-GCM encryption of secrets at rest |
| `METRICS_COLLECT_INTERVAL` | No | `30s` (Docker deployments set `10s`) | How often metrics are collected from Proxmox VE. PBS servers are synced on a loop of their own at the same interval, but no more often than every `30s`, so a slow PBS server never delays the Proxmox VE samples |
| `RESOURCE_SYNC_INTERVAL` | No | `5s` | How often the fast inventory loop runs — one `GET /cluster/resources` per cluster per tick, so guest add/remove/move, status flips, renames and node status converge in seconds. Floored at `2s`; `0` disables the fast loop and leaves all freshness to `METRICS_COLLECT_INTERVAL` |
| `SNAPSHOT_SYNC_INTERVAL` | No | `5m` | How often the guest snapshot inventory is collected (feeds the central Snapshots page and `snapshot_age_days` alerts). One Proxmox listing per guest per pass, so it runs well below the metrics cadence; floored at `60s`, `0` disables collection |
| `NODE_PRESSURE_SYNC_INTERVAL` | No | `5m` | How often the background pass copies each online node's pressure stall steps (the share of time its tasks waited for CPU, IO or memory) from its Proxmox VE 9 RRD, which keeps a day of them at one-minute steps. Floored at `1m`; `0` turns it off. A guest's pressure needs no pass: it arrives with every metrics cycle |
| `OBJECT_OBSERVE_INTERVAL` | No | `5m` | How often the background pass looks for edits made directly in Proxmox — in its GUI, its API or a node's shell — that ran no task and so left no audit entry: a guest's configuration, the HA configuration, pools, SDN, the cluster firewall, Proxmox users, tokens, groups, roles and ACLs, ACME accounts and plugins, storage definitions, backup jobs, resource mappings, metric servers, the datacenter options, and each node's network, options, notes, DNS, time zone and firewall rules. It records the names of the fields that changed, never their values, in the object's History. Each pass reads every guest's configuration, one Proxmox call per guest spread over the interval and never more than ten a second per cluster, so at the default interval the cap binds at about 2,400 guests a cluster, and a pass takes longer than the interval at about 3,000. Held between `1m` and `1h`; `0` turns it off (a PBS's sync and verify job edits, which need no reads, are still recorded), and a negative value stops Nexara at startup. See [Changes made directly in Proxmox](admin-guide.md#changes-made-directly-in-proxmox) |
| `NUT_POLL_INTERVAL` | No | `10s` | How often each NUT server is read for the state of its UPSes, which feeds the Power page, the health pill and the UPS alerts. A reading older than three intervals (and at least two minutes) counts as unreadable. Floored at `2s`; `0` stops polling every server, so every UPS then reads as unreachable: to stop watching one server, turn its polling off on the Power page instead. See [Power (UPS monitoring)](admin-guide.md#power-ups-monitoring) |
| `NODE_MEMORY_READ_INTERVAL` | No | `24h` | How old a node's memory DIMM read may get before the background pass reads it again with `dmidecode` over the cluster's SSH credentials, as root. Only clusters with SSH credentials and a pinned host key are dialed; a read that reached the node and failed waits as long. `0` turns the pass off, and a node page's **Read now** still works |
| `TASK_HISTORY_RETENTION` | No | `168h` | How long finished Proxmox task records are kept before the retention sweep deletes them. Go duration in hours (`168h` = 7 days, `720h` = 30 days); running tasks are never removed. Raised from `24h` in v1.10.0 — at a day, task history aged out before the incident it was being used to investigate. A guest task's outcome outlives the sweep: it moves to a separate archive that the guest's History tab reads, kept like the audit log. |
| `VEEAM_SYNC_INTERVAL` | No | `5m` | Veeam inventory pass — repositories and their capacity sample, job states, backup objects, and the restore points of any object whose count moved. Floored at `60s`; `0` disables Veeam collection entirely. Only relevant once a Veeam server is registered |
| `VEEAM_SESSION_INTERVAL` | No | `60s` | The cheaper session poll — one watermarked, server-side-filtered listing per server, so a running job's progress is visible without a full inventory pass. Floored at `30s`; `0` falls back to the inventory cadence |
| `VEEAM_RESTORE_POINT_RETENTION` | No | `720h` | How long a restore point Veeam has **stopped** reporting is kept. Measured against when Nexara last saw it, never the point's own age — a restore point Veeam still holds must never be pruned, or every RPO and coverage figure derived from it silently becomes wrong |
| `VEEAM_SESSION_RETENTION` | No | `720h` | The window of job runs Nexara mirrors. Unlike restore points this **is** measured against the session's own age: a real server holds tens of thousands, and Nexara keeps a recent window rather than the full history |
| `LOG_LEVEL` | No | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `ACCESS_TOKEN_TTL` | No | `15m` | Lifetime of the JWT access token. The SPA refreshes proactively well inside this window |
| `REFRESH_TOKEN_TTL` | No | `168h` | Lifetime of the refresh-token cookie — how long a session survives without re-login (`168h` = 7 days) |
| `RATE_LIMIT_MAX` | No | `600` | Requests per window per client IP for the general limiter, which covers `/api/v1/*` except `/api/v1/auth/*` and `/ws*`. The auth, refresh, ws-token, snapshot-resync, cluster-create and Veeam limiters have their own fixed budgets and are not affected by this |
| `RATE_LIMIT_EXPIRATION` | No | `1m` | Window length for the general limiter |
| `WS_PING_INTERVAL` | No | `25s` | How often the WebSocket server pings a connected client |
| `WS_PONG_TIMEOUT` | No | `30s` | Silence after which a WebSocket connection is dropped. Keep above `WS_PING_INTERVAL` |
| `WS_MAX_CONNECTIONS` | No | `1000` | Maximum concurrent WebSocket connections to the hub |
| `CHANGELOG_REPO` | No | `bigjakk/Nexara` | GitHub `owner/repo` the in-app changelog popup reads releases from. Point it at a fork if you publish your own releases |
| `PPROF_ENABLED` | No | `false` | Expose Go pprof profiling endpoints. Development only — do not enable on an internet-facing deployment |
| `PPROF_PORT` | No | `6060` | Port pprof listens on when `PPROF_ENABLED` is true |
| `PUID` | No | `1000` | User ID for the container process and data directory |
| `PGID` | No | `1000` | Group ID for the container process and data directory |
| `DATA_DIR` | No | Docker volumes | Host path that relocates **all** persistent state (PostgreSQL, Redis, app data) from named volumes to `db/`/`redis/`/`data/` subdirectories (e.g. an NFS mount) |
| `TRUSTED_PROXIES` | **Yes for production behind a reverse proxy** | empty | Comma-separated IPs/CIDRs whose `X-Forwarded-For` is honored. Without it the rate limiters can't tell clients apart behind nginx/Traefik/Caddy. Examples: `127.0.0.1`, `10.0.0.0/8,172.16.0.0/12`. Leave empty when Nexara is exposed directly. |
| `PROXY_HEADER` | No | `X-Forwarded-For` | Header consulted for the client IP when the remote is on `TRUSTED_PROXIES`. Override only for non-standard upstreams. |
| `WS_ALLOWED_ORIGINS` | **Recommended for production** | empty (allow all) | Comma-separated exact `Origin` values accepted on WebSocket upgrades (`/ws`, `/ws/console`, `/ws/vnc`), e.g. `https://nexara.example.com`. Empty or `*` keeps the permissive default (fine for labs, warned at startup). |
| `SECURE_COOKIES` | No | `auto` | `Secure` attribute on the refresh-token cookie: `auto` (set when the request is detected as HTTPS), `always` (recommended behind a TLS-terminating proxy), `never` (intentional plain-HTTP lab only). |
| `HSTS_MAX_AGE` | No | `0` (disabled) | `Strict-Transport-Security` max-age in seconds (e.g. `31536000`). Enable only on HTTPS with a trusted certificate — with a self-signed cert it makes certificate errors unbypassable. |
| `COMPRESSION_ENABLED` | No | `true` | Compress responses with brotli/gzip/deflate, negotiated from `Accept-Encoding`. Set `false` only when a reverse proxy in front of Nexara already compresses (nginx `gzip on`, Traefik's compress middleware, Caddy's `encode`), so the work isn't done twice. A client that sends no `Accept-Encoding` gets the response unchanged either way; mutating responses and everything under `/api/v1/auth/` are never compressed, because those are the bodies that can carry a credential. |
| `CORS_ALLOW_ORIGINS` | No | empty | Comma-separated `Origin:` values the API accepts cross-origin. Only needed when the SPA is served from a different origin than the API — the normal single-container deployment is same-origin, so leaving it empty is correct there. Both empty and `*` log a startup warning so the posture is visible in the logs |

> Compose reads `.env` only to substitute `${VAR}` references inside `docker-compose.yml` — it is **not** an env file for the container. A variable reaches Nexara only if the `nexara` service's `environment:` block passes it through, and that block names just these: `PUID`, `PGID`, `DATABASE_URL`, `REDIS_URL`, `JWT_SECRET`, `ENCRYPTION_KEY`, `METRICS_COLLECT_INTERVAL`, `TASK_HISTORY_RETENTION`, `NODE_MEMORY_READ_INTERVAL`, `NODE_PRESSURE_SYNC_INTERVAL`, `SNAPSHOT_SYNC_INTERVAL`, `OBJECT_OBSERVE_INTERVAL`, `NUT_POLL_INTERVAL`, `SECURE_COOKIES`, `HSTS_MAX_AGE`, `LOG_LEVEL`, `TRUSTED_PROXIES`, `PROXY_HEADER`, `WS_ALLOWED_ORIGINS`, `COMPRESSION_ENABLED`. A few more are consumed by the **compose file itself** rather than forwarded to the container — `NEXARA_VERSION` picks the image tag, `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` configure the database service and are substituted into `DATABASE_URL`, and `DATA_DIR` rewrites the volume paths — so those work as documented. **Everything remaining in this table is silently ignored in the default Docker deployment** until you add a line for it (e.g. `RESOURCE_SYNC_INTERVAL: ${RESOURCE_SYNC_INTERVAL:-5s}`) to that block, or to a `docker-compose.override.yml`. The values apply as written when you run the binary directly.

## First-Time Setup

### 1. Create Your Admin Account

Open `http://localhost` (or your configured domain) in a browser. On first run, you'll be redirected to the registration page. The first user created automatically receives the **Admin** role.

### 2. Add a Proxmox Cluster

1. From the dashboard, click **Add Cluster**
2. Enter a display name and the API URL: `https://your-proxmox-host:8006`
3. Click **Connect**. If the host uses a self-signed certificate, verify the
   fingerprint that appears and accept it
4. Choose how Nexara gets its credential:
   - **Create a token for me** (default) — enter a privileged Proxmox login
     (e.g. `root@pam`) and its password. Nexara signs in once, creates a
     dedicated `nexara@pve` user with the `Administrator` role, and issues
     itself an API token. The password is used for that one request and is
     never stored; the token secret never reaches your browser.
   - **I have a token** — paste an existing token id and secret
     (`user@realm!tokenid` + `xxxxxxxx-…`). See
     [Creating a Proxmox API Token](#creating-a-proxmox-api-token) below.
5. Click **Create Token & Add** (or **Add Cluster**)

The collector begins syncing inventory and metrics within seconds. You'll see nodes, VMs, and containers appear on the dashboard.

A few notes on the automatic option:

- If the account has TOTP two-factor authentication enabled, Nexara asks for a
  one-time code and you resubmit the same form. Hardware keys (WebAuthn/U2F)
  cannot be used here — paste an API token instead.
- If the token name is already taken on the cluster, you get an error rather
  than a silently renamed second token — otherwise each retry would leave
  another full-privilege credential behind that nobody holds.
- Re-running after a failure is safe. Nexara adopts a user or role grant that
  already exists instead of duplicating it, and the user it creates has no
  password, so a half-finished attempt leaves nothing usable behind.
- **Serve Nexara over HTTPS before using this.** The password is typed into the
  browser and posted to Nexara; over plain HTTP it travels in the clear. Nexara
  warns and makes you confirm when the page is not in a secure context — pasting
  a token is the better option there, since a token is scoped to one cluster and
  can be revoked on its own.
- When you later delete the cluster, Nexara offers to remove the user and token
  it created. It only ever removes what it created, it skips deleting the user
  entirely if that user holds any other API token, and it leaves the account
  completely alone if another cluster in Nexara still authenticates as it — so a
  credential you added, or one another cluster depends on, is never taken with it.
- A failed attempt is recorded in the audit log along with anything it left
  behind on the cluster, so there is always something to reconcile against.

### 3. Install on Your Phone (Optional)

The UI is fully responsive and ships a PWA manifest: open Nexara in your phone's browser and choose **Add to Home Screen** (or **Install app**) to get a standalone app-style window. All features — including consoles — work on mobile.

### Creating a Proxmox API Token

Only needed for the **I have a token** option above, or if you would rather
create the credential yourself. On your Proxmox host:

```bash
# Create an API token for an existing user
pveum user token add root@pam nexara --privsep 0

# Or create a dedicated user first
pveum user add nexara@pve
pveum aclmod / -user nexara@pve -role Administrator
pveum user token add nexara@pve api --privsep 0
```

Copy the token value — it is only shown once. The format for Nexara is:
```
user@realm!tokenid=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

#### Why `Administrator` and not `PVEAdmin`

`PVEAdmin` looks like the least-privilege choice, but it is not sufficient for
Nexara. Proxmox builds its bundled roles from the *admin*, *user* and *audit*
privilege tiers only — the *root* tier is excluded. That tier holds
`Sys.PowerMgmt`, `Sys.Modify`, `Sys.Incoming`, `Sys.AccessNetwork` and
`Realm.Allocate`, so a `PVEAdmin` token cannot:

- shut down or reboot a node (`Sys.PowerMgmt`)
- change node network configuration (`Sys.Modify`)
- change a node's options — start-on-boot delay, ballooning target, Wake-on-LAN, location and notes (`Sys.Modify` on `/`)
- manage Proxmox roles from **Access Control** (`Sys.Modify` on `/access`)
- manage authentication realms (`Realm.Allocate`)

On a stock PVE 9 cluster the difference is 40 privileges versus 47. If you
previously followed this guide with `PVEAdmin`, those actions have been failing
with a permission error; re-run the `aclmod` line above to fix it. No new token
is needed — the ACL is attached to the user, and a `--privsep 0` token inherits
whatever the user holds.

To check what your token can actually do, open a cluster in Nexara and go to
**Access Control → Permissions**; sections Nexara's own token cannot use are
disabled with the missing privilege named.

Nexara's automatic option grants `Administrator` for exactly this reason, so a
cluster onboarded that way is not affected by the `PVEAdmin` shortfall above.

## Services

| Service | Container | Port | Description |
|---------|-----------|------|-------------|
| Nexara | `nexara` | 8080 (mapped to 80) | Unified: API + WebSocket + frontend + collector + scheduler |
| PostgreSQL | `nexara-db` | 5432 (internal only) | Primary database + TimescaleDB |
| Redis | `nexara-redis` | 6379 (internal only) | Pub/sub, permission and session cache, short-lived sign-in state and lockout counters |

## Updating

```bash
# The directory you installed into: ~/nexara with the install script, ./Nexara with a manual clone
cd ~/nexara

# Back up first. Rolling back an upgrade that ran migrations needs a dump from before it,
# and the TimescaleDB version that dump was taken on, so note the version this prints.
docker compose exec -T nexara-db psql -U nexara -d nexara -tAc \
  "SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'"
docker compose exec -T nexara-db pg_dump -U nexara -Fc nexara > nexara-pre-upgrade-$(date +%Y%m%d).dump

# Pull the compose/.env.example changes that ship with the release
git pull

# Pull the new image and recreate the containers (migrations run automatically)
docker compose pull
docker compose up -d
```

The stack runs a prebuilt image from GHCR — there is nothing to compile locally. By default it tracks `latest`; pin `NEXARA_VERSION` in `.env` (e.g. `NEXARA_VERSION=1.17.0` — the published image tags drop the leading `v` from the git tag) when you want to control exactly which release you move to. Pinning an older tag does not by itself undo an upgrade that ran migrations, because an older image will not start on a schema newer than it knows: see [Recovering from a failed upgrade](#recovering-from-a-failed-upgrade). A pin changes only the Nexara image; if you also revert the compose file to a release that predates Redis 8, see [Rolling back to Redis 7](#rolling-back-to-redis-7).

Database migrations are applied automatically when the API server starts. There is no need to run them manually.

### Updating TimescaleDB

`docker compose pull` also fetches the database image, and its `latest-pg16` tag moves with each TimescaleDB release. The database keeps running on the extension version it already has: neither the new image nor Nexara updates it. It is safe to leave for a while, and when you want the newer version, run this on its own, as the session's first command (`-X` keeps a `.psqlrc` from running anything ahead of it):

```bash
docker compose stop nexara
docker compose exec -T nexara-db psql -X -U nexara -d nexara -c "ALTER EXTENSION timescaledb UPDATE;"
docker compose up -d
```

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
docker compose exec -T nexara-db pg_dump -U nexara -Fc nexara > nexara-backup.dump

# 3. Clear the dirty flag. Check what the failed migration's .up.sql does
#    (the status output names the version; the SQL is in the repo's
#    migrations/ directory), then record the version that is actually
#    fully applied — usually the one BEFORE the failure:
docker compose run --rm nexara migrate force <version>

# 4. Retry the pending migrations, or just restart:
docker compose run --rm nexara migrate up
docker compose up -d
```

One exception: if `migrate status` reports the schema is dirty at version 1,
there is nothing to force back to — `migrate force 0` is rejected deliberately,
because no `000000` migration exists and forcing it wedges the install. A
first-migration failure means the database was never usable: fix the cause (the
first migration needs only the `pgcrypto` extension and a writable database —
check the Postgres logs), then drop and recreate the database — or restore your
backup — and start the stack again.

If the migration fails again the error is real. Restore the backup you took before
the upgrade, not the dump from step 2, which holds the failed state; pin the previous
image version (`NEXARA_VERSION`); and report the migration error. With no such backup,
force the schema back as in step 3 and use `migrate down` below.

`nexara migrate down <n>` rolls back the last `n` migrations (running their
`.down.sql`). Down migrations can drop data — only use it as part of a
deliberate rollback to a matching older image, and always back up first. Run it
from the newer image, before pinning the older one. An older image has no
`.down.sql` for migrations newer than it, and refuses to start at all on a schema
newer than it knows (`no migration found for version N`), so pinning an older tag
alone leaves the container restart-looping.

### Rolling back to Redis 7

From v1.12.0 the bundled `docker-compose.yml` runs `redis:8-alpine` (it was
`redis:7-alpine` before). Upgrading is automatic — Redis 8 reads a Redis 7
volume without help. **Going back is not**, because Redis 8 writes a newer RDB
format that Redis 7 refuses to load:

```
# Can't handle RDB format version 15
# Fatal error loading the DB, check server logs. Exiting.
```

Redis then restart-loops, and because the app waits on
`depends_on: nexara-redis: condition: service_healthy`, Nexara never starts
either. You will see `dependency failed to start: container nexara-redis is
unhealthy`. This only happens if you revert the compose file (for example
`git checkout` of an older tag) after having run Redis 8 — pinning
`NEXARA_VERSION` alone does not change the Redis image.

The fix is to delete the Redis dump and let it start empty:

```bash
docker compose stop nexara-redis
docker compose run --rm --no-deps --entrypoint sh nexara-redis -c 'rm -f /data/dump.rdb'
docker compose up -d
```

Going through `docker compose run` rather than a bare `docker run -v ...` matters:
Compose prefixes named volumes with the project name, and `DATA_DIR` replaces
them with bind mounts entirely, so naming the volume by hand hits the wrong
target in both cases.

**Nothing is lost.** Nexara uses Redis purely as a cache and pub/sub bus —
every key it writes carries a TTL, sessions are authoritative in PostgreSQL, and
the permission cache repopulates on the next request. Users stay logged in.

> **Running your own stack?** If you deploy from your own compose file, a Swarm
> stack, or Kubernetes rather than the bundled `docker-compose.yml`, `git pull`
> never touches your Redis image — it stays on whatever you pinned. That is
> fine: **Nexara works with Redis 7 and 8 alike**, so there is nothing you have
> to do. Move to `redis:8-alpine` when it suits you, and note that once you do,
> going back needs the `dump.rdb` step above.
>
> The same applies if you pin the bundled service to `redis:7-alpine`, or point
> `REDIS_URL` at an external, managed or Valkey instance **and remove the
> `nexara-redis` service from your compose file**. If you left that service in
> place it still starts, and the app still waits on it — so the note above
> applies to it even though your data lives elsewhere.
>
> Any Redis you bring must allow `EVAL`/`EVALSHA`, as the stock image does: the
> change-password and two-factor lockout runs on Lua scripts, and those routes
> answer 503 without them.

## Backup & Restore

A database backup holds your clusters' credentials encrypted under `ENCRYPTION_KEY`, so **a dump without its key cannot bring them back**: every cluster, PBS server, Veeam server and notification channel would need its secret entered again. Unless you set the key yourself in `.env`, it lives in the data volume, so copy it out once and keep it apart from the dumps:

```bash
docker compose cp nexara:/data/nexara/.secrets.json ./nexara-secrets.json
chmod 600 nexara-secrets.json
```

### Database Backup

```bash
# Note the TimescaleDB version: a dump has to go back onto the same one
docker compose exec -T nexara-db psql -U nexara -d nexara -tAc \
  "SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'"

# Dump the database
docker compose exec -T nexara-db pg_dump -U nexara -Fc nexara > nexara-backup-$(date +%Y%m%d).dump
```

`pg_dump` warns about circular foreign keys on TimescaleDB's own tables (`hypertable`, `chunk` or `continuous_agg`, depending on the version) and hints that the dump might not restore. That is expected; the steps below restore it.

### Database Restore

TimescaleDB needs the steps below in this order. A plain `pg_restore --clean` over the live database reports well over a hundred errors and leaves it with hypertables missing, while the row counts still look right. On a new host, install Nexara and start it first, so there is a stack to restore into.

```bash
# Stop the application (keep the database running)
docker compose stop nexara

# On a new host or data volume, put the key back too, unless ENCRYPTION_KEY is set in .env
docker compose cp ./nexara-secrets.json nexara:/data/nexara/.secrets.json

# Replace the database. This deletes what is in it now.
docker compose exec -T nexara-db psql -U nexara -d postgres \
  -c "DROP DATABASE nexara;" -c "CREATE DATABASE nexara TEMPLATE template0;"

# Install the TimescaleDB version the dump was taken on, and enter restore mode
docker compose exec -T nexara-db psql -U nexara -d nexara \
  -c "CREATE EXTENSION timescaledb VERSION '<version from the backup>';" \
  -c "SELECT timescaledb_pre_restore();"

# Restore, and leave restore mode
docker compose exec -T nexara-db pg_restore -U nexara -d nexara --no-owner --no-acl < nexara-backup-20260101.dump
docker compose exec -T nexara-db psql -U nexara -d nexara -c "SELECT timescaledb_post_restore();"

# Bring the extension up to the image's version, then restart
docker compose exec -T nexara-db psql -X -U nexara -d nexara -c "ALTER EXTENSION timescaledb UPDATE;"
docker compose up -d
```

Restoring into a different TimescaleDB version fails with errors such as `column "schema_name" of relation "chunk" does not exist`. If `CREATE EXTENSION` says the version has no installation script, the image no longer carries it: set the `nexara-db` image in a `docker-compose.override.yml` to that release (`timescale/timescaledb:<version>-pg16`) for the restore.

### Volume Backup

Nexara stores state in **three** volumes: `nexara-db-data` (PostgreSQL), `nexara-redis-data` (Redis), and `nexara-data` (secrets, branding, uploads). Compose prefixes each with the project name, by default the install directory's name in lower case, so `nexara_nexara-db-data` for `~/nexara`. Use the full name: `docker run -v` with the short one creates a new, empty volume and backs that up without complaint. For a full cold backup:

```bash
# The prefix Compose uses for this install
PROJECT=$(docker compose config | sed -n 's/^name: //p')

# Stop all services
docker compose down

# Back up all three Docker volumes into one archive
docker run --rm \
  -v "${PROJECT}_nexara-db-data":/vol/db -v "${PROJECT}_nexara-redis-data":/vol/redis -v "${PROJECT}_nexara-data":/vol/data \
  -v "$(pwd)":/backup alpine \
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

### "resolve secrets", "JWT_SECRET must be at least 16 characters" or "ENCRYPTION_KEY must be exactly 64 hex characters"

Secrets are auto-generated on first start and persisted to `DATA_DIR/.secrets.json`. `resolve secrets: …` at startup means that file could not be read or written, so check that the data volume is writable and owned by `PUID`/`PGID`. The other two mean a value you set yourself is invalid: `JWT_SECRET` needs at least 16 characters, and `ENCRYPTION_KEY` is 64 hex characters (`openssl rand -hex 32`). Values set in `.env` take precedence over the file. Don't change `ENCRYPTION_KEY` on an install that has stored credentials, since they can no longer be decrypted afterwards.

### Database connection refused

- Ensure `nexara-db` is healthy: `docker compose ps nexara-db`
- Check that `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` in `.env` match the database. `docker-compose.yml` builds `DATABASE_URL` from those three values and sets it on the container, so a `DATABASE_URL` written into `.env` is ignored in the default compose deployment — it only applies when you run the binary outside compose
- The API server waits for the database health check — if the DB is slow to start, the API will retry

### Duplicate rows or corrupt indexes

If the logs show `removed duplicate rows`, or inventory pages list the same guest
twice after a hard crash, the image ships a repair command. Nexara already
deduplicates the inventory tables on every startup; the CLI adds a hypertable
REINDEX, which takes an exclusive lock and can run for a long time on a large
metrics history — run it in a quiet window:

```bash
# Stop the app (keep the database running)
docker compose stop nexara

# Dedupe + REINDEX; Ctrl-C aborts it cleanly
docker compose run --rm nexara repair-integrity

docker compose up -d
```

### Port conflicts

Only the Nexara container publishes a host port. PostgreSQL and Redis are
reachable only on the internal compose network, so they never collide with a
database or cache you already run on the host.

If port 80 is already in use:

1. Edit `docker-compose.yml` and change only the host side of the Nexara mapping
2. Change `"80:8080"` to e.g. `"8443:8080"` — leave `8080` (the container port) alone
3. Internal service-to-service communication uses container names, not host ports

### Frontend shows blank page

- Check the Nexara container logs: `docker compose logs nexara`
- Verify the container is healthy: `docker compose ps`

### Proxmox connection fails

- Verify the API URL is reachable from the Docker host
- Check that the API token has sufficient privileges
- For self-signed certificates, verify and accept the fingerprint **Connect** shows when adding the cluster. If it worked until a Proxmox upgrade, see [When a Node's Certificate Changes](admin-guide.md#when-a-nodes-certificate-changes)
- Check logs: `docker compose logs nexara`

### Reset admin password

If you lose access to a local account, set a new password in the database. Nexara stores bcrypt hashes, which `pgcrypto` can produce:

```bash
docker compose exec nexara-db psql -U nexara nexara
```

```sql
-- See the accounts; only an auth_source of 'local' signs in with this password.
-- system@nexara.local is Nexara's internal actor, not a login, so it is left out.
SELECT email, role, auth_source, is_active FROM users WHERE email <> 'system@nexara.local';

UPDATE users
   SET password_hash = crypt('a-new-long-password', gen_salt('bf', 12)), is_active = true
 WHERE email = 'admin@example.com' AND auth_source = 'local';

-- Only if the account's two-factor device is gone too
UPDATE users SET totp_secret = NULL WHERE email = 'admin@example.com';
DELETE FROM totp_recovery_codes
 WHERE user_id = (SELECT id FROM users WHERE email = 'admin@example.com');
```

Sign in, change the password on your **Profile** page (user menu, top right) so the temporary one is no longer in your `psql` history, and use **Sign Out All Devices** in the same menu if the old password may have leaked.

Don't delete rows from `users` to get the first-run setup back. Alert rules, notification channels, reports, rolling update jobs and imports reference the user who created them, so the `DELETE` fails on an install that has any, and on one that has none it takes that user's task history and API keys with it.
