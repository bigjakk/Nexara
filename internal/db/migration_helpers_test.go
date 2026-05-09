package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/migrations"
)

// migrationTestEnv is the shared scaffolding every migration round-trip
// test needs. Constructing the pgx pool, opening the embedded migrations
// fs, and wiring golang-migrate are pure ceremony — `setupMigration`
// hands back a ready-to-use environment so tests can focus on seeding
// rows + asserting the up/down behaviour.
//
// Phase 5.10: this harness was carved out to lower the bar for adding
// future round-trip tests. Existing TestMigration057 / TestMigration058
// still inline their own setup (changing them risks invalidating the
// load-bearing 4.8a/4.8c data-preservation locks); new tests should
// prefer this helper.
type migrationTestEnv struct {
	Ctx     context.Context
	Pool    *pgxpool.Pool
	Migrate *migrate.Migrate
	Cleanup func()
}

// setupMigration returns a ready migrationTestEnv. Skips the test if
// NEXARA_TEST_DB_URL is unset (locally throwaway DB inside the dev
// nexara-db container; CI sets this from a fresh Postgres job
// service). Caller MUST defer Cleanup() — it closes the pool, the
// migrate instance, and the iofs source.
func setupMigration(t *testing.T) *migrationTestEnv {
	t.Helper()

	dbURL := os.Getenv("NEXARA_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("NEXARA_TEST_DB_URL not set; skipping migration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		cancel()
		t.Fatalf("connect test db: %v", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		pool.Close()
		cancel()
		t.Fatalf("init iofs source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5URL(dbURL))
	if err != nil {
		_ = src.Close()
		pool.Close()
		cancel()
		t.Fatalf("init migrate: %v", err)
	}

	cleanup := func() {
		_, _ = m.Close()
		_ = src.Close()
		pool.Close()
		cancel()
	}

	return &migrationTestEnv{
		Ctx:     ctx,
		Pool:    pool,
		Migrate: m,
		Cleanup: cleanup,
	}
}

