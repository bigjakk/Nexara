package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// up-front purge finds.
var (
	schedReconcileUser    = uuid.MustParse("97000000-0000-4000-8000-000000000001")
	schedReconcileCluster = uuid.MustParse("97000000-0000-4000-8000-00000000000a")
	schedReconcileOther   = uuid.MustParse("97000000-0000-4000-8000-00000000000b")
)

// The last_status values the scheduler hands ReconcileDispatchedScheduledTasks
// (internal/scheduler, the runStatus* constants — pinned to the Schedules tab
// by TestRunStatusVocabularyMatchesTheSPA, and to what a dispatch writes by
// TestSettleDispatchedRuns_LooksForWhatADispatchWrites).
var reconcileStatuses = gen.ReconcileDispatchedScheduledTasksParams{
	DispatchedStatus: "dispatched",
	SucceededStatus:  "success",
	FailedStatus:     "failed",
}

// schedUPID is a synthetic UPID for fixture n: placeholder node and token, and
// a pid range no other fixture in this package uses (task_history.upid is
// unique across the whole table).
func schedUPID(n int) string {
	return fmt.Sprintf("UPID:pve-01:0000C%03d:00000001:66000001:qmsnapshot:100:nexara@pve!api:", n)
}

// parkedMessage is what parkUnschedulableTask writes for a dispatched run of a
// schedule that can never fire again.
const parkedMessage = `disabled: cron expression never comes round: "0 8 31 4 *"`

// unknownOutcome is what a run settles with when the collector gave up on its
// task (exit_status 'vanished' after staleTaskGrace with no status).
const unknownOutcome = "Proxmox never reported how this task ended; Nexara stopped following it after 24 hours"

// TestReconcileDispatchedScheduledTasks_SettlesEachRunFromItsOwnTask drives the
// reconcile against the real schema, with every row written through the same
// generated queries production uses: UpdateTaskLastRun and
// DisableScheduledTaskForBadSchedule for the schedules, InsertTaskHistory /
// InsertExternalTaskHistory and the collector's ReconcileTaskHistory for the
// tasks.
//
// Each schedule differs from a plain dispatched run in ONE respect, so a
// failure names the rule that broke:
//
//	succeeded        its task completed                       → success
//	failed           its task failed                          → failed, Proxmox's exit status
//	stillRunning     its task has not ended                   → untouched
//	superseded       an OLDER run's task failed; its own runs → untouched
//	offsite          its UPID's task is on the other cluster  → untouched
//	external         task ingested by the collector, not
//	                 recorded by the scheduler, WARNINGS: 1   → success
//	parkedFailed     disabled, its task failed                → failed, "<exit>; disabled: …"
//	parkedSucceeded  disabled, its task completed             → success, "disabled: …" kept
//	emptyExit        task failed with no exit status          → failed, a fixed reason
//	stoppedByHand    task set to 'stopped' by hand            → untouched
//	neverDispatched  failed before dispatch, no UPID          → untouched
//	vanished         the collector gave up on its task        → failed, "outcome unknown"
//
// superseded is the late-finishing-run case: a run that finishes after the
// next one has started must not write its outcome over the newer run's.
// vanished must settle — left dispatched, it would read Running until the
// next run — but not with the bare sentinel: the collector wrote it because
// Proxmox stopped reporting, which is not evidence that the task failed.
// offsite is the cluster predicate's own fixture — every other task sits on
// the schedule's cluster. neverDispatched is there so a query that settled
// rows by some other key than last_upid would have a failed row to trample.
//
// The second pass is the idempotence check, and the status predicate's
// fixture: settled rows no longer read 'dispatched', so nothing matches again.
// Without the predicate the failed rows would gain another copy of their exit
// status on every tick.
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it).
// Seeds and deletes its own rows; never migrates the schema down.
func TestReconcileDispatchedScheduledTasks_SettlesEachRunFromItsOwnTask(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := schedReconcilePurge(pool.Exec)
	purge()
	defer purge()
	seedSchedReconcileScope(ctx, t, pool.Exec)

	q := gen.New(pool)
	s := &schedFixtures{t: t, ctx: ctx, q: q}

	succeeded := s.dispatched(schedReconcileCluster, schedUPID(1))
	s.task(schedReconcileCluster, schedUPID(1), "completed", "OK")

	failed := s.dispatched(schedReconcileCluster, schedUPID(2))
	s.task(schedReconcileCluster, schedUPID(2), "failed", "snapshot name 'nightly-20260926-020000' already used")

	stillRunning := s.dispatched(schedReconcileCluster, schedUPID(3))
	s.task(schedReconcileCluster, schedUPID(3), "running", "")

	superseded := s.dispatched(schedReconcileCluster, schedUPID(5))
	s.task(schedReconcileCluster, schedUPID(4), "failed", "VM 100 is locked (snapshot)")
	s.task(schedReconcileCluster, schedUPID(5), "running", "")

	offsite := s.dispatched(schedReconcileCluster, schedUPID(6))
	s.task(schedReconcileOther, schedUPID(6), "completed", "OK")

	external := s.dispatched(schedReconcileCluster, schedUPID(7))
	if err := q.InsertExternalTaskHistory(ctx, gen.InsertExternalTaskHistoryParams{
		ClusterID:   schedReconcileCluster,
		UserID:      auth.SystemUserID,
		Upid:        schedUPID(7),
		Description: "snapshot 100",
		Status:      "completed",
		ExitStatus:  "WARNINGS: 1",
		Node:        "pve-01",
		TaskType:    "qmsnapshot",
		StartedAt:   time.Now().Add(-time.Minute),
		FinishedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		t.Fatalf("InsertExternalTaskHistory: %v", err)
	}

	parkedFailed := s.parked(schedUPID(8))
	s.task(schedReconcileCluster, schedUPID(8), "failed", "VM 100 is locked (backup)")

	parkedSucceeded := s.parked(schedUPID(9))
	s.task(schedReconcileCluster, schedUPID(9), "completed", "OK")

	// Only a hand-made PUT /api/v1/tasks/:upid writes either of these; the
	// collector writes 'completed' or 'failed', and never an empty failure.
	emptyExit := s.dispatched(schedReconcileCluster, schedUPID(10))
	s.handMadeTask(schedUPID(10), "failed", "")
	stoppedByHand := s.dispatched(schedReconcileCluster, schedUPID(11))
	s.handMadeTask(schedUPID(11), "stopped", "OK")

	// What the collector's reconcile writes after staleTaskGrace with no
	// status from Proxmox (internal/collector, vanishedExitStatus).
	vanished := s.dispatched(schedReconcileCluster, schedUPID(12))
	s.task(schedReconcileCluster, schedUPID(12), "failed", "vanished")

	neverDispatched := s.schedule(schedReconcileCluster, true)
	if err := q.UpdateTaskLastRun(ctx, gen.UpdateTaskLastRunParams{
		ID:         neverDispatched,
		LastRunAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		NextRunAt:  pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		LastStatus: pgtype.Text{String: "failed", Valid: true},
		LastError:  pgtype.Text{String: "create client: connection refused", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateTaskLastRun (never dispatched): %v", err)
	}

	settled, err := q.ReconcileDispatchedScheduledTasks(ctx, reconcileStatuses)
	if err != nil {
		t.Fatalf("ReconcileDispatchedScheduledTasks: %v", err)
	}
	got := map[uuid.UUID]string{}
	for _, r := range settled {
		got[r.ID] = r.LastStatus.String
	}

	want := []struct {
		name      string
		id        uuid.UUID
		status    string
		lastError *string // nil wants NULL
		settled   bool    // whether this pass returned the row
	}{
		{"succeeded", succeeded, "success", nil, true},
		{"failed", failed, "failed", ptr("snapshot name 'nightly-20260926-020000' already used"), true},
		{"stillRunning", stillRunning, "dispatched", nil, false},
		{"superseded", superseded, "dispatched", nil, false},
		{"offsite", offsite, "dispatched", nil, false},
		{"external", external, "success", nil, true},
		{"parkedFailed", parkedFailed, "failed", ptr("VM 100 is locked (backup); " + parkedMessage), true},
		{"parkedSucceeded", parkedSucceeded, "success", ptr(parkedMessage), true},
		{"emptyExit", emptyExit, "failed", ptr("Proxmox task failed"), true},
		{"stoppedByHand", stoppedByHand, "dispatched", nil, false},
		{"neverDispatched", neverDispatched, "failed", ptr("create client: connection refused"), false},
		{"vanished", vanished, "failed", ptr(unknownOutcome), true},
	}

	for _, w := range want {
		row, err := q.GetScheduledTask(ctx, w.id)
		if err != nil {
			t.Fatalf("%s: GetScheduledTask: %v", w.name, err)
		}
		if row.LastStatus.String != w.status {
			t.Errorf("%s: last_status = %q, want %q", w.name, row.LastStatus.String, w.status)
		}
		switch {
		case w.lastError == nil && row.LastError.Valid:
			t.Errorf("%s: last_error = %q, want NULL", w.name, row.LastError.String)
		case w.lastError != nil && (!row.LastError.Valid || row.LastError.String != *w.lastError):
			t.Errorf("%s: last_error = %q (valid %v), want %q", w.name, row.LastError.String, row.LastError.Valid, *w.lastError)
		}
		if status, returned := got[w.id]; returned != w.settled {
			t.Errorf("%s: returned by the reconcile = %v (as %q), want %v", w.name, returned, status, w.settled)
		}
	}

	// The parked rows stay parked: settling the run says nothing about the
	// schedule, which still cannot fire.
	for _, id := range []uuid.UUID{parkedFailed, parkedSucceeded} {
		if row, err := q.GetScheduledTask(ctx, id); err != nil || row.Enabled {
			t.Errorf("a parked schedule is enabled = %v after its run settled (err %v), want disabled", row.Enabled, err)
		}
	}

	again, err := q.ReconcileDispatchedScheduledTasks(ctx, reconcileStatuses)
	if err != nil {
		t.Fatalf("ReconcileDispatchedScheduledTasks (second pass): %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a second pass settled %d rows again, want 0 — a settled row must not match", len(again))
	}
	if row, err := q.GetScheduledTask(ctx, failed); err != nil ||
		row.LastError.String != "snapshot name 'nightly-20260926-020000' already used" {
		t.Errorf("after a second pass the failed run's last_error = %q (err %v); it must not grow on every tick",
			row.LastError.String, err)
	}
}

// TestClaimDueTasks_ClearsThePreviousRunsUPIDAndError pins what a claim takes
// and what it leaves behind.
//
// What it takes: a due row whatever its previous run left — including one
// still reading 'dispatched', whose task has not finished. 'running' is the
// claim's in-flight marker and 'dispatched' is not, so a long task never holds
// its schedule out of its next slot; the status then follows the newer run,
// and the older run's outcome stays in task_history.
//
// What it leaves: 'running', with neither the previous run's error nor its
// UPID, which belong to that run — the error would show beside Running in the
// Schedules tab, and the UPID would name a task the row no longer describes.
//
// And the reconcile must not settle a claimed row. The cleared UPID already
// makes that impossible, so to test the reconcile's OWN status predicate the
// old UPID is put back by hand afterwards: the predicate is then the only thing
// standing between a run being started and the previous run's failure.
func TestClaimDueTasks_ClearsThePreviousRunsUPIDAndError(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := schedReconcilePurge(pool.Exec)
	purge()
	defer purge()
	seedSchedReconcileScope(ctx, t, pool.Exec)

	q := gen.New(pool)
	s := &schedFixtures{t: t, ctx: ctx, q: q}

	// A previous run that has settled: dispatched, its task failed.
	settled := s.dispatched(schedReconcileCluster, schedUPID(20))
	s.task(schedReconcileCluster, schedUPID(20), "failed", "VM 100 is locked (backup)")
	if _, err := q.ReconcileDispatchedScheduledTasks(ctx, reconcileStatuses); err != nil {
		t.Fatalf("ReconcileDispatchedScheduledTasks: %v", err)
	}
	// A previous run whose task is still going.
	inFlight := s.dispatched(schedReconcileCluster, schedUPID(21))
	s.task(schedReconcileCluster, schedUPID(21), "running", "")

	// Both due now. last_run_at was just written, well inside the claim's
	// stale window, so the stale-claim branch cannot take either row: each is
	// claimable through its status alone.
	if _, err := pool.Exec(ctx,
		`UPDATE scheduled_tasks SET next_run_at = now() - interval '1 minute' WHERE id = ANY($1)`,
		[]uuid.UUID{settled, inFlight}); err != nil {
		t.Fatalf("make the schedules due: %v", err)
	}
	for _, pre := range []struct {
		name   string
		id     uuid.UUID
		status string
	}{
		{"the settled run", settled, "failed"},
		{"the run still in flight", inFlight, "dispatched"},
	} {
		row, err := q.GetScheduledTask(ctx, pre.id)
		if err != nil || row.LastStatus.String != pre.status || !row.LastUpid.Valid ||
			!row.LastRunAt.Valid || time.Since(row.LastRunAt.Time) > time.Minute {
			t.Fatalf("precondition: %s reads %q (upid valid %v, last_run_at %v, err %v), want %q with "+
				"a UPID and a fresh last_run_at — otherwise nothing here tests what it says", pre.name,
				row.LastStatus.String, row.LastUpid.Valid, row.LastRunAt.Time, err, pre.status)
		}
	}
	if row, _ := q.GetScheduledTask(ctx, settled); !row.LastError.Valid {
		t.Fatal("precondition: the settled run carries no error, so there is none for the claim to clear")
	}

	claimed, err := q.ClaimDueTasks(ctx, gen.ClaimDueTasksParams{StaleSeconds: 600, GuardSeconds: 600})
	if err != nil {
		t.Fatalf("ClaimDueTasks: %v", err)
	}
	byID := map[uuid.UUID]gen.ScheduledTask{}
	for _, row := range claimed {
		byID[row.ID] = row
	}
	for _, c := range []struct {
		name string
		id   uuid.UUID
	}{
		{"the settled run", settled},
		{"the run still in flight", inFlight},
	} {
		row, ok := byID[c.id]
		if !ok {
			t.Errorf("a due schedule after %s was not claimed — only 'running' marks a run in flight", c.name)
			continue
		}
		if row.LastStatus.String != "running" || row.LastError.Valid || row.LastUpid.Valid {
			t.Errorf("claimed after %s: last_status %q, last_error %q (valid %v), last_upid %q (valid %v); "+
				"want running with no error and no UPID", c.name, row.LastStatus.String,
				row.LastError.String, row.LastError.Valid, row.LastUpid.String, row.LastUpid.Valid)
		}
	}

	// The old UPID back on the claimed row, pointing at a task that failed.
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET last_upid = $2 WHERE id = $1`,
		settled, schedUPID(20)); err != nil {
		t.Fatalf("restore the old UPID: %v", err)
	}
	if _, err := q.ReconcileDispatchedScheduledTasks(ctx, reconcileStatuses); err != nil {
		t.Fatalf("ReconcileDispatchedScheduledTasks after the claim: %v", err)
	}
	if row, err := q.GetScheduledTask(ctx, settled); err != nil || row.LastStatus.String != "running" {
		t.Errorf("after a reconcile the claimed row reads %q (err %v), want running — "+
			"a run being started must not be settled from the previous run's task", row.LastStatus.String, err)
	}
}

// TestMigration104_DownGivesThePreviousReleaseAStatusItReads pins 000104's down
// migration. The release before it never wrote 'dispatched' and shows every
// last_status but NULL and 'success' as Failed, so a rollback that left a run
// still waiting on its task reading 'dispatched' would show a failure that
// has not happened. The down migration rewrites it to 'success' — what that
// release itself wrote for a dispatched run — and must leave every other value
// alone.
//
// Round-trips the schema to 103 and back, so it needs the throwaway database
// the other tests here do.
func TestMigration104_DownGivesThePreviousReleaseAStatusItReads(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := schedReconcilePurge(pool.Exec)
	purge()
	defer purge()
	seedSchedReconcileScope(ctx, t, pool.Exec)

	rows := []struct {
		id        uuid.UUID
		status    *string
		lastError *string
		upid      *string
		wantDown  *string // last_status after the down migration
	}{
		{uuid.MustParse("97000000-0000-4000-8000-000000000101"), ptr("dispatched"), nil, ptr(schedUPID(30)), ptr("success")},
		{uuid.MustParse("97000000-0000-4000-8000-000000000102"), ptr("success"), nil, ptr(schedUPID(31)), ptr("success")},
		{uuid.MustParse("97000000-0000-4000-8000-000000000103"), ptr("failed"), ptr("VM 100 is locked (backup)"), nil, ptr("failed")},
		{uuid.MustParse("97000000-0000-4000-8000-000000000104"), ptr("running"), nil, nil, ptr("running")},
		{uuid.MustParse("97000000-0000-4000-8000-000000000105"), nil, nil, nil, nil},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO scheduled_tasks
			   (id, cluster_id, resource_type, resource_id, node, action, schedule, last_status, last_error, last_upid)
			 VALUES ($1, $2, 'vm', '100', 'pve-01', 'snapshot', '0 2 * * *', $3, $4, $5)`,
			r.id, schedReconcileCluster, r.status, r.lastError, r.upid); err != nil {
			t.Fatalf("seed %v: %v", r.id, err)
		}
	}

	if err := m.Migrate(103); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 103: %v", err)
	}
	for _, r := range rows {
		var status, lastError *string
		if err := pool.QueryRow(ctx, `SELECT last_status, last_error FROM scheduled_tasks WHERE id = $1`,
			r.id).Scan(&status, &lastError); err != nil {
			t.Fatalf("read %v at 103: %v", r.id, err)
		}
		if deref(status) != deref(r.wantDown) {
			t.Errorf("at 103, a row that read %q reads %q, want %q", deref(r.status), deref(status), deref(r.wantDown))
		}
		if deref(lastError) != deref(r.lastError) {
			t.Errorf("at 103, last_error of the %q row changed to %q", deref(r.status), deref(lastError))
		}
	}
	var cols int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'scheduled_tasks' AND column_name = 'last_upid'`,
	).Scan(&cols); err != nil || cols != 0 {
		t.Errorf("at 103 scheduled_tasks still has last_upid (%d, err %v)", cols, err)
	}

	if err := m.Migrate(104); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate back up to 104: %v", err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<NULL>"
	}
	return *s
}

// schedFixtures writes this file's fixtures through the generated queries.
type schedFixtures struct {
	t   *testing.T
	ctx context.Context
	q   *gen.Queries
}

// schedule inserts a snapshot schedule on cluster and returns its id. Its next
// run is an hour out, so a claim elsewhere in the package never takes it.
func (s *schedFixtures) schedule(cluster uuid.UUID, enabled bool) uuid.UUID {
	s.t.Helper()
	row, err := s.q.InsertScheduledTask(s.ctx, gen.InsertScheduledTaskParams{
		ClusterID:    cluster,
		ResourceType: "vm",
		ResourceID:   "100",
		Node:         "pve-01",
		Action:       "snapshot",
		Schedule:     "0 2 * * *",
		Params:       json.RawMessage(`{}`),
		Enabled:      enabled,
		NextRunAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		s.t.Fatalf("InsertScheduledTask: %v", err)
	}
	return row.ID
}

// dispatched is a schedule whose last run started the task upid, recorded the
// way finishTaskRun records it.
func (s *schedFixtures) dispatched(cluster uuid.UUID, upid string) uuid.UUID {
	s.t.Helper()
	id := s.schedule(cluster, true)
	if err := s.q.UpdateTaskLastRun(s.ctx, gen.UpdateTaskLastRunParams{
		ID:         id,
		LastRunAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		NextRunAt:  pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		LastStatus: pgtype.Text{String: reconcileStatuses.DispatchedStatus, Valid: true},
		LastUpid:   pgtype.Text{String: upid, Valid: true},
	}); err != nil {
		s.t.Fatalf("UpdateTaskLastRun: %v", err)
	}
	return id
}

// parked is a schedule disabled for a cron that can never fire, whose last
// run started the task upid — what parkUnschedulableTask records for it.
func (s *schedFixtures) parked(upid string) uuid.UUID {
	s.t.Helper()
	id := s.schedule(schedReconcileCluster, true)
	if err := s.q.DisableScheduledTaskForBadSchedule(s.ctx, gen.DisableScheduledTaskForBadScheduleParams{
		ID:         id,
		LastStatus: pgtype.Text{String: reconcileStatuses.DispatchedStatus, Valid: true},
		LastError:  pgtype.Text{String: parkedMessage, Valid: true},
		LastUpid:   pgtype.Text{String: upid, Valid: true},
	}); err != nil {
		s.t.Fatalf("DisableScheduledTaskForBadSchedule: %v", err)
	}
	return id
}

// task records upid on cluster the way the scheduler's trackTask does, then —
// unless it is still running — finalizes it the way the collector does.
func (s *schedFixtures) task(cluster uuid.UUID, upid, status, exitStatus string) {
	s.t.Helper()
	if _, err := s.q.InsertTaskHistory(s.ctx, gen.InsertTaskHistoryParams{
		ClusterID:   cluster,
		UserID:      schedReconcileUser,
		Upid:        upid,
		Description: "Scheduled snapshot",
		Status:      "running",
		Node:        "pve-01",
		TaskType:    "scheduled_snapshot",
	}); err != nil {
		s.t.Fatalf("InsertTaskHistory %s: %v", upid, err)
	}
	if status == "running" {
		return
	}
	if n, err := s.q.ReconcileTaskHistory(s.ctx, gen.ReconcileTaskHistoryParams{
		Upid:       upid,
		Status:     status,
		ExitStatus: exitStatus,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil || n != 1 {
		s.t.Fatalf("ReconcileTaskHistory %s: %d rows, err %v", upid, n, err)
	}
}

// handMadeTask records upid and then rewrites it through UpdateTaskHistory,
// the query behind PUT /api/v1/tasks/:upid.
func (s *schedFixtures) handMadeTask(upid, status, exitStatus string) {
	s.t.Helper()
	s.task(schedReconcileCluster, upid, "running", "")
	if err := s.q.UpdateTaskHistory(s.ctx, gen.UpdateTaskHistoryParams{
		Upid:       upid,
		Status:     status,
		ExitStatus: exitStatus,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		s.t.Fatalf("UpdateTaskHistory %s: %v", upid, err)
	}
}

// execFunc is pgxpool.Pool.Exec's shape, so the helpers below take the pool's
// method rather than the pool.
type execFunc = func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)

// seedSchedReconcileScope inserts the user and the two clusters the fixtures
// hang off.
func seedSchedReconcileScope(ctx context.Context, t *testing.T, exec execFunc) {
	t.Helper()
	if _, err := exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '') ON CONFLICT (id) DO NOTHING`,
		schedReconcileUser, "scheduled-task-reconcile-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{
		{schedReconcileCluster, "cluster01"},
		{schedReconcileOther, "cluster02"},
	} {
		if _, err := exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://pve.invalid:8006', 'tok', 'enc') ON CONFLICT (id) DO NOTHING`,
			c.id, c.name); err != nil {
			t.Fatalf("seed cluster %s: %v", c.name, err)
		}
	}
}

// schedReconcilePurge deletes every row this file seeds. scheduled_tasks and
// task_history cascade from clusters; the user goes last. Its own timeout,
// so it still runs after the test's context is gone.
func schedReconcilePurge(exec execFunc) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = exec(ctx, `DELETE FROM clusters WHERE id = ANY($1)`,
			[]uuid.UUID{schedReconcileCluster, schedReconcileOther})
		_, _ = exec(ctx, `DELETE FROM users WHERE id = $1`, schedReconcileUser)
	}
}

func ptr(s string) *string { return &s }
