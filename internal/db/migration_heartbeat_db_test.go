package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/migration"
)

var heartbeatClusterID = uuid.MustParse("b5000000-0000-4000-8000-00000000000f")

// A job a stopped Nexara left "migrating" stayed so, as nothing else takes a
// started job. Only a stopped heartbeat says nothing follows a job (more than one
// instance may serve one database), and only one seen lapsed twice, a minute
// apart (after a database outage the first look can come before a live stamp).
// Not parallel: it writes to the shared schema. Skipped without NEXARA_TEST_DB_URL.
func TestMigrationHeartbeat_TheSweepEndsOnlyWhatNothingFollows(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// The audit rows first: a cluster's delete leaves them (000122).
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = $1`, heartbeatClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, heartbeatClusterID) // jobs and tasks cascade
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
	        VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')`, heartbeatClusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	const upid = "UPID:pve-01:0000A1B2:00000001:6A8CE407:qmigrate:103:root@pam:"
	const crossUPID = "UPID:pve-01:0000A1B3:00000001:6A8CE408:qmigrate:104:root@pam:"
	ago := func(d time.Duration) *float64 { s := d.Seconds(); return &s }
	lapsed := ago(15 * time.Minute)
	jobs := []struct {
		name, status, upid, mode, kind string
		beat, updated                  *float64 // seconds ago; no beat: claimed by a release without heartbeats
		between                        bool     // stamped after the first look
		want, wantError                string
	}{
		{name: "stamped a minute ago", status: "migrating", upid: upid, beat: ago(time.Minute), updated: ago(time.Minute), want: "migrating"},
		{name: "stamped 15 minutes ago", status: "migrating", upid: upid, beat: lapsed, updated: lapsed, want: "failed",
			wantError: "may have moved all the same. Last task: " + upid},
		{name: "lapsed, stamped before the second look", status: "migrating", upid: upid, beat: lapsed, updated: lapsed,
			between: true, want: "migrating"},
		{name: "no task recorded", status: "migrating", beat: lapsed, updated: lapsed, want: "failed",
			wantError: "No Proxmox task was recorded"},
		{name: "a storage move", status: "migrating", upid: upid, mode: "storage", beat: lapsed, updated: lapsed,
			want: "failed", wantError: "the disks after it were not moved"},
		{name: "a guest and disk move", status: "migrating", upid: upid, mode: "both", beat: lapsed, updated: lapsed,
			want: "failed", wantError: "some of its disks"},
		{name: "a cross-cluster move", status: "migrating", upid: crossUPID, kind: "cross-cluster", beat: lapsed,
			updated: lapsed, want: "failed", wantError: "may have moved all the same"},
		{name: "no heartbeat, touched an hour ago", status: "migrating", upid: upid, updated: ago(time.Hour), want: "migrating"},
		{name: "no heartbeat, touched two days ago", status: "migrating", upid: upid, updated: ago(48 * time.Hour),
			want: "failed", wantError: "may have moved all the same"},
		{name: "a check begun two hours ago", status: "checking", updated: ago(2 * time.Hour), want: "pending"},
		{name: "a check begun a minute ago", status: "checking", updated: ago(time.Minute), want: "checking"},
		{name: "completed, an old heartbeat", status: "completed", upid: upid, beat: lapsed, updated: lapsed, want: "completed"},
	}
	ids := make([]uuid.UUID, len(jobs))
	for i, j := range jobs {
		mode, kind := j.mode, j.kind
		if mode == "" {
			mode = "live"
		}
		if kind == "" {
			kind = "intra-cluster"
		}
		if err := pool.QueryRow(ctx, `INSERT INTO migration_jobs (source_cluster_id, target_cluster_id, source_node,
		        vmid, status, upid, migration_mode, migration_type, heartbeat_at, updated_at)
		        VALUES ($1, $1, 'pve-01', $2, $3, $4, $5, $6,
		                CASE WHEN $7::float8 IS NULL THEN NULL ELSE now() - make_interval(secs => $7::float8) END,
		                now() - make_interval(secs => $8::float8))
		        RETURNING id`,
			heartbeatClusterID, 100+i, j.status, j.upid, mode, kind, j.beat, j.updated).Scan(&ids[i]); err != nil {
			t.Fatalf("seed %s: %v", j.name, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO task_history (cluster_id, user_id, upid, status, task_type)
	        VALUES ($1, $2, $3, 'running', 'qmigrate')`, heartbeatClusterID, systemUserID, crossUPID); err != nil {
		t.Fatalf("seed the cross-cluster task row: %v", err)
	}

	q := gen.New(pool)
	clock := time.Now()
	o := migration.NewOrchestrator(q, "", nil, nil)
	o.SetSweepClockForTesting(func() time.Time { return clock })
	sweep := func(after time.Duration) {
		t.Helper()
		clock = clock.Add(after)
		if err := o.ReconcileLapsed(ctx); err != nil {
			t.Fatalf("ReconcileLapsed: %v", err)
		}
	}
	status := func(id uuid.UUID) (gen.MigrationJob, error) { return q.GetMigrationJob(ctx, id) }

	sweep(0) // the first look ends nothing
	for i, j := range jobs {
		if got, err := status(ids[i]); err != nil || (j.status == "migrating" && got.Status != "migrating") {
			t.Errorf("%s after one look: %q (err %v), want still migrating", j.name, got.Status, err)
		}
		if j.between {
			if _, err := pool.Exec(ctx, `UPDATE migration_jobs SET heartbeat_at = now() WHERE id = $1`, ids[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	sweep(2 * time.Minute)
	for i, j := range jobs {
		got, err := status(ids[i])
		if err != nil || got.Status != j.want || (j.wantError == "") != (got.ErrorMessage == "") ||
			!strings.Contains(got.ErrorMessage, j.wantError) {
			t.Errorf("%s: status %q, error %q (err %v); want %q with an error saying %q",
				j.name, got.Status, got.ErrorMessage, err, j.want, j.wantError)
		}
	}
	var audited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE cluster_id = $1
	        AND action IN ('migrate_failed', 'cross_cluster_migrate_failed') AND user_id = $2`,
		heartbeatClusterID, systemUserID).Scan(&audited); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audited != 6 {
		t.Errorf("%d failed entries by the system user, want one per ended job (6)", audited)
	}
	var taskStatus, exitStatus string
	if err := pool.QueryRow(ctx, `SELECT status, exit_status FROM task_history WHERE upid = $1`, crossUPID).
		Scan(&taskStatus, &exitStatus); err != nil || taskStatus != "failed" || exitStatus != "interrupted" {
		t.Errorf("the cross-cluster task row: %q/%q (err %v), want failed/interrupted", taskStatus, exitStatus, err)
	}

	// The write repeats the test: neither a stamped job nor a finished one is ended.
	for _, i := range []int{0, len(jobs) - 1} {
		if n, err := q.FailLapsedMigrationJob(ctx, gen.FailLapsedMigrationJobParams{ErrorMessage: "x", ID: ids[i],
			LapseSecs: (10 * time.Minute).Seconds(), UnbeatenSecs: (24 * time.Hour).Seconds()}); err != nil || n != 0 {
			t.Errorf("failing %s: %d rows (err %v), want 0", jobs[i].name, n, err)
		}
	}
	// A job that lapses again after a stamp needs two new looks, a minute apart and
	// with no failed pass between them: a database that comes and goes must not
	// stand in for the second look.
	if _, err := pool.Exec(ctx, `UPDATE migration_jobs SET heartbeat_at = now() - interval '15 minutes' WHERE id = $1`,
		ids[2]); err != nil {
		t.Fatal(err)
	}
	stillRunning := func(after string) {
		t.Helper()
		if got, err := status(ids[2]); err != nil || got.Status != "migrating" {
			t.Errorf("lapsed again, %s: %q (err %v), want still migrating", after, got.Status, err)
		}
	}
	sweep(2 * time.Minute)
	stillRunning("after one new look")
	sweep(30 * time.Second)
	stillRunning("half a minute after it")
	failed, cancel := context.WithCancel(ctx)
	cancel()
	if err := o.ReconcileLapsed(failed); err == nil {
		t.Fatal("a pass on a cancelled context succeeded")
	}
	sweep(2 * time.Minute)
	stillRunning("on the first look after a failed pass")
	sweep(2 * time.Minute)
	if got, err := status(ids[2]); err != nil || got.Status != "failed" {
		t.Errorf("lapsed again, after two new looks: %q (err %v), want failed", got.Status, err)
	}

	// The claim starts the heartbeat.
	var pending uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO migration_jobs (source_cluster_id, target_cluster_id, source_node, vmid)
	        VALUES ($1, $1, 'pve-01', 120) RETURNING id`, heartbeatClusterID).Scan(&pending); err != nil {
		t.Fatalf("seed a pending job: %v", err)
	}
	if n, err := q.ClaimMigrationJob(ctx, pending); err != nil || n != 1 {
		t.Fatalf("claim: %d rows (err %v)", n, err)
	}
	if got, err := q.GetMigrationJob(ctx, pending); err != nil || !got.HeartbeatAt.Valid {
		t.Errorf("a claimed job's heartbeat %+v (err %v), want stamped", got.HeartbeatAt, err)
	}
}
