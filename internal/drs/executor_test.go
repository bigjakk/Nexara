package drs

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestNewExecutor_NilShutdownCtxFallback ensures nil ctx falls back to
// context.Background() so partial construction (tests, etc.) doesn't
// nil-panic when waitForTask reads e.shutdownCtx.
func TestNewExecutor_NilShutdownCtxFallback(t *testing.T) {
	e := NewExecutor(nil, nil, slog.Default(), nil) //nolint:staticcheck // nil ctx is the path under test
	if e.shutdownCtx == nil {
		t.Fatal("shutdownCtx is nil; constructor must fall back to context.Background()")
	}
	select {
	case <-e.shutdownCtx.Done():
		t.Fatal("background-derived ctx should not be Done()")
	default:
	}
}

// TestExecutor_WaitForTask_CancelsOnShutdown is the load-bearing test for
// Finding #14 on the DRS path: a SIGTERM-induced shutdown context cancel
// must abort the 30-minute poll loop promptly. The final check against
// context.Background() still runs (sees "running" → returns "interrupted")
// so a tracked task isn't orphaned without an outcome.
func TestExecutor_WaitForTask_CancelsOnShutdown(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/nodes/test-node/tasks/UPID:test:0/status" {
			payload, _ := json.Marshal(map[string]interface{}{
				"data": proxmox.TaskStatus{Status: "running", UPID: "UPID:test:0", Node: "test-node"},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer stub.Close()

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     stub.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	e := NewExecutor(shutdownCtx, nil, slog.Default(), nil)

	type result struct {
		status string
		detail string
	}
	done := make(chan result, 1)
	go func() {
		s, d := e.waitForTask(context.Background(), client, "test-node", "UPID:test:0")
		done <- result{s, d}
	}()

	time.Sleep(50 * time.Millisecond)
	cancelShutdown()

	select {
	case r := <-done:
		if r.status != statusInterrupted {
			t.Errorf("waitForTask status = %q, want %q after shutdown cancel", r.status, statusInterrupted)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForTask did not return within 3s of shutdown cancel — pollCtx is not derived from shutdownCtx")
	}
}

// recordingExecQueries records what Execute writes and, like the database, refuses
// a write whose context has ended.
type recordingExecQueries struct {
	statuses []string
	task     string // the status the task_history row was finalized with
	audits   []db.InsertAuditLogParams
}

func (q *recordingExecQueries) InsertDRSHistory(ctx context.Context, _ db.InsertDRSHistoryParams) (db.DrsHistory, error) {
	return db.DrsHistory{ID: uuid.New()}, ctx.Err()
}

func (q *recordingExecQueries) UpdateDRSHistoryStatus(ctx context.Context, arg db.UpdateDRSHistoryStatusParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.statuses = append(q.statuses, arg.Status)
	return nil
}

func (q *recordingExecQueries) InsertTaskHistory(ctx context.Context, _ db.InsertTaskHistoryParams) (db.TaskHistory, error) {
	return db.TaskHistory{}, ctx.Err()
}

func (q *recordingExecQueries) ReconcileTaskHistory(ctx context.Context, arg db.ReconcileTaskHistoryParams) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	q.task = arg.Status
	return 1, nil
}

func (q *recordingExecQueries) GetVMByClusterAndVmid(ctx context.Context, _ db.GetVMByClusterAndVmidParams) (db.Vm, error) {
	return db.Vm{ID: uuid.New()}, ctx.Err()
}

func (q *recordingExecQueries) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.audits = append(q.audits, arg)
	return nil
}

// taskLogHandler serves a task's log a page at a time, as Proxmox does.
func taskLogHandler(lines []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		start = min(start, len(lines))
		page := lines[start:min(start+proxmox.TaskLogPageLines, len(lines))]
		entries := make([]proxmox.TaskLogEntry, 0, len(page))
		for i, line := range page {
			entries = append(entries, proxmox.TaskLogEntry{N: start + i + 1, T: line})
		}
		haData(w, entries)
	}
}

// The audit log shows a DRS failure's reason from the row's details.error, and the
// back-off counts the history status. For an HA-managed guest both must come from
// the migration, not the hamigrate task that asked for it, which ends OK whatever
// the migration then does.
func TestExecute_RecordsWhyAMigrationFailed(t *testing.T) {
	t.Parallel()
	const (
		upid        = "UPID:pve-01:0:0:6AECD000:qmigrate:101:user@pam:"
		haUPID      = "UPID:pve-01:0:0:6AECD100:hamigrate:101:user@pam:"
		realUPID    = "UPID:pve-01:0:0:6AECD10D:qmigrate:101:root@pam:"
		earlierUPID = "UPID:pve-01:0:0:6AECD000:qmigrate:101:root@pam:"
		otherUPID   = "UPID:pve-01:0:0:6AECD101:qmigrate:102:root@pam:"
		haStart     = 0x6AECD100
		failedAudit = "drs_migrate_failed"
		grace       = 20 * time.Millisecond
		lockError   = "migration aborted (duration 00:00:10): can't lock file '/var/lock/qemu-server/lock-101.conf' - got timeout"
		at          = "2026-11-05 13:40:32 ERROR: "
		taskEnd     = "TASK ERROR: migration aborted"
	)
	started := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { haData(w, id) }
	}

	for _, tc := range []struct {
		name       string
		migrate    http.HandlerFunc
		exit       string   // of the task doing the migration
		haExit     string   // of the hamigrate, which normally ends OK
		log        []string // of the task doing the migration; nil cannot be read
		noRealTask bool
		wantStatus string
		wantAction string
		wantError  string
		wantTask   string // the hamigrate's own row, which the collector finalizes too
	}{
		{
			name: "Proxmox refuses the request",
			migrate: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "VM is locked (backup)", http.StatusInternalServerError)
			},
			wantStatus: "failed",
			wantAction: failedAudit,
			wantError:  "migrate VM 101 on pve-01: proxmox API error 500: VM is locked (backup)",
		},
		{
			name: "the task fails", migrate: started(upid), exit: "migration aborted",
			wantStatus: "failed", wantAction: failedAudit, wantError: "migration aborted",
		},
		{
			name: "a URL in the text keeps no secret", migrate: started(upid), exit: "fetch https://user:pw@192.0.2.10/x?sig=abc failed",
			wantStatus: "failed", wantAction: failedAudit, wantError: "fetch https://(set)@192.0.2.10/x?(set) failed",
		},
		{
			name: "the log gives the reason", migrate: started(upid), exit: "migration aborted",
			log:        []string{"trying to acquire lock...", at + lockError, taskEnd},
			wantStatus: "failed", wantAction: failedAudit, wantError: lockError,
		},
		{
			name: "a local disk's reason is logged as a warning", migrate: started(upid), exit: "migration aborted",
			log: []string{
				"2026-11-05 13:40:32 found local disk 'local-lvm:vm-101-disk-0' (attached)",
				"2026-11-05 13:40:32 can't migrate local disk 'local-lvm:vm-101-disk-0': can't live migrate attached local disks without with-local-disks option",
				at + "Problem found while scanning volumes - can't migrate VM - check log",
				at + "migration aborted (duration 00:00:01): Problem found while scanning volumes - can't migrate VM - check log",
				taskEnd,
			},
			wantStatus: "failed", wantAction: failedAudit,
			wantError: "can't migrate local disk 'local-lvm:vm-101-disk-0': can't live migrate attached local disks without with-local-disks option; " +
				"migration aborted (duration 00:00:01): Problem found while scanning volumes - can't migrate VM - check log",
		},
		{
			name: "QEMU's reason is logged as information", migrate: started(upid), exit: "migration problems",
			log: []string{
				"2026-11-05 13:40:32 migration active, transferred 1.0 GiB of 4.0 GiB VM-state, 100.0 MiB/s",
				"2026-11-05 13:40:32 migration status error: failed - Unable to write to socket: Broken pipe",
				at + "online migrate failure - aborting",
				"2026-11-05 13:40:32 aborting phase 2 - cleanup resources",
				at + "migration finished with problems (duration 00:00:15)",
				"TASK ERROR: migration problems",
			},
			wantStatus: "failed", wantAction: failedAudit,
			wantError: "migration status error: failed - Unable to write to socket: Broken pipe; online migrate failure - aborting; " +
				"migration finished with problems (duration 00:00:15)",
		},
		{
			name: "the log gives it past its first page", migrate: started(upid), exit: "migration aborted",
			log:        append(slices.Repeat([]string{"progress"}, proxmox.TaskLogPageLines), at+lockError, taskEnd),
			wantStatus: "failed", wantAction: failedAudit, wantError: lockError,
		},
		{
			name: "the aborted line repeats a reason logged earlier", migrate: started(upid), exit: "migration aborted",
			log:        []string{at + "storage is offline", at + "line two", at + "migration aborted (duration 00:00:01): storage is offline", at + "line two", taskEnd},
			wantStatus: "failed", wantAction: failedAudit, wantError: "migration aborted (duration 00:00:01): storage is offline; line two",
		},
		{
			name: "problems: the reason comes before the last line", migrate: started(upid), exit: "migration problems",
			log:        []string{at + "online migrate failure - disk gone", "aborting phase 2", at + "migration finished with problems (duration 00:00:33)", "TASK ERROR: migration problems"},
			wantStatus: "failed", wantAction: failedAudit, wantError: "online migrate failure - disk gone; migration finished with problems (duration 00:00:33)",
		},
		{
			name: "a flood of errors keeps both ends", migrate: started(upid), exit: "migration problems",
			log: []string{
				at + "a", at + "b", at + "c", at + "d", at + "e", at + "f", at + "g", at + "h", at + "i", at + "j", at + "k", at + "l",
			},
			wantStatus: "failed", wantAction: failedAudit, wantError: "a; b; c; d; e; h; i; j; k; l",
		},
		{
			name: "the log names nothing", migrate: started(upid), exit: "migration aborted", log: []string{"starting", taskEnd},
			wantStatus: "failed", wantAction: failedAudit, wantError: "migration aborted",
		},
		{name: "the task succeeds", migrate: started(upid), exit: "OK", wantStatus: "completed", wantAction: "drs_migrate"},
		{
			name: "HA: the migration fails", migrate: started(haUPID), exit: "migration aborted", log: []string{at + lockError, taskEnd},
			wantStatus: "failed", wantAction: failedAudit, wantError: lockError, wantTask: "completed",
		},
		{
			name: "HA: the migration succeeds", migrate: started(haUPID), exit: "OK",
			wantStatus: "completed", wantAction: "drs_migrate", wantTask: "completed",
		},
		{
			name: "HA: no migration starts", migrate: started(haUPID), noRealTask: true,
			wantStatus: "timeout", wantAction: failedAudit, wantError: "the HA manager started no migration within " + grace.String(), wantTask: "completed",
		},
		{
			name: "HA: the request itself fails", migrate: started(haUPID), haExit: "service is in an error state", noRealTask: true,
			wantStatus: "failed", wantAction: failedAudit, wantError: "service is in an error state", wantTask: "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			taskStatus := func(exit string) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					haData(w, proxmox.TaskStatus{Status: "stopped", ExitStatus: exit})
				}
			}
			handlers := map[string]http.HandlerFunc{
				"/api2/json/nodes/pve-01/qemu/101/migrate":              tc.migrate,
				"/api2/json/nodes/pve-01/tasks/" + upid + "/status":     taskStatus(tc.exit),
				"/api2/json/nodes/pve-01/tasks/" + haUPID + "/status":   taskStatus(cmp.Or(tc.haExit, "OK")),
				"/api2/json/nodes/pve-01/tasks/" + realUPID + "/status": taskStatus(tc.exit),
				// Following the wrong task fails on its text instead of polling a 404.
				"/api2/json/nodes/pve-01/tasks/" + earlierUPID + "/status": taskStatus("the wrong task"),
				"/api2/json/nodes/pve-01/tasks/" + otherUPID + "/status":   taskStatus("the wrong task"),
				// PVE lists what started from since on. A task of another guest and
				// the hamigrate itself are in that list; an earlier attempt is not.
				"/api2/json/nodes/pve-01/tasks": func(w http.ResponseWriter, r *http.Request) {
					tasks := []proxmox.NodeTask{
						{UPID: earlierUPID, ID: "101", Type: "qmigrate", StartTime: haStart - 0x100},
						{UPID: haUPID, ID: "101", Type: "hamigrate", StartTime: haStart},
						{UPID: otherUPID, ID: "102", Type: "qmigrate", StartTime: haStart + 1},
					}
					if !tc.noRealTask {
						tasks = append(tasks, proxmox.NodeTask{UPID: realUPID, ID: "101", Type: "qmigrate", StartTime: haStart + 13})
					}
					since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
					haData(w, slices.DeleteFunc(tasks, func(n proxmox.NodeTask) bool { return n.StartTime < since }))
				},
			}
			if tc.log != nil {
				handlers["/api2/json/nodes/pve-01/tasks/"+upid+"/log"] = taskLogHandler(tc.log)
				handlers["/api2/json/nodes/pve-01/tasks/"+realUPID+"/log"] = taskLogHandler(tc.log)
				// Reading the hamigrate's log instead fails on its text.
				handlers["/api2/json/nodes/pve-01/tasks/"+haUPID+"/log"] = taskLogHandler([]string{at + "the wrong task"})
			}
			srv, _ := haTestServer(t, handlers)
			q := &recordingExecQueries{}
			e := &Executor{
				queries:          q,
				logger:           slog.New(slog.DiscardHandler),
				shutdownCtx:      context.Background(),
				taskPollInterval: time.Millisecond,
				haStartGrace:     time.Minute,
			}
			if tc.noRealTask {
				e.haStartGrace = grace
			}
			rec := Recommendation{VMID: 101, VMType: "qemu", SourceNode: "pve-01", TargetNode: "pve-02"}
			if err := e.Execute(context.Background(), newHAClient(t, srv.URL), uuid.New(), "automatic", []Recommendation{rec}); err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if got := q.statuses[len(q.statuses)-1]; got != tc.wantStatus {
				t.Errorf("drs_history status = %q, want %q", got, tc.wantStatus)
			}
			last := q.audits[len(q.audits)-1]
			if last.Action != tc.wantAction {
				t.Errorf("audit action = %q, want %q", last.Action, tc.wantAction)
			}
			var details map[string]any
			if err := json.Unmarshal(last.Details, &details); err != nil {
				t.Fatalf("audit details: %v", err)
			}
			if got, _ := details["error"].(string); got != tc.wantError {
				t.Errorf("details.error = %q, want %q", got, tc.wantError)
			}
			if tc.wantTask != "" && q.task != tc.wantTask {
				t.Errorf("hamigrate's task row = %q, want %q", q.task, tc.wantTask)
			}
		})
	}
}

// A shutdown or a move of the leader role ends Execute's context, not the
// migration. What DRS then knows still has to reach the database, and not knowing
// how a migration ended must not read as a failure: the back-off would hold the
// guest back for a migration that succeeded.
func TestExecute_RecordsWhatItKnowsAfterItsContextEnds(t *testing.T) {
	t.Parallel()
	const (
		upid   = "UPID:pve-01:0:0:6AECD000:qmigrate:101:user@pam:"
		haUPID = "UPID:pve-01:0:0:6AECD100:hamigrate:101:user@pam:"
		reason = "migration aborted (duration 00:00:10): can't lock file - got timeout"
	)
	running := proxmox.TaskStatus{Status: "running"}
	aborted := proxmox.TaskStatus{Status: "stopped", ExitStatus: "migration aborted"}
	haOK := proxmox.TaskStatus{Status: "stopped", ExitStatus: "OK"}

	for _, tc := range []struct {
		name         string
		shutdown     bool               // the process stops, not only the leader role
		upid         string             // what the migrate call answers; empty cancels during it
		first, later proxmox.TaskStatus // the task's status at the poll that cancels, and after
		wantStatus   string
		wantAction   string
		wantError    string
		wantTask     string // how its task_history row was finalized; empty leaves it running
	}{
		{
			name: "shutdown while the task runs", shutdown: true, upid: upid, first: running, later: running,
			wantStatus: statusInterrupted, wantAction: "drs_migrate_interrupted",
		},
		{
			name: "shutdown just as the task fails", shutdown: true, upid: upid, first: running, later: aborted,
			wantStatus: "failed", wantAction: "drs_migrate_failed", wantError: "migration aborted", wantTask: "failed",
		},
		{
			name: "shutdown before the HA manager starts it", shutdown: true, upid: haUPID, first: haOK, later: haOK,
			wantStatus: statusInterrupted, wantAction: "drs_migrate_interrupted", wantTask: "completed",
		},
		{
			name: "shutdown during the request", shutdown: true,
			wantStatus: statusInterrupted, wantAction: "drs_migrate_interrupted",
		},
		{
			name:       "the leader role moves during the request",
			wantStatus: statusInterrupted, wantAction: "drs_migrate_interrupted",
		},
		{
			name: "the leader role moves while the task runs: DRS follows it to its end", upid: upid, first: running, later: aborted,
			wantStatus: "failed", wantAction: "drs_migrate_failed", wantError: reason, wantTask: "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancelTick := context.WithCancel(context.Background())
			defer cancelTick()
			shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
			defer cancelShutdown()
			end := func() {
				cancelTick()
				if tc.shutdown {
					cancelShutdown()
				}
			}

			var once sync.Once
			srv, _ := haTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/nodes/pve-01/qemu/101/migrate": func(w http.ResponseWriter, _ *http.Request) {
					if tc.upid == "" {
						end()
						http.Error(w, "the client has gone", http.StatusInternalServerError)
						return
					}
					haData(w, tc.upid)
				},
				"/api2/json/nodes/pve-01/tasks/" + tc.upid + "/status": func(w http.ResponseWriter, _ *http.Request) {
					status := tc.later
					once.Do(func() { status = tc.first; end() })
					haData(w, status)
				},
				"/api2/json/nodes/pve-01/tasks/" + tc.upid + "/log": taskLogHandler([]string{"2026-11-05 13:40:32 ERROR: " + reason}),
				"/api2/json/nodes/pve-01/tasks":                     func(w http.ResponseWriter, _ *http.Request) { haData(w, []proxmox.NodeTask{}) },
			})
			q := &recordingExecQueries{}
			e := &Executor{
				queries:          q,
				logger:           slog.New(slog.DiscardHandler),
				shutdownCtx:      shutdownCtx,
				taskPollInterval: time.Millisecond,
				haStartGrace:     time.Minute,
			}
			rec := Recommendation{VMID: 101, VMType: "qemu", SourceNode: "pve-01", TargetNode: "pve-02"}
			if err := e.Execute(ctx, newHAClient(t, srv.URL), uuid.New(), "automatic", []Recommendation{rec, rec}); err != nil {
				t.Fatalf("Execute: %v", err)
			}

			// The second recommendation is never started, so one outcome is all there is.
			if !slices.Equal(q.statuses, []string{tc.wantStatus}) {
				t.Fatalf("drs_history statuses = %v, want only %q", q.statuses, tc.wantStatus)
			}
			if len(q.audits) == 0 {
				t.Fatal("no audit row reached the database")
			}
			last := q.audits[len(q.audits)-1]
			if last.Action != tc.wantAction {
				t.Errorf("audit action = %q, want %q", last.Action, tc.wantAction)
			}
			var details map[string]any
			if err := json.Unmarshal(last.Details, &details); err != nil {
				t.Fatalf("audit details: %v", err)
			}
			if got, _ := details["error"].(string); got != tc.wantError {
				t.Errorf("details.error = %q, want %q", got, tc.wantError)
			}
			if got, _ := details["note"].(string); (got == interruptedNote) != (tc.wantStatus == statusInterrupted) {
				t.Errorf("details.note = %q for status %q", got, tc.wantStatus)
			}
			if q.task != tc.wantTask {
				t.Errorf("task row finalized as %q, want %q", q.task, tc.wantTask)
			}
		})
	}
}

// The process exits when main returns, so what a shutdown leaves Execute to write
// is written only if Drain waits for it.
func TestDrain_WaitsForExecuteAndGivesUpAtItsBound(t *testing.T) {
	t.Parallel()
	const upid = "UPID:pve-01:0:0:6AECD000:qmigrate:101:user@pam:"
	polled := make(chan struct{})
	var once sync.Once
	srv, _ := haTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve-01/qemu/101/migrate": func(w http.ResponseWriter, _ *http.Request) { haData(w, upid) },
		"/api2/json/nodes/pve-01/tasks/" + upid + "/status": func(w http.ResponseWriter, _ *http.Request) {
			once.Do(func() { close(polled) })
			haData(w, proxmox.TaskStatus{Status: "running"})
		},
	})
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	q := &recordingExecQueries{}
	e := &Executor{
		queries:          q,
		logger:           slog.New(slog.DiscardHandler),
		shutdownCtx:      shutdownCtx,
		taskPollInterval: time.Millisecond,
	}
	rec := Recommendation{VMID: 101, VMType: "qemu", SourceNode: "pve-01", TargetNode: "pve-02"}
	go func() {
		_ = e.Execute(shutdownCtx, newHAClient(t, srv.URL), uuid.New(), "automatic", []Recommendation{rec})
	}()
	<-polled
	shutdown()

	if !e.Drain(time.Minute) {
		t.Fatal("Drain gave up with a minute to spare")
	}
	if !slices.Equal(q.statuses, []string{statusInterrupted}) {
		t.Errorf("after Drain, drs_history statuses = %v, want the interrupted outcome written", q.statuses)
	}

	e.executing.Add(1)
	if e.Drain(time.Millisecond) {
		t.Error("Drain reported done with an Execute still in progress")
	}
}
