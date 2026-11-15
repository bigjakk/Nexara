package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// For an HA-managed guest Proxmox answers the migrate call with an hamigrate
// that ends OK once queued. The job must end as the HA manager's migration
// ends, and as not moved when the HA manager starts none.
func TestPollTaskStatus_FollowsTheHAManagersMigration(t *testing.T) {
	t.Parallel()
	const (
		request = "UPID:pve-01:00001234:00000000:68000000:hamigrate:100:nexara@pve!api:"
		worker  = "UPID:pve-01:00001240:00000000:68000010:qmigrate:100:root@pam:"
	)
	for _, tc := range []struct {
		name      string
		tasks     []map[string]any
		listFails bool
		wantError string
	}{
		{
			name:      "its migration fails",
			tasks:     []map[string]any{{"upid": worker, "type": "qmigrate", "id": "100", "node": credTestNode}},
			wantError: "migration aborted",
		},
		{name: "no migration starts", wantError: "not moved"},
		{name: "the task list cannot be read", listFails: true, wantError: "could not confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reply := func(w http.ResponseWriter, data any) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/api2/json/nodes/"+credTestNode+"/tasks/"+request+"/status", func(w http.ResponseWriter, _ *http.Request) {
				reply(w, map[string]any{"status": "stopped", "exitstatus": "OK", "upid": request})
			})
			mux.HandleFunc("/api2/json/nodes/"+credTestNode+"/tasks/"+worker+"/status", func(w http.ResponseWriter, _ *http.Request) {
				reply(w, map[string]any{"status": "stopped", "exitstatus": "migration aborted", "upid": worker})
			})
			mux.HandleFunc("/api2/json/nodes/"+credTestNode+"/tasks", func(w http.ResponseWriter, _ *http.Request) {
				if tc.listFails {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				reply(w, tc.tasks)
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			var execs []execCall
			o := newCredTestOrchestrator(t, srv.URL, &execs)
			o.pollInterval, o.haStartGrace = time.Millisecond, 50*time.Millisecond
			mc := &migrationContext{job: db.MigrationJob{
				SourceClusterID: uuid.New(), Vmid: credTestSourceVM, VmType: VMTypeQEMU, MigrationType: TypeIntraCluster,
			}}
			o.pollTaskStatus(context.Background(), mustClient(t, o), credTestNode, request, uuid.New(), mc)

			// CompleteMigrationJob binds (id, status, completed_at, error_message).
			if got := findExecArg(t, execs, "SET status = $2, completed_at", 1); got != StatusFailed {
				t.Errorf("job status = %q, want %q", got, StatusFailed)
			}
			if got := findExecArg(t, execs, "SET status = $2, completed_at", 3); !strings.Contains(got, tc.wantError) {
				t.Errorf("job error = %q, want it to contain %q", got, tc.wantError)
			}
		})
	}
}

// A follow cut off by shutdown leaves the HA manager's migration's row to the
// collector, which owns it: only the request's row is Nexara's to abandon.
func TestPollTaskStatus_InterruptedFollowLeavesTheWorkersRow(t *testing.T) {
	t.Parallel()
	const (
		request = "UPID:pve-01:00001234:00000000:68000000:hamigrate:100:nexara@pve!api:"
		worker  = "UPID:pve-01:00001240:00000000:68000010:qmigrate:100:root@pam:"
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reply := func(w http.ResponseWriter, data any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/"+credTestNode+"/tasks/"+request+"/status", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"status": "stopped", "exitstatus": "OK", "upid": request})
	})
	mux.HandleFunc("/api2/json/nodes/"+credTestNode+"/tasks", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, []map[string]any{{"upid": worker, "type": "qmigrate", "id": "100", "node": credTestNode}})
	})
	mux.HandleFunc("/api2/json/nodes/"+credTestNode+"/tasks/"+worker+"/status", func(w http.ResponseWriter, _ *http.Request) {
		cancel() // the process is going while the migration runs
		reply(w, map[string]any{"status": "running", "upid": worker})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var execs []execCall
	o := newCredTestOrchestrator(t, srv.URL, &execs)
	o.pollInterval, o.haStartGrace = time.Millisecond, time.Minute
	mc := &migrationContext{job: db.MigrationJob{
		SourceClusterID: uuid.New(), Vmid: credTestSourceVM, VmType: VMTypeQEMU, MigrationType: TypeIntraCluster,
	}}
	o.pollTaskStatus(ctx, mustClient(t, o), credTestNode, request, uuid.New(), mc)

	for _, e := range execs {
		if strings.Contains(e.sql, "UPDATE task_history") && len(e.args) > 0 && e.args[0] == worker {
			t.Errorf("wrote the HA manager's migration's row: %v", e.args)
		}
	}
	if got := findExecArg(t, execs, "SET status = $2, completed_at", 1); got != StatusFailed {
		t.Errorf("job status = %q, want %q", got, StatusFailed)
	}
}
