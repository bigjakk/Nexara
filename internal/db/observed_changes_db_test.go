package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed so a run that dies before its purge leaves rows the next
// run's up-front purge deletes.
var (
	observedCluster = uuid.MustParse("128a0000-0000-4000-8000-00000000000a")
	observedOther   = uuid.MustParse("128a0000-0000-4000-8000-00000000000b")
)

// TestObservedChanges runs the SQL behind the observer (migrations/000128,
// queries/cluster_object_state.sql) against Postgres: a reading written in one
// statement, and the rules that decide whether something on record explains a
// change it filed.
//
// Not parallel: it writes rows the package's other DB tests share a schema with.
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database.
func TestObservedChanges(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Audit rows first: an audit log may outlive its cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, []uuid.UUID{observedCluster, observedOther})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{observedCluster, observedOther})
	}
	purge()
	defer purge()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for i, id := range []uuid.UUID{observedCluster, observedOther} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, "cluster0"+string(rune('1'+i)))
	}
	now := time.Now().UTC().Truncate(time.Second)
	ago := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }
	jsonOf := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// ── One reading, written whole: a change, two objects stored, the kind marked.
	record := func(arg gen.RecordObservedObjectsParams) {
		t.Helper()
		arg.ClusterID, arg.Kind = observedCluster, "storage"
		for _, s := range []*[]string{&arg.Removed, &arg.Touched} {
			if *s == nil {
				*s = []string{}
			}
		}
		if arg.Changes == nil {
			arg.Changes = json.RawMessage(`[]`)
		}
		if arg.States == nil {
			arg.States = json.RawMessage(`[]`)
		}
		if err := q.RecordObservedObjects(ctx, arg); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	state := func(id, tag string, seen time.Time) map[string]any {
		return map[string]any{"object_id": id, "fields": map[string]any{"nodes": tag}, "node": "",
			"signature": "sig1", "last_seen_at": seen}
	}
	record(gen.RecordObservedObjectsParams{
		Changes: jsonOf([]map[string]any{{"resource_type": "storage", "resource_id": "store01", "vmid": nil,
			"change": "changed", "fields": []string{"nodes"}, "detail": map[string]any{}, "node": "",
			"last_seen_at": ago(9), "observed_at": ago(4)}}),
		States: jsonOf([]map[string]any{state("store01", "t1", ago(5)), state("store02", "t2", ago(5))}),
		SeenAt: ago(5), Baseline: "sig1",
	})
	var fields []string
	var observedBy string
	var vmid pgtype.Int4
	if err := pool.QueryRow(ctx, `SELECT fields, observed_by, vmid FROM cluster_object_changes
		WHERE cluster_id = $1 AND resource_id = 'store01'`, observedCluster).Scan(&fields, &observedBy, &vmid); err != nil {
		t.Fatalf("read the change: %v", err)
	}
	if len(fields) != 1 || fields[0] != "nodes" || observedBy != "observer" || vmid.Valid {
		t.Fatalf("change filed as %v %s %v", fields, observedBy, vmid)
	}
	marks, err := q.ListClusterObjectBaselines(ctx, observedCluster)
	if err != nil || len(marks) != 1 || marks[0].Signature != "sig1" || !marks[0].LastReadAt.Equal(ago(5)) {
		t.Fatalf("marks %+v, %v", marks, err)
	}

	// The next reading touches one, drops the other, and marks nothing.
	record(gen.RecordObservedObjectsParams{Touched: []string{"store01"}, Removed: []string{"store02"}, SeenAt: ago(1)})
	rows, err := q.ListClusterObjectState(ctx, observedCluster)
	if err != nil || len(rows) != 1 || rows[0].ObjectID != "store01" || !rows[0].LastSeenAt.Equal(ago(1)) ||
		string(rows[0].Fields) != `{"nodes": "t1"}` {
		t.Fatalf("stored after the second reading: %+v, %v", rows, err)
	}

	// ── What explains a change: the rules the observer passes, applied here.
	exec("forget the readings' change", `DELETE FROM cluster_object_changes WHERE cluster_id = $1`, observedCluster)
	seed := func(cluster uuid.UUID, rtype, rid string, guest int32, change, node string, fields []string, observed time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		v := pgtype.Int4{Int32: guest, Valid: guest != 0}
		if fields == nil {
			fields = []string{}
		}
		if err := pool.QueryRow(ctx, `INSERT INTO cluster_object_changes
				(cluster_id, resource_type, resource_id, vmid, change, fields, node, observed_by, last_seen_at, observed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'observer', $8, $9) RETURNING id`,
			cluster, rtype, rid, v, change, fields, node, observed.Add(-5*time.Minute), observed).Scan(&id); err != nil {
			t.Fatalf("seed %s/%s: %v", rtype, rid, err)
		}
		return id
	}
	audit := func(cluster uuid.UUID, at time.Time, rtype, rid string, guest int32, action string, details map[string]any) {
		t.Helper()
		v := pgtype.Int4{Int32: guest, Valid: guest != 0}
		exec("audit", `INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, vmid, action, details, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, cluster, systemUserID, rtype, rid, v, action, jsonOf(details), at)
	}
	task := func(cluster uuid.UUID, upid string, guest int32, started, finished time.Time) {
		t.Helper()
		exec("task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at, finished_at, source, vmid)
			VALUES ($1, $2, $3, 'stopped', 'pve-01', split_part($3, ':', 6), $4, $5, 'proxmox', $6)`,
			cluster, systemUserID, upid, started, finished, pgtype.Int4{Int32: guest, Valid: guest != 0})
	}
	upid := func(worker string, n int) string {
		return fmt.Sprintf("UPID:pve-01:%08X:00000001:00000001:%s:%d:root@pam:", n, worker, n)
	}

	at := ago(60)              // when each change below was seen
	in := at.Add(-time.Minute) // inside its window
	byOwnRow := seed(observedCluster, "storage", "store01", 0, "changed", "", []string{"nodes"}, at)
	audit(observedCluster, in, "storage", "store01", 0, "update", nil)
	byRunOnly := seed(observedCluster, "backup", "backup-01", 0, "changed", "", []string{"schedule"}, at)
	audit(observedCluster, in, "backup", "backup-01", 0, "run", nil)
	tooEarly := seed(observedCluster, "storage", "store02", 0, "changed", "", []string{"nodes"}, at)
	audit(observedCluster, at.Add(-9*time.Minute), "storage", "store02", 0, "update", nil)
	otherCluster := seed(observedCluster, "storage", "store03", 0, "changed", "", []string{"nodes"}, at)
	audit(observedOther, in, "storage", "store03", 0, "update", nil)

	// A guest: a disk move explains the disk, not the memory changed beside it,
	// unless a config task explains that too; Nexara's own untasked edit counts,
	// a long task counts while it runs, and an untasked row of another action
	// does not.
	diskOnly := seed(observedCluster, "vm", "100", 100, "changed", "pve-01", []string{"scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "100", 100, "qmmove", map[string]any{"upid": upid("qmmove", 1)})
	diskAndMemory := seed(observedCluster, "vm", "101", 101, "changed", "pve-01", []string{"memory", "scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "101", 101, "qmmove", map[string]any{"upid": upid("qmmove", 2)})
	bothCovered := seed(observedCluster, "vm", "102", 102, "changed", "pve-01", []string{"memory", "scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "102", 102, "qmmove", map[string]any{"upid": upid("qmmove", 3)})
	audit(observedCluster, in, "vm", "102", 102, "config_change", map[string]any{"upid": upid("qmconfig", 4)})
	byNexaraEdit := seed(observedCluster, "vm", "103", 103, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 103, "config_update", nil)
	byOtherUntasked := seed(observedCluster, "vm", "104", 104, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 104, "drs_migrate", nil)
	byLongTask := seed(observedCluster, "vm", "105", 105, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, at.Add(-3*time.Hour), "vm", "105", 105, "config_change", map[string]any{"upid": upid("qmconfig", 5)})
	task(observedCluster, upid("qmconfig", 5), 105, at.Add(-3*time.Hour), at.Add(-2*time.Minute))
	byFinishedTask := seed(observedCluster, "vm", "106", 106, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, at.Add(-3*time.Hour), "vm", "106", 106, "config_change", map[string]any{"upid": upid("qmconfig", 6)})
	task(observedCluster, upid("qmconfig", 6), 106, at.Add(-3*time.Hour), at.Add(-2*time.Hour))

	// The other match keys. A rule matching any row of the cluster would
	// explain every change of its kind in the window, so those cases are an
	// hour and more apart.
	bySid := seed(observedCluster, "ha_resource", "vm:110", 0, "changed", "", []string{"state"}, at)
	audit(observedCluster, in, "proxmox_task", "110", 110, "hastart", map[string]any{"upid": upid("hastart", 7)})
	byClusterTask := seed(observedCluster, "pool", "web", 0, "changed", "", []string{"vms"}, at)
	audit(observedCluster, in, "vm", "111", 111, "destroy", map[string]any{"upid": upid("qmdestroy", 8)})
	byDetails := seed(observedCluster, "pool", "db", 0, "changed", "", []string{"vms"}, ago(200))
	audit(observedCluster, ago(201), "vm", uuid.NewString(), 112, "set_pool", map[string]any{"new_pool": "db"})
	byNode := seed(observedCluster, "network", "pve-01/vmbr1", 0, "removed", "pve-01", nil, at)
	audit(observedCluster, in, "network", "pve-01", 0, "network_reverted", nil)
	addedByTask := seed(observedCluster, "storage", "store04", 0, "added", "", nil, ago(300))
	audit(observedCluster, ago(301), "node", "pve-01", 0, "create_directory", map[string]any{"upid": upid("dircreate", 9)})
	addedUnexplained := seed(observedCluster, "storage", "store05", 0, "added", "", nil, ago(400))

	// Timing and scope.
	fresh := seed(observedCluster, "storage", "store06", 0, "changed", "", []string{"nodes"}, ago(1))
	nothingOnRecord := seed(observedCluster, "storage", "store07", 0, "changed", "", []string{"nodes"}, ago(90))
	gaveUp := seed(observedCluster, "storage", "store08", 0, "changed", "", []string{"nodes"}, now.Add(-26*time.Hour))
	notMine := seed(observedCluster, "replication", "100-0", 0, "changed", "", []string{"schedule"}, at)
	exec("a trigger's row", `UPDATE cluster_object_changes SET observed_by = 'trigger' WHERE id = $1`, notMine)

	rule := func(kind, change, auditType, key string, untasked bool, actionRe, exceptRe, workerRe, fieldRe string) map[string]any {
		return map[string]any{"kind": kind, "change": change, "audit_type": auditType, "match_key": key,
			"untasked": untasked, "action_re": actionRe, "except_re": exceptRe, "worker_re": workerRe, "field_re": fieldRe}
	}
	rules := jsonOf([]map[string]any{
		rule("storage", "", "storage", "id", false, "", "", "", ""),
		rule("backup", "", "backup", "id", false, "", "^run$", "", ""),
		rule("storage", "added", "", "cluster", false, "", "", "^dircreate$", ""),
		rule("vm", "changed", "", "vmid", false, "", "", "^qmmove$", "^scsi[0-9]+$"),
		rule("vm", "changed", "", "vmid", false, "", "", "^qmconfig$", ""),
		rule("vm", "changed", "vm", "vmid", true, "^config_update$", "", "", ""),
		rule("ha_resource", "changed", "", "sid", false, "", "", "^hastart$", "^state$"),
		rule("pool", "changed", "", "cluster", false, "", "", "^qmdestroy$", "^vms$"),
		rule("pool", "changed", "vm", "details:new_pool", false, "^set_pool$", "", "", "^vms$"),
		rule("network", "", "network", "node", false, "^network_reverted$", "", "", ""),
	})
	classify := func(allListed bool) {
		t.Helper()
		if _, err := q.ClassifyObservedChanges(ctx, gen.ClassifyObservedChangesParams{
			Rules: rules, ClusterID: observedCluster, SettledBefore: ago(5), AllNodesListed: allListed,
			GiveUpBefore: now.Add(-24 * time.Hour),
		}); err != nil {
			t.Fatalf("classify: %v", err)
		}
	}
	explained := func(id uuid.UUID) string {
		t.Helper()
		var e pgtype.Bool
		if err := pool.QueryRow(ctx, `SELECT explained FROM cluster_object_changes WHERE id = $1`, id).Scan(&e); err != nil {
			t.Fatalf("read explained: %v", err)
		}
		switch {
		case !e.Valid:
			return "waiting"
		case e.Bool:
			return "explained"
		}
		return "shown"
	}

	classify(false) // a node's task list unread: only what has waited past the give-up is decided
	for name, id := range map[string]uuid.UUID{"recent, nodes unlisted": byOwnRow, "past the give-up": gaveUp} {
		want := map[bool]string{true: "shown", false: "waiting"}[id == gaveUp]
		if got := explained(id); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	classify(true)
	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want string
	}{
		{"its own audit row in the window", byOwnRow, "explained"},
		{"its own row, of an action the rule excepts", byRunOnly, "shown"},
		{"its own row from before the window", tooEarly, "shown"},
		{"another cluster's row", otherCluster, "shown"},
		{"a disk move, the disk changed", diskOnly, "explained"},
		{"a disk move, the disk and the memory changed", diskAndMemory, "shown"},
		{"a disk move and a config task, both changed", bothCovered, "explained"},
		{"Nexara's own untasked edit", byNexaraEdit, "explained"},
		{"an untasked row of another action", byOtherUntasked, "shown"},
		{"a task started long before, running into the window", byLongTask, "explained"},
		{"a task that ended long before", byFinishedTask, "shown"},
		{"an HA start of the sid's guest", bySid, "explained"},
		{"a guest destroyed anywhere in the cluster", byClusterTask, "explained"},
		{"a guest moved into the pool from its page", byDetails, "explained"},
		{"a revert of the node's pending network", byNode, "explained"},
		{"added by a disk task", addedByTask, "explained"},
		{"added, nothing on record", addedUnexplained, "shown"},
		{"seen too recently", fresh, "waiting"},
		{"settled, nothing on record", nothingOnRecord, "shown"},
		{"another writer's row", notMine, "waiting"},
	} {
		if got := explained(tc.id); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Fixture ids for the global kinds' test.
var (
	observedPBS     = uuid.MustParse("128a0000-0000-4000-8000-0000000000a1")
	observedPBSGone = uuid.MustParse("128a0000-0000-4000-8000-0000000000a2")
)

// TestObservedGlobalChanges runs migrations 000129 and 000130 against Postgres:
// only a global kind's row may name no cluster, the PBS job triggers record an
// edit, a job added after the server's jobs were read and a job removed — never
// a server's own removal, nor the first read of its jobs — and the global
// classification reads audit rows by type and id alone.
//
// Not parallel, for the reason TestObservedChanges gives.
func TestObservedGlobalChanges(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		servers := []string{observedPBS.String(), observedPBSGone.String()}
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = 'pbs_server' AND resource_id = ANY($1)`, servers)
		_, _ = pool.Exec(pctx, `DELETE FROM pbs_servers WHERE id = ANY($1)`, []uuid.UUID{observedPBS, observedPBSGone})
		_, _ = pool.Exec(pctx, `DELETE FROM cluster_object_changes WHERE cluster_id IS NULL
			AND resource_id = ANY($1)`, servers)
	}
	purge()
	defer purge()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for _, id := range []uuid.UUID{observedPBS, observedPBSGone} {
		exec("pbs server", `INSERT INTO pbs_servers (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'pbs01', 'https://192.0.2.20:8007', 'tok', 'enc')`, id)
	}

	// Only a global kind may name no cluster.
	if _, err := pool.Exec(ctx, `INSERT INTO cluster_object_changes (resource_type, resource_id, change, observed_by, last_seen_at)
		VALUES ('storage', 'store01', 'changed', 'observer', now())`); err == nil {
		t.Fatal("a storage change with no cluster was accepted")
	}

	type row struct {
		Change string
		Fields []string
		Detail map[string]any
	}
	changes := func(server uuid.UUID) []row {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT change, fields, detail FROM cluster_object_changes
			WHERE cluster_id IS NULL AND resource_type = 'pbs_server' AND resource_id = $1 ORDER BY observed_at, change`,
			server.String())
		if err != nil {
			t.Fatalf("read changes: %v", err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			var detail []byte
			if err := rows.Scan(&r.Change, &r.Fields, &detail); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if err := json.Unmarshal(detail, &r.Detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	syncJob := func(server uuid.UUID, id, schedule, remoteStore, state string) {
		t.Helper()
		if _, err := q.UpsertPBSSyncJob(ctx, gen.UpsertPBSSyncJobParams{PbsServerID: server, JobID: id,
			Store: "datastore01", Remote: "remote01", RemoteStore: remoteStore, Schedule: schedule,
			LastRunState: state}); err != nil {
			t.Fatalf("upsert sync job %s: %v", id, err)
		}
	}

	syncJob(observedPBS, "s-01", "hourly", "datastore02", "OK")
	syncJob(observedPBS, "s-01", "hourly", "datastore02", "ERROR") // run state only
	if got := changes(observedPBS); len(got) != 0 {
		t.Fatalf("the first read of a server's jobs recorded %+v", got)
	}
	if err := q.MarkPBSJobBaseline(ctx, gen.MarkPBSJobBaselineParams{PbsServerID: observedPBS, JobType: "sync"}); err != nil {
		t.Fatal(err)
	}
	syncJob(observedPBS, "s-01", "daily", "datastore03", "OK")
	syncJob(observedPBS, "s-02", "daily", "datastore02", "")
	exec("prune", `DELETE FROM pbs_sync_jobs WHERE pbs_server_id = $1 AND job_id = 's-02'`, observedPBS)
	got := changes(observedPBS)
	if len(got) != 3 || got[0].Change != "changed" || fmt.Sprint(got[0].Fields) != "[remote-store schedule]" ||
		got[0].Detail["job"] != "s-01" || got[0].Detail["job_type"] != "sync" || got[0].Detail["datastore"] != "datastore01" ||
		got[1].Change != "added" || got[2].Change != "removed" {
		t.Fatalf("recorded %+v, want s-01 changed (remote-store, schedule), s-02 added and removed", got)
	}

	// A server removed takes its jobs with it, and that is no job's removal.
	if err := q.MarkPBSJobBaseline(ctx, gen.MarkPBSJobBaselineParams{PbsServerID: observedPBSGone, JobType: "sync"}); err != nil {
		t.Fatal(err)
	}
	syncJob(observedPBSGone, "s-09", "daily", "datastore02", "")
	exec("remove the server", `DELETE FROM pbs_servers WHERE id = $1`, observedPBSGone)
	if got := changes(observedPBSGone); len(got) != 1 || got[0].Change != "added" {
		t.Fatalf("a removed server's jobs recorded %+v, want only the add", got)
	}

	// The global classification: an audit row of the server, in any cluster or
	// none, explains a change of it; with nothing on record it shows.
	exec("settle", `UPDATE cluster_object_changes SET observed_at = now() - interval '1 hour',
			last_seen_at = now() - interval '70 minutes'
		WHERE cluster_id IS NULL AND resource_id = ANY($1)`, []string{observedPBS.String(), observedPBSGone.String()})
	exec("audit", `INSERT INTO audit_log (user_id, resource_type, resource_id, action, created_at)
		VALUES ($1, 'pbs_server', $2, 'pbs_updated', now() - interval '65 minutes')`, systemUserID, observedPBS.String())
	rules, _ := json.Marshal([]map[string]any{{"kind": "pbs_server", "change": "", "audit_type": "pbs_server",
		"match_key": "id", "untasked": false, "action_re": "", "except_re": "", "worker_re": "", "field_re": ""}})
	if _, err := q.ClassifyGlobalObjectChanges(ctx, gen.ClassifyGlobalObjectChangesParams{
		Rules: rules, SettledBefore: time.Now().Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	var explained, shown int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE explained AND resource_id = $1),
			count(*) FILTER (WHERE NOT explained AND resource_id = $2)
		FROM cluster_object_changes WHERE cluster_id IS NULL`, observedPBS.String(), observedPBSGone.String()).
		Scan(&explained, &shown); err != nil {
		t.Fatal(err)
	}
	if explained != 3 || shown != 1 {
		t.Fatalf("explained %d of the server's 3, shown %d of the other's 1", explained, shown)
	}
}
