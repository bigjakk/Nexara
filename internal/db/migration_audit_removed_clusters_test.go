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

func removedClusterID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12200000-0000-4000-8000-%012d", n))
}

// Removing a cluster used to delete every audit entry recorded against it. The
// migration lets the entry keep its cluster_id and keeps the cluster's name in
// removed_clusters; the first half is what the cascade did, so a fixture that
// never exercised it cannot pass the second. The down detaches such an entry
// (cluster_id NULL) rather than deleting it, and puts the cascade back.
func TestMigrationAuditRemovedClusters_EntriesOutliveTheirCluster(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	before, version, _ := migrationByName(t, "audit_log_removed_clusters")
	var (
		user      = removedClusterID(1)
		clusterA  = removedClusterID(2) // removed before the migration
		clusterB  = removedClusterID(3) // removed after it
		clusterC  = removedClusterID(4) // added under B's name once B is gone, then removed
		clusterD  = removedClusterID(5) // still there when the down runs
		rowA      = removedClusterID(10)
		rowB      = removedClusterID(11)
		rowGlobal = removedClusterID(12)
		rowC      = removedClusterID(13)
		rowD      = removedClusterID(14)
	)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ids := []uuid.UUID{clusterA, clusterB, clusterC, clusterD}
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12200000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, ids)
		_, _ = pool.Exec(pctx, `DELETE FROM removed_clusters WHERE id = ANY($1)`, ids) // absent below the migration
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
	// constraint is the cluster foreign key's delete rule and whether it is validated;
	// rule is "" when there is no such constraint.
	constraint := func() (rule string, validated bool) {
		t.Helper()
		err := pool.QueryRow(ctx, `SELECT confdeltype::text, convalidated FROM pg_constraint
			WHERE conname = 'audit_log_cluster_id_fkey' AND conrelid = 'audit_log'::regclass`).Scan(&rule, &validated)
		if err != nil && err.Error() != "no rows in result set" {
			t.Fatalf("read the constraint: %v", err)
		}
		return rule, validated
	}
	// removed returns the removed_clusters row of an id, "" if it has none.
	removed := func(id uuid.UUID) string {
		t.Helper()
		var name string
		err := pool.QueryRow(ctx, `SELECT name FROM removed_clusters WHERE id = $1`, id).Scan(&name)
		if err != nil && err.Error() != "no rows in result set" {
			t.Fatalf("read removed_clusters: %v", err)
		}
		return name
	}
	addCluster := func(id uuid.UUID, name string) {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, id, name)
	}
	addEntry := func(id uuid.UUID, cluster any) {
		exec("audit row", `INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details)
			VALUES ($1, $2, $3, 'storage', 'store01', 'update', '{"storage":"store01"}')`, id, cluster, user)
	}
	wantDetails := map[string]any{"storage": "store01"}

	exec("user", `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, 'migration-audit-cluster@example.invalid', 'x', 'Audit cluster')`, user)
	addCluster(clusterA, "cluster01")
	addCluster(clusterB, "cluster02")
	addEntry(rowA, clusterA)
	addEntry(rowB, clusterB)
	addEntry(rowGlobal, nil)

	// Before: removing the cluster removes its entries.
	exec("remove cluster A", `DELETE FROM clusters WHERE id = $1`, clusterA)
	if found, _, _ := read(rowA); found {
		t.Fatal("the entry of a removed cluster survived at the schema before the migration: the fixture is not exercising the cascade")
	}
	if rule, _ := constraint(); rule != "c" {
		t.Fatalf("delete rule before the migration = %q, want c (cascade)", rule)
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate over the constraint change: %v", err)
	}
	if rule, _ := constraint(); rule != "" {
		t.Errorf("the cluster foreign key is still there after the migration (delete rule %q)", rule)
	}

	// After: the entry stays exactly as written, and the cluster's name is kept.
	exec("remove cluster B", `DELETE FROM clusters WHERE id = $1`, clusterB)
	if found, cluster, details := read(rowB); !found || cluster == nil || *cluster != clusterB || !reflect.DeepEqual(details, wantDetails) {
		t.Errorf("entry of a removed cluster: found=%v cluster=%v details=%v, want it kept as written for %s", found, cluster, details, clusterB)
	}
	if got := removed(clusterB); got != "cluster02" {
		t.Errorf("removed_clusters names %s %q, want cluster02", clusterB, got)
	}
	if found, cluster, _ := read(rowGlobal); !found || cluster != nil {
		t.Errorf("an entry with no cluster changed along with a cluster: found=%v cluster=%v", found, cluster)
	}

	// A cluster added under the first one's name has its own id and its own row.
	addCluster(clusterC, "cluster02")
	addEntry(rowC, clusterC)
	exec("remove cluster C", `DELETE FROM clusters WHERE id = $1`, clusterC)
	if got := removed(clusterC); got != "cluster02" {
		t.Errorf("removed_clusters names %s %q, want cluster02", clusterC, got)
	}
	// An id removed a second time must not fail the removal, and keeps the name it was first removed under.
	addCluster(clusterB, "cluster02-renamed")
	exec("remove cluster B again", `DELETE FROM clusters WHERE id = $1`, clusterB)
	if got := removed(clusterB); got != "cluster02" {
		t.Errorf("removed_clusters names %s %q after a second removal, want the first name, cluster02", clusterB, got)
	}

	// Down: an entry whose cluster is gone is detached, not deleted; one whose
	// cluster is still there is left alone; the cascade is back, unvalidated.
	addCluster(clusterD, "cluster03")
	addEntry(rowD, clusterD)
	if err := m.Migrate(before); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	for _, id := range []uuid.UUID{rowB, rowC} {
		if found, cluster, details := read(id); !found || cluster != nil || !reflect.DeepEqual(details, wantDetails) {
			t.Errorf("entry %s after the down: found=%v cluster=%v details=%v, want it kept, detached, unchanged", id, found, cluster, details)
		}
	}
	if found, cluster, _ := read(rowD); !found || cluster == nil || *cluster != clusterD {
		t.Errorf("an entry of a cluster that exists was changed by the down: found=%v cluster=%v", found, cluster)
	}
	if rule, validated := constraint(); rule != "c" || validated {
		t.Errorf("after the down the constraint is delete rule %q, validated=%v, want c and not validated", rule, validated)
	}
	var left bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('removed_clusters') IS NOT NULL OR to_regproc('record_removed_cluster') IS NOT NULL`).Scan(&left); err != nil || left {
		t.Errorf("after the down removed_clusters or its trigger function is still there: present=%v err=%v", left, err)
	}
	// Removal still works with the trigger gone, and takes the entry with it as before.
	exec("remove cluster D", `DELETE FROM clusters WHERE id = $1`, clusterD)
	if found, _, _ := read(rowD); found {
		t.Error("the entry of a cluster removed after the down survived: the cascade is not back")
	}
}
