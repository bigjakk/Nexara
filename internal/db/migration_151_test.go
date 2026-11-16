package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids, so a run that dies before its cleanup leaves rows the next deletes.
var (
	m151User    = uuid.MustParse("15100000-0000-4000-8000-000000000001")
	m151Cluster = uuid.MustParse("15100000-0000-4000-8000-00000000000c")
	m151Kept    = uuid.MustParse("15100000-0000-4000-8000-0000000000b1")
	m151Gone    = uuid.MustParse("15100000-0000-4000-8000-0000000000b2")
	m151Rule    = uuid.MustParse("15100000-0000-4000-8000-0000000000e1")
	m151Moved   = uuid.MustParse("15100000-0000-4000-8000-0000000000e2")
	m151Window  = uuid.MustParse("15100000-0000-4000-8000-0000000000f1")
	m151MovedW  = uuid.MustParse("15100000-0000-4000-8000-0000000000f2")
)

// TestMigration151_NodeRejoinAndClusterName: rows from before 000151 read the
// new columns as empty; a removed node's rule and window reattach to the new
// row of a node that rejoins under its name (the rule turned back on, a rule
// or window moved to another node meanwhile left there); the recorded corosync
// name is set once, a mismatch reported once, and only the shown name accepted. The
// down step is reversible. Not parallel: it migrates the shared schema.
func TestMigration151_NodeRejoinAndClusterName(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, m151Cluster)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, m151User)
	}
	purge()
	defer purge()
	if err := m.Migrate(150); err != nil {
		t.Fatalf("migrate to 150: %v", err)
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, 'migration-151-test@nexara.test', '')`, m151User)
	exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
	                 VALUES ($1, 'cluster01', 'https://192.0.2.10:8006', 'tok', 'enc')`, m151Cluster)
	exec("nodes", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $3, 'pve-01'), ($2, $3, 'pve-02')`,
		m151Kept, m151Gone, m151Cluster)
	exec("rules", `INSERT INTO alert_rules (id, name, metric, operator, threshold, scope_type, cluster_id, node_id, created_by)
	               VALUES ($1, 'cpu', 'cpu_usage', '>', 90, 'node', $3, $4, $5),
	                      ($2, 'mem', 'mem_usage', '>', 90, 'node', $3, $4, $5)`,
		m151Rule, m151Moved, m151Cluster, m151Gone, m151User)
	exec("window", `INSERT INTO maintenance_windows (id, cluster_id, node_id, starts_at, ends_at, created_by)
	                VALUES ($1, $3, $4, now(), now() + interval '1 day', $5),
	                       ($2, $3, $4, now(), now() + interval '1 day', $5)`, m151Window, m151MovedW, m151Cluster, m151Gone, m151User)
	migrateUp(t, m)

	q := gen.New(pool)
	if got, err := q.GetClusterCorosyncName(ctx, m151Cluster); err != nil || got != (gen.GetClusterCorosyncNameRow{}) {
		t.Fatalf("an existing cluster's names = %+v, %v; want both empty", got, err)
	}

	// pve-02 leaves: detached, deleted; the operator points one rule at pve-01.
	gone := pgtype.UUID{Bytes: m151Gone, Valid: true}
	if n, err := q.DetachNodeAlertRules(ctx, gen.DetachNodeAlertRulesParams{NodeID: gone, NodeName: "pve-02"}); err != nil || n != 2 {
		t.Fatalf("DetachNodeAlertRules = %d, %v; want 2 rules that were on", n, err)
	}
	if n, err := q.DetachNodeMaintenanceWindows(ctx, gen.DetachNodeMaintenanceWindowsParams{NodeID: gone, NodeName: "pve-02"}); err != nil || n != 2 {
		t.Fatalf("DetachNodeMaintenanceWindows = %d, %v; want 2", n, err)
	}
	if removed, err := q.DeleteClusterNodes(ctx, gen.DeleteClusterNodesParams{ClusterID: m151Cluster, Ids: []uuid.UUID{m151Gone}}); err != nil || len(removed) != 1 {
		t.Fatalf("DeleteClusterNodes = %+v, %v", removed, err)
	}
	exec("move rule", `UPDATE alert_rules SET node_id = $2 WHERE id = $1`, m151Moved, m151Kept)
	exec("move window", `UPDATE maintenance_windows SET node_id = $2 WHERE id = $1`, m151MovedW, m151Kept)

	// pve-02 rejoins with a new id.
	var rejoined uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO nodes (cluster_id, name) VALUES ($1, 'pve-02') RETURNING id`,
		m151Cluster).Scan(&rejoined); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if n, err := q.ReattachNodeAlertRules(ctx, m151Cluster); err != nil || n != 1 {
		t.Errorf("ReattachNodeAlertRules = %d, %v; want the one rule still waiting", n, err)
	}
	if n, err := q.ReattachNodeMaintenanceWindows(ctx, m151Cluster); err != nil || n != 1 {
		t.Errorf("ReattachNodeMaintenanceWindows = %d, %v; want 1", n, err)
	}
	var ruleNode, movedNode, windowNode, movedWindowNode uuid.UUID
	var ruleOn, movedOn bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT node_id FROM alert_rules WHERE id = $1), (SELECT enabled FROM alert_rules WHERE id = $1),
	                                     (SELECT node_id FROM alert_rules WHERE id = $2), (SELECT enabled FROM alert_rules WHERE id = $2),
	                                     (SELECT node_id FROM maintenance_windows WHERE id = $3),
	                                     (SELECT node_id FROM maintenance_windows WHERE id = $4)`,
		m151Rule, m151Moved, m151Window, m151MovedW).Scan(&ruleNode, &ruleOn, &movedNode, &movedOn, &windowNode, &movedWindowNode); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if ruleNode != rejoined || !ruleOn || windowNode != rejoined {
		t.Errorf("rule on %v (enabled %v), window on %v; want both on the rejoined row %v, the rule on", ruleNode, ruleOn, windowNode, rejoined)
	}
	if movedNode != m151Kept || movedOn || movedWindowNode != m151Kept {
		t.Errorf("the moved rule is on %v (enabled %v), the moved window on %v; want both left on pve-01, the rule off",
			movedNode, movedOn, movedWindowNode)
	}

	// The corosync name: recorded once, a mismatch reported once, only the shown name accepted.
	for _, step := range []struct {
		what string
		run  func() (int64, error)
		want int64
	}{
		{"record nothing", func() (int64, error) {
			return q.RecordClusterCorosyncName(ctx, gen.RecordClusterCorosyncNameParams{ID: m151Cluster, Name: ""})
		}, 0},
		{"record cluster01", func() (int64, error) {
			return q.RecordClusterCorosyncName(ctx, gen.RecordClusterCorosyncNameParams{ID: m151Cluster, Name: "cluster01"})
		}, 1},
		{"record cluster01 again", func() (int64, error) {
			return q.RecordClusterCorosyncName(ctx, gen.RecordClusterCorosyncNameParams{ID: m151Cluster, Name: "cluster01"})
		}, 0},
		{"record cluster02 over it", func() (int64, error) {
			return q.RecordClusterCorosyncName(ctx, gen.RecordClusterCorosyncNameParams{ID: m151Cluster, Name: "cluster02"})
		}, 0},
		{"report cluster02", func() (int64, error) {
			return q.ReportClusterCorosyncNameMismatch(ctx, gen.ReportClusterCorosyncNameMismatchParams{ID: m151Cluster, Reported: "cluster02"})
		}, 1},
		{"report cluster02 again", func() (int64, error) {
			return q.ReportClusterCorosyncNameMismatch(ctx, gen.ReportClusterCorosyncNameMismatchParams{ID: m151Cluster, Reported: "cluster02"})
		}, 0},
		{"accept a name not reported", func() (int64, error) {
			return q.AcceptClusterCorosyncName(ctx, gen.AcceptClusterCorosyncNameParams{ID: m151Cluster, Reported: "cluster03"})
		}, 0},
		{"accept cluster02", func() (int64, error) {
			return q.AcceptClusterCorosyncName(ctx, gen.AcceptClusterCorosyncNameParams{ID: m151Cluster, Reported: "cluster02"})
		}, 1},
	} {
		if n, err := step.run(); err != nil || n != step.want {
			t.Errorf("%s = %d, %v; want %d", step.what, n, err, step.want)
		}
	}
	if got, err := q.GetClusterCorosyncName(ctx, m151Cluster); err != nil ||
		got != (gen.GetClusterCorosyncNameRow{CorosyncName: "cluster02", CorosyncNameAccepted: true}) {
		t.Errorf("names after accepting = %+v, %v; want cluster02 recorded and accepted, nothing reported", got, err)
	}
	if err := q.ClearClusterCorosyncNameAccepted(ctx, m151Cluster); err != nil {
		t.Fatalf("ClearClusterCorosyncNameAccepted: %v", err)
	}
	if got, err := q.GetClusterCorosyncName(ctx, m151Cluster); err != nil || got.CorosyncNameAccepted {
		t.Errorf("names after the waiver ended = %+v, %v; want it off", got, err)
	}

	if err := m.Migrate(150); err != nil {
		t.Fatalf("down to 150: %v", err)
	}
	migrateUp(t, m)
}
