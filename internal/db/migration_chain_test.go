package db

import (
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

// TestMigrationChain_FullUpDownUp runs every migration down to 0, up, down to 0 and up again,
// so a broken or missing .down.sql, a continuous aggregate dropped inside a transaction, or a
// down that drops what the up does not recreate fails here, not in a real rollback. It does not
// check data preservation (the migration_NNN tests do). Skipped without NEXARA_TEST_DB_URL (a
// throwaway database); 10-30 s warm, so a hang past 120 s is a real fault.
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

	// Bring the throwaway database back to 0, non-fatally. A defer, not t.Cleanup: cleanups run
	// after `defer env.Cleanup()` has closed the migrate instance, so the Down did nothing; this
	// defer runs first.
	defer func() { _ = env.Migrate.Down() }()
}
