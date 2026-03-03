# migrations — Database Migration Rules

## Naming
`YYYYMMDDHHMMSS_description.up.sql` / `.down.sql`

## Rules
1. Always create both `.up.sql` and `.down.sql`
2. Down migrations must be fully reversible
3. **Never modify an existing migration** — create a new one instead
4. Test every migration: `make migrate-up` then `make migrate-down`
5. Use sequential timestamps to avoid ordering conflicts

## Guidelines
- One logical change per migration (don't mix table creation with data changes)
- Use `IF NOT EXISTS` / `IF EXISTS` where appropriate
- Use `$1`-style placeholders for pgx compatibility
- TimescaleDB hypertables: create base table first, then `SELECT create_hypertable(...)` in same migration
