package db

import (
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

// TestMigrationChain_FullUpDownUp exercises every migration's `.up.sql`
// and `.down.sql` end-to-end against a real Postgres + TimescaleDB
// database. The check is "does the full chain compose without errors,"
// which is the cheapest possible smoke test for things like
//   - a `.down.sql` that's syntactically broken or missing
//   - a `DROP MATERIALIZED VIEW` for a TimescaleDB continuous
//     aggregate that fails inside a transaction
//   - a hypertable conversion whose down-side `SELECT
//     decompress_chunk` reference goes out of sync with the up-side
//   - a privilege/extension dependency that's only present at certain
//     versions
//
// What it does NOT check: data preservation. That's the job of the
// per-migration round-trip tests under migration_NNN_test.go (see 057
// and 058 for the canonical shape — seed real rows, up, assert, down,
// assert the seed survived).
//
// Sequence:
//
//  1. migrate down to 0 (clean slate). Any state left from previous test
//     runs is rolled back. Errors here mean a previous migration's
//     down-side is broken or the DB has hand-applied changes the chain
//     doesn't know about.
//  2. migrate all the way up. Asserts every .up.sql succeeds against a
//     fresh schema.
//  3. migrate all the way back down to 0. Asserts every .down.sql
//     succeeds — the load-bearing assertion of this test.
//  4. migrate all the way back up again. Catches "down accidentally
//     drops something the up doesn't recreate" — re-running up after
//     down should land in the exact same shape as a fresh up from 0.
//
// Skipped unless NEXARA_TEST_DB_URL is set — locally use a throwaway
// database inside the dev nexara-db container (e.g.
// `nexara_chaintest`). The chain test takes ~10–30s on a warm
// container; if it hangs past the harness's 120s ctx, something is
// genuinely broken with one of the migrations.
//
// Phase 5.10 added this test alongside the migrationTestEnv helper.
// At the time of authoring, the chain ran cleanly through all 58
// migrations both directions including the four continuous-aggregate
// tear-downs (000002 / 000006 / 000007 metric rollups + the 000010
// DRS migration's history-table flip). If a future migration's
// down-side gets the TimescaleDB-in-transaction interaction wrong,
// this test will fail loudly at PR time rather than during a real
// rollback.
func TestMigrationChain_FullUpDownUp(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	// Step 1: clean slate.
	if err := env.Migrate.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("step 1 (initial Down to 0): %v", err)
	}

	// Step 2: full up.
	if err := env.Migrate.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("step 2 (Up from 0 to head): %v", err)
	}
	upVersion, _, err := env.Migrate.Version()
	if err != nil {
		t.Fatalf("step 2 read version: %v", err)
	}

	// Step 3: full down. This is the load-bearing assertion — every
	// .down.sql in the chain must compose cleanly.
	if err := env.Migrate.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("step 3 (Down from head to 0): %v", err)
	}
	if _, _, err := env.Migrate.Version(); err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("step 3 expected ErrNilVersion at version 0, got: %v", err)
	}

	// Step 4: re-up. If a .down.sql forgot to drop something a later
	// .up.sql tries to create, this catches it.
	if err := env.Migrate.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("step 4 (re-Up from 0 to head): %v", err)
	}
	reUpVersion, _, err := env.Migrate.Version()
	if err != nil {
		t.Fatalf("step 4 read version: %v", err)
	}
	if reUpVersion != upVersion {
		t.Fatalf("step 4 landed at version %d, expected %d (matches step 2)", reUpVersion, upVersion)
	}

	// Final cleanup: bring the DB back down to a known clean state so
	// the throwaway test database doesn't carry forward schema across
	// runs. Errors here are non-fatal — the operator can recreate the
	// throwaway DB if cleanup fails.
	t.Cleanup(func() {
		_ = env.Migrate.Down()
	})
}
