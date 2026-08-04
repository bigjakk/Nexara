package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"

	"github.com/bigjakk/nexara/internal/db"
)

// runMigrateCLI implements the `nexara migrate` subcommand family — the
// operator's way out when the automatic startup migration (db.EnsureSchema)
// fails. A failed migration leaves schema_migrations marked dirty, and every
// subsequent container start refuses to boot, so recovery MUST be reachable
// from inside the shipped image without installing golang-migrate:
//
//	docker compose run --rm nexara migrate status
//	docker compose run --rm nexara migrate force 77
//	docker compose run --rm nexara migrate up
//
// The recovery runbook lives in docs/installation.md ("Recovering from a
// failed upgrade").
func runMigrateCLI(args []string) {
	if len(args) == 0 {
		printMigrateUsage()
		os.Exit(2)
	}

	// Read DATABASE_URL directly instead of config.Load(): recovery must work
	// even when unrelated config (JWT_SECRET, ENCRYPTION_KEY, …) is missing
	// or invalid — a half-broken .env may be exactly why the operator is here.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprint(os.Stderr, "migrate: DATABASE_URL is not set\n")
		os.Exit(1)
	}

	m, cleanup, err := db.NewMigrator(databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	switch args[0] {
	case "status":
		runMigrateStatus(m)
	case "up":
		runMigrateUp(m)
	case "down":
		runMigrateDown(m, args[1:])
	case "force":
		runMigrateForce(m, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "migrate: unknown subcommand %q\n\n", args[0])
		printMigrateUsage()
		os.Exit(2)
	}
}

func printMigrateUsage() {
	fmt.Fprint(os.Stderr, `Usage: nexara migrate <subcommand>

Operator recovery tooling for the schema migrations that normally run
automatically at startup. Take a database backup before force/down.

Subcommands:
  status           Show the applied schema version, the dirty flag, and the
                   version embedded in this binary.
  up               Apply all pending migrations without starting the server.
  down <n>         Roll back exactly <n> migrations (runs their .down.sql).
                   Down migrations can drop data — back up first.
  force <version>  Overwrite the recorded schema version and clear the dirty
                   flag WITHOUT running any SQL. Use after a failed upgrade:
                   inspect the database, then force the version whose
                   migrations are actually fully applied (usually the version
                   before the one that failed), then run "up" or restart.
`)
}

func runMigrateStatus(m *migrate.Migrate) {
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate status: %v\n", err)
		os.Exit(1)
	}

	version, dirty, err := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		fmt.Printf("applied:  none (empty schema)\nembedded: %d\n", latest)
		return
	case err != nil:
		fmt.Fprintf(os.Stderr, "migrate status: read version: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("applied:  %d\ndirty:    %t\nembedded: %d\n", version, dirty, latest)
	switch {
	case dirty && version <= 1:
		// There is no version 0 to force back to (and golang-migrate cannot
		// run from a forced 0 — no 000000 source file exists), so the only
		// clean recovery from a first-migration failure is a fresh database.
		fmt.Print("\nThe schema is DIRTY: the FIRST migration failed on an empty database.\n")
		fmt.Print("Recovery: fix the cause (e.g. missing TimescaleDB extension), then drop\n")
		fmt.Print("and recreate the database (or restore your backup) and retry the start.\n")
	case dirty:
		fmt.Printf("\nThe schema is DIRTY: migration %d started but did not finish, and\n", version)
		fmt.Print("the server refuses to boot. Recovery: verify what migration ")
		fmt.Printf("%d\napplied (see its .up.sql), then either\n", version)
		fmt.Printf("  nexara migrate force %d   # if it did NOT complete\n", version-1)
		fmt.Printf("  nexara migrate force %d   # if it DID fully apply\n", version)
		fmt.Print("and re-run \"nexara migrate up\" or restart the container.\n")
	case uint64(version) < latest:
		fmt.Printf("\n%d migration(s) pending — \"nexara migrate up\" or a normal start applies them.\n", latest-uint64(version))
	default:
		fmt.Print("\nSchema is up to date.\n")
	}
}

func runMigrateUp(m *migrate.Migrate) {
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			fmt.Println("no pending migrations")
			return
		}
		fmt.Fprintf(os.Stderr, "migrate up: %v\n", err)
		os.Exit(1)
	}
	version, dirty, _ := m.Version()
	fmt.Printf("migrated up to version %d (dirty=%t)\n", version, dirty)
}

func runMigrateDown(m *migrate.Migrate, args []string) {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "migrate down: exactly one argument required — the number of migrations to roll back (e.g. \"nexara migrate down 1\")\n")
		os.Exit(2)
	}
	steps, err := strconv.Atoi(args[0])
	if err != nil || steps < 1 {
		fmt.Fprintf(os.Stderr, "migrate down: %q is not a positive step count\n", args[0])
		os.Exit(2)
	}

	// Refuse over-large step counts up front: m.Steps rolls back everything
	// it can BEFORE reporting ErrShortLimit, so a typo'd count would empty
	// the schema and then "fail". Versions are contiguous from 1, so the
	// applied version equals the applied migration count.
	version, _, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Fprint(os.Stderr, "migrate down: no migrations are applied; nothing to roll back\n")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate down: read version: %v\n", err)
		os.Exit(1)
	}
	if uint64(steps) > uint64(version) { //nolint:gosec // steps validated >= 1 above
		fmt.Fprintf(os.Stderr, "migrate down: only %d migration(s) are applied; refusing to roll back %d\n", version, steps)
		os.Exit(1)
	}

	if err := m.Steps(-steps); err != nil {
		fmt.Fprintf(os.Stderr, "migrate down: %v\n", err)
		os.Exit(1)
	}
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("rolled back to empty schema")
		return
	}
	fmt.Printf("rolled back %d migration(s), now at version %d (dirty=%t)\n", steps, version, dirty)
}

func runMigrateForce(m *migrate.Migrate, args []string) {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "migrate force: exactly one argument required — the version to record (e.g. \"nexara migrate force 77\")\n")
		os.Exit(2)
	}
	version, err := strconv.Atoi(args[0])
	if err != nil || version < 1 {
		// 0 is rejected deliberately: no 000000 migration exists, and
		// golang-migrate cannot step up FROM a forced version whose source
		// file is missing — forcing 0 wedges the install until re-forced.
		fmt.Fprintf(os.Stderr, "migrate force: %q is not a valid version (must be >= 1)\n", args[0])
		os.Exit(2)
	}

	if err := m.Force(version); err != nil {
		fmt.Fprintf(os.Stderr, "migrate force: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("forced schema version to %d and cleared the dirty flag (no SQL was run)\n", version)
}
