package db

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The History's audit indexes are built once, after startup: a replica that
// finds the lock taken builds nothing, and an INVALID leftover of an
// interrupted build is dropped and built again. Not parallel: it builds
// indexes on the schema the package's DB tests share.
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
	defer run(`DELETE FROM audit_log WHERE resource_type = 'history-index-probe'`)
	for _, ix := range historyIndexes {
		run("DROP INDEX IF EXISTS " + ix.name)
	}
	// What an interrupted concurrent build leaves: the name, INVALID — here a
	// unique build that duplicates refused.
	run(`INSERT INTO audit_log (resource_type, resource_id, action, details)
		VALUES ('history-index-probe', 'a', 'dup', '{}'), ('history-index-probe', 'b', 'dup', '{}')`)
	if _, err := pool.Exec(ctx, `CREATE UNIQUE INDEX CONCURRENTLY idx_audit_log_details ON audit_log (action)`,
		pgx.QueryExecModeSimpleProtocol); err == nil {
		t.Fatal("the unique build over duplicates succeeded; no invalid index to recover from")
	}

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

	if err := EnsureHistoryIndexes(ctx, pool, logger); err != nil {
		t.Fatal(err)
	}
	for _, ix := range historyIndexes {
		def, valid := indexDef(ix.name)
		if !valid || !strings.Contains(def, strings.Trim(ix.columns, "()")) {
			t.Errorf("%s: %q (valid %v), want a valid index on %s", ix.name, def, valid, ix.columns)
		}
	}
}
