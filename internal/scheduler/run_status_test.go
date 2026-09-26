package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// These tests pin the status a scheduled task's run leaves on its row. The
// recorder and the PVE stub they drive are in scheduler_test.go.
//
// The bug they exist for: executeTask wrote last_status 'success' as soon as
// the snapshot or reboot call returned. That call returns once Proxmox has
// forked the task's worker, and Proxmox refuses much of what it refuses inside
// the worker, so the schedule read Success while its runs failed — on
// 2026-09-26 a snapshot schedule's second run failed with "snapshot name '…'
// already used", visible only in the task history and a pve_task_failed alert.

// claimedTask is a scheduled_tasks row as ClaimDueTasks hands it to
// executeTask: claimed, so reading 'running', with the previous run's UPID
// and error already cleared by the claim.
func claimedTask(action, schedule string) db.ScheduledTask {
	return db.ScheduledTask{
		ID:           uuid.New(),
		ClusterID:    uuid.New(),
		ResourceType: "vm",
		ResourceID:   "100",
		Node:         "pve-01",
		Action:       action,
		Schedule:     schedule,
		Params:       []byte(`{}`),
		Enabled:      true,
		LastStatus:   pgtype.Text{String: "running", Valid: true},
	}
}

// recordRun runs task through executeTask against a stub Proxmox that answers
// status and body, and returns what the run recorded.
func recordRun(t *testing.T, task db.ScheduledTask, status int, body string) *taskRunRecorder {
	t.Helper()
	srv, _ := newPVEStubAnswering(t, status, body)
	recorder := &taskRunRecorder{}
	newRecordingScheduler(recorder).executeTask(context.Background(), newStubPVEClient(t, srv.URL), task)
	return recorder
}

// lockedBody is how Proxmox refuses a call outright, before any task exists.
const lockedBody = "VM 100 is locked (backup)"

// TestExecuteTask_RecordsWhetherTheRunReachedProxmox is the regression test for
// the status a run records as it leaves the scheduler.
//
// A run that reached Proxmox records 'dispatched' and the UPID of the task it
// started — not 'success', because nothing is known yet about how the task
// ends, and the UPID is all RunScheduledTaskReconcile needs to settle it later.
// A run that did not reach Proxmox records 'failed' and its reason, with
// last_upid written NULL: a run with no task must give the reconcile nothing to
// follow.
//
// The refusal is an error status from the stub rather than a check the client
// makes, so the failure crosses the wire the way a real one does.
func TestExecuteTask_RecordsWhetherTheRunReachedProxmox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action string
		// status and body are the stub's answer to the run's call.
		status int
		body   string

		wantStatus string
		// wantUPID is the task the row must point at; "" wants NULL.
		wantUPID string
		// wantError must appear in last_error; "" wants NULL.
		wantError string
		// wantHistory is how many task_history rows trackTask inserts. The
		// task a run started is recorded there, and that row is what the
		// reconcile reads; a run with no task has none.
		wantHistory int
	}{
		{
			name: "a snapshot Proxmox started", action: "snapshot",
			status: http.StatusOK, body: upidBody(snapshotUPID),
			wantStatus: runStatusDispatched, wantUPID: snapshotUPID, wantHistory: 1,
		},
		{
			name: "a reboot Proxmox started", action: "reboot",
			status: http.StatusOK, body: upidBody(rebootUPID),
			wantStatus: runStatusDispatched, wantUPID: rebootUPID, wantHistory: 1,
		},
		{
			name: "a call Proxmox refused", action: "snapshot",
			status: http.StatusInternalServerError, body: lockedBody,
			wantStatus: runStatusFailed, wantError: lockedBody,
		},
		// Snapshot and reboot always start a task, so this is not expected.
		// If it happens there is nothing to follow, and the row must neither
		// read as under way forever nor as a success nobody saw.
		{
			name: "a call answered with no task", action: "reboot",
			status: http.StatusOK, body: `{"data":""}`,
			wantStatus: runStatusFailed, wantError: noTaskIDError,
		},
		{
			name: "an action the scheduler has no branch for", action: "backup",
			status: http.StatusOK, body: upidBody(snapshotUPID),
			wantStatus: runStatusFailed, wantError: "unsupported action: backup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := recordRun(t, claimedTask(tt.action, "0 2 * * *"), tt.status, tt.body)
			w := recorder.onlyWrite(t)
			if w.query != "UpdateTaskLastRun" {
				t.Fatalf("the run was recorded by %s, want UpdateTaskLastRun", w.query)
			}

			if status, _ := w.text(t, "last_status"); status != tt.wantStatus {
				t.Errorf("last_status = %q, want %q", status, tt.wantStatus)
			}
			upid, upidSet := w.text(t, "last_upid")
			switch {
			case tt.wantUPID == "" && upidSet:
				t.Errorf("last_upid = %q, want NULL — a run with no task must give the reconcile nothing to follow",
					upid)
			case tt.wantUPID != "" && (!upidSet || upid != tt.wantUPID):
				t.Errorf("last_upid = %q (set %v), want %q — the task this run started, which the reconcile "+
					"settles the row from", upid, upidSet, tt.wantUPID)
			}
			msg, msgSet := w.text(t, "last_error")
			switch {
			case tt.wantError == "" && msgSet:
				t.Errorf("last_error = %q, want NULL for a run whose task has not ended", msg)
			case tt.wantError != "" && (!msgSet || !strings.Contains(msg, tt.wantError)):
				t.Errorf("last_error = %q (set %v), want it to carry %q", msg, msgSet, tt.wantError)
			}
			if recorder.historyInserts != tt.wantHistory {
				t.Errorf("trackTask recorded %d task_history rows, want %d", recorder.historyInserts, tt.wantHistory)
			}
		})
	}
}

// TestExecuteTask_ParksARunWithItsOwnOutcome pins what the parking path records
// for each kind of run, now that a run can end after it is recorded.
//
// A task whose cron can never fire again is disabled as its run finishes
// (parkUnschedulableTask), and the run and the schedule stay independent. A run
// that reached Proxmox has not ended, so it is parked as 'dispatched' with its
// UPID, and the reconcile settles it like any other; last_error carries only
// the schedule's problem, since the run has no error of its own yet. Writing
// 'success' there would be this change's bug again, and dropping the UPID would
// leave the row reading Running for good — a disabled task has no next run to
// overwrite it. A run that failed before dispatch keeps its own reason in front
// of the schedule's.
//
// "0 8 31 4 *" parses and never fires: April has no 31st.
func TestExecuteTask_ParksARunWithItsOwnOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus string
		// wantUPID is the task the row must point at; "" wants NULL.
		wantUPID string
		// wantRunError is what must precede the schedule's "disabled: …"
		// in last_error; "" wants nothing there.
		wantRunError string
	}{
		{
			name: "a run that reached Proxmox", status: http.StatusOK, body: upidBody(snapshotUPID),
			wantStatus: runStatusDispatched, wantUPID: snapshotUPID,
		},
		{
			name: "a run Proxmox refused", status: http.StatusInternalServerError, body: lockedBody,
			wantStatus: runStatusFailed, wantRunError: lockedBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := recordRun(t, claimedTask("snapshot", "0 8 31 4 *"), tt.status, tt.body)
			w := recorder.onlyWrite(t)
			if w.query != "DisableScheduledTaskForBadSchedule" {
				t.Fatalf("the run was recorded by %s, want DisableScheduledTaskForBadSchedule — "+
					"a schedule that can never fire must be parked", w.query)
			}

			if status, _ := w.text(t, "last_status"); status != tt.wantStatus {
				t.Errorf("last_status = %q, want %q — the run's own outcome, not the schedule's", status, tt.wantStatus)
			}
			upid, upidSet := w.text(t, "last_upid")
			switch {
			case tt.wantUPID == "" && upidSet:
				t.Errorf("last_upid = %q, want NULL for a run with no task", upid)
			case tt.wantUPID != "" && (!upidSet || upid != tt.wantUPID):
				t.Errorf("last_upid = %q (set %v), want %q — without it nothing ever settles this "+
					"row, and a disabled task has no next run to replace it", upid, upidSet, tt.wantUPID)
			}
			msg, _ := w.text(t, "last_error")
			runPart, _, found := strings.Cut(msg, "disabled: ")
			if !found {
				t.Fatalf("last_error = %q, want it to say the schedule was disabled", msg)
			}
			switch {
			case tt.wantRunError == "" && runPart != "":
				t.Errorf("last_error = %q, want only the schedule's reason: a dispatched run has no error of "+
					"its own yet, and the reconcile puts Proxmox's in front if the task fails", msg)
			case tt.wantRunError != "" && !strings.Contains(runPart, tt.wantRunError):
				t.Errorf("last_error = %q, want the run's own reason %q in front of the schedule's", msg, tt.wantRunError)
			}
		})
	}
}

// fakeSettler is a dispatchedRunSettler that records the params it was given.
type fakeSettler struct {
	got   db.ReconcileDispatchedScheduledTasksParams
	calls int
	// settled is what the query answers: the rows it settled.
	settled []db.ReconcileDispatchedScheduledTasksRow
}

func (f *fakeSettler) ReconcileDispatchedScheduledTasks(
	_ context.Context, arg db.ReconcileDispatchedScheduledTasksParams,
) ([]db.ReconcileDispatchedScheduledTasksRow, error) {
	f.got = arg
	f.calls++
	return f.settled, nil
}

// TestSettleDispatchedRuns_LooksForWhatADispatchWrites pins the pairing the
// reconcile rests on.
//
// ReconcileDispatchedScheduledTasks names none of the scheduler's statuses; it
// is handed them. So the only way it can miss a dispatched run is to be handed
// a value other than the one executeTask recorded — and then every run would
// read Running for good, with no error anywhere. A task that failed must
// settle to the status a run that never reached Proxmox records, so the two
// failures read the same, and a task that succeeded to the one the Schedules
// tab shows as Success (runStatusSucceeded, which
// TestRunStatusVocabularyMatchesTheSPA pins to the tab).
//
// The dispatch side is read from what executeTask actually writes rather than
// from the constants, so the test holds whichever constant either side is
// changed to use.
func TestSettleDispatchedRuns_LooksForWhatADispatchWrites(t *testing.T) {
	t.Parallel()

	dispatched := recordRun(t, claimedTask("snapshot", "0 2 * * *"), http.StatusOK, upidBody(snapshotUPID))
	refused := recordRun(t, claimedTask("snapshot", "0 2 * * *"), http.StatusInternalServerError, lockedBody)
	dispatchedStatus, _ := dispatched.onlyWrite(t).text(t, "last_status")
	failedStatus, _ := refused.onlyWrite(t).text(t, "last_status")

	settler := &fakeSettler{}
	if _, err := settleDispatchedRunsOn(context.Background(), settler); err != nil {
		t.Fatalf("settleDispatchedRunsOn: %v", err)
	}
	if settler.calls != 1 {
		t.Fatalf("the reconcile query ran %d times, want once", settler.calls)
	}
	if settler.got.DispatchedStatus != dispatchedStatus {
		t.Errorf("the reconcile looks for last_status %q, but a dispatched run records %q — "+
			"no run would ever be settled", settler.got.DispatchedStatus, dispatchedStatus)
	}
	if settler.got.FailedStatus != failedStatus {
		t.Errorf("a failed task settles to %q, but a run that failed before dispatch records %q",
			settler.got.FailedStatus, failedStatus)
	}
	if settler.got.SucceededStatus != runStatusSucceeded {
		t.Errorf("a task that succeeded settles to %q, want %q — the value the Schedules tab shows as Success",
			settler.got.SucceededStatus, runStatusSucceeded)
	}
}

// scheduleStatusTS is the Schedules tab's copy of the last_status vocabulary.
const scheduleStatusTS = "../../frontend/src/features/vms/lib/schedule-status.ts"

var (
	tsScheduleStatusRe = regexp.MustCompile(`export type ScheduleLastStatus\s*=([^;]+);`)
	// A member is one whole `|`-separated alternative that is exactly one
	// double-quoted string, so a reference to another type or an Exclude<…>
	// fails the parse instead of contributing whatever quoted words it holds.
	tsLiteralMemberRe = regexp.MustCompile(`^"([^"\\]+)"$`)
	// tsCommentsRe strips comments before the union is looked for, so a
	// commented-out member cannot stand in for a real one. It does not know
	// string literals: a "//" inside one would be read as a comment, which
	// fails loudly — the union goes missing — rather than silently.
	tsCommentsRe = regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)
	// claimSetRe is the literal ClaimDueTasks assigns to last_status.
	claimSetRe = regexp.MustCompile(`(?i)\bSET\s+last_status\s*=\s*'([^']+)'`)
)

// TestRunStatusVocabularyMatchesTheSPA pins the last_status values the
// scheduler writes against the union the Schedules tab reads them through.
//
// They are two copies with nothing else comparing them, and the drift is
// silent in the direction that matters: rename 'dispatched' here and the tab,
// which shows any value it does not know as Failed, reports every run under
// way as a failure. tsc holds the tab's table to its union; this holds the
// union to the scheduler.
//
// 'running' is written by ClaimDueTasks as a literal, not through
// runStatusClaimed, so the constant is checked against that query's literal
// before the list is compared.
func TestRunStatusVocabularyMatchesTheSPA(t *testing.T) {
	t.Parallel()

	if claimed := claimMarker(t); claimed != runStatusClaimed {
		t.Errorf("ClaimDueTasks writes last_status %q but runStatusClaimed is %q", claimed, runStatusClaimed)
	}
	backend := RunStatusValues()
	frontend := scheduleStatusUnion(t)

	slices.Sort(frontend)
	if !slices.Equal(backend, frontend) {
		t.Errorf("last_status vocabularies differ\n  scheduler:     %v\n  Schedules tab: %v (%s)",
			backend, frontend, scheduleStatusTS)
	}
}

// claimMarker is the last_status ClaimDueTasks writes when it claims a run.
func claimMarker(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../queries/scheduled_tasks.sql")
	if err != nil {
		t.Fatalf("read scheduled_tasks.sql: %v", err)
	}
	_, rest, found := strings.Cut(string(raw), "-- name: ClaimDueTasks ")
	if !found {
		t.Fatal("ClaimDueTasks not found in queries/scheduled_tasks.sql — if it was renamed, follow it here")
	}
	// The query's code, without its comments, up to the end of the statement:
	// a comment can hold a ';' or an example assignment.
	var code strings.Builder
	for _, line := range strings.Split(rest, "\n") {
		line, _, _ = strings.Cut(line, "--")
		code.WriteString(line + "\n")
		if strings.Contains(line, ";") {
			break
		}
	}
	m := claimSetRe.FindStringSubmatch(code.String())
	if m == nil {
		t.Fatalf("ClaimDueTasks assigns no last_status literal:\n%s", code.String())
	}
	return m[1]
}

// scheduleStatusUnion is the members of ScheduleLastStatus.
func scheduleStatusUnion(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(scheduleStatusTS)
	if err != nil {
		t.Fatalf("read %s: %v — if the type moved, update scheduleStatusTS", scheduleStatusTS, err)
	}
	m := tsScheduleStatusRe.FindStringSubmatch(tsCommentsRe.ReplaceAllString(string(raw), ""))
	if m == nil {
		t.Fatalf("no `export type ScheduleLastStatus = …;` in %s", scheduleStatusTS)
	}
	var members []string
	for _, alt := range strings.Split(m[1], "|") {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue // the optional leading `|` prettier writes on a wrapped union
		}
		lit := tsLiteralMemberRe.FindStringSubmatch(alt)
		if lit == nil {
			t.Fatalf("ScheduleLastStatus member %q is not a string literal", alt)
		}
		members = append(members, lit[1])
	}
	if len(members) == 0 {
		t.Fatalf("ScheduleLastStatus in %s has no members", scheduleStatusTS)
	}
	return members
}

// queryOrderRecorder is a db.DBTX that records the sqlc name of every statement
// in the order it arrives, answers each Query with no rows, fails the
// statements named in failing, and panics on those named in panicking.
type queryOrderRecorder struct {
	names     []string
	failing   map[string]error
	panicking map[string]string
}

func (r *queryOrderRecorder) note(sql string) string {
	name := sql
	if m := queryNameRe.FindStringSubmatch(sql); m != nil {
		name = m[1]
	}
	r.names = append(r.names, name)
	return name
}

func (r *queryOrderRecorder) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if err := r.failing[r.note(sql)]; err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (r *queryOrderRecorder) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	name := r.note(sql)
	if msg, ok := r.panicking[name]; ok {
		panic(msg)
	}
	if err := r.failing[name]; err != nil {
		return nil, err
	}
	return noRows{}, nil
}

func (r *queryOrderRecorder) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	panic("unexpected query row: " + r.note(sql))
}

// noRows is a result set with nothing in it.
type noRows struct{}

func (noRows) Close()                                       {}
func (noRows) Err() error                                   { return nil }
func (noRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 0") }
func (noRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (noRows) Next() bool                                   { return false }
func (noRows) Scan(...any) error                            { return nil }
func (noRows) Values() ([]any, error)                       { return nil, nil }
func (noRows) RawValues() [][]byte                          { return nil }
func (noRows) Conn() *pgx.Conn                              { return nil }

// TestRun_SettlesBeforeItClaims pins where Run settles dispatched runs: first,
// and whatever that settle's outcome.
//
// In Run at all, because the dedicated 15-second tick is one line in
// cmd/nexara that nothing else checks; with the settle here too, the tick that
// starts runs also settles every row that is not due. Before the claim, so a
// due row's finished run is at least logged and announced before the claim
// overwrites it with 'running' in the same pass. And a settle that fails — or
// panics — must not stop due runs from starting.
func TestRun_SettlesBeforeItClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		failing   map[string]error
		panicking map[string]string
	}{
		{name: "the settle succeeds"},
		{
			name:    "the settle fails",
			failing: map[string]error{"ReconcileDispatchedScheduledTasks": errors.New("connection reset")},
		},
		{
			name:      "the settle panics",
			panicking: map[string]string{"ReconcileDispatchedScheduledTasks": "settle exploded"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := &queryOrderRecorder{failing: tt.failing, panicking: tt.panicking}
			s := &Scheduler{
				queries: db.New(recorder),
				logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			s.Run(context.Background())

			// Nothing is due (the claim returns no rows), so these two are
			// all Run may send.
			want := []string{"ReconcileDispatchedScheduledTasks", "ClaimDueTasks"}
			if !slices.Equal(recorder.names, want) {
				t.Errorf("Run sent %v, want %v — settle the finished runs, then claim the due ones",
					recorder.names, want)
			}
		})
	}
}

// announcedEvent is what an announcement carries, and the Redis channel it
// went out on.
type announcedEvent struct {
	channel string
	event   events.Event
}

// drainAnnouncements reads everything published so far, up to a sentinel sent
// after it, so "nothing was published" is an answer rather than a timeout.
func drainAnnouncements(t *testing.T, rdb *redis.Client, pubsub *redis.PubSub) []announcedEvent {
	t.Helper()
	sentinel := "sentinel-" + uuid.NewString()
	if err := rdb.Publish(context.Background(), "nexara:test-sentinel", sentinel).Err(); err != nil {
		t.Fatalf("publish sentinel: %v", err)
	}
	var got []announcedEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-pubsub.Channel():
			if !ok {
				t.Fatal("the event channel closed before the sentinel arrived")
			}
			if msg.Payload == sentinel {
				return got
			}
			var ev events.Event
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				t.Fatalf("decode %q: %v", msg.Payload, err)
			}
			ev.Timestamp = ""
			got = append(got, announcedEvent{channel: msg.Channel, event: ev})
		case <-deadline:
			t.Fatal("the sentinel never arrived, so the events gathered may be incomplete")
		}
	}
}

// TestSettleDispatchedRuns_AnnouncesEachSettledRow pins the event an open
// Schedules tab depends on to see a run settle.
//
// A run's own task_update arrives BEFORE the settle — it is published when the
// collector finalizes the task, and the reconcile settles the row up to 15 s
// later — so the re-read it triggers finds the run still "dispatched". Only an
// event published by the settle itself can tell the tab the row moved on:
// one per settled row, on that row's cluster, and none when nothing settled.
// Driven through a real events.Publisher over miniredis, so the channel the
// event lands on is the one a WebSocket subscriber would read.
func TestSettleDispatchedRuns_AnnouncesEachSettledRow(t *testing.T) {
	t.Parallel()

	clusterA := uuid.MustParse("98000000-0000-4000-8000-00000000000a")
	clusterB := uuid.MustParse("98000000-0000-4000-8000-00000000000b")
	schedA := uuid.MustParse("98000000-0000-4000-8000-000000000001")
	schedB := uuid.MustParse("98000000-0000-4000-8000-000000000002")
	text := func(v string) pgtype.Text { return pgtype.Text{String: v, Valid: true} }

	tests := []struct {
		name    string
		settled []db.ReconcileDispatchedScheduledTasksRow
		want    []announcedEvent
	}{
		{name: "nothing settled"},
		{
			name: "two runs settled on two clusters",
			settled: []db.ReconcileDispatchedScheduledTasksRow{
				{ID: schedA, ClusterID: clusterA, LastStatus: text(runStatusSucceeded)},
				{ID: schedB, ClusterID: clusterB, LastStatus: text(runStatusFailed), LastError: text(lockedBody)},
			},
			want: []announcedEvent{
				{channel: "nexara:events:" + clusterA.String(), event: events.Event{
					Kind: events.KindScheduleChange, ClusterID: clusterA.String(),
					ResourceType: "schedule", ResourceID: schedA.String(), Action: runStatusSucceeded,
				}},
				{channel: "nexara:events:" + clusterB.String(), event: events.Event{
					Kind: events.KindScheduleChange, ClusterID: clusterB.String(),
					ResourceType: "schedule", ResourceID: schedB.String(), Action: runStatusFailed,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			pubsub := rdb.PSubscribe(context.Background(), "nexara:*")
			t.Cleanup(func() { _ = pubsub.Close() })
			if _, err := pubsub.Receive(context.Background()); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			s := &Scheduler{logger: logger, eventPub: events.NewPublisher(rdb, logger)}

			if err := s.settleDispatchedRunsWith(context.Background(), &fakeSettler{settled: tt.settled}); err != nil {
				t.Fatalf("settleDispatchedRunsWith: %v", err)
			}
			got := drainAnnouncements(t, rdb, pubsub)
			if !slices.Equal(got, tt.want) {
				t.Errorf("the settle published %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestScheduleChangeEventIsHandledByTheSPA pins the kind the settle publishes
// to the one the SPA acts on: a member of its EventKind union, and a case in
// useEventInvalidation. Without both, a respelled kind is published into a
// socket nobody reads it from, and the tab goes back to showing Running until
// something unrelated makes it re-read. Comments are stripped first, so a
// mention in one cannot stand in for the code.
func TestScheduleChangeEventIsHandledByTheSPA(t *testing.T) {
	t.Parallel()

	kind := regexp.QuoteMeta(events.KindScheduleChange)
	for _, f := range []struct {
		path string
		re   *regexp.Regexp
		what string
	}{
		{"../../frontend/src/types/ws.ts", regexp.MustCompile(`\|\s*"` + kind + `"`), "a member of the EventKind union"},
		{"../../frontend/src/hooks/useEventInvalidation.ts", regexp.MustCompile(`case\s+"` + kind + `"\s*:`),
			"a case in useEventInvalidation"},
	} {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v", f.path, err)
		}
		if !f.re.MatchString(tsCommentsRe.ReplaceAllString(string(raw), "")) {
			t.Errorf("%s has no %q as %s — the settle's announcement would reach no one",
				f.path, events.KindScheduleChange, f.what)
		}
	}
}
