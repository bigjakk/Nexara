package db

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	linkTestCluster = uuid.MustParse("138a0000-0000-4000-8000-000000000001")
	linkTestOther   = uuid.MustParse("138a0000-0000-4000-8000-000000000002")
	linkTestUser    = uuid.MustParse("138a0000-0000-4000-8000-0000000000aa")
)

// Proxmox runs an HA guest's start, stop or migration as a request and a worker
// task, and a live migration starts the guest on its target as a third; the two
// link queries (queries/proxmox_task_sync.sql) record which belong together, for
// the lists to mark. This holds which pairs they link and which they must leave
// unmarked, each a real action of its own.
//
// Not parallel: it writes to the shared schema. Skipped without NEXARA_TEST_DB_URL.
func TestLinkRelatedTasks(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	clusters := []uuid.UUID{linkTestCluster, linkTestOther}
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// The audit rows go first: a cluster's delete leaves them (000122).
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, linkTestUser)
	}
	purge()
	defer purge() // A defer, not t.Cleanup: the pool must still be open (see setupMigration).

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, 'task-link-test@nexara.test', '')`, linkTestUser)
	for i, id := range clusters {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, "cluster0"+strconv.Itoa(i+1))
	}

	const token, ha = "root@pam!token01", "root@pam"
	t0 := time.Now().UTC().Truncate(time.Second).Add(-30 * time.Minute)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	seq := 0
	// row is one task: its audit row and its task row, as Nexara records one it
	// dispatched (source nexara) or as the collector ingests one it did not.
	type row struct {
		upid, source string
	}
	add := func(cluster uuid.UUID, source, node, taskType string, vmid int, user string, start time.Time, status string, runFor time.Duration) row {
		t.Helper()
		seq++
		upid := fmt.Sprintf("UPID:%s:%08X:00000001:%08X:%s:%d:%s:", node, seq, start.Unix(), taskType, vmid, user)
		details := map[string]any{"upid": upid, "node": node}
		resourceID, uid, created := strconv.Itoa(vmid), systemUserID, start
		if source == "nexara" {
			// Nexara files its row once Proxmox has answered, under its own id for the guest.
			details["vmid"] = vmid
			resourceID, uid, created = uuid.NewString(), linkTestUser, start.Add(time.Second)
		} else {
			// What the task list said at ingest: RUNNING, or how the task ended.
			recorded := map[string]string{"running": "RUNNING", "failed": "command failed"}[status]
			if recorded == "" {
				recorded = "OK"
			}
			details["proxmox_user"], details["status"], details["task_type"] = user, recorded, taskType
		}
		raw, _ := json.Marshal(details)
		if err := q.InsertAuditLogWithSource(ctx, gen.InsertAuditLogWithSourceParams{
			ClusterID: pgtype.UUID{Bytes: cluster, Valid: true}, UserID: pgtype.UUID{Bytes: uid, Valid: true},
			ResourceType: "vm", ResourceID: resourceID, Action: taskType, Details: raw, Source: source, CreatedAt: created,
		}); err != nil {
			t.Fatalf("audit row: %v", err)
		}
		var finished *time.Time
		if status != "running" {
			end := start.Add(runFor)
			finished = &end
		}
		exec("task", `INSERT INTO task_history (cluster_id, user_id, upid, description, status, exit_status, node, task_type,
				vmid, started_at, finished_at, source)
			VALUES ($1, $2, $3, $4, $5, CASE $5 WHEN 'failed' THEN 'command failed' WHEN 'completed' THEN 'OK' ELSE '' END,
				$6, $7, $8, $9, $10, $11)`,
			cluster, uid, upid, source+" "+taskType, status, node, taskType, vmid, start, finished, source)
		return row{upid, source}
	}
	asked := func(taskType string, vmid, sec int) row { // Nexara's request, through its token.
		return add(linkTestCluster, "nexara", "pve-01", taskType, vmid, token, at(sec), "completed", time.Second)
	}
	ran := func(node, taskType string, vmid int, user string, sec int, status string) row { // Ingested from Proxmox.
		return add(linkTestCluster, "proxmox", node, taskType, vmid, user, at(sec), status, time.Minute)
	}

	// An HA request and its worker. Each guest is one case.
	startReq, startWork := asked("hastart", 100, 0), ran("pve-01", "qmstart", 100, ha, 4, "failed")
	guiReq := ran("pve-01", "hamigrate", 101, "admin@pam", 0, "completed")
	guiWork := ran("pve-01", "qmigrate", 101, ha, 12, "completed")
	asked("hastop", 102, 0)
	tokenWork := ran("pve-01", "qmshutdown", 102, token, 5, "completed")
	asked("hastart", 103, 0)
	otherKind := ran("pve-01", "qmshutdown", 103, ha, 5, "completed")
	asked("hastart", 104, 0)
	late := ran("pve-01", "qmstart", 104, ha, 180, "completed")
	early := ran("pve-01", "qmstart", 105, ha, -5, "completed")
	asked("hastart", 105, 0)
	firstReq, secondReq := asked("hamigrate", 106, 0), asked("hamigrate", 106, 20)
	nearest := ran("pve-01", "qmigrate", 106, ha, 30, "completed")
	onceReq := asked("hastop", 107, 0)
	firstWork, retry := ran("pve-01", "qmstop", 107, ha, 5, "failed"), ran("pve-01", "qmstop", 107, ha, 40, "completed")
	skewWork, skewReq := ran("pve-01", "qmstart", 110, ha, 0, "completed"), asked("hastart", 110, 1)
	otherNodeReq, otherNodeWork := asked("hastart", 111, 0), ran("pve-02", "qmstart", 111, ha, 25, "completed")
	// Two hours in: the request long finished, its worker still running.
	longReq, longWork := asked("hamigrate", 112, -7200), ran("pve-01", "qmigrate", 112, ha, -7190, "running")
	add(linkTestOther, "nexara", "pve-01", "hastart", 100, token, at(0), "completed", time.Second)
	otherCluster := add(linkTestOther, "proxmox", "pve-01", "qmstart", 100, ha, at(4), "completed", time.Minute)

	// A live migration and the start on its target node.
	mig := add(linkTestCluster, "nexara", "pve-01", "qmigrate", 120, token, at(0), "completed", time.Minute)
	target := ran("pve-02", "qmstart", 120, ha, 2, "completed")
	add(linkTestCluster, "nexara", "pve-01", "qmigrate", 121, token, at(0), "completed", 10*time.Second)
	afterEnd := ran("pve-02", "qmstart", 121, ha, 30, "completed")
	add(linkTestCluster, "nexara", "pve-01", "qmigrate", 122, token, at(0), "completed", time.Minute)
	sameNode := ran("pve-01", "qmstart", 122, ha, 2, "completed")
	add(linkTestCluster, "nexara", "pve-01", "qmigrate", 123, token, at(0), "running", 0)
	longAfter := ran("pve-02", "qmstart", 123, ha, 180, "completed")
	// Ingested while it ran and closed by the reconciler, a migration's finished_at is when a
	// pass noticed, not when it ended: the start made after it ended is no part of it.
	polled := ran("pve-01", "qmigrate", 126, ha, 0, "completed")
	exec("ingested running", `UPDATE audit_log SET details = details || '{"status":"RUNNING"}' WHERE details->>'upid' = $1`, polled.upid)
	afterPolled := ran("pve-02", "qmstart", 126, ha, 20, "completed")
	// Ingested finished, its end is Proxmox's own, and a late start before it is the migration's.
	slow := ran("pve-01", "qmigrate", 127, ha, 0, "completed")
	slowStart := ran("pve-02", "qmstart", 127, ha, 30, "completed")
	// An HA migration is all three: the worker stands for the start, the request for the worker.
	chainReq, chainWork := asked("hamigrate", 124, 0), ran("pve-01", "qmigrate", 124, ha, 10, "failed")
	chainStart := ran("pve-02", "qmstart", 124, ha, 12, "completed")
	// A start the HA manager had nothing to do for, then a migration: its start is the migration's.
	idleReq := asked("hastart", 125, 0)
	laterMig := add(linkTestCluster, "nexara", "pve-01", "qmigrate", 125, token, at(30), "completed", time.Minute)
	laterStart := ran("pve-02", "qmstart", 125, ha, 32, "completed")

	link := func() (starts, workers int64) {
		t.Helper()
		starts, err := q.LinkMigrationTargetStarts(ctx, gen.LinkMigrationTargetStartsParams{ClusterID: linkTestCluster, Since: at(-3 * 3600)})
		if err != nil {
			t.Fatalf("link target starts: %v", err)
		}
		workers, err = q.LinkHAWorkerTasks(ctx, gen.LinkHAWorkerTasksParams{ClusterID: linkTestCluster, Since: at(-3 * 3600)})
		if err != nil {
			t.Fatalf("link HA workers: %v", err)
		}
		return starts, workers
	}
	if starts, workers := link(); starts != 4 || workers != 8 {
		t.Errorf("first pass linked %d target starts and %d HA workers, want 4 and 8", starts, workers)
	}

	for _, tc := range []struct {
		name string
		row  row
		want map[string]string // the link keys the row carries, and no others
	}{
		{"Nexara's request reads its worker", startReq, map[string]string{"ha_worker_upid": startWork.upid}},
		{"its worker names the request", startWork, map[string]string{"ha_request_upid": startReq.upid}},
		{"a request made in Proxmox reads its worker too", guiReq, map[string]string{"ha_worker_upid": guiWork.upid}},
		{"that worker names who asked", guiWork, map[string]string{"ha_request_upid": guiReq.upid, "ha_requested_by": "admin@pam"}},
		{"a token's own shutdown", tokenWork, nil},
		{"a shutdown after a start request", otherKind, nil},
		{"a start three minutes after the request", late, nil},
		{"a start before the request", early, nil},
		{"the nearer of two requests takes the worker", secondReq, map[string]string{"ha_worker_upid": nearest.upid}},
		{"the earlier one keeps none", firstReq, nil},
		{"a request keeps its first worker", onceReq, map[string]string{"ha_worker_upid": firstWork.upid}},
		{"the HA manager's retry stays a row of its own", retry, nil},
		{"a request a second after its worker, inside the slack", skewReq, map[string]string{"ha_worker_upid": skewWork.upid}},
		{"a worker on another node than its request", otherNodeWork, map[string]string{"ha_request_upid": otherNodeReq.upid}},
		{"a worker still running long after its request", longWork, map[string]string{"ha_request_upid": longReq.upid}},
		{"another cluster's worker", otherCluster, nil},
		{"a migration's start on its target", target, map[string]string{"migration_upid": mig.upid}},
		{"the migration names it", mig, map[string]string{"target_start_upid": target.upid}},
		{"a start after the migration ended", afterEnd, nil},
		{"a start on the migration's own node", sameNode, nil},
		{"a start three minutes into a migration still running", longAfter, nil},
		{"a start after a migration whose end a poll stamped late", afterPolled, nil},
		{"a late start before a migration's own recorded end", slowStart, map[string]string{"migration_upid": slow.upid}},
		{"an HA migration's start is its worker's", chainStart, map[string]string{"migration_upid": chainWork.upid}},
		{"and the worker its request's", chainWork, map[string]string{
			"ha_request_upid": chainReq.upid, "target_start_upid": chainStart.upid}},
		{"a migration's start is not an idle request's worker", laterStart, map[string]string{"migration_upid": laterMig.upid}},
		{"the idle request keeps none", idleReq, nil},
	} {
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT details FROM audit_log WHERE details->>'upid' = $1 AND source = $2`,
			tc.row.upid, tc.row.source).Scan(&raw); err != nil {
			t.Fatalf("%s: read: %v", tc.name, err)
		}
		var details map[string]any
		_ = json.Unmarshal(raw, &details)
		got := map[string]string{}
		for _, k := range []string{"ha_worker_upid", "ha_request_upid", "ha_requested_by", "migration_upid", "target_start_upid"} {
			if v, ok := details[k].(string); ok {
				got[k] = v
			}
		}
		if !maps.Equal(got, tc.want) {
			t.Errorf("%s: link keys %v, want %v", tc.name, got, tc.want)
		}
	}
	if starts, workers := link(); starts != 0 || workers != 0 {
		t.Errorf("a second pass linked %d and %d, want none", starts, workers)
	}
}
