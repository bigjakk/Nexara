package db

import (
	"log/slog"
	"testing"
)

// TestMigration153_DropsANarrowIndexOnlyWhenItsWideOneExists: 000153 drops
// idx_audit_log_cluster_id because idx_audit_log_cluster_created serves it, but
// keeps idx_audit_log_cluster_vmid while its wide index (built after startup, not
// by a migration) is missing; EnsureHistoryIndexes drops it once it has built
// that index, and the down step puts both back. Not parallel: it migrates the
// shared schema.
func TestMigration153_DropsANarrowIndexOnlyWhenItsWideOneExists(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	ensure := func() {
		t.Helper()
		if err := EnsureHistoryIndexes(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
			t.Fatalf("build the history indexes: %v", err)
		}
	}
	migrateUp(t, m)
	ensure()
	defer func() { migrateUp(t, m); ensure() }()

	if err := m.Migrate(151); err != nil {
		t.Fatalf("migrate to 151: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP INDEX idx_audit_log_cluster_vmid_created`); err != nil {
		t.Fatalf("drop the wide guest index: %v", err)
	}
	exists := func(name string) bool {
		t.Helper()
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok); err != nil {
			t.Fatalf("look up %s: %v", name, err)
		}
		return ok
	}

	if err := m.Migrate(153); err != nil {
		t.Fatalf("migrate to 153: %v", err)
	}
	if exists("idx_audit_log_cluster_id") || !exists("idx_audit_log_cluster_vmid") {
		t.Errorf("after up: cluster_id index present=%v (want false), cluster_vmid present=%v (want true)",
			exists("idx_audit_log_cluster_id"), exists("idx_audit_log_cluster_vmid"))
	}
	migrateUp(t, m)
	ensure()
	if exists("idx_audit_log_cluster_vmid") || !exists("idx_audit_log_cluster_vmid_created") {
		t.Errorf("after the history indexes were built: the narrow guest index is still there, or the wide one is not")
	}
	if err := m.Migrate(151); err != nil {
		t.Fatalf("migrate down to 151: %v", err)
	}
	if !exists("idx_audit_log_cluster_id") || !exists("idx_audit_log_cluster_vmid") {
		t.Errorf("after down: both narrow indexes must be back")
	}
}
