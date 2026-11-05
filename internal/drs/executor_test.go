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
// must abort the 30-minute poll loop promptly. The 10-second final-check
// against context.Background() still runs (sees "running" → returns
// "timeout") so a tracked task isn't orphaned without an outcome.
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
		if r.status != "timeout" {
			t.Errorf("waitForTask status = %q, want %q after shutdown cancel", r.status, "timeout")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForTask did not return within 3s of shutdown cancel — pollCtx is not derived from shutdownCtx")
	}
}

// recordingExecQueries records what Execute writes and accepts all of it.
type recordingExecQueries struct {
	statuses []string
	task     string // the status the task_history row was finalized with
	audits   []db.InsertAuditLogParams
}

func (q *recordingExecQueries) InsertDRSHistory(context.Context, db.InsertDRSHistoryParams) (db.DrsHistory, error) {
	return db.DrsHistory{ID: uuid.New()}, nil
}

func (q *recordingExecQueries) UpdateDRSHistoryStatus(_ context.Context, arg db.UpdateDRSHistoryStatusParams) error {
	q.statuses = append(q.statuses, arg.Status)
	return nil
}

func (q *recordingExecQueries) InsertTaskHistory(context.Context, db.InsertTaskHistoryParams) (db.TaskHistory, error) {
	return db.TaskHistory{}, nil
}

func (q *recordingExecQueries) ReconcileTaskHistory(_ context.Context, arg db.ReconcileTaskHistoryParams) (int64, error) {
	q.task = arg.Status
	return 1, nil
}

func (q *recordingExecQueries) GetVMByClusterAndVmid(context.Context, db.GetVMByClusterAndVmidParams) (db.Vm, error) {
	return db.Vm{ID: uuid.New()}, nil
}

func (q *recordingExecQueries) InsertAuditLog(_ context.Context, arg db.InsertAuditLogParams) error {
	q.audits = append(q.audits, arg)
	return nil
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
	)
	started := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { haData(w, id) }
	}

	for _, tc := range []struct {
		name       string
		migrate    http.HandlerFunc
		exit       string // of the task doing the migration
		haExit     string // of the hamigrate, which normally ends OK
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
		{name: "the task succeeds", migrate: started(upid), exit: "OK", wantStatus: "completed", wantAction: "drs_migrate"},
		{
			name: "HA: the migration fails", migrate: started(haUPID), exit: "migration aborted",
			wantStatus: "failed", wantAction: failedAudit, wantError: "migration aborted", wantTask: "completed",
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
			srv, _ := haTestServer(t, map[string]http.HandlerFunc{
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
			})
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
