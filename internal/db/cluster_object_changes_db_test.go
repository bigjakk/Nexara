package db

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed so a run that dies before its purge leaves rows the next
// run's up-front purge deletes.
var (
	objChangesCluster  = uuid.MustParse("125a0000-0000-4000-8000-00000000000a") // replication baselined
	objChangesOther    = uuid.MustParse("125a0000-0000-4000-8000-00000000000b") // an upgrade: rows from before 000125
	objChangesDoomed   = uuid.MustParse("125a0000-0000-4000-8000-00000000000c") // deleted with its jobs
	objChangesUnmarked = uuid.MustParse("125a0000-0000-4000-8000-00000000000d") // never baselined
)

// TestClusterObjectChanges runs the SQL behind the History of the cluster page's
// objects (migrations 000124 and 000125, queries/cluster_object_changes.sql): what
// the replication triggers record and must not, when a change counts as explained,
// and what the prune and the read keep. Not parallel: it shares a schema with the
// package's other DB tests. Skipped unless NEXARA_TEST_DB_URL names a throwaway DB.
func TestClusterObjectChanges(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	ids := []uuid.UUID{objChangesCluster, objChangesOther, objChangesDoomed, objChangesUnmarked}
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Every fixture table cascades from clusters.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, ids)
	}
	purge()
	defer purge()

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for i, id := range ids {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, "cluster0"+string(rune('1'+i)))
	}

	type job struct {
		id, node, target, schedule, rate, comment string
		disable, failCount                        int32
		runError                                  string
	}
	upsert := func(cluster uuid.UUID, j job) {
		t.Helper()
		if j.node == "" {
			j.node = "pve-01"
		}
		if err := q.UpsertReplicationJob(ctx, gen.UpsertReplicationJobParams{
			ClusterID: cluster, JobID: j.id, Guest: 100, Node: j.node, Target: j.target,
			FailCount: j.failCount, Error: j.runError,
			Schedule: j.schedule, Rate: j.rate, Disable: j.disable, Comment: j.comment,
		}); err != nil {
			t.Fatalf("upsert %s: %v", j.id, err)
		}
	}
	changes := func(cluster uuid.UUID) []gen.ClusterObjectChange {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT id, cluster_id, resource_type, resource_id, vmid, change, fields, detail,
				node, observed_by, last_seen_at, observed_at, explained FROM cluster_object_changes
			WHERE cluster_id = $1 ORDER BY observed_at, resource_id`, cluster)
		if err != nil {
			t.Fatalf("read changes: %v", err)
		}
		defer rows.Close()
		var out []gen.ClusterObjectChange
		for rows.Next() {
			var c gen.ClusterObjectChange
			if err := rows.Scan(&c.ID, &c.ClusterID, &c.ResourceType, &c.ResourceID, &c.Vmid, &c.Change, &c.Fields, &c.Detail,
				&c.Node, &c.ObservedBy, &c.LastSeenAt, &c.ObservedAt, &c.Explained); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, c)
		}
		return out
	}
	expectN := func(step string, cluster uuid.UUID, n int) []gen.ClusterObjectChange {
		t.Helper()
		got := changes(cluster)
		if len(got) != n {
			t.Fatalf("%s: %d changes recorded, want %d: %+v", step, len(got), n, got)
		}
		return got
	}
	detail := func(c gen.ClusterObjectChange) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(c.Detail, &m); err != nil {
			t.Fatalf("detail %s: %v", c.Detail, err)
		}
		return m
	}

	// ── The replication triggers.
	base := job{id: "100-0", target: "pve-02", schedule: "*/15", rate: "10", comment: "nightly"}
	other := job{id: "101-0", target: "pve-03", schedule: "*/30"}
	upsert(objChangesCluster, base)
	upsert(objChangesCluster, other)
	expectN("the first read of a cluster, before its baseline", objChangesCluster, 0)

	if err := q.MarkClusterObjectBaseline(ctx, gen.MarkClusterObjectBaselineParams{
		ClusterID: objChangesCluster, ResourceType: "replication"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	base.failCount, base.runError = 5, "connection refused"
	upsert(objChangesCluster, base)
	upsert(objChangesCluster, other)
	expectN("run state alone: fail count and error", objChangesCluster, 0)

	// A guest's migration swaps a job's source and target; it is not an edit.
	base.node, base.target = "pve-02", "pve-01"
	upsert(objChangesCluster, base)
	expectN("a migration swapping the job's source and target", objChangesCluster, 0)
	base.node, base.target = "pve-01", "pve-02"
	upsert(objChangesCluster, base)

	// A job that appears was absent when the last pass finished reading: that is
	// when it was last seen as it was, and each pass moves it on.
	exec("the last read", `UPDATE cluster_object_baselines SET last_read_at = now() - interval '2 minutes'
		WHERE cluster_id = $1 AND resource_type = 'replication'`, objChangesCluster)
	upsert(objChangesCluster, job{id: "102-0", target: "pve-02", schedule: "hourly"})
	got := expectN("a job appears after the baseline", objChangesCluster, 1)
	if age := time.Since(got[0].LastSeenAt); age < time.Minute+50*time.Second || age > 3*time.Minute {
		t.Errorf("the added job's last_seen_at is %v ago, want the previous read, 2 minutes ago", age)
	}
	var movedOn bool
	if err := q.MarkClusterObjectBaseline(ctx, gen.MarkClusterObjectBaselineParams{ClusterID: objChangesCluster, ResourceType: "replication"}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT last_read_at > now() - interval '1 minute' FROM cluster_object_baselines
		WHERE cluster_id = $1 AND resource_type = 'replication'`, objChangesCluster).Scan(&movedOn); err != nil || !movedOn {
		t.Errorf("a pass that wrote the listing whole leaves last_read_at at %v (err %v), want now", movedOn, err)
	}
	if c := got[0]; c.ResourceType != "replication" || c.ResourceID != "102-0" || c.Change != "added" ||
		detail(c)["target"] != "pve-02" || c.Explained.Valid || c.ObservedBy != "trigger" || len(c.Fields) != 0 {
		t.Errorf("added row = %+v (%s), want replication/102-0 added, unclassified", c, c.Detail)
	}

	base.schedule, base.comment = "*/5", "hourly check"
	upsert(objChangesCluster, base)
	got = expectN("a schedule and comment edit", objChangesCluster, 2)
	d := detail(got[1])
	if got[1].Change != "changed" || got[1].ResourceID != "100-0" || len(d) != 2 ||
		got[1].ObservedBy != "trigger" || !reflect.DeepEqual(got[1].Fields, []string{"schedule", "comment"}) ||
		!jsonEq(d["schedule"], map[string]any{"from": "*/15", "to": "*/5"}) ||
		!jsonEq(d["comment"], map[string]any{"from": "nightly", "to": "hourly check"}) {
		t.Errorf("changed row = %s, want the schedule and the comment, each from and to", got[1].Detail)
	}

	// Pre-upgrade rows have NULL where the configuration is now stored; the
	// first reading of it is not a change.
	exec("pre-upgrade job", `INSERT INTO replication_jobs (cluster_id, job_id, guest, node, target)
		VALUES ($1, '200-0', 200, 'pve-01', 'pve-02')`, objChangesOther)
	if err := q.MarkClusterObjectBaseline(ctx, gen.MarkClusterObjectBaselineParams{
		ClusterID: objChangesOther, ResourceType: "replication"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	upsert(objChangesOther, job{id: "200-0", target: "pve-02", schedule: "*/15", rate: "10", comment: "x"})
	expectN("the first reading of a job from before 000125", objChangesOther, 0)

	exec("age a job", `UPDATE replication_jobs SET last_seen_at = now() - interval '20 minutes'
		WHERE cluster_id = $1 AND job_id = '101-0'`, objChangesCluster)
	if err := q.DeleteStaleReplicationJobs(ctx, objChangesCluster); err != nil {
		t.Fatalf("delete stale: %v", err)
	}
	got = expectN("a job gone", objChangesCluster, 3)
	if c := got[2]; c.Change != "removed" || c.ResourceID != "101-0" || detail(c)["target"] != "pve-03" ||
		time.Since(c.LastSeenAt) < 19*time.Minute {
		t.Errorf("removed row = %+v (%s), want 101-0 removed, last seen 20 minutes ago", c, c.Detail)
	}

	// Never baselined: nothing appearing or going is a change. And a cluster
	// that is deleted takes its jobs with it without a row left pointing at it.
	upsert(objChangesUnmarked, job{id: "300-0", target: "pve-02"})
	exec("age", `UPDATE replication_jobs SET last_seen_at = now() - interval '20 minutes' WHERE cluster_id = $1`, objChangesUnmarked)
	if err := q.DeleteStaleReplicationJobs(ctx, objChangesUnmarked); err != nil {
		t.Fatalf("delete stale: %v", err)
	}
	expectN("a job gone before any baseline", objChangesUnmarked, 0)

	upsert(objChangesDoomed, job{id: "400-0", target: "pve-02"})
	if err := q.MarkClusterObjectBaseline(ctx, gen.MarkClusterObjectBaselineParams{
		ClusterID: objChangesDoomed, ResourceType: "replication"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	upsert(objChangesDoomed, job{id: "401-0", target: "pve-02"})
	expectN("a job added", objChangesDoomed, 1)
	exec("delete the cluster", `DELETE FROM clusters WHERE id = $1`, objChangesDoomed)
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cluster_object_changes WHERE cluster_id = $1`, objChangesDoomed).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d changes left for a deleted cluster (err %v)", left, err)
	}

	// ── The read, and what the sweep leaves. The classification has its own test.
	now := time.Now().UTC().Truncate(time.Second)
	ago := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }
	seed := func(rtype, rid string, explained pgtype.Bool, observed time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec("change "+rtype+"/"+rid, `INSERT INTO cluster_object_changes
			(id, cluster_id, resource_type, resource_id, change, observed_by, last_seen_at, observed_at, explained)
			VALUES ($1, $2, $3, $4, 'changed', 'ceph', $5, $5, $6)`, id, objChangesCluster, rtype, rid, observed, explained)
		return id
	}
	shownFalse, shownTrue, undecided := pgtype.Bool{Bool: false, Valid: true}, pgtype.Bool{Bool: true, Valid: true}, pgtype.Bool{}
	for _, rid := range []string{"111-0", "112-0", "113-0", "115-0"} {
		seed("replication", rid, shownFalse, ago(10))
	}
	explainedRow := seed("replication", "110-0", shownTrue, ago(10))
	seed("replication", "114-0", undecided, ago(1))
	seed("ceph_osd", "10", shownFalse, ago(10))

	// The observer's path: a guest's configuration edited in Proxmox, named by
	// field, with the vmid beside the audit identity. The observer decides its own.
	if err := q.InsertClusterObjectChange(ctx, gen.InsertClusterObjectChangeParams{
		ClusterID: objChangesCluster, ResourceType: "vm", ResourceID: "100",
		Vmid: pgtype.Int4{Int32: 100, Valid: true}, Change: "changed", Fields: []string{"memory", "net0"},
		Detail: json.RawMessage(`{}`), ObservedBy: "observer", LastSeenAt: ago(10.5),
	}); err != nil {
		t.Fatalf("insert a guest change: %v", err)
	}
	exec("the observer decides", `UPDATE cluster_object_changes SET explained = false
		WHERE cluster_id = $1 AND resource_type = 'vm'`, objChangesCluster)

	// The read shows only what nothing explained, of the kinds asked for. (The
	// changes the triggers recorded just now are not classified and are not shown.)
	rows, err := q.ListClusterObjectChanges(ctx, gen.ListClusterObjectChangesParams{
		ClusterID: objChangesCluster, ResourceTypes: []string{"replication"}, RowLimit: 50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	shown := make([]string, 0, len(rows))
	for _, r := range rows {
		shown = append(shown, r.ResourceID)
	}
	if len(rows) != 4 || rows[0].Total != 4 {
		t.Errorf("list shows %v (total %d), want the four decided unexplained", shown, rows[0].Total)
	}
	list := func(limit int32, since time.Time) []gen.ListClusterObjectChangesRow {
		t.Helper()
		out, err := q.ListClusterObjectChanges(ctx, gen.ListClusterObjectChangesParams{
			ClusterID: objChangesCluster, ResourceTypes: []string{"replication"}, RowLimit: limit,
			Since: pgtype.Timestamptz{Time: since, Valid: true},
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return out
	}
	if page := list(2, ago(11)); len(page) != 2 || page[0].Total != 4 {
		t.Errorf("a page of two = %+v, want 2 rows of a total of 4", page)
	}
	if recent := list(50, ago(9)); len(recent) != 0 {
		t.Errorf("list since 9 minutes ago = %d rows, want none (they were seen 10 minutes ago)", len(recent))
	}
	one, err := q.ListClusterObjectChanges(ctx, gen.ListClusterObjectChangesParams{
		ClusterID: objChangesCluster, ResourceTypes: []string{"replication", "ceph_osd"},
		ResourceID: pgtype.Text{String: "10", Valid: true}, RowLimit: 50,
	})
	if err != nil || len(one) != 1 || one[0].ResourceType != "ceph_osd" {
		t.Errorf("list for one object = %+v (err %v), want OSD 10 alone", one, err)
	}
	guests, err := q.ListClusterObjectChanges(ctx, gen.ListClusterObjectChangesParams{
		ClusterID: objChangesCluster, ResourceTypes: []string{"vm"}, RowLimit: 50,
	})
	if err != nil || len(guests) != 1 || !guests[0].Vmid.Valid || guests[0].Vmid.Int32 != 100 ||
		guests[0].ObservedBy != "observer" || !reflect.DeepEqual(guests[0].Fields, []string{"memory", "net0"}) {
		t.Errorf("list of a guest's changes = %+v (err %v), want the observer's row with its vmid and field names", guests, err)
	}

	// The task-history sweep leaves every change alone, explained or not: an
	// explained one stays hidden, with the id of the audit row that explained it.
	exec("age", `UPDATE cluster_object_changes SET observed_at = now() - interval '3 hours' WHERE cluster_id = $1`, objChangesCluster)
	count := func() (n int) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM cluster_object_changes WHERE cluster_id = $1`, objChangesCluster).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := count()
	if err := q.DeleteCompletedTasks(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if after := count(); after != before || before < 10 {
		t.Errorf("%d changes after the sweep, %d before: want every one kept, the explained one (%s) among them", after, before, explainedRow)
	}
}

// jsonEq compares two decoded JSON values.
func jsonEq(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
