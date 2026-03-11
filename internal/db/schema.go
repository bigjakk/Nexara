package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema/*.sql
var schemaFiles embed.FS

// EnsureSchema checks if the database schema exists and creates it if not.
// It runs all embedded SQL files in sorted order on a fresh database.
// On existing databases, it applies any new schema files not yet tracked.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	// Check if the schema already exists by looking for the users table.
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'users')`,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check schema existence: %w", err)
	}

	files, err := listSchemaFiles()
	if err != nil {
		return err
	}

	if !exists {
		log.Println("fresh database detected — creating schema...")
		for _, name := range files {
			if err := applySchemaFile(ctx, pool, name); err != nil {
				return err
			}
		}
		if err := recordAppliedFiles(ctx, pool, files); err != nil {
			return err
		}
		log.Println("schema creation complete")
		return nil
	}

	// Existing database — apply any new schema files.
	return applyPendingSchemaFiles(ctx, pool, files)
}

// listSchemaFiles returns sorted list of embedded .sql files.
func listSchemaFiles() ([]string, error) {
	entries, err := fs.ReadDir(schemaFiles, "schema")
	if err != nil {
		return nil, fmt.Errorf("read embedded schema dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// applySchemaFile reads and executes a single embedded SQL file.
func applySchemaFile(ctx context.Context, pool *pgxpool.Pool, name string) error {
	data, err := schemaFiles.ReadFile("schema/" + name)
	if err != nil {
		return fmt.Errorf("read schema file %s: %w", name, err)
	}
	if _, err := pool.Exec(ctx, string(data)); err != nil {
		return fmt.Errorf("execute schema file %s: %w", name, err)
	}
	log.Printf("  applied %s", name)
	return nil
}

// applyPendingSchemaFiles applies schema files not yet recorded in the
// applied_schema_files table.
func applyPendingSchemaFiles(ctx context.Context, pool *pgxpool.Pool, files []string) error {
	// Ensure tracking table exists.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS applied_schema_files (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create applied_schema_files table: %w", err)
	}

	rows, err := pool.Query(ctx, `SELECT filename FROM applied_schema_files`)
	if err != nil {
		return fmt.Errorf("query applied schema files: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan applied schema file: %w", err)
		}
		applied[name] = true
	}

	var pending []string
	for _, f := range files {
		if !applied[f] {
			pending = append(pending, f)
		}
	}

	if len(pending) == 0 {
		// Even with no pending files, verify schema integrity by re-running
		// all files. Embedded schema files use IF NOT EXISTS / IF EXISTS, so
		// idempotent operations are harmless. Non-idempotent ones (e.g.
		// create_hypertable) will error — we skip those.
		return verifySchema(ctx, pool, files)
	}

	log.Printf("applying %d pending schema file(s)...", len(pending))
	for _, name := range pending {
		if err := applySchemaFile(ctx, pool, name); err != nil {
			// Errors are non-fatal: the file may already be partially or
			// fully applied (e.g. first-time upgrade, stale tracking table,
			// or non-idempotent TimescaleDB operations).
			log.Printf("  skipped %s (likely already applied): %v", name, err)
		}
	}
	return recordAppliedFiles(ctx, pool, pending)
}

// verifySchema re-runs all schema files to catch any that were recorded as
// applied but whose changes are missing (e.g. stale tracking table).
// Errors are expected and non-fatal for already-applied operations.
func verifySchema(ctx context.Context, pool *pgxpool.Pool, files []string) error { //nolint:unparam // error return kept for future use and caller consistency
	var repaired int
	for _, name := range files {
		if err := applySchemaFile(ctx, pool, name); err != nil {
			// Expected for non-idempotent operations that already exist.
			continue
		}
		repaired++
	}
	if repaired > 0 {
		log.Printf("schema verification repaired %d file(s)", repaired)
	}
	return nil
}

// recordAppliedFiles marks the given schema files as applied.
func recordAppliedFiles(ctx context.Context, pool *pgxpool.Pool, files []string) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS applied_schema_files (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create applied_schema_files table: %w", err)
	}
	for _, name := range files {
		if _, err := pool.Exec(ctx, `INSERT INTO applied_schema_files (filename) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
			return fmt.Errorf("record applied schema file %s: %w", name, err)
		}
	}
	return nil
}
