package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
)

func auditClusterNullID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12200000-0000-4000-8000-%012d", n))
}

// Removing a cluster used to delete every audit entry recorded against it
// (audit_log.cluster_id was ON DELETE CASCADE). The migration makes the entry
// outlive the cluster, detached from it, with everything else it recorded; the
// first half of the test is what the cascade did, so a fixture that never
// exercised it cannot pass for the second.
func TestMigrationAuditClusterSetNull_EntriesOutliveTheirCluster(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	before, version, _ := migrationByName(t, "audit_log_cluster_set_null")
	var (
		user      = auditClusterNullID(1)
		clusterA  = auditClusterNullID(2)
		clusterB  = auditClusterNullID(3)
		rowA      = auditClusterNullID(10)
		rowB      = auditClusterNullID(11)
		rowGlobal = auditClusterNullID(12)
	)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12200000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{clusterA, clusterB})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, user)
	}
	purge()
	defer purge()
	defer func() {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("restore to head: %v", err)
		}
	}()

	if err := m.Migrate(before); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", before, err)
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exists := func(id uuid.UUID) (found bool, cluster *uuid.UUID, details string) {
		t.Helper()
		err := pool.QueryRow(ctx, `SELECT cluster_id, details::text FROM audit_log WHERE id = $1`, id).Scan(&cluster, &details)
		if err != nil && err.Error() != "no rows in result set" {
			t.Fatalf("read %s: %v", id, err)
		}
		return err == nil, cluster, details
	}
	deleteRule := func() string {
		t.Helper()
		var rule string
		if err := pool.QueryRow(ctx, `SELECT confdeltype::text FROM pg_constraint
			WHERE conname = 'audit_log_cluster_id_fkey' AND conrelid = 'audit_log'::regclass`).Scan(&rule); err != nil {
			t.Fatalf("read the constraint: %v", err)
		}
		return rule
	}

	exec("user", `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, 'migration-audit-cluster@example.invalid', 'x', 'Audit cluster')`, user)
	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{{clusterA, "cluster01"}, {clusterB, "cluster02"}} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, c.id, c.name)
	}
	for _, r := range []struct {
		id      uuid.UUID
		cluster any
	}{{rowA, clusterA}, {rowB, clusterB}, {rowGlobal, nil}} {
		exec("audit row", `INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details)
			VALUES ($1, $2, $3, 'storage', 'store01', 'update', '{"storage":"store01"}')`, r.id, r.cluster, user)
	}

	// Before: removing the cluster removes its entries.
	exec("remove cluster A", `DELETE FROM clusters WHERE id = $1`, clusterA)
	if found, _, _ := exists(rowA); found {
		t.Fatal("the entry of a removed cluster survived at the schema before the migration: the fixture is not exercising the cascade")
	}
	if got := deleteRule(); got != "c" {
		t.Fatalf("delete rule before the migration = %q, want c (cascade)", got)
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate over the constraint change: %v", err)
	}
	if got := deleteRule(); got != "n" {
		t.Errorf("delete rule after the migration = %q, want n (set null)", got)
	}

	// After: the entry stays, detached; and the removal's own entry, which cannot
	// name a cluster that no longer exists, is accepted beside it.
	exec("remove cluster B", `DELETE FROM clusters WHERE id = $1`, clusterB)
	found, cluster, details := exists(rowB)
	if !found || cluster != nil || details != `{"storage": "store01"}` {
		t.Errorf("entry of a removed cluster: found=%v cluster=%v details=%s, want it kept, detached, unchanged", found, cluster, details)
	}
	exec("the removal's own entry", `INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details)
		VALUES ($1, NULL, $2, 'cluster', $3, 'cluster_deleted', jsonb_build_object('cluster_id', $3::text))`,
		auditClusterNullID(13), user, clusterB.String())
	if found, _, _ := exists(rowGlobal); !found {
		t.Error("an entry with no cluster was removed along with a cluster")
	}

	// Down: the cascade is back; the detached entry stays, for a NULL violates no
	// foreign key.
	if err := m.Migrate(before); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate back: %v", err)
	}
	if got := deleteRule(); got != "c" {
		t.Errorf("delete rule after the down migration = %q, want c (cascade)", got)
	}
	if found, _, _ := exists(rowB); !found {
		t.Error("the down migration removed an entry")
	}
}
