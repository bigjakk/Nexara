package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// fakeClaimer is a minimal dueTaskClaimer used to assert that
// claimDueTasks wires the standard guard/stale constants through to
// the underlying query and to inject errors/panics.
type fakeClaimer struct {
	gotArg   db.ClaimDueTasksParams
	gotCalls int
	rows     []db.ScheduledTask
	err      error
	panicVal any
}

func (f *fakeClaimer) ClaimDueTasks(_ context.Context, arg db.ClaimDueTasksParams) ([]db.ScheduledTask, error) {
	f.gotArg = arg
	f.gotCalls++
	if f.panicVal != nil {
		panic(f.panicVal)
	}
	return f.rows, f.err
}

func TestClaimDueTasks_PassesGuardAndStaleConstants(t *testing.T) {
	t.Parallel()

	claimer := &fakeClaimer{}
	if _, err := claimDueTasks(context.Background(), claimer); err != nil {
		t.Fatalf("claimDueTasks: unexpected error: %v", err)
	}

	if claimer.gotCalls != 1 {
		t.Fatalf("ClaimDueTasks called %d times, want 1", claimer.gotCalls)
	}
	if claimer.gotArg.GuardSeconds != taskClaimGuardSeconds {
		t.Errorf("GuardSeconds = %v, want %v", claimer.gotArg.GuardSeconds, taskClaimGuardSeconds)
	}
	if claimer.gotArg.StaleSeconds != taskClaimStaleSeconds {
		t.Errorf("StaleSeconds = %v, want %v", claimer.gotArg.StaleSeconds, taskClaimStaleSeconds)
	}
}

func TestClaimDueTasks_PropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("db unreachable")
	claimer := &fakeClaimer{err: wantErr}

	rows, err := claimDueTasks(context.Background(), claimer)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if rows != nil {
		t.Errorf("rows = %v, want nil on error", rows)
	}
}

func TestClaimDueTasks_GuardCoversStaleWindow(t *testing.T) {
	t.Parallel()

	// Invariant the SQL relies on: while a claim is in flight, the row's
	// next_run_at sits past `now() + guard`, AND its last_run_at is
	// fresh enough that the stale-recovery branch doesn't fire. If guard
	// were shorter than stale, a non-crashed run could be re-claimed by
	// another tick after `guard` seconds even though the runner is still
	// healthy. Locking guard >= stale here is what stops that from
	// drifting if someone tunes one constant in isolation later.
	if taskClaimGuardSeconds < taskClaimStaleSeconds {
		t.Fatalf("taskClaimGuardSeconds (%d) must be >= taskClaimStaleSeconds (%d) "+
			"to prevent re-claim of an in-flight task before the stale-recovery threshold",
			taskClaimGuardSeconds, taskClaimStaleSeconds)
	}
}

// TestSchedulerRun_RecoversFromClaimPanic exercises the deferred recover
// at the top of Run(). We swap s.queries via a Scheduler built with the
// concrete *db.Queries set to nil; then calling Run() naturally panics
// (nil-pointer deref on the ClaimDueTasks call) and the recover should
// catch it. This is the cheapest way to exercise the production
// panic-recovery path without a full DB.
func TestSchedulerRun_RecoversFromClaimPanic(t *testing.T) {
	t.Parallel()

	// Logger that drops output so the test stays quiet.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	s := &Scheduler{
		queries: nil, // nil *db.Queries → method call panics with nil deref
		logger:  logger,
	}

	// If the panic recover is missing or broken, this call propagates
	// the panic and the test fails with a stack trace; if it works, Run
	// returns normally and the test passes.
	s.Run(context.Background())
}

// --- Scheduled snapshot names ---

// newPVEStub is a stand-in Proxmox that records every request it receives.
//
// It answers with an EMPTY UPID deliberately. executeSnapshot hands a non-empty
// one to s.trackTask, which writes a task_history row and an audit entry — so a
// realistic UPID would drag a database into a test about whether the request is
// sent at all. trackTask returns immediately on an empty UPID, which keeps the
// success path reachable with a zero-valued Scheduler.
func newPVEStub(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, r.Method+" "+r.RequestURI+" snapname="+r.PostForm.Get("snapname"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":""}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func newStubPVEClient(t *testing.T, serverURL string) *proxmox.Client {
	t.Helper()
	c, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     serverURL,
		TokenID:     "nexara@pve!api",
		TokenSecret: "secret-token-value",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// runAt is the instant a test run starts. A fixed one, because executeSnapshot
// names the snapshot from it: time.Now() would make the expected names
// unknowable and let two back-to-back runs share a second.
//
// In a zone of its own, not UTC, so every expected name below also holds the
// name to runAt's own wall clock. CI runs in UTC, where a conversion to UTC
// changes nothing; here it turns 02:00 into 00:30. No zone in use today has a
// 90-minute offset.
var runAt = time.Date(2026, time.September, 26, 2, 0, 0, 0, time.FixedZone("synthetic", 90*60))

// TestExecuteSnapshot_ASecondRunWithATypedNameTakesANewName is the regression
// test for the bug a typed name had.
//
// executeSnapshot used to send snap_name verbatim on every run. A guest holds
// each snapshot name once — pve-guest-common's __snapshot_prepare dies with
// "snapshot name 'nightly' already used" — so the first run took the snapshot
// and every run after it failed. Only an EMPTY snap_name got a timestamp.
//
// The stub does not refuse the duplicate itself, and cannot honestly: upstream
// refuses inside the worker, so the create call returns a UPID either way and
// the failure lands on the task. What decides whether a later run fails is the
// name it asks for, so that is what is asserted — three runs of one
// twice-daily task, and no run may ask for a name an earlier one already took.
// The 14:00 run is there for the hour: a 12-hour clock would give it the 02:00
// run's name.
func TestExecuteSnapshot_ASecondRunWithATypedNameTakesANewName(t *testing.T) {
	t.Parallel()

	runs := []time.Time{runAt, runAt.Add(12 * time.Hour), runAt.AddDate(0, 0, 1)}

	tests := []struct {
		name         string
		resourceType string
		params       string
		want         []string
	}{
		{"a typed name on a VM", "vm", `{"snap_name":"nightly"}`,
			[]string{"nightly-20260926-020000", "nightly-20260926-140000", "nightly-20260927-020000"}},
		{"a typed name on a container", "ct", `{"snap_name":"nightly"}`,
			[]string{"nightly-20260926-020000", "nightly-20260926-140000", "nightly-20260927-020000"}},
		// The case that always worked, kept beside the typed one so the two
		// visibly get the same treatment: "auto" is just the default prefix.
		{"no name at all", "vm", `{}`,
			[]string{"auto-20260926-020000", "auto-20260926-140000", "auto-20260927-020000"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, seen := newPVEStub(t)
			client := newStubPVEClient(t, srv.URL)
			s := &Scheduler{}
			task := db.ScheduledTask{
				ResourceType: tt.resourceType,
				ResourceID:   "100",
				Node:         "pve-01",
				Action:       "snapshot",
				Schedule:     "0 2,14 * * *",
				Params:       []byte(tt.params),
			}

			for i, at := range runs {
				if err := s.executeSnapshot(context.Background(), client, task, at); err != nil {
					t.Fatalf("run %d: executeSnapshot(%s) = %v, want nil", i+1, tt.params, err)
				}
			}
			if len(*seen) != len(runs) {
				t.Fatalf("%d runs issued %d requests %v, want exactly %d", len(runs), len(*seen), *seen, len(runs))
			}

			taken := map[string]int{}
			for i, request := range *seen {
				name := snapnameOf(t, request)
				if earlier, dup := taken[name]; dup {
					t.Fatalf("runs %d and %d both asked Proxmox for a snapshot named %q; the guest holds it "+
						"after the first, so the later run's task fails with \"snapshot name '%s' already used\"",
						earlier+1, i+1, name, name)
				}
				taken[name] = i
				if name != tt.want[i] {
					t.Errorf("run %d took %q, want %q — the stored name followed by the run's date and time",
						i+1, name, tt.want[i])
				}
			}
		})
	}
}

// taskRunRecorder is a db.DBTX that records the UPDATE finishTaskRun writes
// for a run and answers anything else with an error. It stands in for the
// database in the one test that has to go through executeTask.
//
// err keeps the reason for a refusal: finishTaskRun only logs a failed write,
// to a logger the test discards, so without it every refusal would read as a
// bare "recorded 0 times".
type taskRunRecorder struct {
	lastRunAt pgtype.Timestamptz
	updates   int
	err       error
}

// lastRunAtParam finds last_run_at's placeholder in the statement rather than
// hard-coding its position, so a reordered query cannot quietly hand this
// recorder a different column.
var lastRunAtParam = regexp.MustCompile(`last_run_at = \$(\d+)`)

func (r *taskRunRecorder) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	ts, err := lastRunAtArg(sql, args)
	if err != nil {
		r.err = err
		return pgconn.CommandTag{}, err
	}
	r.lastRunAt = ts
	r.updates++
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

// lastRunAtArg picks the last_run_at value out of a scheduled_tasks UPDATE.
func lastRunAtArg(sql string, args []any) (pgtype.Timestamptz, error) {
	m := lastRunAtParam.FindStringSubmatch(sql)
	if !strings.Contains(sql, "UPDATE scheduled_tasks") || m == nil {
		return pgtype.Timestamptz{}, fmt.Errorf("unexpected statement: %s", sql)
	}
	n, _ := strconv.Atoi(m[1])
	if n < 1 || n > len(args) {
		return pgtype.Timestamptz{}, fmt.Errorf("last_run_at is $%d but %d args were passed", n, len(args))
	}
	ts, ok := args[n-1].(pgtype.Timestamptz)
	if !ok {
		return pgtype.Timestamptz{}, fmt.Errorf("last_run_at arg is %T", args[n-1])
	}
	return ts, nil
}

func (r *taskRunRecorder) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	r.err = fmt.Errorf("unexpected query: %s", sql)
	return nil, r.err
}

func (r *taskRunRecorder) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	panic("unexpected query row: " + sql)
}

// TestExecuteTask_NamesTheSnapshotForTheRunItRecords pins the wiring the tests
// above cannot reach, because they hand executeSnapshot an instant of their
// own: that executeTask passes each run's OWN start time. Pass it anything
// fixed — the zero time, a constant — and every run sends the same name again,
// which is the bug this change exists to fix. The run's start time is also
// what finishTaskRun records as last_run_at, so the name must be exactly the
// prefix composed with that recorded instant, that instant must be now, and it
// must be on the server's local clock, which is what the admin guide tells
// operators the name shows.
func TestExecuteTask_NamesTheSnapshotForTheRunItRecords(t *testing.T) {
	t.Parallel()

	srv, seen := newPVEStub(t)
	recorder := &taskRunRecorder{}
	s := &Scheduler{
		queries: db.New(recorder),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	task := db.ScheduledTask{
		ID:           uuid.New(),
		ResourceType: "vm",
		ResourceID:   "100",
		Node:         "pve-01",
		Action:       "snapshot",
		Schedule:     "0 2 * * *",
		Params:       []byte(`{"snap_name":"nightly"}`),
	}

	before := time.Now()
	s.executeTask(context.Background(), newStubPVEClient(t, srv.URL), task)
	after := time.Now()

	if recorder.updates != 1 || recorder.err != nil {
		t.Fatalf("the run was recorded %d times, want once (recorder error: %v)", recorder.updates, recorder.err)
	}
	recorded := recorder.lastRunAt.Time
	if !recorder.lastRunAt.Valid || recorded.Before(before) || recorded.After(after) {
		t.Fatalf("last_run_at = %v (valid %v), want the time of this run, between %v and %v",
			recorded, recorder.lastRunAt.Valid, before, after)
	}
	// time.Now() carries time.Local and a UTC conversion carries time.UTC, a
	// different pointer, so this holds the zone even on a host whose local
	// zone is UTC.
	if recorded.Location() != time.Local {
		t.Errorf("the run's clock is in %v, want the server's local zone", recorded.Location())
	}
	if len(*seen) != 1 {
		t.Fatalf("the run issued %d requests %v, want exactly 1", len(*seen), *seen)
	}
	if got, want := snapnameOf(t, (*seen)[0]), proxmox.TimestampedSnapshotName("nightly", recorded); got != want {
		t.Errorf("the run asked for %q, want %q — the prefix with the start time the run recorded", got, want)
	}
}

// snapnameOf reads the snapname a stub-recorded request carried.
func snapnameOf(t *testing.T, request string) string {
	t.Helper()
	_, name, found := strings.Cut(request, " snapname=")
	if !found {
		t.Fatalf("request %q carries no snapname", request)
	}
	return name
}

// TestScheduledSnapshotName pins the composition on its own, including the cut
// that keeps a row written before the 24-character budget running.
func TestScheduledSnapshotName(t *testing.T) {
	t.Parallel()

	budget := proxmox.SnapshotNamePrefixMaxLen
	tests := []struct {
		name   string
		stored string
		want   string
		why    string
	}{
		{"empty", "", "auto-20260926-020000", "the default prefix"},
		{"a typed prefix", "nightly", "nightly-20260926-020000", "the stored value is a prefix"},
		{"a one-letter prefix", "a", "a-20260926-020000",
			"Proxmox refuses a one-letter NAME, not a one-letter prefix of a longer one"},
		{"a reserved word", "current", "current-20260926-020000",
			"Proxmox reserves the whole name current, and this name is not it"},
		{"exactly the budget", strings.Repeat("b", budget),
			strings.Repeat("b", budget) + "-20260926-020000", "24 characters fit whole"},
		{"one over the budget", strings.Repeat("c", budget) + "x",
			strings.Repeat("c", budget) + "-20260926-020000",
			"a row stored before the budget existed is cut rather than left failing"},
		{"the old 40-character maximum", "a" + strings.Repeat("d", 39),
			"a" + strings.Repeat("d", budget-1) + "-20260926-020000",
			"what the API accepted while the name was sent verbatim"},
		{"a pre-v1.14.0 row of any length", strings.Repeat("e", 200),
			strings.Repeat("e", budget) + "-20260926-020000", "nothing capped the name before v1.14.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := scheduledSnapshotName(tt.stored, runAt)
			if got != tt.want {
				t.Errorf("scheduledSnapshotName(%q) = %q, want %q (%s)", tt.stored, got, tt.want, tt.why)
			}
			// Whatever the stored length, what is sent must be a name the
			// client lets through — for both guest kinds.
			for _, kind := range []proxmox.SnapshotGuestKind{proxmox.QemuSnapshot, proxmox.LXCSnapshot} {
				if err := proxmox.ValidateSnapshotName(kind, got); err != nil {
					t.Errorf("scheduledSnapshotName(%q) = %q, which the %s client refuses: %v",
						tt.stored, got, kind, err)
				}
			}
		})
	}
}

// TestExecuteSnapshot_RefusesAStoredNameProxmoxWouldReject is the regression
// test for the bug this path had before the name became a prefix.
//
// snap_name is decoded out of scheduled_tasks.params — a jsonb blob the
// endpoint declaration carries through without describing ("params" is a bare
// apischema.Object) and whose producer in the SPA was, until v1.14.0, a
// free-text field with no pattern. So a row can hold a name no run could ever
// send, and before the client refused it the operator believed a recurring
// snapshot was protecting the guest while every fire failed with a raw PVE
// error buried in last_error.
//
// What such a row can hold has changed shape since: a reserved word or an
// over-long value now makes a legal name once the date is added and the cut
// applied (TestScheduledSnapshotName). A character Proxmox never allows does
// not, and those are the cases here.
//
// The fix is at the client, not here, which is why this test asserts the stub
// saw NOTHING rather than asserting on the error text: what makes the scheduler
// safe is that it cannot reach the wire with such a name, not that it happens
// to check first.
func TestExecuteSnapshot_RefusesAStoredNameProxmoxWouldReject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		params       string
		why          string
	}{
		{"a name with a space", "ct", `{"snap_name":"my snap"}`,
			"not a legal pve-configid"},
		{"a name with a dot", "vm", `{"snap_name":"nightly.1"}`,
			"not a legal pve-configid"},
		{"a name starting with a digit", "vm", `{"snap_name":"1nightly"}`,
			"pve-configid starts with a letter"},
		{"a name starting with an underscore", "ct", `{"snap_name":"_nightly"}`,
			"pve-configid starts with a letter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, seen := newPVEStub(t)
			s := &Scheduler{}
			task := db.ScheduledTask{
				ResourceType: tt.resourceType,
				ResourceID:   "100",
				Node:         "pve-01",
				Action:       "snapshot",
				Params:       []byte(tt.params),
			}

			err := s.executeSnapshot(context.Background(), newStubPVEClient(t, srv.URL), task, runAt)
			// The sentinel, not merely "an error": the stub answers 200 to
			// everything, so a refusal arriving for some unrelated reason —
			// a params unmarshal, an unhandled resource type — would satisfy
			// a bare non-nil check and leave this green while the name rule
			// it is named for had stopped running.
			if !errors.Is(err, proxmox.ErrInvalidInput) {
				t.Errorf("executeSnapshot(%s) = %v, want a refusal wrapping ErrInvalidInput (%s)",
					tt.params, err, tt.why)
			}
			if len(*seen) != 0 {
				t.Errorf("executeSnapshot(%s) reached Proxmox as %v; the task must fail before "+
					"it is dispatched, not on every fire", tt.params, *seen)
			}
		})
	}
}

// TestExecuteSnapshot_DispatchesNamesProxmoxAccepts is the other half, and it
// is what stops the test above passing against an executeSnapshot that refuses
// everything.
//
// The auto-generated fallback is included because it is what fires for every
// task whose snap_name was left blank — by far the common case — and a cap or
// shape rule that refused it would take the whole feature down. The reserved
// words are included because a row can hold one (the API refused them only
// while the name was sent verbatim), and with the date added none of them is
// the whole name Proxmox reserves.
func TestExecuteSnapshot_DispatchesNamesProxmoxAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		params       string
		wantSnapPart string
		// wantGuestPath is the guest-family segment the request must carry.
		// Asserting the name alone left the resource_type -> family binding
		// unchecked: scheduledResourceTypes maps the wire value to a family
		// and executeSnapshot switches the family to a client call, so the
		// two halves can be transposed. Swap the map's values and a container
		// snapshot goes to the qemu endpoint while every snapname assertion
		// still passes.
		wantGuestPath string
	}{
		{"an ordinary VM prefix", "vm", `{"snap_name":"nightly"}`, "snapname=nightly-20260926-020000", "/qemu/100/"},
		{"an ordinary container prefix", "ct", `{"snap_name":"nightly"}`, "snapname=nightly-20260926-020000", "/lxc/100/"},
		{"current on a VM", "vm", `{"snap_name":"current"}`, "snapname=current-20260926-020000", "/qemu/100/"},
		{"current on a container", "ct", `{"snap_name":"current"}`, "snapname=current-20260926-020000", "/lxc/100/"},
		{"pending on a VM", "vm", `{"snap_name":"Pending"}`, "snapname=Pending-20260926-020000", "/qemu/100/"},
		{"vzdump on a container", "ct", `{"snap_name":"vzdump"}`, "snapname=vzdump-20260926-020000", "/lxc/100/"},
		{"a stored name over the budget", "vm", `{"snap_name":"` + strings.Repeat("a", 41) + `"}`,
			"snapname=" + strings.Repeat("a", proxmox.SnapshotNamePrefixMaxLen) + "-20260926-020000", "/qemu/100/"},
		{"the auto-generated fallback", "vm", `{}`, "snapname=auto-20260926-020000", "/qemu/100/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, seen := newPVEStub(t)
			s := &Scheduler{}
			task := db.ScheduledTask{
				ResourceType: tt.resourceType,
				ResourceID:   "100",
				Node:         "pve-01",
				Action:       "snapshot",
				Params:       []byte(tt.params),
			}

			if err := s.executeSnapshot(context.Background(), newStubPVEClient(t, srv.URL), task, runAt); err != nil {
				t.Fatalf("executeSnapshot(%s) = %v, want nil; Proxmox accepts this name", tt.params, err)
			}
			if len(*seen) != 1 {
				t.Fatalf("executeSnapshot(%s) issued %d requests %v, want exactly 1",
					tt.params, len(*seen), *seen)
			}
			// HasSuffix, not Contains: the snapname is the last thing the stub
			// records, and a Contains would let a longer name that merely
			// starts with the expected one through.
			if !strings.HasSuffix((*seen)[0], tt.wantSnapPart) {
				t.Errorf("request = %q, want it to carry %q", (*seen)[0], tt.wantSnapPart)
			}
			if !strings.Contains((*seen)[0], tt.wantGuestPath) {
				t.Errorf("resource_type %q dispatched to %q, want the %q endpoint — the wire value "+
					"reached the wrong guest family", tt.resourceType, (*seen)[0], tt.wantGuestPath)
			}
		})
	}
}
