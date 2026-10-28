package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed so a run that dies before its cleanup leaves rows the
// next run deletes instead of accumulating them.
var (
	historyClusterID = uuid.MustParse("115a0000-0000-4000-8000-00000000000a")
	historyOtherID   = uuid.MustParse("115a0000-0000-4000-8000-00000000000b")
	historyThirdID   = uuid.MustParse("115a0000-0000-4000-8000-00000000000c")
	historyUserID    = uuid.MustParse("115a0000-0000-4000-8000-000000000001")
	historyNodeID    = uuid.MustParse("115a0000-0000-4000-8000-0000000000d1")
	// A firewall template's audit rows outlive any cluster: no cluster_id.
	historyTemplateID = "115a0000-0000-4000-8000-0000000000f1"
	// A PBS server's changes seen in Proxmox name no cluster either (000125).
	historyPBSID = "115a0000-0000-4000-8000-0000000000f2"
	systemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
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
	// ListHistory as the handler runs it: planned for each call's own arguments.
	hq := gen.New(handlers.HistoryDBTX(pool))

	// Deferred rather than t.Cleanup: see setupMigration.
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Every fixture table cascades from clusters except audit_log, whose entries
		// outlive a removed cluster (000122), so they go first, by cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, []uuid.UUID{historyClusterID, historyOtherID, historyThirdID})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{historyClusterID, historyOtherID, historyThirdID})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, historyUserID)
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = 'firewall_template' AND resource_id = $1`, historyTemplateID)
		_, _ = pool.Exec(pctx, `DELETE FROM cluster_object_changes WHERE resource_type = 'pbs_server' AND resource_id = $1`, historyPBSID)
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

	// The helpers report to the subtest calling them: the subtests run in order,
	// one at a time, over one fixture, each kind's after the ones it builds on.
	cur := t
	exec := func(what, sql string, args ...any) {
		cur.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			cur.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, 'guest-history-test@nexara.test', '')`, historyUserID)
	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{{historyClusterID, "cluster01"}, {historyOtherID, "cluster02"}, {historyThirdID, "cluster03"}} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://invalid.local', 'tok', 'enc')`, c.id, c.name)
	}
	exec("node", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`, historyNodeID, historyClusterID)

	now := time.Now().UTC().Truncate(time.Second)
	hoursAgo := func(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }
	minsAgo := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }

	audit := func(cluster uuid.UUID, at time.Time, source, resourceType, resourceID, action string, details map[string]any) {
		cur.Helper()
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
			cur.Fatalf("seed audit %s: %v", action, err)
		}
	}
	task := func(upid, status string, vmid any, started time.Time, finished any) {
		cur.Helper()
		exec("task "+upid, `INSERT INTO task_history (cluster_id, user_id, upid, status, exit_status, node,
				task_type, vmid, started_at, finished_at, source)
			VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'completed' THEN 'OK' WHEN $4 = 'failed' THEN 'command failed' ELSE '' END,
				'pve-01', split_part($3, ':', 6), $5, $6, $7, 'proxmox')`,
			historyClusterID, systemUserID, upid, status, vmid, started, finished)
	}

	// The timeline, through the guest kind's own rules and selection as the
	// handler builds them (HistoryKind.ListParams): VM 100 from its clone.
	guest := func(inclusive bool, guests ...handlers.HistoryGuest) gen.ListHistoryParams {
		cur.Helper()
		if len(guests) == 0 {
			guests = []handlers.HistoryGuest{{VMID: 100,
				After: pgtype.Timestamptz{Time: hoursAgo(7), Valid: true}, Inclusive: inclusive}}
		}
		p, err := handlers.GuestHistory().ListParams(historyClusterID, handlers.HistorySelection{Guests: guests})
		if err != nil {
			cur.Fatal(err)
		}
		p.RowLimit = 50
		return p
	}
	// The guest kind, reading the objects these cases name beside its guests.
	withObjects := handlers.GuestHistory()
	withObjects.ObjectTypes = []string{"vm", "ha_resource", "pbs_server", "ceph_osd", "pve_acl", "node", "network"}
	selection := func(sel handlers.HistorySelection) gen.ListHistoryParams {
		cur.Helper()
		p, err := withObjects.ListParams(historyClusterID, sel)
		if err != nil {
			cur.Fatal(err)
		}
		p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 50, true, []string{"qemu"}
		return p
	}
	list := func(p gen.ListHistoryParams) ([]string, int64) {
		cur.Helper()
		rows, err := hq.ListHistory(ctx, p)
		if err != nil {
			cur.Fatalf("list: %v", err)
		}
		got := make([]string, 0, len(rows))
		var total int64
		for _, r := range rows {
			if r.EntryID == "" {
				continue
			}
			got = append(got, r.Kind+" "+r.Action+" "+r.Category+" "+r.TaskStatus)
			total = r.Total
		}
		return got, total
	}
	// walk reads p capped at rowCap from top, an hour ahead, and on below each
	// searched_from, as Show older does: every page whole for its stretch, so
	// no entry on two pages. Meanwhile two rows of each source the walks read
	// lie above top: none may be listed, and an arm without its until bound
	// would count them toward its cap and stop at them.
	top := now.Add(time.Hour)
	walk := func(p gen.ListHistoryParams, rowCap int32, entry func(gen.ListHistoryRow) string) []string {
		cur.Helper()
		for i, at := range []time.Time{top.Add(time.Minute), top.Add(2 * time.Minute)} {
			audit(historyClusterID, at, "nexara", "vm", uuid.NewString(), "clone", map[string]any{"vmid": 50, "new_id": 100})
			audit(historyClusterID, at, "nexara", "ha_resource", "vm:100", "updated", map[string]any{})
			exec("above: a status change", `INSERT INTO guest_status_changes (cluster_id, vmid, guest_type, node,
				from_status, to_status, last_seen_at, observed_at, explained)
			VALUES ($1, 100, 'qemu', 'pve-01', 'running', 'stopped', $2, $2, false)`, historyClusterID, at)
			for _, cluster := range []uuid.UUID{historyClusterID, historyThirdID} {
				exec("above: a backup", `INSERT INTO guest_backups (cluster_id, upid, vmid, node, source, status,
					started_at, finished_at, log_start, log_end)
				VALUES ($1, $2, 100, 'pve-01', 'proxmox', 'completed', $3, $3, 0, 0)`,
					cluster, historyUPID(90+i, "vzdump", ""), at)
			}
			exec("above: an archived task", `INSERT INTO task_history_archive (upid, cluster_id, vmid, status, node,
				task_type, source, started_at)
			VALUES ($1, $2, 310, 'completed', 'pve-01', 'qmstart', 'proxmox', $3)`,
				historyUPID(92+i, "qmstart", "310"), historyThirdID, at)
			exec("above: a run of status changes", `INSERT INTO node_status_changes (cluster_id, node, from_status,
				to_status, last_seen_at, observed_at, run_count)
			VALUES ($1, 'pve-01', 'online', 'offline', $2, $2, 3)`, historyThirdID, at)
			exec("above: a run of moves", `INSERT INTO guest_node_changes (cluster_id, vmid, guest_type, from_node,
				to_node, last_seen_at, observed_at, run_count, nodes)
			VALUES ($1, 330, 'qemu', 'pve-01', 'pve-02', $2, $2, 2, '{pve-01,pve-02}')`, historyThirdID, at)
			// Changes seen in Proxmox: the guest's, the node's and an
			// interface it names, and the third cluster's pool.
			for _, o := range []struct {
				cluster       uuid.UUID
				typ, id, node string
				vmid          any
			}{
				{historyClusterID, "vm", "100", "pve-01", 100}, {historyThirdID, "node", "pve-01", "pve-01", nil},
				{historyThirdID, "network", "pve-01/vmbr7", "pve-01", nil}, {historyThirdID, "pool", "pool9", "", nil},
			} {
				exec("above: a change seen", `INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id,
					vmid, change, fields, node, observed_by, last_seen_at, observed_at, explained)
				VALUES ($1, $2, $3, $4, 'changed', '{x}', $5, 'observer', $6, $6, false)`, o.cluster, o.typ, o.id, o.vmid,
					o.node, at)
			}
		}
		defer func() {
			for _, dated := range []string{"audit_log.created_at", "guest_status_changes.observed_at",
				"guest_backups.started_at", "task_history_archive.started_at", "node_status_changes.observed_at",
				"guest_node_changes.observed_at", "cluster_object_changes.observed_at"} {
				table, column, _ := strings.Cut(dated, ".")
				exec("above: gone", "DELETE FROM "+table+" WHERE "+column+" > $1", top)
			}
		}()
		p.RowCap, p.RowLimit, p.Until = rowCap, 200, pgtype.Timestamptz{Time: top, Valid: true}
		p.ObservedTypes = []string{"vm", "container", "node", "network", "pool"}
		var got []string
		seen := map[string]bool{}
		for range 50 {
			rows, err := hq.ListHistory(ctx, p)
			if err != nil {
				cur.Fatalf("walk: %v", err)
			}
			var next pgtype.Timestamptz
			for _, r := range rows {
				if r.SearchedFrom.Valid {
					next = r.SearchedFrom
				}
				if r.EntryID == "" {
					continue
				}
				if seen[r.EntryID] {
					cur.Errorf("entry %s on two pages", r.EntryID)
				}
				seen[r.EntryID] = true
				got = append(got, entry(r))
			}
			if !next.Valid {
				return got
			}
			p.Until = next
		}
		cur.Fatal("walk: still paging after 50 pages")
		return nil
	}
	listed := func(r gen.ListHistoryRow) string {
		return r.Kind + " " + r.Action + " " + r.Category + " " + r.TaskStatus
	}
	// Read by a later kind: the guest's clone row, the third cluster's node.
	var cloneRow string
	var thirdNode uuid.UUID
	run := func(name string, f func(t *testing.T)) {
		t.Run(name, func(t *testing.T) {
			cur = t
			f(t)
		})
	}

	run("guest", func(t *testing.T) {
		// ── The previous guest to hold VMID 100: before the cutoff, so absent —
		// and an older holder's clone into the VMID and HA resource.
		audit(historyClusterID, hoursAgo(11), "nexara", "vm", uuid.NewString(), "clone", map[string]any{"upid": historyUPID(36, "qmclone", "60"), "vmid": 60, "new_id": 100})
		audit(historyClusterID, hoursAgo(10.5), "nexara", "ha_resource", "vm:100", "created", map[string]any{"state": "started"})
		audit(historyClusterID, hoursAgo(10), "proxmox", "vm", "100", "create", map[string]any{"upid": historyUPID(1, "qmcreate", "100"), "status": "OK"})
		audit(historyClusterID, hoursAgo(9), "proxmox", "vm", "100", "start", map[string]any{"upid": historyUPID(2, "qmstart", "100"), "status": "OK"})
		audit(historyClusterID, hoursAgo(8), "proxmox", "vm", "100", "destroy", map[string]any{"upid": historyUPID(3, "qmdestroy", "100"), "status": "OK"})

		// ── This guest, VMID 100, cloned from 50 seven hours ago: the birth.
		cloneUPID := historyUPID(4, "qmclone", "50")
		cloneRow = uuid.NewString()
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
			ClusterID: pgtype.UUID{Bytes: historyClusterID, Valid: true}, Vmids: []int32{100},
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
			ClusterID: pgtype.UUID{Bytes: historyClusterID, Valid: true}, Vmids: []int32{150},
		}); err != nil || len(created) != 1 || created[0].TaskStatus != "completed" {
			t.Errorf("an archived create under a planted verdict: %+v (%v), want the archive's completed", created, err)
		}
		// …and so is the outcome the timeline shows.
		p150, err := handlers.GuestHistory().ListParams(historyClusterID, handlers.HistorySelection{
			Guests: []handlers.HistoryGuest{{VMID: 150}}})
		if err != nil {
			t.Fatal(err)
		}
		p150.RowLimit = 50
		if rows, err := hq.ListHistory(ctx, p150); err != nil || len(rows) != 1 || rows[0].TaskStatus != "completed" {
			t.Errorf("the timeline's outcome of an archived create under a planted verdict: %+v (%v), want completed", rows, err)
		}
		// POST /api/v1/tasks refuses a UPID on record: in an audit row or the
		// archive, of any cluster — another cluster's included, whose node names
		// a node of this one too.
		for upid, want := range map[string]bool{archivedCreate: true, historyUPID(45, "qmcreate", "151"): false,
			historyUPID(10, "qmstart", "100"): true} {
			if got, err := q.TaskUPIDOnRecord(ctx, upid); err != nil || got != want {
				t.Errorf("TaskUPIDOnRecord(%s) = %v (%v), want %v", upid, got, err, want)
			}
		}
		if want := []string{"birth", "destroy OK", "birth", "birth"}; !slices.Equal(shapes, want) {
			t.Fatalf("lifecycle events = %v, want %v (the clone, the old guest's destroy, its create, an older clone)", shapes, want)
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
		// Capped at 2 and paged on below each point a page stopped, it is the
		// whole timeline, each entry once and in order.
		if got := walk(full, 2, listed); !slices.Equal(got, want) {
			t.Errorf("capped at 2, page on page:\n\t%v\nwant\n\t%v", got, want)
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
		backups, err := hq.ListHistory(ctx, withFilter(full, func(p *gen.ListHistoryParams) {
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
			{"categories left out", withFilter(full, func(p *gen.ListHistoryParams) { p.ExcludeCategories = []string{"power", "ha"} }), 6, ""},
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
			rows, err := hq.ListHistory(ctx, tt.p)
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
	})

	run("objects", func(t *testing.T) {
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
			ObjectTypes: []string{"node", "network", "vm", "container"},
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

		// A Proxmox row is its Nexara twin's only while the twin is on the page:
		// a type or a source the Nexara row is not keeps the Proxmox row.
		refreshUPID := historyUPID(31, "aptupdate", "")
		audit(historyClusterID, minsAgo(4), "nexara", "node", "pve-03", "apt_refresh", map[string]any{"upid": refreshUPID})
		audit(historyClusterID, minsAgo(4), "proxmox", "node", "pve-03", "apt_update", map[string]any{"upid": refreshUPID, "status": "OK"})
		for _, tt := range []struct {
			name string
			f    func(*gen.ListHistoryParams)
			want []string
		}{
			{"unfiltered", func(*gen.ListHistoryParams) {}, []string{"audit apt_refresh tasks "}},
			{"its type", func(p *gen.ListHistoryParams) { p.Category = pgtype.Text{String: "updates", Valid: true} },
				[]string{"audit apt_update updates "}},
			{"its source", func(p *gen.ListHistoryParams) { p.Source = pgtype.Text{String: "proxmox", Valid: true} },
				[]string{"audit apt_update updates "}},
		} {
			p, err := node.ListParams(historyClusterID, handlers.HistorySelection{Objects: []handlers.HistoryObject{
				{ResourceType: "node", Value: "pve-03"}}})
			if err != nil {
				t.Fatal(err)
			}
			p.RowLimit = 50
			tt.f(&p)
			if got, _ := list(p); !slices.Equal(got, tt.want) {
				t.Errorf("a twin, %s: %q, want %q", tt.name, got, tt.want)
			}
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
		twinParams.RowLimit, twinParams.StatusGuestTypes = 50, []string{"qemu", "lxc"}
		if got, _ := list(twinParams); !slices.Equal(got, []string{"audit start other "}) {
			t.Errorf("a node's guest task whose Nexara twin is the guest's: %v, want the Proxmox row kept", got)
		}

		// An object's row naming a guest outside the guests is shown under that
		// guest's own view permission: container 108's with view:container; VM
		// 106's, a guest the inventory does not hold, only with both.
		if _, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: historyClusterID, NodeID: historyNodeID,
			Vmid: 108, Name: "linux08", Type: "lxc", Status: "running"}); err != nil {
			t.Fatal(err)
		}
		audit(historyClusterID, minsAgo(0.5), "proxmox", "container", "108", "start",
			map[string]any{"upid": historyUPID(35, "vzstart", "108"), "node": "pve-01", "status": "OK"})
		for _, tt := range []struct {
			types []string
			want  []string
		}{
			{[]string{"qemu", "lxc"}, []string{"audit start 108", "audit start 106"}},
			{[]string{"lxc"}, []string{"audit start 108"}},
			{[]string{"qemu"}, []string{}},
		} {
			p, err := node.ListParams(historyClusterID, handlers.HistorySelection{Objects: []handlers.HistoryObject{
				{ResourceType: "vm", DetailKey: "node", Value: "pve-01"}, {ResourceType: "container", DetailKey: "node", Value: "pve-01"}}})
			if err != nil {
				t.Fatal(err)
			}
			p.RowLimit, p.StatusGuestTypes = 50, tt.types
			rows, err := hq.ListHistory(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			for _, r := range rows {
				got = append(got, r.Kind+" "+r.Action+" "+r.ResourceID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("a node's guest rows with view of %v: %v, want %v", tt.types, got, tt.want)
			}
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
		// A tab's whole type: every interface of the cluster, not the other's.
		audit(historyClusterID, minsAgo(1.5), "nexara", "network", "pve-02/vmbr9", "interface_created", map[string]any{"node": "pve-02"})
		audit(historyOtherID, minsAgo(1.5), "nexara", "network", "pve-02/vmbr9", "interface_created", map[string]any{"node": "pve-02"})
		for _, tt := range []struct {
			name string
			sel  handlers.HistorySelection
			want []string
		}{
			{"every row of a type", handlers.HistorySelection{Objects: []handlers.HistoryObject{
				{ResourceType: "network", WholeType: true}}},
				[]string{"audit interface_created pve-02/vmbr9", "audit interface_updated vmbr0"}},
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
			rows, err := hq.ListHistory(ctx, selection(tt.sel))
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

		// Under the handler's exec mode each value arrives as it is: commas,
		// quotes, braces and backslashes in an object's id, a details value and
		// a rule's pattern, and an id spelled NULL — none split, unquoted or
		// taken for a missing value, and the prefix of the odd id not matched.
		odd, oddAction := `/a,b"c{d}\e`, `set,"{x}\`
		audit(historyClusterID, minsAgo(4), "nexara", "pve_acl", odd, oddAction, map[string]any{"path": odd})
		audit(historyClusterID, minsAgo(3), "nexara", "pve_acl", "NULL", "updated", map[string]any{})
		audit(historyClusterID, minsAgo(2), "nexara", "pve_acl", "/a", "updated", map[string]any{"path": "/a"})
		oddKind := withObjects
		oddKind.Rules = append([]handlers.HistoryRule{
			{Category: "odd", Field: handlers.HistoryFieldAction, Equals: []string{oddAction}},
			{Category: "odd", Field: handlers.HistoryFieldResourceType, Like: []string{`{pve\_acl},%`}},
		}, withObjects.Rules...)
		if p, err := oddKind.ListParams(historyClusterID, handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "pve_acl", Value: odd}, {ResourceType: "pve_acl", Value: "NULL"},
			{ResourceType: "pve_acl", DetailKey: "path", Value: odd}}}); err != nil {
			t.Fatal(err)
		} else {
			p.RowLimit = 50
			if got, _ := list(p); !slices.Equal(got, []string{"audit updated other ", "audit " + oddAction + " odd "}) {
				t.Errorf("odd values: %q", got)
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
	})

	run("folder", func(t *testing.T) {
		// ── A folder's History, through its own selector: its guests and its
		// subfolders' (VM 120 in F, VM 121 in S below it), each from its own
		// cutoff, and the folders' rows — the guests and subfolders that left
		// (VM 123 moved to G, VM 124 unfiled, T moved to G, U deleted). Never G's
		// own, nor VM 122 there.
		folderOf := map[string]string{}
		mkFolder := func(cluster uuid.UUID, name string, parent *gen.VmFolder) gen.VmFolder {
			t.Helper()
			p := gen.CreateVMFolderParams{ClusterID: cluster, Name: name}
			if parent != nil {
				p.ParentID = pgtype.UUID{Bytes: parent.ID, Valid: true}
			}
			f, err := q.CreateVMFolder(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			folderOf[f.ID.String()] = name
			return f
		}
		fF := mkFolder(historyClusterID, "F", nil)
		fS, fT, fU, fG := mkFolder(historyClusterID, "S", &fF), mkFolder(historyClusterID, "T", &fF),
			mkFolder(historyClusterID, "U", &fF), mkFolder(historyClusterID, "G", nil)
		guestRow := map[int32]string{}
		for vmid, typ := range map[int32]string{120: "qemu", 121: "qemu", 122: "qemu", 123: "qemu", 124: "lxc"} {
			v, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: historyClusterID, NodeID: historyNodeID,
				Vmid: vmid, Name: fmt.Sprintf("linux%d", vmid), Type: typ, Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
			guestRow[vmid] = v.ID.String()
		}
		// Filed as AssignVM files a guest: the folder it left comes back, for its
		// audit row (from_folder_id).
		file := func(at float64, vmid int32, folder *gen.VmFolder, wantFrom string) {
			t.Helper()
			details := map[string]any{"vmid": vmid, "folder_id": nil}
			action, from := "unassign", ""
			if folder != nil {
				action, details["folder_id"] = "assign", folder.ID.String()
				var err error
				if from, err = q.AssignVMToFolder(ctx, gen.AssignVMToFolderParams{ClusterID: historyClusterID,
					Vmid: vmid, FolderID: folder.ID}); err != nil {
					t.Fatal(err)
				}
			} else if left, err := q.UnassignVMFromFolder(ctx, gen.UnassignVMFromFolderParams{ClusterID: historyClusterID,
				Vmid: vmid}); err == nil {
				from = left.String()
			}
			if from != wantFrom {
				t.Errorf("filing VM %d: the folder it left = %q, want %q", vmid, folderOf[from], folderOf[wantFrom])
			}
			if from != "" {
				details["from_folder_id"] = from
			}
			audit(historyClusterID, minsAgo(at), "nexara", "vm_folder_membership", guestRow[vmid], action, details)
		}
		folderRow := func(at float64, f gen.VmFolder, action string, details map[string]any) {
			audit(historyClusterID, minsAgo(at), "nexara", "vm_folder", f.ID.String(), action, details)
		}
		folderRow(60, fF, "create", map[string]any{"name": "F", "parent_id": nil})
		folderRow(59, fS, "create", map[string]any{"name": "S", "parent_id": fF.ID})
		file(58, 120, &fF, "")
		audit(historyClusterID, minsAgo(58.5), "proxmox", "vm", "121", "destroy", // the previous VM 121: before its cutoff
			map[string]any{"upid": historyUPID(37, "qmdestroy", "121"), "status": "OK"})
		audit(historyClusterID, minsAgo(57.5), "proxmox", "vm", "121", "create",
			map[string]any{"upid": historyUPID(38, "qmcreate", "121"), "status": "OK"})
		file(57, 121, &fS, "")
		file(56, 123, &fF, "")
		file(55, 124, &fF, "")
		folderRow(54, fT, "create", map[string]any{"name": "T", "parent_id": fF.ID})
		folderRow(53, fG, "create", map[string]any{"name": "G", "parent_id": nil})
		file(52, 122, &fG, "")
		folderRow(51, fF, "update", map[string]any{"name": "F"})
		file(50, 123, &fG, fF.ID.String())
		file(49, 124, nil, fF.ID.String())
		folderRow(48, fT, "update", map[string]any{"parent_id": fG.ID, "from_parent_id": fF.ID})
		exec("T moved", `UPDATE vm_folders SET parent_id = $1 WHERE id = $2`, fG.ID, fT.ID)
		folderRow(47, fU, "delete", map[string]any{"name": "U", "parent_id": fF.ID})
		exec("U deleted", `DELETE FROM vm_folders WHERE id = $1`, fU.ID)
		audit(historyClusterID, minsAgo(46), "proxmox", "vm", "120", "start", map[string]any{"upid": historyUPID(39, "qmstart", "120"), "status": "OK"})
		audit(historyClusterID, minsAgo(45), "proxmox", "vm", "122", "start", map[string]any{"upid": historyUPID(40, "qmstart", "122"), "status": "OK"})

		folderProps := apischema.Properties{"folder_id": {Type: apischema.String}}
		if err := folderProps.Compile(); err != nil {
			t.Fatal(err)
		}
		selectFolder := func(cluster uuid.UUID, folder string, types ...string) (handlers.HistorySelection, error) {
			params, err := folderProps.Validate(map[string]any{"folder_id": folder})
			if err != nil {
				t.Fatal(err)
			}
			if len(types) == 0 {
				types = []string{"qemu", "lxc"}
			}
			return handlers.FolderHistory().Select(ctx, handlers.HistoryRequest{Queries: q, ClusterID: cluster, Params: params,
				GuestTypes: types})
		}
		folderEntry := func(r gen.ListHistoryRow) string {
			name := r.ResourceID
			switch r.ResourceType {
			case "vm_folder":
				name = folderOf[r.ResourceID]
			case "vm_folder_membership":
				name = fmt.Sprint(r.Vmid)
			}
			return r.Action + " " + name
		}
		folderParams := func(sel handlers.HistorySelection, types []string) gen.ListHistoryParams {
			t.Helper()
			p, err := handlers.FolderHistory().ListParams(historyClusterID, sel)
			if err != nil {
				t.Fatal(err)
			}
			p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 50, true, types
			return p
		}
		folderHistory := func(sel handlers.HistorySelection, types []string, rowCap int32) []string {
			t.Helper()
			p := folderParams(sel, types)
			p.RowCap = rowCap
			rows, err := hq.ListHistory(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			for _, r := range rows {
				if r.EntryID != "" {
					got = append(got, folderEntry(r))
				}
			}
			return got
		}
		sel, err := selectFolder(historyClusterID, fF.ID.String())
		if err != nil {
			t.Fatal(err)
		}
		if got, want := folderHistory(sel, []string{"qemu", "lxc"}, 0), []string{"start 120", "delete U", "update T",
			"unassign 124", "assign 123", "update F", "create T", "assign 124", "assign 123", "assign 121", "create 121",
			"assign 120", "create S", "create F"}; !slices.Equal(got, want) {
			t.Errorf("folder F's History =\n\t%v\nwant\n\t%v", got, want)
		}
		// Capped at 2, it lists what it is whole for: the guests' rows back to
		// 57 minutes, the folders' to 48 — the start, the delete. Paged on, it is
		// the whole History, each entry once.
		if got, want := folderHistory(sel, []string{"qemu", "lxc"}, 2), []string{"start 120", "delete U"}; !slices.Equal(got, want) {
			t.Errorf("folder F's History capped at 2 = %v, want %v", got, want)
		}
		if got, want := walk(folderParams(sel, []string{"qemu", "lxc"}), 2, folderEntry), folderHistory(sel,
			[]string{"qemu", "lxc"}, 0); !slices.Equal(got, want) {
			t.Errorf("folder F's History capped at 2, page on page:\n\t%v\nwant\n\t%v", got, want)
		}
		// Narrowed to Nexara's, the guests' rows are still read from their
		// floor — where their newest row cap begins, a Proxmox start 46 minutes
		// back — and the read is whole only after it: none listed.
		{
			p := folderParams(sel, []string{"qemu", "lxc"})
			p.RowCap, p.Source = 1, pgtype.Text{String: "nexara", Valid: true}
			rows, err := hq.ListHistory(ctx, p)
			if err != nil || len(rows) != 1 || rows[0].EntryID != "" ||
				!rows[0].SearchedFrom.Time.Equal(minsAgo(46).Add(time.Microsecond)) {
				t.Errorf("folder F's Nexara rows capped at 1: %+v (%v), want none, whole after 46 minutes", rows, err)
			}
		}
		// Without view:vm its VMs are dropped (viewableGuests), and so is every
		// row naming one of them that the folder's own rows would show: what is
		// left is the folders' and container 124's.
		sel.Guests = nil
		if got, want := folderHistory(sel, []string{"lxc"}, 0), []string{"delete U", "update T", "unassign 124",
			"update F", "create T", "assign 124", "create S", "create F"}; !slices.Equal(got, want) {
			t.Errorf("folder F's History without view:vm =\n\t%v\nwant\n\t%v", got, want)
		}
		// The guests in no folder; a folder of the other cluster is not this one's.
		if unfiled, err := selectFolder(historyClusterID, handlers.UnfiledFolderSegment); err != nil {
			t.Fatal(err)
		} else {
			vmids := make([]int32, 0, len(unfiled.Guests))
			for _, g := range unfiled.Guests {
				vmids = append(vmids, g.VMID)
			}
			slices.Sort(vmids)
			if want := []int32{100, 108, 124}; !slices.Equal(vmids, want) || len(unfiled.Objects) != 0 {
				t.Errorf("the unfiled guests = %v and %d objects, want %v and none", vmids, len(unfiled.Objects), want)
			}
		}
		// A folder of more than 500 guests follows the 500 with the latest audit
		// rows, the lower VMIDs among those with none, and says of how many:
		// VMID 1500's start makes it one, and leaves 1499 out.
		big := mkFolder(historyClusterID, "B", nil)
		exec("big folder", `INSERT INTO vm_folder_memberships (cluster_id, vmid, folder_id)
			SELECT $1, g, $2 FROM generate_series(1000, 1500) g`, historyClusterID, big.ID)
		audit(historyClusterID, minsAgo(1), "nexara", "vm", "1500", "start", map[string]any{})
		capped, err := selectFolder(historyClusterID, big.ID.String())
		if err != nil {
			t.Fatal(err)
		}
		followed := make([]int32, 0, len(capped.Guests))
		for _, g := range capped.Guests {
			followed = append(followed, g.VMID)
		}
		if len(followed) != 500 || followed[0] != 1500 || slices.Contains(followed, 1499) || capped.GuestsTotal != 501 {
			t.Errorf("a folder of 501 guests follows %d of %d, first %v; want 500 of 501, first 1500, not 1499",
				len(followed), capped.GuestsTotal, followed[:min(len(followed), 3)])
		}
		// Only the guests the caller may open count: none of these, which the
		// inventory does not hold, for a caller with one of the two types.
		if mine, err := selectFolder(historyClusterID, big.ID.String(), "lxc"); err != nil || len(mine.Guests)+mine.GuestsTotal != 0 {
			t.Errorf("the folder of 501 for a container viewer: %d guests of %d (%v), want none", len(mine.Guests), mine.GuestsTotal, err)
		}
		exec("its start", `DELETE FROM audit_log WHERE cluster_id = $1 AND vmid = 1500`, historyClusterID)
		elsewhere := mkFolder(historyOtherID, "F", nil)
		var notFound *fiber.Error
		if _, err := selectFolder(historyClusterID, elsewhere.ID.String()); !errors.As(err, &notFound) || notFound.Code != fiber.StatusNotFound {
			t.Errorf("another cluster's folder: %v, want a 404", err)
		}
	})

	run("scope", func(t *testing.T) {
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
		for _, o := range []struct {
			typ, id string
			vmid    any
		}{{"vm", "100", 100}, {"node", "pve-01", nil}} {
			exec("other change seen", `INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, vmid,
				change, observed_by, last_seen_at, observed_at, explained)
			VALUES ($1, $2, $3, $4, 'changed', 'observer', $5, $5, false)`, historyOtherID, o.typ, o.id, o.vmid, minsAgo(5))
		}
		ownRows := 0
		for _, sel := range []handlers.HistorySelection{
			{Guests: []handlers.HistoryGuest{{VMID: 100}}},
			{Objects: []handlers.HistoryObject{{ResourceType: "node", Value: "pve-01"},
				{ResourceType: "network", DetailKey: "node", Value: "pve-01"},
				{ResourceType: "pbs_server", ResourceID: "srv-a", DetailKey: "datastore", Value: "datastore01"},
				{ResourceType: "ceph_osd", DetailKey: "osd_id", Value: "12"},
				{ResourceType: "pve_acl", DetailKey: "users", Value: "bob@pve", InList: true},
				{ResourceType: "ceph_osd", WholeType: true}}},
		} {
			for _, cluster := range []uuid.UUID{historyClusterID, historyOtherID} {
				p, err := withObjects.ListParams(cluster, sel)
				if err != nil {
					t.Fatal(err)
				}
				p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 200, true, []string{"qemu"}
				p.ObservedTypes = []string{"vm", "node"}
				rows, err := hq.ListHistory(ctx, p)
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
		// The vmid arm's start, the clone, HA, task, archive, status, backup and
		// change seen; the node's reboot, its change seen and the four other
		// object shapes.
		if ownRows != 14 {
			t.Errorf("the other cluster's own History holds %d of its 14 rows", ownRows)
		}
	})

	run("cluster", func(t *testing.T) {
		// ── A cluster's History: every row of every source filed under it — the
		// third cluster's, so the other two's many rows must all stay out — its
		// sync failures as runs, split where they paused, the last one's error
		// their details.
		thirdNode = uuid.New()
		exec("third node", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`, thirdNode, historyThirdID)
		if _, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: historyThirdID, NodeID: thirdNode,
			Vmid: 300, Name: "linux30", Type: "qemu", Status: "running"}); err != nil {
			t.Fatal(err)
		}
		for i, m := range []float64{100, 95, 90, 40, 35} {
			audit(historyThirdID, minsAgo(m), "nexara", "cluster", historyThirdID.String(), "sync_failed",
				map[string]any{"error": fmt.Sprintf("refused %d", i)})
		}
		audit(historyThirdID, minsAgo(60), "nexara", "node", "pve-01", "apt_update", map[string]any{})
		audit(historyThirdID, minsAgo(50), "proxmox", "vm", "300", "start", map[string]any{"upid": historyUPID(41, "qmstart", "300"), "status": "OK"})
		// Tasks: the start's, which its audit row is; one with no audit row.
		for _, tk := range []struct {
			upid, typ string
			at        float64
		}{{historyUPID(41, "qmstart", "300"), "qmstart", 50}, {historyUPID(42, "cephcreatemgr", "pve-01"), "cephcreatemgr", 30}} {
			exec("third task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at, source)
			VALUES ($1, $2, $3, 'completed', 'pve-01', $4, $5, 'proxmox')`, historyThirdID, systemUserID, tk.upid, tk.typ, minsAgo(tk.at))
		}
		// VM 300's status change, and one of the container that held VMID 300
		// before it: each is held to its own type.
		for _, st := range []struct {
			typ string
			at  float64
		}{{"qemu", 20}, {"lxc", 25}} {
			exec("third status", `INSERT INTO guest_status_changes (cluster_id, vmid, guest_type, node, from_status, to_status, last_seen_at, observed_at, explained)
			VALUES ($1, 300, $2, 'pve-01', 'running', 'stopped', $3, $3, false)`, historyThirdID, st.typ, minsAgo(st.at))
		}
		exec("third backup", `INSERT INTO guest_backups (cluster_id, upid, vmid, node, source, status, started_at, finished_at, log_start, log_end)
		VALUES ($1, $2, 300, 'pve-01', 'proxmox', 'completed', $3, $3, 1, 2)`, historyThirdID, historyUPID(43, "vzdump", ""), minsAgo(10))
		// The entries, the total and how far back the read is whole, if not
		// all of it: read from every row, an empty page's lone one too.
		clusterHistory := func(f func(*gen.ListHistoryParams)) ([]string, int64, time.Time) {
			t.Helper()
			p, err := handlers.ClusterHistory().ListParams(historyThirdID, handlers.HistorySelection{})
			if err != nil {
				t.Fatal(err)
			}
			p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 50, true, []string{"qemu", "lxc"}
			f(&p)
			rows, err := hq.ListHistory(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			var total int64
			var searched time.Time
			for _, r := range rows {
				if r.SearchedFrom.Valid {
					searched = r.SearchedFrom.Time
				}
				if r.EntryID == "" {
					continue
				}
				if uuid.UUID(r.ClusterID.Bytes) != historyThirdID {
					t.Errorf("the cluster's History holds %s %s of cluster %s", r.Kind, r.Action, uuid.UUID(r.ClusterID.Bytes))
				}
				entry := r.Kind + " " + r.Action + " " + r.Category
				if r.Kind == "run" {
					var d map[string]any
					_ = json.Unmarshal(r.Details, &d)
					entry += fmt.Sprintf(" %d since %s, %v", r.RunCount, r.LastSeenAt.Time.Format("15:04:05"), d["error"])
				}
				got = append(got, entry)
				total = r.Total
			}
			return got, total, searched
		}
		runSince := func(m float64) string { return minsAgo(m).Format("15:04:05") }
		// searched is how many minutes back a capped read is whole — the latest
		// of its full sources' oldest rows — and 0 for one whole.
		for _, tt := range []struct {
			name      string
			f         func(*gen.ListHistoryParams)
			want      []string
			wantTotal int64
			searched  float64
		}{
			{"everything", func(*gen.ListHistoryParams) {}, []string{"backup vzdump backup", "status status_change guests",
				"status status_change guests", "task cephcreatemgr ceph", "run sync_failed sync 2 since " + runSince(40) + ", refused 4",
				"audit start guests", "audit apt_update nodes", "run sync_failed sync 3 since " + runSince(100) + ", refused 2"}, 8, 0},
			{"without view:vm", func(p *gen.ListHistoryParams) { p.StatusGuestTypes = []string{"lxc"} }, []string{
				"task cephcreatemgr ceph", "run sync_failed sync 2 since " + runSince(40) + ", refused 4",
				"audit apt_update nodes", "run sync_failed sync 3 since " + runSince(100) + ", refused 2"}, 4, 0},
			{"without view:container", func(p *gen.ListHistoryParams) { p.StatusGuestTypes = []string{"qemu"} }, []string{
				"backup vzdump backup", "status status_change guests", "task cephcreatemgr ceph",
				"run sync_failed sync 2 since " + runSince(40) + ", refused 4", "audit start guests", "audit apt_update nodes",
				"run sync_failed sync 3 since " + runSince(100) + ", refused 2"}, 7, 0},
			{"without view:task", func(p *gen.ListHistoryParams) { p.IncludeTasks = false }, []string{
				"status status_change guests", "status status_change guests", "run sync_failed sync 2 since " + runSince(40) + ", refused 4",
				"audit start guests", "audit apt_update nodes", "run sync_failed sync 3 since " + runSince(100) + ", refused 2"}, 6, 0},
			// At 2 the audit rows, the status changes, the tasks searched and the
			// sync failures are full, back to 60, 25, 50 and 40 minutes: whole
			// only after 25, it lists the backup and the newer status change, and
			// counts them. At 10 none is full, and the read is whole.
			{"capped at 2", func(p *gen.ListHistoryParams) { p.RowCap = 2 }, []string{"backup vzdump backup",
				"status status_change guests"}, 2, 25},
			// Capped for a caller without view:vm, each source's cap counts only
			// rows the caller sees: of those, only the sync failures are full.
			{"capped at 2, without view:vm", func(p *gen.ListHistoryParams) {
				p.RowCap, p.StatusGuestTypes = 2, []string{"lxc"}
			}, []string{"task cephcreatemgr ceph", "run sync_failed sync 1 since " + runSince(35) + ", refused 4"}, 2, 40},
			{"capped at 10", func(p *gen.ListHistoryParams) { p.RowCap = 10 }, []string{"backup vzdump backup",
				"status status_change guests", "status status_change guests", "task cephcreatemgr ceph",
				"run sync_failed sync 2 since " + runSince(40) + ", refused 4", "audit start guests", "audit apt_update nodes",
				"run sync_failed sync 3 since " + runSince(100) + ", refused 2"}, 8, 0},
			// Nothing of the type asked for, from a capped read: it still says.
			{"capped, nothing of the type", func(p *gen.ListHistoryParams) {
				p.RowCap, p.Category = 2, pgtype.Text{String: "certificates", Valid: true}
			}, []string{}, 0, 25},
			// Narrowed to Nexara's, each source's cap counts only its rows: the
			// audit rows' one, the apt update, fills no cap; the sync failures do.
			{"capped at 2, Nexara's", func(p *gen.ListHistoryParams) {
				p.RowCap, p.Source = 2, pgtype.Text{String: "nexara", Valid: true}
			}, []string{"run sync_failed sync 1 since " + runSince(35) + ", refused 4"}, 1, 40},
			// Proxmox's, for a caller without view:vm: the tasks searched, back
			// to the Ceph task, are the one full source.
			{"capped, Proxmox's, without view:vm", func(p *gen.ListHistoryParams) {
				p.RowCap, p.Source, p.StatusGuestTypes = 1, pgtype.Text{String: "proxmox", Valid: true}, []string{"lxc"}
			}, []string{}, 0, 30},
		} {
			got, total, searched := clusterHistory(tt.f)
			if !slices.Equal(got, tt.want) || total != tt.wantTotal {
				t.Errorf("%s: the cluster's History =\n\t%v (total %d)\nwant\n\t%v (total %d)", tt.name, got, total, tt.want, tt.wantTotal)
			}
			// Whole from just after the point: a tie with the last row read may
			// be another row the cap left.
			if want := minsAgo(tt.searched).Add(time.Microsecond); tt.searched == 0 && !searched.IsZero() ||
				tt.searched != 0 && !searched.Equal(want) {
				t.Errorf("%s: whole back to %v, want just after %v minutes back", tt.name, searched, tt.searched)
			}
		}
		// Capped at 2 and paged on below each point a page stopped, it is the
		// whole History, each entry once and in order — a burst cut at a page's
		// end a run on each side of it, its five failures counted once.
		p, err := handlers.ClusterHistory().ListParams(historyThirdID, handlers.HistorySelection{})
		if err != nil {
			t.Fatal(err)
		}
		p.IncludeTasks, p.StatusGuestTypes = true, []string{"qemu", "lxc"}
		var failures int32
		var paged []string
		for _, e := range walk(p, 2, func(r gen.ListHistoryRow) string {
			if r.Kind == "run" {
				failures += r.RunCount
			}
			return r.Kind + " " + r.Action + " " + r.Category
		}) {
			if !strings.HasPrefix(e, "run ") {
				paged = append(paged, e)
			}
		}
		whole, _, _ := clusterHistory(func(*gen.ListHistoryParams) {})
		whole = slices.DeleteFunc(whole, func(e string) bool { return strings.HasPrefix(e, "run ") })
		if !slices.Equal(paged, whole) || failures != 5 {
			t.Errorf("capped at 2, page on page: %v and %d failures, want %v and 5", paged, failures, whole)
		}
	})

	run("node", func(t *testing.T) {
		// ── A node's History, through its own selector: the third cluster's
		// pve-01 — whose name the other clusters' nodes share — its own rows, its
		// parts', the tasks it ran, its status changes and its guests' moves.
		// The two triggers first: a status write records only a change, and a
		// move is measured against where the guest was last seen, across a row
		// the collector deleted and inserted again.
		otherNode := uuid.New()
		exec("third node 2", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-02')`, otherNode, historyThirdID)
		// The slow pass writes status on every pass, changed or not.
		for _, status := range []string{"online", "online", "offline"} {
			if _, err := q.UpsertNode(ctx, gen.UpsertNodeParams{ClusterID: historyThirdID, Name: "pve-01",
				Status: status}); err != nil {
				t.Fatal(err)
			}
		}
		var nodeChanges []string
		if err := pool.QueryRow(ctx, `SELECT array_agg(from_status || '>' || to_status ORDER BY observed_at, from_status DESC)
		FROM node_status_changes WHERE cluster_id = $1`, historyThirdID).Scan(&nodeChanges); err != nil {
			t.Fatal(err)
		}
		if want := []string{"unknown>online", "online>offline"}; !slices.Equal(nodeChanges, want) {
			t.Errorf("node status changes recorded = %v, want %v", nodeChanges, want)
		}
		exec("clear node trigger rows", `DELETE FROM node_status_changes WHERE cluster_id = $1`, historyThirdID)
		place := func(vmid int32, node uuid.UUID) {
			t.Helper()
			if _, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: historyThirdID, NodeID: node,
				Vmid: vmid, Name: fmt.Sprintf("linux%d", vmid), Type: "qemu", Status: "running"}); err != nil {
				t.Fatal(err)
			}
		}
		place(310, thirdNode)
		place(310, otherNode) // a move by update
		exec("sighting", `UPDATE vms SET last_seen_at = $2 WHERE cluster_id = $1 AND vmid = 310`, historyThirdID, minsAgo(13))
		exec("churned row", `DELETE FROM vms WHERE cluster_id = $1 AND vmid = 310`, historyThirdID)
		place(310, thirdNode) // a move by delete and insert, from the deleted row's last sighting
		var churnedSince time.Time
		if err := pool.QueryRow(ctx, `SELECT last_seen_at FROM guest_node_changes
		WHERE cluster_id = $1 AND vmid = 310 AND from_node = 'pve-02'`, historyThirdID).Scan(&churnedSince); err != nil ||
			!churnedSince.Equal(minsAgo(13)) {
			t.Errorf("a move across a deleted row: last seen %v (%v), want the deleted row's %v", churnedSince, err, minsAgo(13))
		}
		place(311, thirdNode)
		exec("destroyed row", `DELETE FROM vms WHERE cluster_id = $1 AND vmid = 311`, historyThirdID)
		audit(historyThirdID, time.Now(), "proxmox", "vm", "311", "destroy",
			map[string]any{"upid": historyUPID(50, "qmdestroy", "311"), "status": "OK"})
		place(311, otherNode) // a new guest on the VMID, no move
		// Destroyed, and its VMID cloned into on another node more than five
		// minutes after its row went — in Proxmox, no new_id on record: no move.
		place(320, thirdNode)
		exec("destroyed guest", `DELETE FROM vms WHERE cluster_id = $1 AND vmid = 320`, historyThirdID)
		exec("gone a while", `UPDATE guest_node_state SET gone_at = now() - interval '10 minutes'
			WHERE cluster_id = $1 AND vmid = 320`, historyThirdID)
		place(320, otherNode)
		var moves []string
		if err := pool.QueryRow(ctx, `SELECT array_agg(vmid || ' ' || from_node || '>' || to_node ORDER BY observed_at, id)
		FROM guest_node_changes WHERE cluster_id = $1`, historyThirdID).Scan(&moves); err != nil {
			t.Fatal(err)
		}
		if want := []string{"310 pve-01>pve-02", "310 pve-02>pve-01", "311 pve-01>pve-02"}; !slices.Equal(moves, want) {
			t.Errorf("guest moves recorded = %v, want %v", moves, want)
		}
		// The moves at fixed times, and status changes: four trips through
		// unknown — two back within two minutes, one across a capped walk's stop
		// 5 minutes back; one back after five; one on to offline, its rows either
		// side of the walk's stop 7.75 minutes back, within the two minutes a
		// stretch reads past its top — and one too young to tell.
		exec("moves dated", `UPDATE guest_node_changes SET observed_at = $2::timestamptz, last_seen_at = $2::timestamptz - interval '1 minute'
		WHERE cluster_id = $1 AND vmid = 310 AND from_node = 'pve-01'`, historyThirdID, minsAgo(12))
		exec("moves dated", `UPDATE guest_node_changes SET observed_at = $2::timestamptz, last_seen_at = $2::timestamptz - interval '1 minute'
		WHERE cluster_id = $1 AND vmid = 310 AND from_node = 'pve-02'`, historyThirdID, minsAgo(11))
		// VM 310's migration, on record and done as it moved: its moves are
		// arrivals and departures.
		migration := func(n int, vmid int32, source string, details map[string]any, status string, at float64) {
			t.Helper()
			upid := historyUPID(n, "qmigrate", fmt.Sprint(vmid))
			details["upid"] = upid
			audit(historyThirdID, minsAgo(at), source, "vm", fmt.Sprint(vmid), "migrate", details)
			exec("migration's task", `INSERT INTO task_history (cluster_id, user_id, upid, status, exit_status, node,
				task_type, vmid, started_at, finished_at, source)
			VALUES ($1, $2, $3, $4, CASE $4 WHEN 'completed' THEN 'OK' ELSE 'migration aborted' END, 'pve-01', 'qmigrate',
				$5, $6::timestamptz, $6::timestamptz + interval '1 minute', $7)`, historyThirdID, systemUserID, upid, status,
				vmid, minsAgo(at), source)
		}
		migration(57, 310, "proxmox", map[string]any{"status": "OK"}, "completed", 13.5)
		for _, st := range []struct {
			from, to string
			at       time.Time
		}{
			{"online", "unknown", minsAgo(30)}, {"unknown", "online", minsAgo(29)},
			{"online", "unknown", minsAgo(20)}, {"unknown", "online", minsAgo(15)},
			{"online", "offline", minsAgo(10)}, {"online", "unknown", minsAgo(7.75)}, {"unknown", "offline", minsAgo(7.25)},
			{"online", "unknown", minsAgo(5.25)}, {"unknown", "online", minsAgo(4.75)},
			{"offline", "unknown", now.Add(-30 * time.Second)},
		} {
			exec("node status", `INSERT INTO node_status_changes (cluster_id, node, from_status, to_status, last_seen_at, observed_at)
			VALUES ($1, 'pve-01', $2, $3, $4, $4)`, historyThirdID, st.from, st.to, st.at)
		}
		// A create of VMID 310 long before its moves explains neither: only a
		// move's own window is read.
		audit(historyThirdID, minsAgo(70), "proxmox", "vm", "310", "create",
			map[string]any{"upid": historyUPID(56, "qmcreate", "310"), "status": "OK"})
		audit(historyThirdID, minsAgo(9), "nexara", "node", "pve-01", "apt_update", map[string]any{})
		audit(historyThirdID, minsAgo(8.5), "nexara", "apt_repository", "pve-01", "enabled", map[string]any{"node": "pve-01"})
		audit(historyThirdID, minsAgo(8), "nexara", "node", "pve-01", "reboot", map[string]any{})
		audit(historyThirdID, minsAgo(8), "nexara", "node", "pve-02", "reboot", map[string]any{})
		audit(historyThirdID, minsAgo(7), "nexara", "network", "pve-01/vmbr1", "interface_updated", map[string]any{"node": "pve-01"})
		// A part's row that names a guest — a disk copied for VM 310 — is guest
		// activity, though it carries no UPID.
		audit(historyThirdID, minsAgo(6.5), "nexara", "storage", "store01", "image_copy", map[string]any{"node": "pve-01"})
		exec("a part's guest", `UPDATE audit_log SET vmid = 310 WHERE cluster_id = $1 AND action = 'image_copy'`, historyThirdID)
		audit(historyThirdID, minsAgo(6), "nexara", "firewall_rule", "node/pve-01", "firewall_rule_created", map[string]any{"pos": 0})
		audit(historyThirdID, minsAgo(5), "nexara", "certificate", "pve-01", "renewed", map[string]any{})
		audit(historyThirdID, minsAgo(4), "proxmox", "proxmox_task", "pve-01", "srvreload",
			map[string]any{"upid": historyUPID(51, "srvreload", "networking"), "status": "OK"})
		audit(historyThirdID, minsAgo(3), "proxmox", "vm", "310", "start",
			map[string]any{"upid": historyUPID(52, "qmstart", "310"), "status": "OK"})
		for _, tk := range []struct {
			upid string
			at   float64
		}{{historyUPID(53, "aptupdate", ""), 2}, {historyUPID(54, "qmstart", "312"), 1}} {
			exec("node task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, vmid, started_at, source)
			VALUES ($1, $2, $3, 'completed', 'pve-01', split_part($3, ':', 6), NULLIF(split_part($3, ':', 7), '')::int, $4, 'proxmox')`,
				historyThirdID, systemUserID, tk.upid, minsAgo(tk.at))
		}
		nodeProps := apischema.Properties{"node_name": {Type: apischema.String}}
		if err := nodeProps.Compile(); err != nil {
			t.Fatal(err)
		}
		nodeReq, err := nodeProps.Validate(map[string]any{"node_name": "pve-01"})
		if err != nil {
			t.Fatal(err)
		}
		nodeSel, err := handlers.NodeHistory().Select(ctx, handlers.HistoryRequest{Queries: q, ClusterID: historyThirdID, Params: nodeReq})
		if err != nil {
			t.Fatal(err)
		}
		nodeEntry := func(r gen.ListHistoryRow) string {
			if uuid.UUID(r.ClusterID.Bytes) != historyThirdID {
				t.Errorf("the node's History holds %s %s of cluster %s", r.Kind, r.Action, uuid.UUID(r.ClusterID.Bytes))
			}
			entry := r.Kind + " " + r.Action + " " + r.Category
			switch r.Kind {
			case "status":
				entry += " " + r.FromStatus + ">" + r.ToStatus
			case "move":
				entry += fmt.Sprintf(" %d %s", r.Vmid, r.Node)
			}
			if r.RunCount > 0 {
				entry += fmt.Sprintf(" run %d", r.RunCount)
			}
			return entry
		}
		nodeParams := func(f func(*gen.ListHistoryParams)) gen.ListHistoryParams {
			t.Helper()
			p, err := handlers.NodeHistory().ListParams(historyThirdID, nodeSel)
			if err != nil {
				t.Fatal(err)
			}
			p.RowLimit, p.IncludeTasks, p.StatusGuestTypes = 50, true, []string{"qemu", "lxc"}
			f(&p)
			return p
		}
		nodeHistory := func(f func(*gen.ListHistoryParams)) []string {
			t.Helper()
			rows, err := hq.ListHistory(ctx, nodeParams(f))
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			for _, r := range rows {
				if r.EntryID != "" {
					got = append(got, nodeEntry(r))
				}
			}
			return got
		}
		// Its own, the cluster block's task and update on it among them; not the
		// VMID 311 that a destroy made a new guest, nor a round trip through
		// unknown shorter than two minutes, nor one too young to tell.
		own := []string{"task aptupdate checks", "audit srvreload network", "audit renewed certificates",
			"audit firewall_rule_created network", "audit interface_updated network",
			"status status_change status unknown>offline", "status status_change status online>unknown", "audit reboot power",
			"audit enabled updates", "audit apt_update checks", "status status_change status online>offline",
			"move arrived guests 310 pve-02", "move left guests 310 pve-02", "status status_change status unknown>online",
			"status status_change status online>unknown", "task cephcreatemgr ceph", "audit apt_update checks"}
		for _, tt := range []struct {
			name string
			f    func(*gen.ListHistoryParams)
			want []string
		}{
			{"its own", func(*gen.ListHistoryParams) {}, own},
			{"with its guests' tasks", func(p *gen.ListHistoryParams) { p.IncludeGuests = true }, []string{
				"audit destroy guests", "task qmstart guests", "task aptupdate checks", "audit start guests",
				"audit srvreload network", "audit renewed certificates", "audit firewall_rule_created network",
				"audit image_copy storage", "audit interface_updated network", "status status_change status unknown>offline",
				"status status_change status online>unknown", "audit reboot power", "audit enabled updates",
				"audit apt_update checks", "status status_change status online>offline", "move arrived guests 310 pve-02",
				"move left guests 310 pve-02", "audit migrate guests", "status status_change status unknown>online",
				"status status_change status online>unknown", "task cephcreatemgr ceph", "audit start guests",
				"audit apt_update checks", "audit create guests"}},
			// A repository change is an update, not a check, and stays.
			{"without its routine checks", func(p *gen.ListHistoryParams) { p.ExcludeCategories = []string{"checks"} },
				slices.DeleteFunc(slices.Clone(own), func(s string) bool { return strings.HasSuffix(s, " checks") })},
			{"without view:vm, guests' moves and tasks go", func(p *gen.ListHistoryParams) {
				p.IncludeGuests, p.StatusGuestTypes = true, []string{"lxc"}
			}, slices.DeleteFunc(slices.Clone(own), func(s string) bool { return strings.HasPrefix(s, "move ") })},
			// Capped, each source is read newest first to the cap: its tasks, its
			// audit rows, its status changes and its guests' moves.
		} {
			if got := nodeHistory(tt.f); !slices.Equal(got, tt.want) {
				t.Errorf("%s: the node's History =\n\t%v\nwant\n\t%v", tt.name, got, tt.want)
			}
			// Capped at 2 and paged on below each point a page stopped, it is
			// the same History, each entry once — for a caller without view:vm
			// too, whose caps count only rows it sees.
			if got := walk(nodeParams(tt.f), 2, nodeEntry); !slices.Equal(got, tt.want) {
				t.Errorf("%s, capped at 2, page on page:\n\t%v\nwant\n\t%v", tt.name, got, tt.want)
			}
		}
		// Capped at 1 without view:task, the node's audit rows are the full
		// source read latest: whole after their newest, the reload 4 minutes on.
		{
			p := nodeParams(func(p *gen.ListHistoryParams) { p.IncludeTasks, p.RowCap = false, 1 })
			if rows, err := hq.ListHistory(ctx, p); err != nil || len(rows) == 0 ||
				!rows[0].SearchedFrom.Time.Equal(minsAgo(4).Add(time.Microsecond)) {
				t.Errorf("the node's History capped at 1 without view:task: %d rows (%v), want whole after 4 minutes", len(rows), err)
			}
		}
		// Two minutes on, a node that stopped reporting and stayed so is a change.
		exec("settled", `UPDATE node_status_changes SET observed_at = $2, last_seen_at = $2
		WHERE cluster_id = $1 AND from_status = 'offline'`, historyThirdID, minsAgo(3))
		if got := nodeHistory(func(*gen.ListHistoryParams) {}); len(got) < 2 || got[1] != "status status_change status offline>unknown" {
			t.Errorf("a node still unknown after two minutes: %v, want its change second", got)
		}

		// VMID 340 destroyed on pve-01 and cloned into on pve-02 within five
		// minutes, its row not yet let go: the VMID is seen on pve-02, with no
		// migration of it there on record — no move. A container in a VM's
		// place on the VMID records nothing at all.
		place(340, thirdNode)
		audit(historyThirdID, minsAgo(9.5), "proxmox", "vm", "340", "destroy",
			map[string]any{"upid": historyUPID(58, "qmdestroy", "340"), "status": "OK"})
		// None of these is its migration: one that failed, one to another node,
		// and its previous occupant's, done before it was last seen.
		migration(59, 340, "proxmox", map[string]any{"status": "migration aborted"}, "failed", 8)
		migration(60, 340, "nexara", map[string]any{"target": "pve-03"}, "completed", 7)
		migration(61, 340, "proxmox", map[string]any{"status": "OK"}, "completed", 30)
		exec("sighting after the destroy began", `UPDATE vms SET last_seen_at = $2 WHERE cluster_id = $1 AND vmid = 340`,
			historyThirdID, minsAgo(9))
		place(340, otherNode)
		place(341, thirdNode)
		if _, err := q.UpsertVM(ctx, gen.UpsertVMParams{ClusterID: historyThirdID, NodeID: otherNode,
			Vmid: 341, Name: "linux41", Type: "lxc", Status: "running"}); err != nil {
			t.Fatal(err)
		}
		var reused []string
		for _, e := range nodeHistory(func(*gen.ListHistoryParams) {}) {
			if strings.HasPrefix(e, "move ") && (strings.Contains(e, " 340 ") || strings.Contains(e, " 341 ")) {
				reused = append(reused, e)
			}
		}
		if want := []string{"move now_on guests 340 pve-02"}; !slices.Equal(reused, want) {
			t.Errorf("a reused VMID on another node: %v, want %v", reused, want)
		}

		// Twenty changes an hour a node, and moves a guest; past that a run row
		// counts the rest and where they ended, the real rows untouched: 23 of
		// each — 22 moves after the first placing, the last to a third node —
		// leave 20 and a run of 3 or 2.
		lastNode := uuid.New()
		exec("third node 3", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-03')`, lastNode, historyThirdID)
		for i := range 23 {
			if _, err := q.UpsertNode(ctx, gen.UpsertNodeParams{ClusterID: historyThirdID, Name: "pve-02",
				Status: []string{"offline", "online"}[i%2]}); err != nil {
				t.Fatal(err)
			}
			to := []uuid.UUID{otherNode, thirdNode}[i%2]
			if i == 22 {
				to = lastNode
			}
			place(330, to)
		}
		for _, c := range []struct {
			what, sql string
			want      string
		}{
			{"status changes", `SELECT count(*) FILTER (WHERE run_count = 0) || ' ' ||
				(array_agg(to_status ORDER BY observed_at DESC, id DESC) FILTER (WHERE run_count = 0))[1] || ', run ' ||
				string_agg(run_count || ' ' || to_status, ',') FILTER (WHERE run_count > 0)
				FROM node_status_changes WHERE cluster_id = $1 AND node = 'pve-02'`, "20 online, run 3 offline"},
			{"moves", `SELECT count(*) FILTER (WHERE run_count = 0) || ' ' ||
				(array_agg(to_node ORDER BY observed_at DESC, id DESC) FILTER (WHERE run_count = 0))[1] || ', run ' ||
				string_agg(run_count || ' ' || to_node, ',') FILTER (WHERE run_count > 0)
				FROM guest_node_changes WHERE cluster_id = $1 AND vmid = 330`, "20 pve-02, run 2 pve-03"},
		} {
			var got string
			if err := pool.QueryRow(ctx, c.sql, historyThirdID).Scan(&got); err != nil || got != c.want {
				t.Errorf("%s after 23 in an hour: %q (%v), want %q", c.what, got, err, c.want)
			}
		}
		// Each run is one entry, and no real one: the guest's on every node it
		// went through, pve-03's joined last, and the node's after its 20.
		for _, c := range []struct {
			node, prefix string
			want         []string
		}{
			{"pve-01", "move moved ", []string{"move moved guests 330 pve-03 run 2"}},
			{"pve-03", "move ", []string{"move moved guests 330 pve-02 run 2"}},
			{"pve-02", "status status_change status online>offline run 3", nil},
		} {
			var got []string
			for _, e := range nodeHistory(func(p *gen.ListHistoryParams) { p.NodeName, p.IncludeGuests = c.node, true }) {
				if strings.HasPrefix(e, c.prefix) || c.want == nil && strings.HasPrefix(e, "status ") {
					got = append(got, e)
				}
			}
			if c.want == nil && (len(got) != 21 || got[0] != c.prefix) || c.want != nil && !slices.Equal(got, c.want) {
				t.Errorf("the runs on %s's History: %v", c.node, got)
			}
		}
	})

	run("observed", func(t *testing.T) {
		// ── What Nexara saw change in Proxmox, listed only where nothing on
		// record explains it and only of the types the caller may view
		// (observed_types): a guest's by its VMID, from its cutoff; a node's,
		// its interfaces by the node they name; the whole cluster's; a global
		// kind's, which alone name no cluster.
		seen := func(cluster uuid.UUID, typ, id, node string, vmid any, fields []string, at time.Time, explained any) {
			t.Helper()
			exec("seen "+typ+" "+id, `INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, vmid,
				change, fields, node, observed_by, last_seen_at, observed_at, explained)
			VALUES ($1, $2, $3, $4, 'changed', $5, $6, 'observer', $7::timestamptz - interval '5 minutes', $7, $8)`,
				pgtype.UUID{Bytes: cluster, Valid: cluster != uuid.Nil}, typ, id, vmid, fields, node, at, explained)
		}
		seen(historyClusterID, "vm", "100", "pve-01", 100, []string{"cores", "memory"}, minsAgo(6), false)
		seen(historyClusterID, "vm", "100", "pve-01", 100, []string{"cores"}, hoursAgo(8.5), false) // the previous guest's
		seen(historyClusterID, "vm", "100", "pve-01", 100, []string{"name"}, minsAgo(6), true)      // explained
		seen(historyClusterID, "vm", "100", "pve-01", 100, []string{"tags"}, minsAgo(1), nil)       // not yet classified
		seen(historyClusterID, "container", "120", "pve-01", 120, []string{"memory"}, minsAgo(6), false)
		seen(historyThirdID, "node", "pve-01", "pve-01", nil, []string{"dns"}, minsAgo(2.5), false)
		seen(historyThirdID, "node", "pve-01", "pve-01", nil, []string{"comment"}, minsAgo(2.5), true) // explained
		seen(historyThirdID, "firewall_rule", "node/pve-01", "pve-01", nil, []string{"rules"}, minsAgo(2.4), false)
		seen(historyThirdID, "network", "pve-01/vmbr7", "pve-01", nil, []string{"cidr"}, minsAgo(2.3), false)
		seen(historyThirdID, "network", "pve-02/vmbr7", "pve-02", nil, []string{"cidr"}, minsAgo(2.3), false)
		seen(historyThirdID, "acme_config", "pve-01", "pve-01", nil, []string{"domains"}, minsAgo(2.2), false)
		seen(historyThirdID, "pool", "pool1", "", nil, []string{"comment"}, minsAgo(2.1), false)
		seen(historyClusterID, "pool", "pool2", "", nil, []string{"comment"}, minsAgo(2.1), false)
		seen(uuid.Nil, "pbs_server", historyPBSID, "", nil, []string{"comment"}, minsAgo(2), false)
		observedOf := func(p gen.ListHistoryParams, types ...string) []string {
			t.Helper()
			p.RowLimit, p.ObservedTypes = 200, types
			rows, err := hq.ListHistory(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, r := range rows {
				if r.Kind == "observed" {
					got = append(got, r.ResourceType+" "+r.ResourceID+" "+strings.Join(r.Fields, ","))
				}
			}
			return got
		}
		for _, tt := range []struct {
			name  string
			p     gen.ListHistoryParams
			types []string
			want  []string
		}{
			{"a guest's, since its cutoff", guest(true), []string{"vm", "container"}, []string{"vm 100 cores,memory"}},
			{"a guest's, without its type's view", guest(true), []string{"container"}, []string{}},
		} {
			if got := observedOf(tt.p, tt.types...); !slices.Equal(got, tt.want) {
				t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
			}
		}
		nodeReq, err := (apischema.Properties{"node_name": {Type: apischema.String}}).Validate(map[string]any{"node_name": "pve-01"})
		if err != nil {
			t.Fatal(err)
		}
		nodeSel, err := handlers.NodeHistory().Select(ctx, handlers.HistoryRequest{Queries: q, ClusterID: historyThirdID, Params: nodeReq})
		if err != nil {
			t.Fatal(err)
		}
		np, err := handlers.NodeHistory().ListParams(historyThirdID, nodeSel)
		if err != nil {
			t.Fatal(err)
		}
		np.IncludeTasks, np.StatusGuestTypes = true, []string{"qemu", "lxc"}
		cp, err := handlers.ClusterHistory().ListParams(historyThirdID, handlers.HistorySelection{})
		if err != nil {
			t.Fatal(err)
		}
		cp.IncludeTasks, cp.StatusGuestTypes = true, []string{"qemu", "lxc"}
		pools := handlers.HistoryKind{Name: "pools", Categories: []string{"other"}, Default: "other",
			ObjectTypes: []string{"pool", "pbs_server"}, Select: handlers.SelectTypes("pool")}
		wp, err := pools.ListParams(historyThirdID, handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "pool", WholeType: true}, {ResourceType: "pbs_server", Value: historyPBSID}}})
		if err != nil {
			t.Fatal(err)
		}
		pbs := handlers.HistoryKind{Name: "pbs", Categories: []string{"other"}, Default: "other", Global: true,
			ObjectTypes: []string{"pbs_server"}}
		gp, err := pbs.ListParams(uuid.Nil, handlers.HistorySelection{Objects: []handlers.HistoryObject{
			{ResourceType: "pbs_server", Value: historyPBSID}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, tt := range []struct {
			name  string
			p     gen.ListHistoryParams
			types []string
			want  []string
		}{
			{"a node's", np, []string{"node", "network", "firewall_rule", "acme_config"}, []string{"acme_config pve-01 domains",
				"network pve-01/vmbr7 cidr", "firewall_rule node/pve-01 rules", "node pve-01 dns"}},
			{"a node's, without view:network", np, []string{"node", "acme_config"},
				[]string{"acme_config pve-01 domains", "node pve-01 dns"}},
			{"the whole cluster's", cp, []string{"node", "pool", "pbs_server"}, []string{"pool pool1 comment", "node pve-01 dns"}},
			{"a whole type, in its cluster alone", wp, []string{"pool", "pbs_server"}, []string{"pool pool1 comment"}},
			{"a global kind's, which name no cluster", gp, []string{"pbs_server"}, []string{"pbs_server " + historyPBSID + " comment"}},
		} {
			if got := observedOf(tt.p, tt.types...); !slices.Equal(got, tt.want) {
				t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
			}
		}
	})

	run("global", func(t *testing.T) {
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
		templateRows, err := hq.ListHistory(ctx, tp)
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
	})
}

func withFilter(p gen.ListHistoryParams, f func(*gen.ListHistoryParams)) gen.ListHistoryParams {
	f(&p)
	return p
}
