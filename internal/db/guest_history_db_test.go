package db

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed so a run that dies before its cleanup leaves rows the
// next run deletes instead of accumulating them.
var (
	historyClusterID = uuid.MustParse("115a0000-0000-4000-8000-00000000000a")
	historyOtherID   = uuid.MustParse("115a0000-0000-4000-8000-00000000000b")
	historyUserID    = uuid.MustParse("115a0000-0000-4000-8000-000000000001")
	historyNodeID    = uuid.MustParse("115a0000-0000-4000-8000-0000000000d1")
	// A firewall template's audit rows outlive any cluster: no cluster_id.
	historyTemplateID = "115a0000-0000-4000-8000-0000000000f1"
	systemUserID      = uuid.MustParse("00000000-0000-0000-0000-000000000001")
)

func historyUPID(seq int, workerType, id string) string {
	return fmt.Sprintf("UPID:pve-01:%08X:00000001:66000001:%s:%s:root@pam:", seq, workerType, id)
}

// TestGuestHistory runs the History SQL (queries/history.sql and
// guest_history.sql, migrations/000115 and 000116) against Postgres: which rows make one guest's
// timeline, how a status change is classified, and what the prune keeps — and
// the generic selector's rows for an object. Each fixture row is in or out of
// the timeline for exactly one reason, named beside it.
//
// Not parallel: it writes rows the package's other DB tests share a schema
// with. Skipped unless NEXARA_TEST_DB_URL names a throwaway database.
func TestGuestHistory(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	// Deferred rather than t.Cleanup: see setupMigration.
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Every fixture table cascades from clusters except audit_log, whose entries
		// outlive a removed cluster (000122), so they go first, by cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, []uuid.UUID{historyClusterID, historyOtherID})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{historyClusterID, historyOtherID})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, historyUserID)
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = 'firewall_template' AND resource_id = $1`, historyTemplateID)
	}
	purge()
	defer purge()
	// No worker patterns, NULL ones included, keep every guest row.
	var everyRow bool
	if err := pool.QueryRow(ctx, `SELECT history_worker_ok('UPID:pve-01:1:1:1:qmstart:100:root@pam:', NULL)`).Scan(&everyRow); err != nil || !everyRow {
		t.Errorf("history_worker_ok with NULL patterns = %v (%v), want true", everyRow, err)
	}
	// The selector reads without the audit indexes built after startup
	// (EnsureHistoryIndexes), only slower: the timeline runs with them absent.
	for _, ix := range historyIndexes {
		if _, err := pool.Exec(ctx, "DROP INDEX IF EXISTS "+ix.name); err != nil {
			t.Fatal(err)
		}
	}

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, 'guest-history-test@nexara.test', '')`, historyUserID)
	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{{historyClusterID, "cluster01"}, {historyOtherID, "cluster02"}} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://invalid.local', 'tok', 'enc')`, c.id, c.name)
	}
	exec("node", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`, historyNodeID, historyClusterID)

	now := time.Now().UTC().Truncate(time.Second)
	hoursAgo := func(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }
	minsAgo := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }

	audit := func(cluster uuid.UUID, at time.Time, source, resourceType, resourceID, action string, details map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(details)
		user := historyUserID
		if source == "proxmox" {
			user = systemUserID
		}
		if err := q.InsertAuditLogWithSource(ctx, gen.InsertAuditLogWithSourceParams{
			ClusterID: pgtype.UUID{Bytes: cluster, Valid: cluster != uuid.Nil}, UserID: pgtype.UUID{Bytes: user, Valid: true},
			ResourceType: resourceType, ResourceID: resourceID, Action: action, Details: raw,
			Source: source, CreatedAt: at,
		}); err != nil {
			t.Fatalf("seed audit %s: %v", action, err)
		}
	}
	task := func(upid, status string, vmid any, started time.Time, finished any) {
		t.Helper()
		exec("task "+upid, `INSERT INTO task_history (cluster_id, user_id, upid, status, exit_status, node,
				task_type, vmid, started_at, finished_at, source)
			VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'completed' THEN 'OK' WHEN $4 = 'failed' THEN 'command failed' ELSE '' END,
				'pve-01', split_part($3, ':', 6), $5, $6, $7, 'proxmox')`,
			historyClusterID, systemUserID, upid, status, vmid, started, finished)
	}

	// ── The previous guest to hold VMID 100: before the cutoff, so absent —
	// and an older holder's clone into the VMID and HA resource.
	audit(historyClusterID, hoursAgo(11), "nexara", "vm", uuid.NewString(), "clone", map[string]any{"upid": historyUPID(36, "qmclone", "60"), "vmid": 60, "new_id": 100})
	audit(historyClusterID, hoursAgo(10.5), "nexara", "ha_resource", "vm:100", "created", map[string]any{"state": "started"})
	audit(historyClusterID, hoursAgo(10), "proxmox", "vm", "100", "create", map[string]any{"upid": historyUPID(1, "qmcreate", "100"), "status": "OK"})
	audit(historyClusterID, hoursAgo(9), "proxmox", "vm", "100", "start", map[string]any{"upid": historyUPID(2, "qmstart", "100"), "status": "OK"})
	audit(historyClusterID, hoursAgo(8), "proxmox", "vm", "100", "destroy", map[string]any{"upid": historyUPID(3, "qmdestroy", "100"), "status": "OK"})

	// ── This guest, VMID 100, cloned from 50 seven hours ago: the birth.
	cloneUPID, cloneRow := historyUPID(4, "qmclone", "50"), uuid.NewString()
	audit(historyClusterID, hoursAgo(7), "nexara", "vm", cloneRow, "clone", map[string]any{"upid": cloneUPID, "vmid": 50, "new_id": 100})
	// A Nexara start, and the collector's ingest of the same UPID: one entry.
	startUPID := historyUPID(5, "qmstart", "100")
	audit(historyClusterID, hoursAgo(6), "nexara", "vm", uuid.NewString(), "start", map[string]any{"upid": startUPID})
	audit(historyClusterID, hoursAgo(6).Add(time.Second), "proxmox", "vm", "100", "start", map[string]any{"upid": startUPID, "status": "RUNNING"})
	task(startUPID, "completed", 100, hoursAgo(6), hoursAgo(6).Add(5*time.Second))
	audit(historyClusterID, hoursAgo(5), "nexara", "vm", uuid.NewString(), "config_update", map[string]any{"vmid": 100, "fields": map[string]any{"cores": "4"}})
	// Two tasks with no audit row: one running (never pruned), one pruned to
	// the archive. The running one is a disk move, which changes no run state:
	// a running snapshot or backup would explain every status change below.
	moveUPID := historyUPID(6, "qmmove", "100")
	task(moveUPID, "running", 100, hoursAgo(4), nil)
	rollbackUPID := historyUPID(7, "qmrollback", "100")
	task(rollbackUPID, "failed", 100, hoursAgo(3.5), hoursAgo(3.5).Add(time.Minute))
	// A migration whose task row is pruned: its outcome must survive in the archive.
	migrateUPID := historyUPID(8, "qmigrate", "100")
	audit(historyClusterID, hoursAgo(3), "proxmox", "vm", "100", "migrate", map[string]any{"upid": migrateUPID, "status": "RUNNING"})
	task(migrateUPID, "failed", 100, hoursAgo(3), hoursAgo(3).Add(time.Minute))
	// An HA resource row names the guest only by its sid.
	audit(historyClusterID, hoursAgo(2), "nexara", "ha_resource", "vm:100", "created", map[string]any{"state": "started"})

	// ── Never this guest's: Ceph OSD tasks whose worker id reads as 100 — an
	// old one the prune drops, a recent one it keeps — and the other
	// cluster's VM 100.
	osdUPID := historyUPID(9, "cephdestroyosd", "100")
	task(osdUPID, "completed", 100, hoursAgo(2), hoursAgo(2).Add(time.Minute))
	task(historyUPID(14, "cephdestroyosd", "100"), "completed", 100, minsAgo(25), minsAgo(24))
	audit(historyOtherID, hoursAgo(1), "proxmox", "vm", "100", "start", map[string]any{"upid": historyUPID(10, "qmstart", "100"), "status": "OK"})

	// ── The trigger records a status change on any write of vms.status, and
	// only a change.
	upsert := func(status string) {
		t.Helper()
		if _, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: historyClusterID, NodeID: historyNodeID,
			Vmid: 100, Name: "linux01", Type: "qemu", Status: status}); err != nil {
			t.Fatalf("upsert vm: %v", err)
		}
	}
	upsert("running")
	upsert("running")
	upsert("stopped")
	var recorded []string
	rows, err := pool.Query(ctx, `SELECT guest_type || ' ' || node || ' ' || from_status || '>' || to_status
		FROM guest_status_changes WHERE cluster_id = $1`, historyClusterID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		recorded = append(recorded, s)
	}
	if want := []string{"qemu pve-01 running>stopped"}; !slices.Equal(recorded, want) {
		t.Fatalf("status changes recorded = %v, want %v", recorded, want)
	}
	exec("clear trigger row", `DELETE FROM guest_status_changes WHERE cluster_id = $1`, historyClusterID)

	// ── Classification. Each change, and the tasks around it:
	// lastSeen is when the guest was last seen in its old state: one slow pass
	// earlier, unless Nexara was not looking.
	statusChangeSeen := func(name, to string, lastSeen, at time.Time) {
		exec("status change "+name, `INSERT INTO guest_status_changes (id, cluster_id, vmid, guest_type, node, from_status,
				to_status, last_seen_at, observed_at)
			VALUES ($1, $2, 100, 'qemu', 'pve-01', 'running', $3, $4, $5)`,
			uuid.NewSHA1(uuid.Nil, []byte(name)), historyClusterID, to, lastSeen, at)
	}
	statusChange := func(name, to string, at time.Time) {
		statusChangeSeen(name, to, at.Add(-30*time.Second), at)
	}
	shutdownUPID := historyUPID(11, "qmshutdown", "100")
	audit(historyClusterID, minsAgo(31), "proxmox", "vm", "100", "shutdown", map[string]any{"upid": shutdownUPID, "status": "OK"})
	task(shutdownUPID, "completed", 100, minsAgo(31), minsAgo(30).Add(5*time.Second))
	statusChange("shutdown", "stopped", minsAgo(30)) // explained by the shutdown
	statusChange("crash", "stopped", minsAgo(20))    // NOT explained: the restart below begins after it
	restartUPID := historyUPID(12, "qmstart", "100")
	audit(historyClusterID, minsAgo(20).Add(10*time.Second), "proxmox", "vm", "100", "start", map[string]any{"upid": restartUPID, "status": "OK"})
	task(restartUPID, "completed", 100, minsAgo(20).Add(10*time.Second), minsAgo(20).Add(15*time.Second))
	statusChange("restart", "running", minsAgo(20).Add(15*time.Second)) // explained by that start
	// Backup jobs on the node, whose vzdumps carry no VMID. One whose log was
	// read in full explains only its own guests' changes, each while its own
	// backup ran; one read in part, every change on the node while it ran, up
	// to when its log was read; one not read at all, none; one with no mark
	// yet, finished or running, holds the node's changes back. Off VM 100, the
	// changes on other guests stay out of its timeline.
	type guestPart struct {
		VMID       int32     `json:"vmid"`
		Status     string    `json:"status"`
		ExitStatus string    `json:"exit_status"`
		StartedAt  time.Time `json:"started_at"`
		FinishedAt time.Time `json:"finished_at"`
		LogStart   int       `json:"log_start"`
		LogEnd     int       `json:"log_end"`
	}
	recordJob := func(cluster uuid.UUID, upid, logRead string, parts ...guestPart) {
		t.Helper()
		raw, _ := json.Marshal(append([]guestPart{}, parts...))
		if err := q.RecordBackupJobLog(ctx, gen.RecordBackupJobLogParams{
			Upid: upid, ClusterID: cluster, Guests: raw, LogRead: logRead,
		}); err != nil {
			t.Fatalf("record job %s: %v", upid, err)
		}
	}
	otherGuestChange := func(name string, vmid int32, at time.Time) {
		exec("status change "+name, `INSERT INTO guest_status_changes (id, cluster_id, vmid, guest_type, node, from_status,
				to_status, last_seen_at, observed_at)
			VALUES ($1, $2, $3, 'qemu', 'pve-01', 'running', 'stopped', $4, $5)`,
			uuid.NewSHA1(uuid.Nil, []byte(name)), historyClusterID, vmid, at.Add(-30*time.Second), at)
	}
	jobUPID := historyUPID(13, "vzdump", "")
	task(jobUPID, "completed", nil, minsAgo(16), minsAgo(10))
	recordJob(historyClusterID, jobUPID, "full",
		guestPart{100, "failed", "storage gone", minsAgo(15.5), minsAgo(14.5), 41, 60},
		guestPart{101, "completed", "OK", minsAgo(14.5), minsAgo(11), 61, 90})
	// The write is pinned to the job's own cluster.
	recordJob(historyOtherID, jobUPID, "full", guestPart{999, "completed", "OK", minsAgo(15), minsAgo(14), 1, 2})
	statusChange("backup", "stopped", minsAgo(15))        // explained by VM 100's own backup
	otherGuestChange("job-other-guest", 103, minsAgo(12)) // NOT: VM 103 was not in the job
	// Its finished_at set far ahead, as PUT /api/v1/tasks/:upid can: the
	// crash and VM 103's stop after its log was read stay unexplained.
	partReadUPID := historyUPID(18, "vzdump", "")
	task(partReadUPID, "completed", nil, minsAgo(28), now.AddDate(100, 0, 0))
	recordJob(historyClusterID, partReadUPID, "partial")
	exec("job read in part", `UPDATE backup_job_logs SET recorded_at = $1 WHERE upid = $2`, minsAgo(26), partReadUPID)
	otherGuestChange("job-read-in-part", 104, minsAgo(27)) // explained by the job on its node
	unreadableUPID := historyUPID(21, "vzdump", "")
	task(unreadableUPID, "completed", nil, minsAgo(35), minsAgo(33))
	recordJob(historyClusterID, unreadableUPID, "none")
	otherGuestChange("job-not-read", 104, minsAgo(34)) // NOT: nothing says the job touched it
	pendingUPID := historyUPID(19, "vzdump", "")
	task(pendingUPID, "completed", nil, minsAgo(9.5), minsAgo(9))
	otherGuestChange("job-unread", 103, minsAgo(9.25)) // waits for the job's log
	task(historyUPID(22, "vzdump", ""), "running", nil, minsAgo(5.4), nil)
	otherGuestChange("job-running", 104, minsAgo(5.2)) // waits for the job to end
	// A container's suspend checkpoints and stops it. Clear of the backup
	// job's window, which would explain it whatever the suspend's type.
	task(historyUPID(16, "vzsuspend", "100"), "completed", 100, minsAgo(7), minsAgo(6).Add(5*time.Second))
	statusChange("suspend", "stopped", minsAgo(6)) // explained by the suspend
	statusChange("young", "stopped", minsAgo(1))   // not settled yet: left unclassified
	// On a node whose tasks this pass did not list: left for a pass that does.
	exec("unlisted change", `INSERT INTO guest_status_changes (id, cluster_id, vmid, guest_type, node, from_status,
			to_status, last_seen_at, observed_at)
		VALUES ($1, $2, 100, 'qemu', 'pve-02', 'running', 'stopped', $3, $3)`,
		uuid.NewSHA1(uuid.Nil, []byte("unlisted")), historyClusterID, minsAgo(25))
	// Left unlisted past the retention: the stop that explains it is in the
	// archive by the time the node lists again.
	archivedStopUPID := historyUPID(17, "qmstop", "100")
	exec("archived stop", `INSERT INTO task_history_archive (upid, cluster_id, vmid, status, exit_status, node,
			task_type, started_at, finished_at)
		VALUES ($1, $2, 100, 'completed', 'OK', 'pve-01', 'qmstop', $3, $4)`,
		archivedStopUPID, historyClusterID, hoursAgo(8*24), hoursAgo(8*24).Add(5*time.Second))
	statusChangeSeen("archived", "stopped", hoursAgo(8*24).Add(-30*time.Second), hoursAgo(8*24).Add(10*time.Second))
	// An offline member: a change to "unknown" is decided (no task explains
	// one), a stop waits for the tasks the node has not handed over. On VM 102
	// to stay out of the timeline and the prune's VM 101 rows.
	for _, c := range []struct{ name, to string }{{"offline-unknown", "unknown"}, {"offline-stop", "stopped"}} {
		exec("offline change", `INSERT INTO guest_status_changes (id, cluster_id, vmid, guest_type, node, from_status,
				to_status, last_seen_at, observed_at)
			VALUES ($1, $2, 102, 'qemu', 'pve-03', 'running', $3, $4, $4)`,
			uuid.NewSHA1(uuid.Nil, []byte(c.name)), historyClusterID, c.to, minsAgo(20))
	}
	// A day on a node whose list never loads: decided anyway, and shown —
	// though a job on that node, its log never read, overlaps it.
	exec("stale change", `INSERT INTO guest_status_changes (id, cluster_id, vmid, guest_type, node, from_status,
			to_status, last_seen_at, observed_at)
		VALUES ($1, $2, 100, 'qemu', 'pve-02', 'running', 'stopped', $3, $3)`,
		uuid.NewSHA1(uuid.Nil, []byte("stale")), historyClusterID, hoursAgo(30))
	staleJobUPID := strings.Replace(historyUPID(24, "vzdump", ""), "pve-01", "pve-02", 1)
	exec("stale change's job", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type,
			started_at, finished_at, source)
		VALUES ($1, $2, $3, 'completed', 'pve-02', 'vzdump', $4, $5, 'proxmox')`,
		historyClusterID, systemUserID, staleJobUPID, hoursAgo(30.1), hoursAgo(29.9))
	// Nexara was down from 50 to 40 minutes ago; a stop made in that gap is
	// first seen when it is back, and the task that made it ended long before.
	gapUPID := historyUPID(15, "qmstop", "100")
	audit(historyClusterID, minsAgo(45), "proxmox", "vm", "100", "stop", map[string]any{"upid": gapUPID, "status": "OK"})
	task(gapUPID, "completed", 100, minsAgo(45), minsAgo(45).Add(5*time.Second))
	statusChangeSeen("gap", "stopped", minsAgo(50), minsAgo(40)) // explained by the stop in the gap

	if _, err := q.ClassifyGuestStatusChanges(ctx, gen.ClassifyGuestStatusChangesParams{
		ClusterID: historyClusterID, SettledBefore: minsAgo(5), ListedNodes: []string{"pve-01"},
		OfflineNodes: []string{"pve-03"}, GiveUpBefore: hoursAgo(24),
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	for name, want := range map[string]pgtype.Bool{
		"shutdown": {Bool: true, Valid: true}, "crash": {Bool: false, Valid: true},
		"restart": {Bool: true, Valid: true}, "backup": {Bool: true, Valid: true}, "young": {},
		"gap": {Bool: true, Valid: true}, "suspend": {Bool: true, Valid: true},
		"unlisted": {}, "archived": {Bool: true, Valid: true}, "stale": {Bool: false, Valid: true},
		"offline-unknown": {Bool: false, Valid: true}, "offline-stop": {},
		"job-other-guest": {Bool: false, Valid: true}, "job-read-in-part": {Bool: true, Valid: true}, "job-unread": {},
		"job-not-read": {Bool: false, Valid: true}, "job-running": {},
	} {
		var got pgtype.Bool
		if err := pool.QueryRow(ctx, `SELECT explained FROM guest_status_changes WHERE id = $1`,
			uuid.NewSHA1(uuid.Nil, []byte(name))).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if got != want {
			t.Errorf("status change %q: explained = %+v, want %+v", name, got, want)
		}
	}

	var foreign int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM guest_backups WHERE vmid = 999`).Scan(&foreign); err != nil || foreign != 0 {
		t.Errorf("a job's guests written through another cluster: %d rows (err %v)", foreign, err)
	}

	// ── The jobs the collector still reads: those with no mark on a listed
	// node, the ones on their last try first. Past the give-up — a day from
	// when the row last changed — one whose node is not listed is marked
	// unread, and that mark goes with the job's row when the prune takes it,
	// while the guests' own record stays.
	lastTryUPID := historyUPID(23, "vzdump", "")
	exec("job on its last try", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type,
			started_at, finished_at, source, updated_at)
		VALUES ($1, $2, $3, 'completed', 'pve-01', 'vzdump', $4, $5, 'proxmox', $6)`,
		historyClusterID, systemUserID, lastTryUPID, minsAgo(50), minsAgo(49), hoursAgo(30))
	unlistedJobUPID := strings.Replace(historyUPID(20, "vzdump", ""), "pve-01", "pve-02", 1)
	exec("unlisted job", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at,
			finished_at, source)
		VALUES ($1, $2, $3, 'completed', 'pve-02', 'vzdump', $4, $5, 'proxmox')`,
		historyClusterID, systemUserID, unlistedJobUPID, hoursAgo(26), hoursAgo(25.9))
	exec("unlisted job's guest", `INSERT INTO guest_backups (upid, vmid, cluster_id, node, source, status,
			started_at, finished_at, log_start, log_end)
		VALUES ($1, 101, $2, 'pve-02', 'proxmox', 'completed', $3, $3, 1, 2)`, unlistedJobUPID, historyClusterID, hoursAgo(26))
	unread, err := q.ListUnreadBackupJobs(ctx, gen.ListUnreadBackupJobsParams{
		ClusterID: historyClusterID, ListedNodes: []string{"pve-01"}, GiveUpBefore: hoursAgo(24), MaxJobs: 4,
	})
	if err != nil {
		t.Fatalf("list unread jobs: %v", err)
	}
	if len(unread) != 2 || unread[0].Upid != lastTryUPID || !unread[0].LastTry ||
		unread[1].Upid != pendingUPID || unread[1].LastTry {
		t.Errorf("unread jobs = %+v, want %s on its last try, then %s", unread, lastTryUPID, pendingUPID)
	}
	for _, tc := range []struct {
		giveUpBefore time.Time
		want         int64
	}{{hoursAgo(24), 0}, {now.Add(time.Minute), 2}} {
		if n, err := q.AbandonUnreadBackupJobs(ctx, gen.AbandonUnreadBackupJobsParams{
			ClusterID: historyClusterID, ListedNodes: []string{"pve-01"}, GiveUpBefore: tc.giveUpBefore,
		}); err != nil || n != tc.want {
			t.Errorf("giving up before %v abandoned %d jobs (err %v), want %d: only the unlisted node's, and only "+
				"a day after their rows last changed", tc.giveUpBefore, n, err, tc.want)
		}
	}

	// ── The prune moves guest tasks to the archive and drops the rest. It
	// also drops old status changes a task explained, and keeps the ones none
	// did (seeded on VM 101 to stay out of the timeline below).
	exec("old status changes", `INSERT INTO guest_status_changes (cluster_id, vmid, guest_type, node, from_status,
			to_status, last_seen_at, observed_at, explained)
		VALUES ($1, 101, 'qemu', 'pve-01', 'running', 'stopped', $2, $2, true),
		       ($1, 101, 'qemu', 'pve-01', 'stopped', 'running', $2, $2, false)`, historyClusterID, hoursAgo(2))
	if err := q.DeleteCompletedTasks(ctx, hoursAgo(1)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	var keptExplained []bool
	kept, err := pool.Query(ctx, `SELECT explained FROM guest_status_changes WHERE cluster_id = $1 AND vmid = 101`, historyClusterID)
	if err != nil {
		t.Fatal(err)
	}
	for kept.Next() {
		var e bool
		if err := kept.Scan(&e); err != nil {
			t.Fatal(err)
		}
		keptExplained = append(keptExplained, e)
	}
	if !slices.Equal(keptExplained, []bool{false}) {
		t.Errorf("old status changes kept by the prune = %v, want only the unexplained one", keptExplained)
	}
	var marks, parts int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM backup_job_logs WHERE upid = $1),
			(SELECT count(*) FROM guest_backups WHERE upid = $1)`, unlistedJobUPID).Scan(&marks, &parts); err != nil {
		t.Fatal(err)
	}
	if marks != 0 || parts != 1 {
		t.Errorf("after the prune took the job: %d marks (want 0), %d guest rows (want 1)", marks, parts)
	}
	var live, archived, osdArchived int
	if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM task_history WHERE cluster_id = $1 AND started_at < $2),
			(SELECT count(*) FROM task_history_archive WHERE cluster_id = $1 AND upid <> $4),
			(SELECT count(*) FROM task_history_archive WHERE upid = $3)`,
		historyClusterID, hoursAgo(1), osdUPID, archivedStopUPID).Scan(&live, &archived, &osdArchived); err != nil {
		t.Fatal(err)
	}
	// The running move stays; start, rollback and migrate move; the OSD task goes.
	if live != 1 || archived != 3 || osdArchived != 0 {
		t.Fatalf("after the prune: %d old rows left in task_history (want 1), %d archived (want 3), OSD task archived %d (want 0)",
			live, archived, osdArchived)
	}
	// A failed migration keeps its verdict but not Proxmox's text, which for a
	// cross-cluster one can carry the target cluster's token.
	// Any other failed task keeps its text.
	for upid, want := range map[string]string{migrateUPID: "", rollbackUPID: "command failed"} {
		var status, exit string
		if err := pool.QueryRow(ctx, `SELECT status, exit_status FROM task_history_archive WHERE upid = $1`,
			upid).Scan(&status, &exit); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || exit != want {
			t.Errorf("archived %s = (%q, %q), want (\"failed\", %q)", upid, status, exit, want)
		}
	}

	// ── Where the timeline starts, which a finished task's outcome decides:
	// that is the collector's, and PUT /api/v1/tasks/:upid (UpdateTaskHistory)
	// writes only a running task Nexara recorded.
	for i, tt := range []struct {
		status, source string
		want           int64
	}{{"completed", "nexara", 0}, {"running", "proxmox", 0}, {"running", "nexara", 1}} {
		upid := historyUPID(40+i, "qmcreate", "190")
		exec("rewritable "+upid, `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, source)
			VALUES ($1, $2, $3, $4, 'pve-01', 'qmcreate', $5)`, historyClusterID, historyUserID, upid, tt.status, tt.source)
		if n, err := q.UpdateTaskHistory(ctx, gen.UpdateTaskHistoryParams{Upid: upid, Status: "failed"}); err != nil || n != tt.want {
			t.Errorf("rewriting a %s %s task: %d rows (%v), want %d", tt.status, tt.source, n, err, tt.want)
		}
	}
	events, err := q.ListGuestLifecycleEvents(ctx, gen.ListGuestLifecycleEventsParams{
		ClusterID: pgtype.UUID{Bytes: historyClusterID, Valid: true}, Vmid: 100,
	})
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	shapes := make([]string, 0, len(events))
	for _, e := range events {
		shape := "birth"
		if e.IsDestroy {
			shape = "destroy " + e.RecordedStatus
		}
		shapes = append(shapes, shape)
	}
	// A lifecycle task's verdict is the archive's once the prune has moved it,
	// whatever a task_history row filed under its UPID since says.
	archivedCreate := historyUPID(44, "qmcreate", "150")
	audit(historyClusterID, hoursAgo(1), "proxmox", "vm", "150", "create", map[string]any{"upid": archivedCreate, "status": "OK"})
	exec("archived create", `INSERT INTO task_history_archive (upid, cluster_id, vmid, status, started_at)
		VALUES ($1, $2, 150, 'completed', $3)`, archivedCreate, historyClusterID, hoursAgo(1))
	exec("planted verdict", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, vmid, source)
		VALUES ($1, $2, $3, 'failed', 'pve-01', 'qmcreate', 150, 'nexara')`, historyClusterID, historyUserID, archivedCreate)
	if created, err := q.ListGuestLifecycleEvents(ctx, gen.ListGuestLifecycleEventsParams{
		ClusterID: pgtype.UUID{Bytes: historyClusterID, Valid: true}, Vmid: 150,
	}); err != nil || len(created) != 1 || created[0].TaskStatus != "completed" {
		t.Errorf("an archived create under a planted verdict: %+v (%v), want the archive's completed", created, err)
	}
	// POST /api/v1/tasks refuses a UPID on record: in an audit row of the
	// cluster, or in the archive — not one only another cluster's rows name.
	for upid, want := range map[string]bool{archivedCreate: true, historyUPID(45, "qmcreate", "151"): false,
		historyUPID(10, "qmstart", "100"): false} {
		if got, err := q.TaskUPIDOnRecord(ctx, gen.TaskUPIDOnRecordParams{Upid: upid,
			ClusterID: pgtype.UUID{Bytes: historyClusterID, Valid: true}}); err != nil || got != want {
			t.Errorf("TaskUPIDOnRecord(%s) = %v (%v), want %v", upid, got, err, want)
		}
	}
	if want := []string{"birth", "destroy OK", "birth", "birth"}; !slices.Equal(shapes, want) {
		t.Fatalf("lifecycle events = %v, want %v (the clone, the old guest's destroy, its create, an older clone)", shapes, want)
	}

	// The timeline, through the guest kind's own rules and selection as the
	// handler builds them (HistoryKind.ListParams): VM 100 from its clone.
	guest := func(inclusive bool, guests ...handlers.HistoryGuest) gen.ListHistoryParams {
		t.Helper()
		if len(guests) == 0 {
			guests = []handlers.HistoryGuest{{VMID: 100,
				After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: inclusive}}
		}
		p, err := handlers.GuestHistory().ListParams(historyClusterID, handlers.HistorySelection{Guests: guests})
		if err != nil {
			t.Fatal(err)
		}
		p.RowLimit = 50
		return p
	}
	// The guest kind, reading the objects these cases name beside its guests.
	withObjects := handlers.GuestHistory()
	withObjects.ObjectTypes = []string{"vm", "ha_resource", "pbs_server", "ceph_osd", "pve_acl", "node", "network"}
	selection := func(sel handlers.HistorySelection) gen.ListHistoryParams {
		t.Helper()
		p, err := withObjects.ListParams(historyClusterID, sel)
		if err != nil {
			t.Fatal(err)
		}
		p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 50, true, []string{"qemu"}
		return p
	}
	list := func(p gen.ListHistoryParams) ([]string, int64) {
		t.Helper()
		rows, err := q.ListHistory(ctx, p)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		got := make([]string, 0, len(rows))
		var total int64
		for _, r := range rows {
			got = append(got, r.Kind+" "+r.Action+" "+r.Category+" "+r.TaskStatus)
			total = r.Total
		}
		return got, total
	}
	full := withFilter(guest(true), func(p *gen.ListHistoryParams) {
		p.IncludeTasks, p.StatusGuestTypes = true, []string{"qemu"}
	})
	got, total := list(full)
	want := []string{
		"task vzsuspend power completed",  // task-only, live; it explained its status change
		"backup vzdump backup failed",     // VM 100's part of the job, with its own outcome
		"audit start power completed",     // the restart, which explained its status change
		"status status_change power ",     // the crash no task explains
		"audit shutdown power completed",  // explained its status change
		"audit stop power completed",      // the stop made while Nexara was down
		"audit created ha ",               // the HA sid row
		"audit migrate migration failed",  // outcome read from the archive
		"task qmrollback snapshot failed", // task-only, from the archive
		"task qmmove migration running",   // task-only, live
		"audit config_update config ",     // no task
		"audit start power completed",     // Nexara's row; the ingested duplicate is gone
		"audit clone clone ",              // the birth, inclusive; its task was never recorded
	}
	if !slices.Equal(got, want) || total != int64(len(want)) {
		t.Fatalf("timeline =\n\t%v (total %d)\nwant\n\t%v (total %d)", got, total, want, len(want))
	}
	// No cutoff — a guest older than the audit trail, whose birth was never
	// recorded — is the whole history of the VMID, the previous guest's too.
	whole, _ := list(withFilter(guest(false, handlers.HistoryGuest{VMID: 100}), func(p *gen.ListHistoryParams) {
		p.IncludeTasks, p.StatusGuestTypes = true, []string{"qemu"}
	}))
	if i := slices.Index(whole, "audit clone clone "); i < 0 || len(whole) < i+4 || !slices.Equal(whole[i+1:i+4], []string{
		"audit destroy lifecycle ", "audit start power ", "audit create lifecycle "}) {
		t.Errorf("with no cutoff: %v, want the previous guest's destroy, start and create after the clone", whole)
	}
	// The backup entry's end, error and lines are VM 100's, not the job's.
	backups, err := q.ListHistory(ctx, withFilter(full, func(p *gen.ListHistoryParams) {
		p.Category = pgtype.Text{String: "backup", Valid: true}
	}))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup entries: %d (err %v), want 1", len(backups), err)
	}
	if b := backups[0]; b.TaskExitStatus != "storage gone" || !b.BackupFinishedAt.Time.Equal(minsAgo(14.5)) ||
		b.LogStart != 41 || b.LogEnd != 60 {
		t.Errorf("backup entry: exit %q, finished %v, lines %d-%d; want VM 100's own",
			b.TaskExitStatus, b.BackupFinishedAt.Time, b.LogStart, b.LogEnd)
	}

	for _, tt := range []struct {
		name      string
		p         gen.ListHistoryParams
		wantTotal int64
		wantFirst string
	}{
		{"without view:task the task-only rows go", withFilter(guest(true), func(p *gen.ListHistoryParams) { p.StatusGuestTypes = []string{"qemu"} }), 9, ""},
		{"without view:vm the status change goes", withFilter(full, func(p *gen.ListHistoryParams) { p.StatusGuestTypes = []string{"lxc"} }), 12, ""},
		{"exclusive after a destroy drops the event itself", withFilter(guest(false), func(p *gen.ListHistoryParams) {
			p.IncludeTasks, p.StatusGuestTypes = true, []string{"qemu"}
		}), 12, ""},
		{"category", withFilter(full, func(p *gen.ListHistoryParams) { p.Category = pgtype.Text{String: "power", Valid: true} }), 6, ""},
		{"source", withFilter(full, func(p *gen.ListHistoryParams) { p.Source = pgtype.Text{String: "detected", Valid: true} }), 1, "status status_change power "},
		{"since", withFilter(full, func(p *gen.ListHistoryParams) { p.Since = pgtype.Timestamptz{Time: hoursAgo(3.25), Valid: true} }), 8, ""},
		{"a page", withFilter(full, func(p *gen.ListHistoryParams) { p.RowLimit, p.RowOffset = 3, 3 }), 13, "status status_change power "},
		// Each guest is held to its own cutoff: VM 101's, an hour ago, keeps
		// its part of the recent job and drops its older part and its status
		// change, which VM 100's cutoff would both have kept.
		{"a second guest from its own cutoff", withFilter(guest(true, handlers.HistoryGuest{VMID: 100,
			After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: true}, handlers.HistoryGuest{VMID: 101,
			After: pgtype.Timestamptz{Time: hoursAgo(1), Valid: true}}),
			func(p *gen.ListHistoryParams) { p.IncludeTasks, p.StatusGuestTypes = true, []string{"qemu"} }), 14, ""},
		// An object naming rows a guest arm already takes — the ingested
		// rows' resource_id is the VMID — adds none of them a second time.
		{"a guest and an object over the same rows", selection(handlers.HistorySelection{
			Guests:  []handlers.HistoryGuest{{VMID: 100, After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: true}},
			Objects: []handlers.HistoryObject{{ResourceType: "vm", Value: "100"}},
		}), 13, ""},
	} {
		got, total := list(tt.p)
		if total != tt.wantTotal || (tt.wantFirst != "" && (len(got) == 0 || got[0] != tt.wantFirst)) {
			t.Errorf("%s: total %d, rows %v; want total %d, first %q", tt.name, total, got, tt.wantTotal, tt.wantFirst)
		}
		// One job's part for each of two guests is two entries: their ids
		// are the page's keys and its last sort tie-break.
		rows, err := q.ListHistory(ctx, tt.p)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, r := range rows {
			if seen[r.EntryID] {
				t.Errorf("%s: entry id %s twice", tt.name, r.EntryID)
			}
			seen[r.EntryID] = true
		}
	}

	// ── The generic selector: one node's own audit rows, and its parts' by a
	// details key — not another node's, nor the other cluster's node of the
	// same name. A Proxmox duplicate of a Nexara task is one entry here too.
	aptUPID := historyUPID(30, "aptupdate", "")
	audit(historyClusterID, minsAgo(4), "nexara", "node", "pve-01", "apt_update", map[string]any{"upid": aptUPID})
	audit(historyClusterID, minsAgo(4), "proxmox", "node", "pve-01", "apt_update", map[string]any{"upid": aptUPID, "status": "OK"})
	audit(historyClusterID, minsAgo(3), "nexara", "network", "vmbr0", "interface_updated", map[string]any{"node": "pve-01"})
	audit(historyClusterID, minsAgo(2), "nexara", "node", "pve-02", "reboot", map[string]any{})
	audit(historyOtherID, minsAgo(2), "nexara", "node", "pve-01", "reboot", map[string]any{})
	// Both rules match the apt update: the first one decides.
	node := handlers.HistoryKind{Name: "node", Categories: []string{"updates", "tasks", "other"}, Default: "other",
		ObjectTypes: []string{"node", "network", "vm"},
		Rules: []handlers.HistoryRule{
			{Category: "updates", Field: handlers.HistoryFieldAction, Equals: []string{"apt_update"}},
			{Category: "tasks", Field: handlers.HistoryFieldWorker, Like: []string{"apt%"}},
		}}
	nodeParams, err := node.ListParams(historyClusterID, handlers.HistorySelection{Objects: []handlers.HistoryObject{
		{ResourceType: "node", Value: "pve-01"}, {ResourceType: "network", DetailKey: "node", Value: "pve-01"}}})
	if err != nil {
		t.Fatal(err)
	}
	nodeParams.RowLimit = 50
	if got, total := list(nodeParams); !slices.Equal(got, []string{"audit interface_updated other ", "audit apt_update updates "}) || total != 2 {
		t.Errorf("node timeline = %v (total %d), want its network change, then its one apt update", got, total)
	}

	// A twin outside the selection drops nothing: the node selects a guest's
	// task by details.node, and the Nexara row of the same task, filed under
	// the guest's inventory id with no node, is not the node's.
	twinUPID := historyUPID(34, "qmstart", "106")
	audit(historyClusterID, minsAgo(1), "nexara", "vm", uuid.NewString(), "start", map[string]any{"upid": twinUPID})
	audit(historyClusterID, minsAgo(1), "proxmox", "vm", "106", "start", map[string]any{"upid": twinUPID, "node": "pve-01", "status": "OK"})
	twinParams, err := node.ListParams(historyClusterID, handlers.HistorySelection{Objects: []handlers.HistoryObject{
		{ResourceType: "vm", DetailKey: "node", Value: "pve-01"}}})
	if err != nil {
		t.Fatal(err)
	}
	twinParams.RowLimit = 50
	if got, _ := list(twinParams); !slices.Equal(got, []string{"audit start other "}) {
		t.Errorf("a node's guest task whose Nexara twin is the guest's: %v, want the Proxmox row kept", got)
	}

	// ── The selector's other shapes, each against a row it must not take.
	audit(historyClusterID, minsAgo(4), "nexara", "pbs_server", "srv-a", "datastore_update", map[string]any{"datastore": "datastore01"})
	audit(historyClusterID, minsAgo(4), "nexara", "pbs_server", "srv-b", "datastore_update", map[string]any{"datastore": "datastore01"})
	audit(historyClusterID, minsAgo(4), "nexara", "ceph_osd", "osd.12", "osd_restart", map[string]any{"osd_id": 12})
	audit(historyClusterID, minsAgo(4), "nexara", "ceph_osd", "osd.120", "osd_restart", map[string]any{"osd_id": 120})
	audit(historyClusterID, minsAgo(4), "nexara", "pve_acl", "/vms/100", "updated", map[string]any{"users": "alice@pve, bob@pve"})
	audit(historyClusterID, minsAgo(4), "nexara", "pve_acl", "/", "updated", map[string]any{"users": "jbob@pve, carol@pve"})
	// VM 105's HA task, its plain start, and its HA resource, which names it
	// only by its sid and has no task.
	audit(historyClusterID, minsAgo(3), "proxmox", "proxmox_task", "105", "hastart", map[string]any{"upid": historyUPID(32, "hastart", "105"), "status": "OK"})
	audit(historyClusterID, minsAgo(3), "proxmox", "vm", "105", "start", map[string]any{"upid": historyUPID(33, "qmstart", "105"), "status": "OK"})
	audit(historyClusterID, minsAgo(2), "nexara", "ha_resource", "vm:105", "updated", map[string]any{"state": "started", "vmid": 105})
	for _, tt := range []struct {
		name string
		sel  handlers.HistorySelection
		want []string
	}{
		{"a details key within one resource id", handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "pbs_server", ResourceID: "srv-a", DetailKey: "datastore", Value: "datastore01"}}},
			[]string{"audit datastore_update srv-a"}},
		{"a details key holding an integer", handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "ceph_osd", DetailKey: "osd_id", Value: "12"}}},
			[]string{"audit osd_restart osd.12"}},
		{"a spelling of the integer is another value", handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "ceph_osd", DetailKey: "osd_id", Value: "012"}}},
			[]string{}},
		{"one element of a comma list", handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "pve_acl", DetailKey: "users", Value: "bob@pve", InList: true}}},
			[]string{"audit updated /vms/100"}},
		// The guest's other rows are turned away, and its HA resource row,
		// which the guest arm would have taken, is still the object's.
		{"a guest's tasks of one worker type", handlers.HistorySelection{
			Guests: []handlers.HistoryGuest{{VMID: 105}}, GuestWorkers: []string{"ha%"},
			Objects: []handlers.HistoryObject{{ResourceType: "ha_resource", Value: "vm:105"}}},
			[]string{"audit updated vm:105", "audit hastart 105"}},
		// VM 100 has a row in every guest source — audit by vmid, clone and HA
		// sid, task-only, archived, status, backup — and no ha* task. Its HA
		// row and the clone into it are still the objects'.
		{"the worker filter reaches every guest source", handlers.HistorySelection{
			Guests:       []handlers.HistoryGuest{{VMID: 100, After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: true}},
			GuestWorkers: []string{"ha%"}, Objects: []handlers.HistoryObject{{ResourceType: "ha_resource", Value: "vm:100"},
				{ResourceType: "vm", DetailKey: "new_id", Value: "100"}}},
			[]string{"audit created vm:100", "audit clone " + cloneRow}},
		// The rows of VM 100 an object takes are still held to its cutoff:
		// not the previous guest's create, start and destroy.
		{"an object's rows naming a guest keep its cutoff", handlers.HistorySelection{
			Guests:       []handlers.HistoryGuest{{VMID: 100, After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: true}},
			GuestWorkers: []string{"ha%"}, Objects: []handlers.HistoryObject{{ResourceType: "vm", Value: "100"}}},
			[]string{"audit start 100", "audit shutdown 100", "audit stop 100", "audit migrate 100", "audit start 100"}},
	} {
		rows, err := q.ListHistory(ctx, selection(tt.sel))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		got := make([]string, 0, len(rows))
		for _, r := range rows {
			got = append(got, r.Kind+" "+r.Action+" "+r.ResourceID)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}

	// …unless the kind's objects stand on their own (ObjectsIgnoreGuestCutoff):
	// then the previous guest's create, start and destroy are the object's too.
	standalone := withObjects
	standalone.ObjectsIgnoreGuestCutoff = true
	sp, err := standalone.ListParams(historyClusterID, handlers.HistorySelection{
		Guests:       []handlers.HistoryGuest{{VMID: 100, After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: true}},
		GuestWorkers: []string{"ha%"}, Objects: []handlers.HistoryObject{{ResourceType: "vm", Value: "100"}}})
	if err != nil {
		t.Fatal(err)
	}
	sp.RowLimit = 50
	if got, _ := list(sp); !slices.Contains(got, "audit destroy lifecycle ") || !slices.Contains(got, "audit create lifecycle ") {
		t.Errorf("a kind whose objects ignore the guest's cutoff: %v, want the previous guest's create and destroy", got)
	}

	// ── Every arm reads the route's cluster alone: audit keys are unique only
	// within one. The other cluster holds, for each arm, a row its selector
	// would take but for that — none may come back here, and each must in the
	// other cluster's own History, or the guard reads nothing.
	otherUPID := func(seq int, workerType, id string) string {
		return fmt.Sprintf("UPID:pve-02:%08X:00000001:66000001:%s:%s:root@pam:", seq, workerType, id)
	}
	audit(historyOtherID, minsAgo(5), "nexara", "vm", uuid.NewString(), "clone", map[string]any{"upid": otherUPID(1, "qmclone", "60"), "vmid": 60, "new_id": 100})
	audit(historyOtherID, minsAgo(5), "nexara", "ha_resource", "vm:100", "created", map[string]any{"state": "started"})
	audit(historyOtherID, minsAgo(5), "nexara", "network", "vmbr0", "interface_updated", map[string]any{"node": "pve-01"})
	audit(historyOtherID, minsAgo(5), "nexara", "pbs_server", "srv-a", "datastore_update", map[string]any{"datastore": "datastore01"})
	audit(historyOtherID, minsAgo(5), "nexara", "ceph_osd", "osd.12", "osd_restart", map[string]any{"osd_id": 12})
	audit(historyOtherID, minsAgo(5), "nexara", "pve_acl", "/vms/100", "updated", map[string]any{"users": "bob@pve"})
	exec("other task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, vmid, started_at, source)
		VALUES ($1, $2, $3, 'completed', 'pve-02', 'qmstart', 100, $4, 'proxmox')`, historyOtherID, systemUserID, otherUPID(2, "qmstart", "100"), minsAgo(5))
	exec("other archive", `INSERT INTO task_history_archive (upid, cluster_id, vmid, status, started_at)
		VALUES ($1, $2, 100, 'completed', $3)`, otherUPID(3, "qmigrate", "100"), historyOtherID, minsAgo(5))
	exec("other status", `INSERT INTO guest_status_changes (cluster_id, vmid, guest_type, from_status, to_status, last_seen_at, observed_at, explained)
		VALUES ($1, 100, 'qemu', 'running', 'stopped', $2, $2, false)`, historyOtherID, minsAgo(5))
	exec("other backup", `INSERT INTO guest_backups (cluster_id, upid, vmid, node, source, status, started_at, finished_at, log_start, log_end)
		VALUES ($1, $2, 100, 'pve-02', 'proxmox', 'completed', $3, $3, 1, 2)`, historyOtherID, otherUPID(4, "vzdump", ""), minsAgo(5))
	ownRows := 0
	for _, sel := range []handlers.HistorySelection{
		{Guests: []handlers.HistoryGuest{{VMID: 100}}},
		{Objects: []handlers.HistoryObject{{ResourceType: "node", Value: "pve-01"},
			{ResourceType: "network", DetailKey: "node", Value: "pve-01"},
			{ResourceType: "pbs_server", ResourceID: "srv-a", DetailKey: "datastore", Value: "datastore01"},
			{ResourceType: "ceph_osd", DetailKey: "osd_id", Value: "12"},
			{ResourceType: "pve_acl", DetailKey: "users", Value: "bob@pve", InList: true}}},
	} {
		for _, cluster := range []uuid.UUID{historyClusterID, historyOtherID} {
			p, err := withObjects.ListParams(cluster, sel)
			if err != nil {
				t.Fatal(err)
			}
			p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 200, true, []string{"qemu"}
			rows, err := q.ListHistory(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if uuid.UUID(r.ClusterID.Bytes) != cluster {
					t.Errorf("cluster %s's History holds %s %s %s of cluster %s", cluster, r.Kind, r.Action, r.ResourceID, uuid.UUID(r.ClusterID.Bytes))
				}
				if cluster == historyOtherID {
					ownRows++
				}
			}
		}
	}
	// The vmid arm's start, the clone, HA, task, archive, status and backup;
	// the node's reboot and the four other object shapes.
	if ownRows != 12 {
		t.Errorf("the other cluster's own History holds %d of its 12 rows", ownRows)
	}

	// ── A global kind's objects are read in every cluster, and in none.
	audit(uuid.Nil, minsAgo(5), "nexara", "firewall_template", historyTemplateID, "template_created", map[string]any{})
	audit(historyOtherID, minsAgo(4), "nexara", "firewall_template", historyTemplateID, "template_applied", map[string]any{})
	audit(historyClusterID, minsAgo(3), "nexara", "firewall_template", historyTemplateID, "template_applied", map[string]any{})
	template := handlers.HistoryKind{Name: "template", Categories: []string{"other"}, Default: "other", Global: true,
		ObjectTypes: []string{"firewall_template"}}
	tp, err := template.ListParams(uuid.Nil, handlers.HistorySelection{Objects: []handlers.HistoryObject{
		{ResourceType: "firewall_template", Value: historyTemplateID}}})
	if err != nil {
		t.Fatal(err)
	}
	tp.RowLimit = 50
	templateRows, err := q.ListHistory(ctx, tp)
	if err != nil {
		t.Fatal(err)
	}
	clusters := make([]string, 0, len(templateRows))
	for _, r := range templateRows {
		clusters = append(clusters, r.Action+" "+map[bool]string{true: uuid.UUID(r.ClusterID.Bytes).String(), false: "none"}[r.ClusterID.Valid])
	}
	if want := []string{"template_applied " + historyClusterID.String(), "template_applied " + historyOtherID.String(),
		"template_created none"}; !slices.Equal(clusters, want) {
		t.Errorf("a global kind's History = %v, want %v", clusters, want)
	}
}

func withFilter(p gen.ListHistoryParams, f func(*gen.ListHistoryParams)) gen.ListHistoryParams {
	f(&p)
	return p
}
