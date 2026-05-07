package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
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
