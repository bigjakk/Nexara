package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// TestExecuteSnapshot_RefusesAStoredNameProxmoxWouldReject is the regression
// test for the bug this path actually had.
//
// snap_name is decoded out of scheduled_tasks.params — a jsonb blob the
// endpoint declaration carries through without describing ("params" is a bare
// apischema.Object) and whose producer in the SPA is a free-text field with no
// maxLength and no pattern. So a manage:schedule holder could store "current",
// "vzdump" or 200 characters, and nothing refused it: the row was created, the
// operator believed a recurring snapshot was protecting the guest, and every
// single fire failed with a raw PVE error buried in last_error.
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
		{"a VM snapshot named current", "vm", `{"snap_name":"current"}`,
			"PVE gives the live config that pseudo-name"},
		{"a VM snapshot named pending", "vm", `{"snap_name":"pending"}`,
			"it collides with the VM config's [PENDING] section"},
		{"a container snapshot named vzdump", "ct", `{"snap_name":"vzdump"}`,
			"vzdump names a container's own backup snapshot"},
		{"a name over Proxmox's cap", "vm",
			`{"snap_name":"` + strings.Repeat("a", 41) + `"}`,
			"pve-snapshot-name is maxLength 40"},
		{"a name with a space", "ct", `{"snap_name":"my snap"}`,
			"not a legal pve-configid"},
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

			err := s.executeSnapshot(context.Background(), newStubPVEClient(t, srv.URL), task)
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
// shape rule that refused it would take the whole feature down.
func TestExecuteSnapshot_DispatchesNamesProxmoxAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		params       string
		wantSnapPart string
	}{
		{"an ordinary VM name", "vm", `{"snap_name":"nightly"}`, "snapname=nightly"},
		{"the VM's own non-reserved name", "vm", `{"snap_name":"vzdump"}`, "snapname=vzdump"},
		{"the container's own non-reserved name", "ct", `{"snap_name":"pending"}`, "snapname=pending"},
		{"the auto-generated fallback", "vm", `{}`, "snapname=auto-"},
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

			if err := s.executeSnapshot(context.Background(), newStubPVEClient(t, srv.URL), task); err != nil {
				t.Fatalf("executeSnapshot(%s) = %v, want nil; Proxmox accepts this name", tt.params, err)
			}
			if len(*seen) != 1 {
				t.Fatalf("executeSnapshot(%s) issued %d requests %v, want exactly 1",
					tt.params, len(*seen), *seen)
			}
			if !strings.Contains((*seen)[0], tt.wantSnapPart) {
				t.Errorf("request = %q, want it to carry %q", (*seen)[0], tt.wantSnapPart)
			}
		})
	}
}
