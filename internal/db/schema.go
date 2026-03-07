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
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	// Check if the schema already exists by looking for the users table.
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'users')`,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check schema existence: %w", err)
	}
	if exists {
		return nil
	}

	log.Println("fresh database detected — creating schema...")

	entries, err := fs.ReadDir(schemaFiles, "schema")
	if err != nil {
		return fmt.Errorf("read embedded schema dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		data, err := schemaFiles.ReadFile("schema/" + name)
		if err != nil {
			return fmt.Errorf("read schema file %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("execute schema file %s: %w", name, err)
		}
		log.Printf("  applied %s", name)
	}

	log.Println("schema creation complete")
	return nil
}
