package rolling

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestRestore_StopsBetweenGuestsWhenTheJobIsPausedOrCancelled: the restore
// re-reads the job before each guest, as the drain does, so a pause or a cancel
// (one check, isJobCancelled, covers both) stops guests being moved back rather
// than waiting until every one of them has been. A running job's restore still
// moves each guest and completes the node.
func TestRestore_StopsBetweenGuestsWhenTheJobIsPausedOrCancelled(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		status        string
		wantMigrates  int
		wantCompleted bool
	}{
		{"cancelled", 0, false},
		{"running", 2, true},
	} {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			d := &memberDB{t: t, jobStatus: tt.status,
				accept: map[string]int64{"SetNodeRestoreStarted": 1, "SetNodeRestoreCompleted": 1}}
			// Both guests are on pve-02. Proxmox refuses each move back, which
			// the restore skips past, so there is no task to wait out.
			o, client, sent := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api2/json/nodes":
					_, _ = w.Write([]byte(`{"data":[{"node":"pve-01","status":"online"},{"node":"pve-02","status":"online"}]}`))
				case "/api2/json/nodes/pve-02/qemu":
					_, _ = w.Write([]byte(`{"data":[{"vmid":101,"status":"running"},{"vmid":102,"status":"running"}]}`))
				default:
					http.Error(w, "refused", http.StatusInternalServerError)
				}
			})
			job := db.RollingUpdateJob{ID: uuid.New(), Status: "running", AutoRestoreGuests: true}
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "health_check",
				GuestsJson: []byte(`[{"vmid":101,"type":"qemu"},{"vmid":102,"type":"qemu"}]`), StoppedPassthroughJson: []byte(`[]`)}

			o.advanceHealthCheck(context.Background(), client, job, node)

			migrates := 0
			for _, req := range sent() {
				if strings.HasSuffix(req, "/migrate") {
					migrates++
				}
			}
			if completed := slices.Contains(d.writes, "SetNodeRestoreCompleted"); migrates != tt.wantMigrates || completed != tt.wantCompleted {
				t.Errorf("moved back %d guests, completed = %v; want %d and %v (sent %v)",
					migrates, completed, tt.wantMigrates, tt.wantCompleted, sent())
			}
		})
	}
}

// TestRestore_RecordsTheMigrationItStarts: a guest task the engine starts gets
// its task_history row and an audit row carrying the UPID, as DRS and the
// scheduler record theirs; without them the collector ingests it as Proxmox's.
func TestRestore_RecordsTheMigrationItStarts(t *testing.T) {
	t.Parallel()
	const upid = "UPID:pve-02:00001234:00000000:68000000:qmigrate:101:nexara@pve!api:"
	d := &memberDB{t: t, jobStatus: "running", audits: true, accept: map[string]int64{"SetNodeRestoreStarted": 1}}
	o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api2/json/nodes":
			_, _ = w.Write([]byte(`{"data":[{"node":"pve-01","status":"online"},{"node":"pve-02","status":"online"}]}`))
		case "/api2/json/nodes/pve-02/qemu":
			_, _ = w.Write([]byte(`{"data":[{"vmid":101,"status":"running"}]}`))
		case "/api2/json/nodes/pve-02/qemu/101/migrate":
			_, _ = w.Write([]byte(`{"data":"` + upid + `"}`))
		default:
			http.Error(w, "refused", http.StatusInternalServerError)
		}
	})
	// A shutting-down engine stops waiting on the task at once: the record is
	// written before the wait, so that is all this needs.
	shutdown, cancel := context.WithCancel(context.Background())
	cancel()
	o.shutdownCtx = shutdown
	job := db.RollingUpdateJob{ID: uuid.New(), Status: "running", AutoRestoreGuests: true}
	node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "health_check",
		GuestsJson: []byte(`[{"vmid":101,"type":"qemu"}]`), StoppedPassthroughJson: []byte(`[]`)}

	o.advanceHealthCheck(context.Background(), client, job, node)

	if !slices.Equal(d.tasks, []string{upid + " rolling_migrate"}) {
		t.Errorf("task rows = %q, want the migration's", d.tasks)
	}
	audits := d.rowsOf("rolling_update")
	if !slices.ContainsFunc(audits, func(r auditRow) bool {
		return r.action == "rolling_update_guest_migrate" && r.details["upid"] == upid
	}) {
		t.Errorf("no rolling_update_guest_migrate audit row with the UPID in %v", audits)
	}
}
