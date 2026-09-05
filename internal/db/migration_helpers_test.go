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

// assertThrowawayDB returns nil only when dbURL names a disposable database.
// A name is disposable when some '_'/'-'-separated segment of it is "test"
// or ends in "test" — accepting the names in real use (nexara_chaintest,
// nexara_test in CI, nexara_freshtest) while rejecting live-shaped names
// (nexara, nexara_dev, prod).
//
// The database name is resolved the way pgx actually resolves it: from the
// URL path, then overridden by a dbname/database query parameter if present
// (pgconn's parseURLSettings applies query params after the path, and
// golang-migrate forwards every non-x- param through). Checking only the
// path would let ?dbname=nexara silently retarget a guard-passing URL at
// the live database.
//
// Scope: this constrains the database NAME only — the host is deliberately
// unconstrained (CI and dev point at different servers), so a *_test
// database on any reachable server is fair game. Keyword/value DSNs are
// rejected outright: golang-migrate needs a scheme anyway, and the parse
// below would treat the whole DSN as an opaque path.
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
//
// Delete your seeded rows with a `defer` registered AFTER `defer
// env.Cleanup()`, never with t.Cleanup. Go runs a test function's defers
// BEFORE its t.Cleanup callbacks, so a t.Cleanup delete fires once
// env.Cleanup has already closed Pool and cancelled Ctx: the delete
// errors out, the error is discarded, and the rows leak into the next
// run's counts. Registering the defer after env.Cleanup's makes it run
// first (LIFO), while the pool is still open. Give it its own
// context.Background() timeout rather than env.Ctx so it survives a
// reordering. See migration_075_test.go for the pattern.
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
