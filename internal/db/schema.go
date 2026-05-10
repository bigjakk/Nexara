package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // register "pgx5" scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/migrations"
)

// EnsureSchema applies any pending SQL migrations from the embedded migrations
// FS using golang-migrate. The schema_migrations table is the single source of
// truth for migration state.
//
// On first boot of a binary upgraded from the old bespoke schema runner, this
// function seeds schema_migrations from the legacy applied_schema_files table
// so the new tracker picks up where the old one left off (one-time, idempotent).
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool, databaseURL string, logger *slog.Logger) error {
	if err := seedFromLegacyTable(ctx, pool, logger); err != nil {
		return fmt.Errorf("seed schema_migrations from legacy table: %w", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("init iofs migration source: %w", err)
	}
	defer src.Close()

	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5URL(databaseURL))
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	if version, dirty, verr := m.Version(); verr == nil {
		logger.Info("database schema up to date", "version", version, "dirty", dirty)
	}
	return nil
}

// toPgx5URL rewrites a libpq-style postgres URL to the pgx5 scheme used by the
// golang-migrate pgx/v5 driver.
func toPgx5URL(databaseURL string) string {
	switch {
	case strings.HasPrefix(databaseURL, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgres://")
	case strings.HasPrefix(databaseURL, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgresql://")
	default:
		return databaseURL
	}
}

// seedFromLegacyTable seeds schema_migrations.version from the legacy
// applied_schema_files tracker so a binary upgrade does not re-apply
// migrations that were already run by the bespoke EnsureSchema.
//
// Idempotent: returns early once schema_migrations has any row, so the seed
// runs at most once per install.
func seedFromLegacyTable(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	smExists, err := tableExists(ctx, pool, "schema_migrations")
	if err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}
	if smExists {
		var rowCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&rowCount); err != nil {
			return fmt.Errorf("count schema_migrations rows: %w", err)
		}
		if rowCount > 0 {
			return nil
		}
	}

	legacyExists, err := tableExists(ctx, pool, "applied_schema_files")
	if err != nil {
		return fmt.Errorf("check applied_schema_files: %w", err)
	}
	if !legacyExists {
		return nil
	}

	maxVersion, found, err := highestLegacyVersion(ctx, pool)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		dirty BOOLEAN NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO schema_migrations (version, dirty) VALUES ($1, FALSE) ON CONFLICT (version) DO NOTHING`,
		maxVersion,
	); err != nil {
		return fmt.Errorf("seed schema_migrations: %w", err)
	}

	logger.Info("seeded schema_migrations from legacy applied_schema_files", "version", maxVersion)
	return nil
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = $1
	)`, name).Scan(&exists)
	return exists, err
}

// highestLegacyVersion returns the largest numeric version prefix found in the
// legacy applied_schema_files.filename column. Filenames look like
// "000037_audit_log_perf_indexes.up.sql".
func highestLegacyVersion(ctx context.Context, pool *pgxpool.Pool) (version uint64, found bool, err error) {
	rows, err := pool.Query(ctx, `SELECT filename FROM applied_schema_files`)
	if err != nil {
		return 0, false, fmt.Errorf("query applied_schema_files: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			return 0, false, fmt.Errorf("scan applied_schema_files row: %w", scanErr)
		}
		v, ok := parseMigrationVersion(name)
		if !ok {
			continue
		}
		found = true
		if v > version {
			version = v
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, false, fmt.Errorf("iterate applied_schema_files: %w", rowsErr)
	}
	return version, found, nil
}

func parseMigrationVersion(filename string) (uint64, bool) {
	idx := strings.Index(filename, "_")
	if idx <= 0 {
		return 0, false
	}
	v, err := strconv.ParseUint(filename[:idx], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
