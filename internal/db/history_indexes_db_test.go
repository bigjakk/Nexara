package db

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The History's audit indexes are built once, after startup: a replica that
// finds the lock taken builds nothing, an INVALID leftover of an interrupted
// build is dropped and built again, an index of their name that is not theirs
// is refused, and one that is, with a storage option of its own, is kept. Not
// parallel: it builds indexes on the schema the package's DB tests share.
func TestEnsureHistoryIndexes(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool := env.Ctx, env.Pool
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	indexDef := func(name string) (def string, valid bool) {
		t.Helper()
		err := pool.QueryRow(ctx, `SELECT pg_get_indexdef(i.indexrelid), i.indisvalid FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = $1`, name).Scan(&def, &valid)
		if err != nil && err != pgx.ErrNoRows {
			t.Fatal(err)
		}
		return def, valid
	}
	for _, ix := range historyIndexes {
		run("DROP INDEX IF EXISTS " + ix.name)
	}
	// Not the History's index, under one of its names — other columns, or
	// the other operator class — refused, and left.
	dropAll := func() {
		for _, ix := range historyIndexes {
			run("DROP INDEX IF EXISTS " + ix.name)
		}
	}
	for name, lookAlike := range map[string]string{
		"idx_audit_log_resource_created": "(resource_type, action, created_at)",
		"idx_audit_log_details":          "USING gin (details)",
	} {
		dropAll()
		run("CREATE INDEX " + name + " ON audit_log " + lookAlike)
		if err := EnsureHistoryIndexes(ctx, pool, logger); err == nil || !strings.Contains(err.Error(), "leaving it alone") {
			t.Fatalf("a look-alike %s: %v, want it refused", lookAlike, err)
		}
		if def, _ := indexDef(name); !strings.HasSuffix(def, strings.TrimPrefix(lookAlike, "USING gin ")) {
			t.Fatalf("the look-alike %s was replaced: %q", lookAlike, def)
		}
	}
	dropAll()
	// The History's index with a fill factor: what it serves is the same.
	run(`CREATE INDEX idx_audit_log_resource_created ON audit_log (resource_type, resource_id, created_at)
		WITH (fillfactor = 90)`)
	if err := EnsureHistoryIndexes(ctx, pool, logger); err != nil {
		t.Fatalf("the History's index with a fill factor: %v, want it kept", err)
	}
	if def, _ := indexDef("idx_audit_log_resource_created"); !strings.Contains(def, "fillfactor") {
		t.Fatalf("the index with a fill factor was rebuilt: %q", def)
	}
	dropAll()
	// What an interrupted concurrent build leaves: the History's index,
	// INVALID. Marking it so takes a superuser, which the test database's
	// owner is (as POSTGRES_USER is in CI).
	run(`CREATE INDEX idx_audit_log_details ON audit_log USING gin (details jsonb_path_ops)`)
	run(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'public.idx_audit_log_details'::regclass`)

	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", historyIndexLock); err != nil {
		t.Fatal(err)
	}
	if err := EnsureHistoryIndexes(ctx, pool, logger); err != nil {
		t.Fatal(err)
	}
	if def, _ := indexDef("idx_audit_log_resource_created"); def != "" {
		t.Errorf("built while another instance held the lock: %s", def)
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", historyIndexLock); err != nil {
		t.Fatal(err)
	}
	holder.Release()

	// A shutdown does not cancel a build, which would leave it INVALID: even
	// begun after one, it runs to its end.
	stopped, stop := context.WithCancel(ctx)
	stop()
	BuildHistoryIndexes(stopped, pool, logger)
	for _, ix := range historyIndexes {
		if def, valid := indexDef(ix.name); !valid {
			t.Errorf("%s: %q, want it valid", ix.name, def)
		}
	}
	// …and each is the History's own: a second run finds nothing to refuse.
	if err := EnsureHistoryIndexes(ctx, pool, logger); err != nil {
		t.Errorf("the indexes it built: %v", err)
	}
}

// After a failed build — a database restart mid-build, a lock timeout — the
// next attempt waits a minute, doubling to half an hour.
func TestHistoryIndexBackoff(t *testing.T) {
	t.Parallel()
	for attempt, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 30 * time.Minute, 30 * time.Minute} {
		if got := historyIndexBackoff(attempt); got != want {
			t.Errorf("attempt %d: %v, want %v", attempt, got, want)
		}
	}
}
