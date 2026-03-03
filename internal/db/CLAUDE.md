# internal/db — Database & Query Conventions

## Adding Queries
1. Write SQL in `queries/*.sql` with sqlc annotations (`-- name: GetNode :one`)
2. Run `make generate` to regenerate Go code
3. **Never edit files in `generated/`** — they are overwritten on generate

## sqlc Annotations
- `:one` — returns a single row
- `:many` — returns multiple rows
- `:exec` — no return value
- `:execresult` — returns affected row count

## Migrations
- Path: `migrations/YYYYMMDDHHMMSS_description.up.sql` / `.down.sql`
- Always write both up and down migrations
- Down migration must be fully reversible
- **Never modify an existing migration** — create a new one instead
- Test by running `make migrate-up` then `make migrate-down`

## Rules
- All DB access goes through sqlc-generated code
- Use pgx parameter placeholders (`$1`, `$2`), not `?`
- TimescaleDB hypertables for time-series data
