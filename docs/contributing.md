# Contributing to Nexara

Thank you for your interest in contributing to Nexara! This guide covers the development environment setup, coding conventions, and contribution process.

## Development Environment

### Prerequisites

- **Go** 1.24+ — [install](https://go.dev/doc/install)
- **Node.js** 20+ — [install](https://nodejs.org/)
- **PostgreSQL** 16 with TimescaleDB — or use Docker
- **Redis** 7 — or use Docker
- **sqlc** — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
- **golang-migrate** — `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
- **golangci-lint** — `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`

### Quick Start

```bash
# Clone the repo
git clone https://github.com/nexara/nexara.git
cd nexara

# Start dependencies (DB + Redis)
docker compose up -d nexara-db nexara-redis

# Apply migrations
make migrate-up

# Build Go binaries
make build

# Start the API server
DATABASE_URL="postgres://nexara:changeme@localhost:5432/nexara?sslmode=disable" \
REDIS_URL="redis://localhost:6379/0" \
JWT_SECRET="dev-secret-change-me-1234567890" \
ENCRYPTION_KEY="$(openssl rand -hex 32)" \
./bin/nexara-api

# In another terminal — start the frontend
cd frontend
npm install
npm run dev
```

The frontend dev server runs at `http://localhost:5173` with hot reload.

## Project Structure

```
nexara/
├── cmd/                    # Go entry points
│   ├── api/                # REST API server
│   ├── ws/                 # WebSocket server
│   ├── collector/          # Proxmox metric collector
│   └── scheduler/          # Task scheduler
├── internal/               # Private Go packages
│   ├── api/                # HTTP handlers, router, middleware
│   │   └── handlers/       # Handler files (one per domain)
│   ├── auth/               # RBAC, LDAP, OIDC, TOTP
│   ├── collector/          # Inventory and metric sync
│   ├── config/             # Configuration loading
│   ├── db/                 # Database layer (sqlc generated)
│   │   └── generated/      # Auto-generated — DO NOT EDIT
│   ├── drs/                # Distributed Resource Scheduler
│   ├── models/             # Shared domain models
│   ├── network/            # Network/firewall/SDN client
│   ├── notifications/      # Alert engine, dispatchers
│   ├── proxmox/            # Proxmox VE/PBS API client
│   ├── recovery/           # Backup/restore orchestration
│   ├── reports/            # Report generation
│   ├── rolling/            # Rolling update orchestrator
│   ├── scanner/            # CVE scanner
│   ├── scheduler/          # Scheduler engine
│   ├── ssh/                # SSH client for rolling updates
│   ├── storage/            # Storage management
│   └── ws/                 # WebSocket hub and handlers
├── pkg/                    # Public library code
├── frontend/               # React SPA
│   └── src/
│       ├── components/     # Shared UI components
│       ├── features/       # Feature modules (pages, components, api, types)
│       ├── hooks/          # Shared hooks
│       ├── lib/            # Utilities
│       └── stores/         # Zustand stores
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
- **Tests:** table-driven tests; use `testcontainers-go` for integration tests
- **Imports:** standard library first, then third-party, then internal
- **Linting:** `golangci-lint` with config at `.golangci.yml`

### TypeScript

- **Strict mode** — `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes` enabled
- **No `any` types** — use `unknown` and narrow with type guards
- **Components:** functional with hooks; no class components
- **State:** TanStack Query for server state, Zustand for client state
- **Styling:** Tailwind CSS utility classes via Shadcn/ui components
- **Icons:** Lucide React exclusively
- **Linting:** ESLint strict-type-checked + Prettier

### Frontend Feature Modules

Each feature lives in `frontend/src/features/<name>/` with:

```
features/<name>/
├── pages/          # Route-level page components
├── components/     # Feature-specific UI components
├── api/            # TanStack Query hooks & API functions
├── types/          # TypeScript interfaces
└── hooks/          # Custom hooks (optional)
```

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
   migrations/000037_add_widgets.up.sql
   migrations/000037_add_widgets.down.sql
   ```

2. The up migration creates the schema; the down migration reverses it

3. Apply:
   ```bash
   make migrate-up
   ```

4. Never modify existing migrations that have been released. Create a new migration instead.

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

```bash
cd frontend

# Run all tests
npm test

# Run tests in watch mode
npm run test -- --watch

# Run tests for a specific file
npm test -- src/features/topology/lib/topology-transform.test.ts
```

### Linting

```bash
# Go
make lint

# Frontend
cd frontend && npx eslint src/
```

## Pull Request Process

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
- [ ] New SQL queries added to `queries/` and regenerated with `make generate`
- [ ] New migrations include both up and down files
- [ ] New API endpoints have RBAC permission checks
- [ ] New features have corresponding audit log entries
- [ ] Sensitive data is encrypted at rest (AES-256-GCM via `ENCRYPTION_KEY`)
- [ ] No secrets in code or config files

### Merge Strategy

- Feature branches: squash merge to `develop`
- Milestones: merge commit to `main`

## Common Commands

```bash
make build          # Build all Go binaries
make test           # Run Go tests with race detection
make lint           # Run golangci-lint
make generate       # Run sqlc generate
make migrate-up     # Apply pending migrations
make migrate-down   # Rollback last migration
make docker-build   # Build all Docker images
make docker-up      # Start full stack
make docker-down    # Stop full stack
make clean          # Remove build artifacts
```

## Getting Help

- Open an issue on [GitHub](https://github.com/nexara/nexara/issues)
- Check existing documentation in `docs/`
- Review `CLAUDE.md` for codebase conventions
