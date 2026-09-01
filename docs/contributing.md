# Contributing to Nexara

Thank you for your interest in contributing to Nexara! This guide covers the development environment setup, coding conventions, and contribution process.

## Development Environment

### Prerequisites

- **Go** 1.25+ — [install](https://go.dev/doc/install)
- **Node.js** 22 — [install](https://nodejs.org/) (CI builds on 22; the hard floor is Vite 8's `^20.19.0 || >=22.12.0`)
- **PostgreSQL** 16 with TimescaleDB — or use Docker
- **Redis** 7 — or use Docker
- **sqlc** — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
- **golang-migrate** — `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
- **golangci-lint** v2 — `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2` (the `/v2` path matters: `.golangci.yml` is a v2-schema config and a v1 binary rejects it; CI pins v2.12.2)
- **govulncheck** (only for `make audit`) — `go install golang.org/x/vuln/cmd/govulncheck@latest`

### Quick Start

```bash
# Clone the repo
git clone https://github.com/bigjakk/Nexara.git
cd Nexara

# Start dependencies (DB + Redis).
# docker-compose.yml deliberately publishes no host ports for them (only the app
# maps 80:8080), so add a local override — docker-compose.override.yml is gitignored:
cat > docker-compose.override.yml <<'YAML'
services:
  nexara-db:
    ports: ["5432:5432"]
  nexara-redis:
    ports: ["6379:6379"]
YAML
docker compose up -d nexara-db nexara-redis

# Apply migrations. Optional — the binary applies pending migrations from its
# embedded copy on startup — but useful for stepping through them. Pass the URL
# explicitly: the Makefile default is nexara:nexara, compose defaults to nexara:changeme.
make migrate-up DATABASE_URL="postgres://nexara:changeme@localhost:5432/nexara?sslmode=disable"

# Build the unified binary
make build

# Generate the dev encryption key ONCE and reuse it. It decrypts the Proxmox
# token secrets stored in the database, so a fresh `openssl rand -hex 32` on
# every start leaves every cluster you already added undecryptable. Must be
# exactly 64 hex characters; JWT_SECRET must be at least 16.
export NEXARA_DEV_KEY=$(openssl rand -hex 32)   # save this somewhere

# Start the server
DATABASE_URL="postgres://nexara:changeme@localhost:5432/nexara?sslmode=disable" \
REDIS_URL="redis://localhost:6379/0" \
JWT_SECRET="dev-secret-change-me-1234567890" \
ENCRYPTION_KEY="$NEXARA_DEV_KEY" \
./bin/nexara

# (Alternative: leave JWT_SECRET and ENCRYPTION_KEY unset and point DATA_DIR at
# a writable directory — the binary then generates both once and persists them
# to $DATA_DIR/.secrets.json. Env vars always win over the stored values.)

# In another terminal — start the frontend
cd frontend
npm install
npm run dev
```

The frontend dev server runs at `http://localhost:3000` with hot reload (configured in `frontend/vite.config.ts`).

## Project Structure

```
nexara/
├── cmd/
│   └── nexara/             # Unified entry point (API + WS + collector + scheduler + embedded frontend)
├── internal/               # Private Go packages
│   ├── api/                # HTTP handlers, router, middleware
│   │   └── handlers/       # Handler files (one per domain)
│   ├── app/                # Composition root — wires every long-lived engine
│   ├── auth/               # RBAC, LDAP, OIDC, TOTP
│   ├── changelog/          # GitHub Releases fetch/parse for the in-app popup
│   ├── collector/          # Inventory and metric sync
│   ├── config/             # Configuration loading
│   ├── crypto/             # Authenticated symmetric encryption for secrets at rest
│   ├── db/                 # Database layer (sqlc generated)
│   │   └── generated/      # Auto-generated — DO NOT EDIT
│   ├── debug/              # pprof endpoints (PPROF_ENABLED)
│   ├── drs/                # Distributed Resource Scheduler
│   ├── events/             # Redis pub/sub event publisher
│   ├── guesttools/         # Windows virtio-win / QEMU-GA tracking and staged in-guest updates
│   ├── migration/          # Cross-cluster guest migration: pre-flight checks + orchestrator
│   ├── netguard/           # SSRF guards for outbound requests
│   ├── notifications/      # Alert engine, dispatchers
│   ├── proxmox/            # Proxmox VE/PBS API client
│   ├── reports/            # Report generation
│   ├── rolling/            # Rolling update orchestrator
│   ├── safeconv/           # Bounds-clamping numeric conversions
│   ├── scanner/            # CVE scanner
│   ├── scheduler/          # Scheduler engine
│   ├── ssh/                # SSH client for rolling updates
│   ├── syslog/             # RFC 5424 audit-event forwarding
│   ├── veeam/              # Veeam Backup & Replication REST client (VBR 13.1+)
│   ├── virtiowin/          # virtio-win release discovery + ISO download into Proxmox storage
│   └── ws/                 # WebSocket hub and handlers
├── pkg/
│   └── redisutil/          # Shared Redis connection helpers
├── frontend/               # React SPA
│   └── src/
│       ├── components/     # Shared UI components
│       ├── features/       # Feature modules (pages, components, api, types, lib)
│       ├── hooks/          # Shared hooks
│       ├── lib/            # Utilities
│       ├── locales/        # i18n namespace JSON (en/)
│       ├── stores/         # Zustand stores
│       ├── test/           # Vitest setup + render helpers
│       └── types/          # Shared TypeScript types
├── queries/                # sqlc SQL queries
├── migrations/             # Database migrations
├── docker/                 # Dockerfiles and configs
├── scripts/                # Utility scripts
└── docs/                   # Documentation
```

## Code Conventions

### Go

- **Error handling:** wrap errors with context: `fmt.Errorf("doing X: %w", err)`
- **Naming:** unexported by default; only export what's needed by other packages
- **Tests:** table-driven tests. Redis-backed code fakes Redis with `miniredis` (`github.com/alicebob/miniredis/v2`); DB-backed tests in `internal/db/` run against a real *throwaway* Postgres named by `NEXARA_TEST_DB_URL` — see [Database-backed tests](#database-backed-tests). There is no `testcontainers-go` in this repo.
- **Imports:** standard library first, then third-party, then internal
- **Linting:** `golangci-lint` with config at `.golangci.yml`

### TypeScript

- **Strict mode** — `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes` enabled
- **No `any` types** — use `unknown` and narrow with type guards
- **Components:** functional with hooks; no class components
- **State:** TanStack Query for server state, Zustand for client state
- **Styling:** Tailwind CSS v4 utility classes via Shadcn/ui components — CSS-first configuration (no `tailwind.config.js`; the theme lives in CSS)
- **Icons:** Lucide React exclusively
- **Linting:** ESLint strict-type-checked + Prettier (flat config at `frontend/eslint.config.js`)

### Frontend Feature Modules

Each feature lives in `frontend/src/features/<name>/` with:

```
features/<name>/
├── pages/          # Route-level page components
├── components/     # Feature-specific UI components
├── api/            # TanStack Query hooks & API functions
├── types/          # TypeScript interfaces
├── lib/            # Pure logic (parsers, transforms) — the easiest thing to unit-test
└── hooks/          # Custom hooks (optional)
```

Tests live next to the code they cover — `components/Foo.test.tsx`, `lib/foo.test.ts` — not in a separate tree.

## Database Workflow

Nexara uses **sqlc** for type-safe SQL. Never write raw SQL in Go code.

### Adding a Query

1. Write SQL in `queries/<resource>.sql`:
   ```sql
   -- name: GetWidget :one
   SELECT * FROM widgets WHERE id = $1;

   -- name: ListWidgets :many
   SELECT * FROM widgets ORDER BY created_at DESC LIMIT $1 OFFSET $2;
   ```

2. Regenerate Go code:
   ```bash
   make generate
   ```

3. Use the generated functions in your handler:
   ```go
   widget, err := s.queries.GetWidget(ctx, id)
   ```

### Adding a Migration

1. Create migration files in `migrations/`:
   ```
   migrations/000083_add_widgets.up.sql   # next free number — check `ls migrations/ | tail`
   migrations/000083_add_widgets.down.sql
   ```

2. The up migration creates the schema; the down migration reverses it

3. Apply (same credential caveat as the Quick Start — the Makefile default is `nexara:nexara`, compose defaults to `nexara:changeme`):
   ```bash
   make migrate-up DATABASE_URL="postgres://nexara:changeme@localhost:5432/nexara?sslmode=disable"
   ```

4. Never modify existing migrations that have been released. Create a new migration instead.

5. Migrations are applied **automatically against existing user databases** on container startup — the binary reads its own `go:embed`ed copy of `migrations/` and runs the same chain the CLI does. The default expectation is therefore that a new migration upgrades an existing install **unattended**. Always safe: `CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS … NOT NULL DEFAULT X`, `CREATE INDEX IF NOT EXISTS`, lookup-table inserts with `ON CONFLICT DO NOTHING`. Renames, drops, `ALTER COLUMN … TYPE`, new `UNIQUE`/`NOT NULL` constraints and derived backfills need the migration to resolve the problem *itself* (dedupe before adding the constraint, backfill before setting `NOT NULL`) — `migrations/000075_settings_null_scope_unique.up.sql` is the worked example. A change is breaking **only if the operator must do something**; then the `.up.sql` opens with a `⚠️ BREAKING UPGRADE` header documenting the full procedure and the commit carries a `BREAKING:` prefix. When in doubt, make the change additive: write the new column or table alongside the old one, ship a release that dual-writes, and drop the old one later.

6. Each migration runs inside a single transaction (golang-migrate's default), so non-transactional statements such as `CREATE INDEX CONCURRENTLY` are not supported.

7. If a startup migration fails, operators recover with `nexara migrate status|up|down|force`, which acts on the exact migration set baked into that binary.

## Testing

### Go Tests

```bash
# Run all tests with race detection
make test

# Run tests for a specific package
go test -race ./internal/drs/...

# Run a specific test
go test -race -run TestEvaluate ./internal/drs/...
```

#### Database-backed tests

The migration round-trip and query-scoping tests in `internal/db/` **skip themselves** unless `NEXARA_TEST_DB_URL` is set. The package still reports `ok`, so a broken migration chain looks green locally:

```
--- SKIP: TestMigrationChain_FullUpDownUp (0.00s)
    migration_helpers_test.go:35: NEXARA_TEST_DB_URL not set; skipping migration test
```

These tests migrate the schema all the way up and back down, destroying every row, so the URL **must** name a throwaway database — the helper hard-fails (not skips) on anything else. A name counts as disposable when some `_`/`-` separated segment is or ends in `test` (`nexara_chaintest`, `nexara_test`):

```bash
docker compose exec nexara-db createdb -U nexara nexara_chaintest

NEXARA_TEST_DB_URL="postgres://nexara:changeme@localhost:5432/nexara_chaintest?sslmode=disable" \
  go test -race ./internal/db/...
```

Never point it at `nexara`, `nexara_dev`, or anything else you care about. CI sets it for every push, PR, and `v*` tag against a TimescaleDB service container, so the round-trip always runs there.

Use table-driven tests:

```go
func TestCalculate(t *testing.T) {
    tests := []struct {
        name     string
        input    int
        expected int
    }{
        {"zero", 0, 0},
        {"positive", 5, 25},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := Calculate(tt.input)
            if got != tt.expected {
                t.Errorf("Calculate(%d) = %d, want %d", tt.input, got, tt.expected)
            }
        })
    }
}
```

### Frontend Tests

Tests run on **Vitest** in a jsdom environment with Testing Library — config in `frontend/vitest.config.ts` (note: separate from `vite.config.ts`), global setup in `src/test/setup.ts`. Test files live next to the code they cover.

```bash
cd frontend

# Run all tests
npm test

# Run tests in watch mode
npm run test -- --watch

# Run tests for a specific file
npm test -- src/features/topology/lib/topology-transform.test.ts
```

### Testing against a disposable stack

Unit tests cannot reach the things that only break against a real hypervisor with
real data — credential minting, cluster deletion, revocation. Both bugs found in
the cluster-onboarding work surfaced only this way, and neither was reachable
from a test suite.

**Never test cluster deletion against your dev stack.** `clusters.id` is the
target of ~30 `ON DELETE CASCADE` foreign keys: deleting one row takes its
nodes, VMs, task history, audit log and every metric sample with it. Re-adding
the cluster gives you an empty shell.

Instead, run a second Nexara seeded from a dump of your dev database.

#### 1. Back up, and prove the backup

TimescaleDB dumps need the pre/post-restore dance. A plain `pg_restore` produces
a database that looks fine and is not:

```bash
mkdir -p ~/nexara-backups
docker exec nexara-db pg_dump -U nexara -d nexara -Fc --no-owner --no-acl \
  > ~/nexara-backups/nexara-$(date +%Y%m%d-%H%M%S).dump
```

The `pg_dump` warning about circular foreign keys on `hypertable` is expected.

```bash
docker exec nexara-db psql -U nexara -d postgres -c "CREATE DATABASE nexara_test;"
docker exec nexara-db psql -U nexara -d nexara_test \
  -c "CREATE EXTENSION IF NOT EXISTS timescaledb;" \
  -c "SELECT timescaledb_pre_restore();"

cat ~/nexara-backups/nexara-*.dump | docker exec -i nexara-db \
  pg_restore -U nexara -d nexara_test --no-owner --no-acl

docker exec nexara-db psql -U nexara -d nexara_test -c "SELECT timescaledb_post_restore();"
```

Verify it actually restored — an untested backup is not a backup:

```bash
docker exec nexara-db psql -U nexara -d nexara_test -c \
  "SELECT (SELECT count(*) FROM clusters) clusters, (SELECT count(*) FROM vms) vms,
          (SELECT count(*) FROM timescaledb_information.hypertables) hypertables;"
```

#### 2. Apply the safety cutouts — before first boot

The restored rows point at your **real** Proxmox hosts, so the test stack runs a
second collector, scheduler and DRS engine against live infrastructure. If DRS is
on `automatic` in dev, two engines will plan migrations on the same cluster and
fight each other.

```sql
UPDATE drs_configs SET enabled = false, mode = 'disabled';
UPDATE notification_channels SET enabled = false;
UPDATE alert_rules SET enabled = false;
```

Also confirm nothing is mid-flight — the cleanup sweep mutates the real cluster
(CRS pause, HA rules):

```sql
SELECT count(*) FROM rolling_update_jobs WHERE cleanup_pending OR status IN ('running','pending');
```

#### 3. Bring it up

```bash
TEST_HOST_IP=<this-box-ip> docker compose --env-file .env \
  -f docker/test/docker-compose.test.yml up -d
```

Reach it at **`https://<this-box-ip>:8443`** and click through the self-signed
warning. Points worth knowing:

- **Use the IP, not your dev hostname.** If a reverse proxy fronts your dev stack,
  that hostname resolves to the proxy, which routes only 80/443 and knows nothing
  about 8443. `ERR_CONNECTION_REFUSED` there is a routing fact, not a firewall.
- **HTTPS is required.** The onboarding password field is gated on
  `window.isSecureContext`. An accepted self-signed cert still counts as one;
  plain HTTP does not.
- **`ENCRYPTION_KEY` and `JWT_SECRET` must match dev**, or the restored token
  secrets will not decrypt and every cluster fails to connect.
- The stack shares the dev **Postgres server** but uses its own database, and gets
  its **own Redis** — Redis pub/sub channels are global across DB indexes, so a
  shared instance cross-wires cache invalidation and WS events between the apps.

Confirm the cutouts held: `docker logs nexara-test | grep -c drs-engine` → `0`.

#### 4. Test, then tear down

```bash
docker compose -f docker/test/docker-compose.test.yml down
docker exec nexara-db psql -U nexara -d postgres -c "DROP DATABASE nexara_test;"
```

#### Verifying Proxmox-side effects

For anything that mutates Proxmox access control, capture a baseline **before**
and diff **after**, rather than eyeballing the UI:

```bash
# users / tokens / ACL / groups, via the Access Control tab or the API
GET /api/v1/clusters/:id/access/users
GET /api/v1/clusters/:id/access/acl
```

Read the "after" state using the **credential under test** where you can. If a
change was supposed to leave a grant intact, a successful read through a
`privsep=0` token proves it — that token inherits its owner's privileges, so a
revoked grant turns the same call into a 403.

### Linting

```bash
# Go
make lint

# Frontend
cd frontend && npx eslint src/
```

## Pull Request Process

> **Where the code lives.** GitHub is a **one-way mirror**. The upstream repository is a private Gitea instance and every push flows Gitea → GitHub, never back. Cloning, building and reading from GitHub all work normally, but a pull request opened there cannot be merged in place — a maintainer replays the commits upstream and they reappear in the mirror on the next sync. Open issues and pull requests on GitHub anyway; that is the contact point. Expect a PR to be closed with thanks rather than merged when the change actually lands.

### Branch Naming

```
feat/phase-X-task-Y-description
fix/short-description
refactor/short-description
```

### Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add widget management API
fix: resolve race condition in DRS evaluation
refactor: extract common auth middleware
test: add integration tests for backup handler
docs: update API reference for CVE endpoints
chore: upgrade Go dependencies
```

### PR Checklist

- [ ] Code follows the project's style guidelines
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] Frontend changes pass `npx tsc --noEmit`, `npm run lint`, and `npm test`; new pure logic under `features/<name>/lib/` has a colocated `*.test.ts`
- [ ] New SQL queries added to `queries/` and regenerated with `make generate`
- [ ] New migrations include both up and down files
- [ ] New API endpoints have RBAC permission checks
- [ ] New features have corresponding audit log entries
- [ ] Sensitive data is encrypted at rest (AES-256-GCM via `ENCRYPTION_KEY`)
- [ ] No secrets in code or config files

Several of those items are enforced mechanically — a violation fails `make test`, not review. They are static-analysis tests (`go/ast`, no database, no running server), so they run in the normal `go test ./...`:

| Guard | Enforces |
|-------|----------|
| `internal/api/rbac_route_guard_test.go` | every registered route resolves to a handler that can reach a permission check, and the permission `api_docs.go` advertises matches the one the handler enforces |
| `internal/api/handlers/tracktask_guard_test.go` | every handler that captures a UPID from a Proxmox client call records it via `handlers.TrackTask`, and no handler defines its own `auditLog` wrapper instead of the shared `handlers.AuditLog` |
| `internal/api/api_docs_drift_test.go` | every `endpointMeta` key in `handlers/api_docs.go` matches a route actually registered in `router.go` |
| `internal/api/handlers/list_envelope_guard_test.go` | every collection response goes out as `handlers.ListResponse[T]` via `RespondItems`/`RespondList`, never a bare JSON array — the shape external clients depend on since v1.10.0 |
| `internal/api/handlers/credential_redirect_guard_test.go` | an `Update` handler that carries a stored secret forward while letting the caller change the address must consult `credentialRedirected` before the write, so a saved credential is never re-pointed at a new host |
| `internal/api/handlers/confirm_gate_guard_test.go` | a confirm gate *returns* its refusal instead of writing the response itself — a gate that writes and returns `nil` leaves `err != nil` false, and the handler runs on and performs the action it just "refused" |
| `internal/api/handlers/scope_params_guard_test.go` | every RBAC-scoped list query is built with `AccessibleClusterIds` set — a nil reaches SQL as NULL and lifts the filter |
| `internal/api/handlers/audit_cluster_guard_test.go` | every `AuditLog`/`AuditLogAs` call passes the audited resource's cluster, unless the call site is on the documented global-audit exemption list |
| `internal/api/handlers/metrics_authz_guard_test.go` | every historical-metrics endpoint gates on a cluster-scoped permission |
| `internal/app/guard_test.go` | each domain-service constructor is called exactly once, from the composition root. Before `internal/app` existed each was called at two to four sites with differing dependencies, and the differences were silent bugs — a rolling orchestrator with a nil notification registry dropped job-failure notifications, per-request DRS engines raced the scheduler's leader lock |
| `internal/proxmox/transport_guard_test.go`, `internal/veeam/transport_guard_test.go` | nothing in those packages builds its own `http.Transport`/`http.Client` — `buildHTTPClient` is the sole owner of the SSRF dial guard, TLS fingerprint pinning and redirect refusal. A second constructor silently forks that hardening, and the fork is the one talking to a user-supplied URL |
| `internal/db/scope_sql_guard_test.go` | scoped reads in `queries/*.sql` carry the exact `cluster_id = ANY(...)` scope clause |
| `internal/db/testdb_guard_test.go` | migration tests obtain `NEXARA_TEST_DB_URL` through the throwaway-database guard, never `os.Getenv` directly |

### Merge Strategy

`master` is the only long-lived branch — there is no `develop` and no `main`. Branch off `master`, squash merge back into `master`, and delete the branch. Releases are cut from `master` by pushing a tag (see [Releases](#releases)).

## Releases

There is no `VERSION` file — a release **is** a tag:

```bash
git tag -a v1.9.0 -m "v1.9.0"
git push origin master v1.9.0
```

The version the binary reports comes from `git describe --tags`, injected via ldflags into `internal/api.Version` (see `LDFLAGS` in the Makefile). Pick the bump from the commits since the last tag: `fix:`/`docs:`/`chore:` → patch, `feat:` → minor, a `BREAKING:` commit → major.

Pushing a `v*` tag fires **two** release workflows — `.gitea/workflows/release.yml` on Gitea Actions and `.github/workflows/release.yml` on GitHub Actions once the mirror syncs. Each one re-runs the full CI suite (including the Postgres-backed migration round-trip — a `v*` tag does *not* match `ci.yml`'s `push: branches` trigger, which is why that block is duplicated there), builds and pushes the image to its registry, and creates the release automatically. The in-app changelog popup reads the GitHub Release, so nothing needs to be published by hand.

The two workflow sets are deliberate copies: **change one, change the other.** They differ only where the runners differ — the Gitea job runs inside a container, so it addresses the Postgres service by name and waits for it explicitly, while the GitHub job maps the port onto localhost.

## Common Commands

```bash
make build          # Build unified Go binary
make test           # Run Go tests with race detection
make lint           # Run golangci-lint
make generate       # Run sqlc generate
make migrate-up     # Apply pending migrations
make migrate-down   # Rollback last migration
make docker-build   # Build Docker image
make docker-up      # Start full stack
make docker-down    # Stop full stack
make frontend-build # npm ci + vite build, then copy dist/ into cmd/nexara/ for go:embed
make coverage-html  # Run tests with coverage and write coverage.html
make audit          # govulncheck + npm audit (or audit-go / audit-npm individually)
make help           # List every target
make clean          # Remove build artifacts
```

## Getting Help

- Open an issue on [GitHub](https://github.com/bigjakk/Nexara/issues)
- Check existing documentation in `docs/`
- Read the `*_guard_test.go` files under `internal/api/` and `internal/db/` — they encode, and enforce, the conventions this guide describes
