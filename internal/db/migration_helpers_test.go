package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/migrations"
)

// testDBURL returns NEXARA_TEST_DB_URL after two gates: t.Skip when unset,
// and a hard t.Fatal when assertThrowawayDB rejects the URL.
//
// The second gate exists because these tests migrate the schema down to
// zero — pointed at a real database they destroy every row in it, which is
// exactly what happened to the live dev database on 2026-08-04 when the URL
// was assembled with database name "nexara" instead of "nexara_chaintest".
// Every migration test MUST obtain the URL through this function (or via
// setupMigration), never os.Getenv directly —
// TestGuard_NoDirectTestDBURLReads enforces that mechanically.
func testDBURL(t *testing.T) string {
	t.Helper()

	dbURL := os.Getenv("NEXARA_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("NEXARA_TEST_DB_URL not set; skipping migration test")
	}
	if err := assertThrowawayDB(dbURL); err != nil {
		t.Fatalf("refusing to run migration tests: %v "+
			"(these tests migrate the schema down to zero and destroy all data; "+
			"NEXARA_TEST_DB_URL must name a throwaway database such as nexara_chaintest)", err)
	}
	return dbURL
}

// assertThrowawayDB passes only a disposable database: a '_'/'-' segment of its name is or ends
// in "test" (nexara_chaintest, nexara_test, nexara_freshtest), never nexara, nexara_dev, prod. The
// name is resolved as pgx does, a dbname/database query parameter overriding the path, or
// ?dbname=nexara would retarget a passing URL. Hosts are unconstrained; keyword DSNs are refused.
func assertThrowawayDB(dbURL string) error {
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		return fmt.Errorf("NEXARA_TEST_DB_URL must be a postgres:// URL, not a keyword/value DSN")
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return fmt.Errorf("NEXARA_TEST_DB_URL is not a valid URL: %w", err)
	}

	dbName := strings.TrimPrefix(u.Path, "/")
	q := u.Query()
	for _, key := range []string{"dbname", "database"} {
		if v := q.Get(key); v != "" {
			dbName = v
		}
	}
	if dbName == "" {
		return fmt.Errorf("NEXARA_TEST_DB_URL names no database")
	}

	for _, segment := range strings.FieldsFunc(strings.ToLower(dbName), func(r rune) bool {
		return r == '_' || r == '-'
	}) {
		if strings.HasSuffix(segment, "test") {
			return nil
		}
	}
	return fmt.Errorf("database %q does not look like a throwaway test database", dbName)
}

// migrateUp brings the schema to head, treating "no change" as success. The
// preamble for tests that assert against the current schema rather than
// driving the migration themselves — the ones that do (migration_chain,
// migration_084's restore) keep their own call so their failure names the step.
func migrateUp(t *testing.T, m *migrate.Migrate) {
	t.Helper()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

// migrationTestEnv is the shared scaffolding of a migration round-trip test (pool, embedded
// migrations, golang-migrate), so a test only seeds and asserts. TestMigration057 and 058 keep
// their own setup, since changing them risks their data-preservation locks; new tests use this.
type migrationTestEnv struct {
	Ctx     context.Context
	Pool    *pgxpool.Pool
	Migrate *migrate.Migrate
	Cleanup func()
}

// setupMigration returns a ready migrationTestEnv, skipping without NEXARA_TEST_DB_URL; the
// caller must defer Cleanup(). Delete seeded rows with a defer registered AFTER that one, never
// t.Cleanup: cleanups run after the pool is closed, the error is discarded and rows leak. Give
// that defer its own context. See migration_075_test.go.
func setupMigration(t *testing.T) *migrationTestEnv {
	t.Helper()

	dbURL := testDBURL(t)

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
