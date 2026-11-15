package db

import (
	"context"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/migrations"
)

// Fixed ids, so a run that dies before its cleanup leaves rows the next deletes.
var (
	nrUserID    = uuid.MustParse("4e000000-0000-4000-8000-000000000001")
	nrClusterID = uuid.MustParse("4e000000-0000-4000-8000-00000000000c")
	nrKeptID    = uuid.MustParse("4e000000-0000-4000-8000-0000000000b1")
	nrGoneID    = uuid.MustParse("4e000000-0000-4000-8000-0000000000b2")
	nrRuleID    = uuid.MustParse("4e000000-0000-4000-8000-0000000000e1")
	nrScanID    = uuid.MustParse("4e000000-0000-4000-8000-0000000000f1")
	nrScanNode  = uuid.MustParse("4e000000-0000-4000-8000-0000000000f2")
)

// TestDeleteClusterNodes_KeepsHistory protects the collector's node removal
// (migration 000149 + DeleteClusterNodes): removing a node row takes its guest
// rows and nothing that is history — CVE scans, alert rules and the alerts they
// raised, maintenance windows — and never the cluster's last row. A node rule
// with no cluster_id gets its node's, so deleting the cluster still removes it.
// Not parallel: it migrates the shared schema down one step and back.
func TestDeleteClusterNodes_KeepsHistory(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, nrClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM alert_rules WHERE id = $1`, nrRuleID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, nrUserID)
	}
	purge()
	defer purge()

	// Seed below 000149, where the rule's NULL cluster_id is the one to fill.
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("iofs: %v", err)
	}
	defer func() { _ = src.Close() }()
	prev, err := src.Prev(149)
	if err != nil {
		t.Fatalf("the migration before 000149: %v", err)
	}
	if err := m.Migrate(prev); err != nil {
		t.Fatalf("migrate to %d: %v", prev, err)
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, 'node-removal-db-test@nexara.test', '')`, nrUserID)
	exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
	                 VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')`, nrClusterID)
	exec("nodes", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $3, 'pve-01'), ($2, $3, 'pve-02')`,
		nrKeptID, nrGoneID, nrClusterID)
	exec("vm", `INSERT INTO vms (cluster_id, node_id, vmid, name, type) VALUES ($1, $2, 101, 'linux01', 'qemu')`,
		nrClusterID, nrGoneID)
	exec("rule", `INSERT INTO alert_rules (id, name, metric, operator, threshold, scope_type, node_id, created_by)
	              VALUES ($1, 'cpu', 'cpu_usage', '>', 90, 'node', $2, $3)`, nrRuleID, nrGoneID, nrUserID)
	exec("alert", `INSERT INTO alert_history (rule_id, cluster_id, node_id, metric, current_value, threshold)
	               VALUES ($1, $2, $3, 'cpu_usage', 95, 90)`, nrRuleID, nrClusterID, nrGoneID)
	exec("window", `INSERT INTO maintenance_windows (cluster_id, node_id, starts_at, ends_at, created_by)
	                VALUES ($1, $2, now() - interval '2 days', now() - interval '1 day', $3)`, nrClusterID, nrGoneID, nrUserID)
	exec("scan", `INSERT INTO cve_scans (id, cluster_id) VALUES ($1, $2)`, nrScanID, nrClusterID)
	exec("scan node", `INSERT INTO cve_scan_nodes (id, scan_id, node_id, node_name) VALUES ($1, $2, $3, 'pve-02')`,
		nrScanNode, nrScanID, nrGoneID)
	exec("vuln", `INSERT INTO cve_scan_vulns (scan_id, scan_node_id, cve_id, package_name, current_version)
	              VALUES ($1, $2, 'CVE-2000-0001', 'pkg', '1.0')`, nrScanID, nrScanNode)
	migrateUp(t, m)

	count := func(sql string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, nrClusterID).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM alert_rules WHERE cluster_id = $1`); n != 1 {
		t.Errorf("node rule's cluster_id not filled from its node: %d rules in the cluster", n)
	}

	q := gen.New(pool)
	removed, err := q.DeleteClusterNodes(ctx, gen.DeleteClusterNodesParams{ClusterID: nrClusterID, Ids: []uuid.UUID{nrGoneID}})
	if err != nil || len(removed) != 1 || removed[0].Name != "pve-02" {
		t.Fatalf("DeleteClusterNodes(pve-02) = %+v, %v; want pve-02 removed", removed, err)
	}
	for sql, want := range map[string]int{
		`SELECT count(*) FROM vms WHERE cluster_id = $1`:                                                     0,
		`SELECT count(*) FROM alert_rules WHERE cluster_id = $1`:                                             1,
		`SELECT count(*) FROM alert_history WHERE cluster_id = $1`:                                           1,
		`SELECT count(*) FROM maintenance_windows WHERE cluster_id = $1`:                                     1,
		`SELECT count(*) FROM cve_scan_nodes n JOIN cve_scans s ON s.id = n.scan_id WHERE s.cluster_id = $1`: 1,
		`SELECT count(*) FROM cve_scan_vulns v JOIN cve_scans s ON s.id = v.scan_id WHERE s.cluster_id = $1`: 1,
	} {
		if got := count(sql); got != want {
			t.Errorf("%s = %d, want %d", sql, got, want)
		}
	}

	// The last row is never removed, whatever the caller asks.
	removed, err = q.DeleteClusterNodes(ctx, gen.DeleteClusterNodesParams{ClusterID: nrClusterID, Ids: []uuid.UUID{nrKeptID}})
	if err != nil || len(removed) != 0 {
		t.Errorf("DeleteClusterNodes(the last row) = %+v, %v; want nothing removed", removed, err)
	}

	// Deleting the cluster still removes the rows unhooked from the node.
	exec("delete cluster", `DELETE FROM clusters WHERE id = $1`, nrClusterID)
	var left int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM alert_rules WHERE id = $1) +
	                                     (SELECT count(*) FROM cve_scan_nodes WHERE id = $2)`,
		nrRuleID, nrScanNode).Scan(&left); err != nil || left != 0 {
		t.Errorf("rows that outlived their cluster = %d, %v; want 0", left, err)
	}
}
