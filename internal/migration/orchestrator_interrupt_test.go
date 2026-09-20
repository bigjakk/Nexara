package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// A cross-cluster migration's task_history row is withheld from the collector's
// reconciler (ListRunningTaskHistoryByCluster) because only this package can
// scrub the target cluster's API token out of PVE's die message. That also
// takes away the collector's 24h stale-task sweep, which used to be the thing
// that eventually finalized a row nobody else closed — so pollTaskStatus has to
// close it on the way out, not just on completion.
//
// Without this, a SIGTERM during a long cross-cluster migration leaves a row
// showing "Running" forever: failJob writes migration_jobs through
// cleanupCtxFor so the JOB row ends up correct, DeleteCompletedTasks refuses to
// prune a running row, and no other writer is left.
//
// The exit status must be a constant. This is the one finalizing write on the
// path that has not polled PVE, so there is no vendor text to carry — and a
// constant is what keeps it that way if someone later reaches for a richer
// message.
func TestPollTaskStatus_CancelledContextFinalizesTheTaskRow(t *testing.T) {
	const (
		node = "pve-01"
		upid = "UPID:pve-01:00001234:00000000:68000000:qmigrate:100:root@pam:"
	)

	var execs []execCall
	// The URL is never dialled: the cancelled context takes the ctx.Done()
	// branch before any poll, which is the branch under test. A nil client is
	// therefore safe, and is also the assertion that no HTTP call happens —
	// this path would panic if one did.
	o := newCredTestOrchestrator(t, "https://invalid.local", &execs)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mc := &migrationContext{job: db.MigrationJob{
		SourceClusterID: uuid.New(),
		Vmid:            credTestSourceVM,
		VmType:          VMTypeQEMU,
		MigrationType:   TypeCrossCluster,
	}}
	mc.armEndpointScrubber(credTestTokenID + "=" + credTestSecret)

	o.pollTaskStatus(ctx, nil, node, upid, uuid.New(), mc)

	// ReconcileTaskHistory binds (upid, status, exit_status, finished_at).
	// findExecArg fails the test when no such write happened, so this cannot
	// pass by the row never being touched — which is the bug it guards.
	gotUPID := findExecArg(t, execs, "UPDATE task_history", 0)
	gotStatus := findExecArg(t, execs, "UPDATE task_history", 1)
	gotExit := findExecArg(t, execs, "UPDATE task_history", 2)

	if gotUPID != upid {
		t.Errorf("finalized the wrong row: upid = %q, want %q", gotUPID, upid)
	}
	if gotStatus != "failed" {
		t.Errorf("task status = %q, want \"failed\" — a row left non-terminal is the "+
			"orphan this exists to prevent", gotStatus)
	}
	if gotExit != "interrupted" {
		t.Errorf("exit_status = %q, want the constant \"interrupted\"", gotExit)
	}
	if strings.Contains(gotExit, credTestSecret) {
		t.Errorf("the target cluster's token secret reached task_history.exit_status: %q", gotExit)
	}

	// The job row is finalized too, and was before this change — asserted here
	// so a future edit to the ctx.Done() branch cannot trade one for the other.
	if findExecArg(t, execs, "UPDATE migration_jobs", 1) != StatusFailed {
		t.Error("the migration job was not marked failed on cancellation")
	}
}

// The other way this loop used to leak a permanently-"Running" row, and the
// likelier one: no process death needed at all.
//
// Withholding a cross-cluster migration's row from the collector took away
// staleTaskGrace, whose own comment names the causes — node reboot, task-log
// rotation. After either, GetTaskStatus errors forever. The loop used to
// `continue` on a poll error with no overall bound, so a healthy Nexara would
// poll every 5s indefinitely while the row sat at "running" and
// DeleteCompletedTasks refused to prune it.
//
// The bound is on CONSECUTIVE failures, not on elapsed runtime, so the
// companion assertion matters as much as the main one: a migration Proxmox is
// still answering about must never be finalized, however long it runs.
func TestPollTaskStatus_UnreachableTaskIsAbandonedAfterTheGrace(t *testing.T) {
	const (
		node = "pve-01"
		upid = "UPID:pve-01:00001234:00000000:68000000:qmigrate:100:root@pam:"
	)

	newOrchestrator := func(t *testing.T, handler http.HandlerFunc, execs *[]execCall) *Orchestrator {
		t.Helper()
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		o := newCredTestOrchestrator(t, srv.URL, execs)
		// Drive the loop in milliseconds. Production keeps the 5s/24h
		// defaults; only the ratio is under test.
		o.pollInterval = time.Millisecond
		o.pollStaleGrace = 20 * time.Millisecond
		return o
	}

	newContext := func() *migrationContext {
		mc := &migrationContext{job: db.MigrationJob{
			SourceClusterID: uuid.New(),
			Vmid:            credTestSourceVM,
			VmType:          VMTypeQEMU,
			MigrationType:   TypeCrossCluster,
		}}
		mc.armEndpointScrubber(credTestTokenID + "=" + credTestSecret)
		return mc
	}

	t.Run("a task Proxmox stops answering for is finalized, not polled forever", func(t *testing.T) {
		var polls atomic.Int64
		var execs []execCall
		o := newOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
			polls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}, &execs)

		done := make(chan struct{})
		go func() {
			defer close(done)
			o.pollTaskStatus(context.Background(), mustClient(t, o), node, upid, uuid.New(), newContext())
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("pollTaskStatus never returned — the loop is still unbounded, which is " +
				"the bug: the task row stays 'running' and nothing else can finalize it")
		}

		if polls.Load() < 2 {
			t.Fatalf("only %d poll attempts — the loop gave up before it could have been "+
				"a persistent failure, so the grace is not doing the deciding", polls.Load())
		}
		if got := findExecArg(t, execs, "UPDATE task_history", 1); got != "failed" {
			t.Errorf("task status = %q, want \"failed\"", got)
		}
		if got := findExecArg(t, execs, "UPDATE task_history", 2); got != "vanished" {
			t.Errorf("exit_status = %q, want the constant \"vanished\" — the same terminal "+
				"value the collector sweep used to write for this case", got)
		}
	})

	t.Run("a transient blip does not finalize a live migration", func(t *testing.T) {
		// Alternates error, running, error, running… If the bound were on
		// elapsed time rather than consecutive failures, this would be
		// finalized mid-flight and the operator would see a successful
		// migration reported as failed.
		var calls atomic.Int64
		var execs []execCall
		o := newOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1)%2 == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"status": "running", "type": "qmigrate", "upid": upid, "node": node,
			}})
		}, &execs)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			o.pollTaskStatus(ctx, mustClient(t, o), node, upid, uuid.New(), newContext())
		}()

		// Far longer than the grace: a runtime-based bound would have fired
		// several times over by now.
		time.Sleep(200 * time.Millisecond)

		// The loop must actually have RUN. Without this the subtest's only
		// assertion is negative — "done is not closed" — which an injected
		// pollInterval of an hour satisfies just as well as a correct bound,
		// by never ticking at all. Proven: setting o.pollInterval = time.Hour
		// here left this green before the check existed.
		if n := calls.Load(); n < 4 {
			t.Fatalf("the server saw only %d polls in 200ms, so 'not abandoned' proves nothing — "+
				"the loop barely ran", n)
		}
		select {
		case <-done:
			t.Fatal("a migration Proxmox is still answering about was abandoned — the bound " +
				"is on total runtime, not on consecutive failures, so any long transfer " +
				"with an intermittent blip gets reported as failed while still copying")
		default:
		}

		cancel()
		<-done
	})
}

// mustClient builds the Proxmox client for the stub's single cluster.
func mustClient(t *testing.T, o *Orchestrator) *proxmox.Client {
	t.Helper()
	client, _, err := o.clientForCluster(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("clientForCluster: %v", err)
	}
	return client
}
