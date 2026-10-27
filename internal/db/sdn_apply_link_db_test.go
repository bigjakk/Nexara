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
// collector ingests as a node network apply. LinkSDNApplyNodeTasks annotates the
// ones run by root@pam from the apply's start to its end with the apply's UPID,
// once the apply has ended (or a quarter of an hour has passed), and changes
// nothing else: not the row's action, another user's reload, another cluster's.
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
	upid := func(n int, taskType, id, user string) string {
		return "UPID:pve-01:0000A1B2:0000000" + string(rune('0'+n)) + ":6A8CE407:" + taskType + ":" + id + ":" + user + ":"
	}
	apply := func(cluster uuid.UUID, source, upid string, at time.Time) {
		t.Helper()
		exec("apply", `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source, created_at)
			VALUES (gen_random_uuid(), $1, 'sdn', 'cluster', 'sdn_applied', jsonb_build_object('upid', $2::text), $3, $4)`, cluster, upid, source, at)
	}
	task := func(cluster uuid.UUID, upid string, started time.Time, finished *time.Time) {
		t.Helper()
		exec("task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at, finished_at, source)
			VALUES ($1, $2, $3, $4, 'pve-01', 'reloadnetworkall', $5, $6, 'nexara')`,
			cluster, systemUserID, upid, map[bool]string{true: "completed", false: "running"}[finished != nil], started, finished)
	}
	reload := func(cluster uuid.UUID, source, user string, at time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec("reload", `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source, created_at)
			VALUES ($1, $2, 'network', 'pve-02', 'network_applied', jsonb_build_object('upid', $3::text, 'task_type', 'srvreload'), $4, $5)`,
			id, cluster, upid(9, "srvreload", "networking", user), source, at)
		return id
	}

	// Nexara's apply, tracked and finished 9 minutes ago; one ingested from Proxmox
	// whose task row is still running, and young; one whose row is gone and which
	// started 40 minutes ago, so is taken to have ended after a quarter of an hour.
	finished := ago(9 * time.Minute)
	up1, up2, up3 := upid(1, "reloadnetworkall", "", "user@pam!token01"), upid(2, "reloadnetworkall", "", "root@pam"), upid(3, "reloadnetworkall", "", "root@pam")
	apply(sdnLinkCluster, "nexara", up1, ago(10*time.Minute))
	task(sdnLinkCluster, up1, ago(10*time.Minute), &finished)
	apply(sdnLinkCluster, "proxmox", up2, ago(2*time.Minute))
	task(sdnLinkCluster, up2, ago(2*time.Minute), nil)
	apply(sdnLinkCluster, "proxmox", up3, ago(40*time.Minute))

	// An apply on another cluster is not this cluster's parent.
	// Two applies at once: a reload in both belongs to the one that started last.
	outer, inner := upid(6, "reloadnetworkall", "", "root@pam"), upid(7, "reloadnetworkall", "", "root@pam")
	outerEnd, innerEnd := ago(13*time.Minute), ago(18*time.Minute)
	apply(sdnLinkCluster, "proxmox", outer, ago(23*time.Minute))
	task(sdnLinkCluster, outer, ago(23*time.Minute), &outerEnd)
	apply(sdnLinkCluster, "proxmox", inner, ago(21*time.Minute))
	task(sdnLinkCluster, inner, ago(21*time.Minute), &innerEnd)

	// An apply from before the window, and a reload inside it from before the window's start.
	apply(sdnLinkCluster, "proxmox", upid(5, "reloadnetworkall", "", "root@pam"), ago(65*time.Minute))
	otherEnd := ago(49 * time.Minute)
	apply(sdnLinkOther, "proxmox", upid(4, "reloadnetworkall", "", "root@pam"), ago(50*time.Minute))
	task(sdnLinkOther, upid(4, "reloadnetworkall", "", "root@pam"), ago(50*time.Minute), &otherEnd)

	cases := []struct {
		name       string
		id         uuid.UUID
		wantParent string
	}{
		{"root@pam's reload during the finished apply", reload(sdnLinkCluster, "proxmox", "root@pam", ago(10*time.Minute-30*time.Second)), up1},
		{"one just after its end, inside the slack", reload(sdnLinkCluster, "proxmox", "root@pam", finished.Add(5*time.Second)), up1},
		{"one a minute after its end", reload(sdnLinkCluster, "proxmox", "root@pam", finished.Add(time.Minute)), ""},
		{"one just before it started", reload(sdnLinkCluster, "proxmox", "root@pam", ago(10*time.Minute+5*time.Second)), ""},
		{"a person's, in the same minute", reload(sdnLinkCluster, "proxmox", "admin@pam", ago(10*time.Minute-20*time.Second)), ""},
		{"a token's, in the same minute", reload(sdnLinkCluster, "proxmox", "root@pam!tool", ago(10*time.Minute-20*time.Second)), ""},
		{"Nexara's own network apply", reload(sdnLinkCluster, "nexara", "root@pam", ago(10*time.Minute-20*time.Second)), ""},
		{"during an apply that is still running", reload(sdnLinkCluster, "proxmox", "root@pam", ago(time.Minute)), ""},
		{"during an apply whose row is gone, past a quarter of an hour", reload(sdnLinkCluster, "proxmox", "root@pam", ago(38*time.Minute)), up3},
		{"a reload in two overlapping applies", reload(sdnLinkCluster, "proxmox", "root@pam", ago(20*time.Minute)), inner},
		{"a reload from before the window, though an apply covers it", reload(sdnLinkCluster, "proxmox", "root@pam", ago(61*time.Minute)), ""},
		{"another cluster's reload during this cluster's apply", reload(sdnLinkOther, "proxmox", "root@pam", ago(10*time.Minute-30*time.Second)), ""},
		{"this cluster's reload during another cluster's apply", reload(sdnLinkCluster, "proxmox", "root@pam", ago(49*time.Minute+30*time.Second)), ""},
	}
	link := func() int64 {
		t.Helper()
		n, err := q.LinkSDNApplyNodeTasks(ctx, gen.LinkSDNApplyNodeTasksParams{ClusterID: sdnLinkCluster, Since: ago(time.Hour)})
		if err != nil {
			t.Fatalf("link: %v", err)
		}
		return n
	}
	if n := link(); n != 4 {
		t.Errorf("first pass annotated %d rows, want 4", n)
	}
	for _, tc := range cases {
		var action string
		var parent *string
		if err := pool.QueryRow(ctx, `SELECT action, details->>'parent_upid' FROM audit_log WHERE id = $1`, tc.id).Scan(&action, &parent); err != nil {
			t.Fatalf("%s: read: %v", tc.name, err)
		}
		gotParent := ""
		if parent != nil {
			gotParent = *parent
		}
		if action != "network_applied" || gotParent != tc.wantParent {
			t.Errorf("%s: (%q, parent %q), want (network_applied, parent %q): the action is never changed", tc.name, action, gotParent, tc.wantParent)
		}
	}
	if n := link(); n != 0 {
		t.Errorf("a second pass annotated %d rows, want none", n)
	}
}
