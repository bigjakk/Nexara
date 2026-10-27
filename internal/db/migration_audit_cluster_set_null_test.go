package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
)

func auditClusterNullID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12200000-0000-4000-8000-%012d", n))
}

// Removing a cluster used to delete every audit entry recorded against it. The
// migration makes the entry outlive the cluster, detached from it and stamped
// with which cluster it was (details.detached_cluster); the first half is what
// the cascade did, so a fixture that never exercised it cannot pass the second.
func TestMigrationAuditClusterSetNull_EntriesOutliveTheirCluster(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	before, version, _ := migrationByName(t, "audit_log_cluster_set_null")
	var (
		user      = auditClusterNullID(1)
		clusterA  = auditClusterNullID(2)
		clusterB  = auditClusterNullID(3)
		clusterC  = auditClusterNullID(4) // added under B's name once B is gone
		rowA      = auditClusterNullID(10)
		rowB      = auditClusterNullID(11)
		rowGlobal = auditClusterNullID(12)
		rowC      = auditClusterNullID(14)
		rowForged = auditClusterNullID(15)
	)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12200000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{clusterA, clusterB, clusterC})
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
	// read returns what is stored of an entry: whether it exists, its cluster and its details.
	read := func(id uuid.UUID) (found bool, cluster *uuid.UUID, details map[string]any) {
		t.Helper()
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT cluster_id, details FROM audit_log WHERE id = $1`, id).Scan(&cluster, &raw)
		if err != nil && err.Error() != "no rows in result set" {
			t.Fatalf("read %s: %v", id, err)
		}
		if err == nil {
			if err := json.Unmarshal(raw, &details); err != nil {
				t.Fatalf("details of %s: %v", id, err)
			}
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
	addCluster := func(id uuid.UUID, name string) {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, id, name)
	}
	addEntry := func(id uuid.UUID, cluster any) {
		exec("audit row", `INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details)
			VALUES ($1, $2, $3, 'storage', 'store01', 'update', '{"storage":"store01"}')`, id, cluster, user)
	}

	exec("user", `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, 'migration-audit-cluster@example.invalid', 'x', 'Audit cluster')`, user)
	addCluster(clusterA, "cluster01")
	addCluster(clusterB, "cluster02")
	addEntry(rowA, clusterA)
	addEntry(rowB, clusterB)
	addEntry(rowGlobal, nil)
	// An entry whose details already say it came from another cluster.
	exec("audit row", `INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details)
		VALUES ($1, $2, $3, 'storage', 'store01', 'update', '{"detached_cluster":{"id":"x","name":"other"}}')`, rowForged, clusterB, user)

	// Before: removing the cluster removes its entries.
	exec("remove cluster A", `DELETE FROM clusters WHERE id = $1`, clusterA)
	if found, _, _ := read(rowA); found {
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

	// After: the entry stays, detached and stamped with the cluster it was.
	exec("remove cluster B", `DELETE FROM clusters WHERE id = $1`, clusterB)
	stamp := func(id uuid.UUID, name string) map[string]any {
		return map[string]any{"id": id.String(), "name": name}
	}
	found, cluster, details := read(rowB)
	want := map[string]any{"storage": "store01", "detached_cluster": stamp(clusterB, "cluster02")}
	if !found || cluster != nil || !reflect.DeepEqual(details, want) {
		t.Errorf("entry of a removed cluster: found=%v cluster=%v details=%v, want it kept, detached, with %v", found, cluster, details, want)
	}
	if _, _, details := read(rowForged); !reflect.DeepEqual(details["detached_cluster"], stamp(clusterB, "cluster02")) {
		t.Errorf("an entry that already named a cluster now says %v, want the trigger's own stamp", details["detached_cluster"])
	}
	if found, _, details := read(rowGlobal); !found || details["detached_cluster"] != nil {
		t.Errorf("an entry with no cluster was removed or stamped along with a cluster: found=%v details=%v", found, details)
	}

	// A second removal, of a cluster added under the first one's name: its entry is
	// stamped with its own id, and the first one's keeps the first's.
	addCluster(clusterC, "cluster02")
	addEntry(rowC, clusterC)
	exec("remove cluster C", `DELETE FROM clusters WHERE id = $1`, clusterC)
	if found, cluster, details := read(rowC); !found || cluster != nil ||
		!reflect.DeepEqual(details["detached_cluster"], stamp(clusterC, "cluster02")) {
		t.Errorf("entry of the second removed cluster: found=%v cluster=%v details=%v, want it stamped with its own id", found, cluster, details)
	}
	if _, _, details := read(rowB); !reflect.DeepEqual(details["detached_cluster"], stamp(clusterB, "cluster02")) {
		t.Errorf("the first removed cluster's entry now says %v, want its own stamp unchanged", details["detached_cluster"])
	}
}
