package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	sdnLinkCluster = uuid.MustParse("12600000-0000-4000-8000-000000000001")
	sdnLinkOther   = uuid.MustParse("12600000-0000-4000-8000-000000000002")
)

// An SDN apply reloads every node's network as a task of its own, which the
// collector ingests as a node network apply. LinkSDNApplyNodeTasks marks those as
// parts of the apply that was running — Nexara's or an ingested one — and leaves
// every other network apply, and another cluster's, alone; once marked, a row is
// not touched again.
func TestLinkSDNApplyNodeTasks(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{sdnLinkCluster, sdnLinkOther})
	}
	purge()
	defer purge()

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for i, id := range []uuid.UUID{sdnLinkCluster, sdnLinkOther} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, "cluster0"+string(rune('1'+i)))
	}

	now := time.Now().UTC().Truncate(time.Second)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	row := func(cluster uuid.UUID, rtype, rid, action, source, upid string, at time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		details := `{"task_type":"srvreload"}`
		if upid != "" {
			details = `{"upid":"` + upid + `"}`
		}
		exec("audit "+action, `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source, created_at)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)`, id, cluster, rtype, rid, action, details, source, at)
		return id
	}
	const upid1, upid2 = "UPID:pve-01:0000A1B2:00000001:6A8CE407:reloadnetworkall::root@pam:", "UPID:pve-02:0000A1B3:00000002:6A8CE408:reloadnetworkall::root@pam:"

	// Nexara's own apply, tracked: ended 9 minutes ago. Then an apply ingested from
	// Proxmox that has no task row, so is assumed to have run for a quarter of an hour.
	row(sdnLinkCluster, "sdn", "cluster", "sdn_applied", "nexara", upid1, ago(10*time.Minute))
	exec("task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at, finished_at, source)
		VALUES ($1, $2, $3, 'completed', 'pve-01', 'reloadnetworkall', $4, $5, 'nexara')`,
		sdnLinkCluster, systemUserID, upid1, ago(10*time.Minute), ago(9*time.Minute))
	row(sdnLinkCluster, "sdn", "cluster", "sdn_applied", "proxmox", upid2, ago(4*time.Minute))

	during := row(sdnLinkCluster, "network", "pve-01", "network_applied", "proxmox", "", ago(9*time.Minute+30*time.Second))
	atItsEnd := row(sdnLinkCluster, "network", "pve-02", "network_applied", "proxmox", "", ago(9*time.Minute-5*time.Second))
	afterIt := row(sdnLinkCluster, "network", "pve-03", "network_applied", "proxmox", "", ago(8*time.Minute))
	ofTheIngested := row(sdnLinkCluster, "network", "pve-01", "network_applied", "proxmox", "", ago(3*time.Minute))
	nexaras := row(sdnLinkCluster, "network", "pve-01", "network_applied", "nexara", "", ago(9*time.Minute+30*time.Second))
	elsewhere := row(sdnLinkOther, "network", "pve-01", "network_applied", "proxmox", "", ago(9*time.Minute+30*time.Second))
	// An apply on another cluster is not this cluster's parent.
	row(sdnLinkOther, "sdn", "cluster", "sdn_applied", "proxmox", "UPID:pve-09:0000A1B4:00000003:6A8CE409:reloadnetworkall::root@pam:", ago(30*time.Minute+30*time.Second))
	ofAnotherClusters := row(sdnLinkCluster, "network", "pve-01", "network_applied", "proxmox", "", ago(30*time.Minute))
	// A reload from before the window is left alone even where an apply covers it.
	row(sdnLinkCluster, "sdn", "cluster", "sdn_applied", "proxmox", "UPID:pve-01:0000A1B5:00000004:6A8CE40A:reloadnetworkall::root@pam:", ago(65*time.Minute))
	tooOld := row(sdnLinkCluster, "network", "pve-01", "network_applied", "proxmox", "", ago(64*time.Minute+30*time.Second))

	link := func() int64 {
		t.Helper()
		n, err := q.LinkSDNApplyNodeTasks(ctx, gen.LinkSDNApplyNodeTasksParams{ClusterID: sdnLinkCluster, Since: ago(time.Hour)})
		if err != nil {
			t.Fatalf("link: %v", err)
		}
		return n
	}
	if n := link(); n != 3 {
		t.Errorf("first pass marked %d rows, want 3", n)
	}
	for _, tc := range []struct {
		name       string
		id         uuid.UUID
		wantAction string
		wantParent string
	}{
		{"a reload during the tracked apply", during, "network_reloaded_by_sdn", upid1},
		{"one at its end, inside the slack", atItsEnd, "network_reloaded_by_sdn", upid1},
		{"one after it", afterIt, "network_applied", ""},
		{"one of the apply ingested without a task row", ofTheIngested, "network_reloaded_by_sdn", upid2},
		{"Nexara's own network apply", nexaras, "network_applied", ""},
		{"another cluster's", elsewhere, "network_applied", ""},
		{"one that only another cluster's apply covers", ofAnotherClusters, "network_applied", ""},
		{"one before the window, though an apply covers it", tooOld, "network_applied", ""},
	} {
		var action string
		var parent *string
		if err := pool.QueryRow(ctx, `SELECT action, details->>'parent_upid' FROM audit_log WHERE id = $1`, tc.id).Scan(&action, &parent); err != nil {
			t.Fatalf("%s: read: %v", tc.name, err)
		}
		gotParent := ""
		if parent != nil {
			gotParent = *parent
		}
		if action != tc.wantAction || gotParent != tc.wantParent {
			t.Errorf("%s: (%q, parent %q), want (%q, parent %q)", tc.name, action, gotParent, tc.wantAction, tc.wantParent)
		}
	}
	if n := link(); n != 0 {
		t.Errorf("a second pass marked %d rows, want none", n)
	}
}
