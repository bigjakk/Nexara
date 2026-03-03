# ProxDash

## Project Overview
ProxDash: free, open-source Proxmox VE/PBS centralized management platform.
Think vCenter for Proxmox. Docker-deployable, API-first, real-time dashboards.

## Tech Stack
- Backend: Go 1.22+ (Fiber v3, sqlc + pgx, gorilla/websocket)
- Frontend: React 19, TypeScript 5, Vite 6, Shadcn/ui, TanStack Query/Table, Zustand, Recharts, xterm.js, noVNC, React Flow, Lucide icons
- Database: PostgreSQL 16 + TimescaleDB extension
- Cache/PubSub: Redis 7 (Valkey compatible)
- Deployment: Docker Compose (8 services)

## Architecture
- cmd/ has 4 Go entry points: api, ws, collector, scheduler
- internal/ has private packages, one per domain
- frontend/ is the React SPA
- All API endpoints defined in OpenAPI 3.1 spec at api/openapi.yaml
- Handlers generated via oapi-codegen from the spec
- All DB queries via sqlc — never write raw SQL in Go code
- Proxmox communication via internal/proxmox client using API tokens

## Code Conventions
### Go
- Follow standard library conventions
- Use golangci-lint config at .golangci.yml
- Errors: wrap with fmt.Errorf("context: %w", err)
- Naming: unexported by default, export only what's needed
- Tests: table-driven tests, use testcontainers-go for integration tests

### TypeScript
- Strict mode, no `any` types
- ESLint + Prettier enforced
- Components: functional with hooks, no class components
- State: TanStack Query for server state, Zustand for client state
- Styling: Tailwind CSS utility classes via Shadcn/ui

## Database
- Migrations in migrations/ directory: YYYYMMDDHHMMSS_description.up.sql / .down.sql
- All queries in queries/ directory, generated to internal/db/ via sqlc
- sqlc.yaml at project root
- TimescaleDB hypertables for time-series metrics

## Git Conventions
- Conventional commits: feat:, fix:, refactor:, test:, docs:, chore:
- Feature branches: feat/phase-X-task-Y-description
- Squash merge to develop, merge commit to main at milestones

## Docker
- Each service has a Dockerfile in docker/
- Multi-stage builds: Go build stage → distroless/static runtime
- Frontend: npm build → nginx:alpine
- Health checks on all services
- All secrets via environment variables, never hardcoded

## Common Commands
```bash
make build          # Build all Go binaries
make test           # Run Go tests
make lint           # Run golangci-lint + frontend ESLint
make generate       # Run sqlc generate + oapi-codegen
make migrate-up     # Run all pending migrations
make migrate-down   # Rollback last migration
make docker-build   # Build all Docker images
make docker-up      # Start full stack
make docker-down    # Stop full stack
cd frontend && npm run dev    # Frontend dev server
cd frontend && npm run build  # Frontend production build
cd frontend && npm test       # Frontend tests
```

## Key Patterns — Reference These
- API handler pattern: internal/api/handlers/clusters.go (once created)
- Proxmox client method: internal/proxmox/client.go (once created)
- Frontend feature module: frontend/src/features/dashboard/ (once created)
- sqlc query file: queries/clusters.sql (once created)
- Database migration: migrations/000001_initial_schema.up.sql (once created)

## Rules
- Always run `make test` after completing a task
- Always run `make lint` before committing
- Never modify generated files (internal/db/generated/) — edit queries/ and regenerate
- Never store secrets in config files — always use environment variables
- Use plan mode ("think hard") before implementing complex features
- Use the security-reviewer subagent for any auth, crypto, or RBAC code
