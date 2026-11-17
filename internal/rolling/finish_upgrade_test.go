package rolling

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// TestFinishUpgrade_RebootsOnlyWhileTheJobRuns: a node whose automated upgrade
// finishes after its job failed (another node failed meanwhile) is left upgraded
// and not rebooted, since the failed job has released what kept guests off it.
// While the job runs, the node is rebooted as before, and also with Reboot after
// update off when its upgrade installed a kernel, with no SSH check (a stopped
// shutdown context would make one end the step).
func TestFinishUpgrade_RebootsOnlyWhileTheJobRuns(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		status     string
		kernel     bool // Reboot after update off, a kernel among the packages
		wantReboot bool
	}{
		{"failed", false, false},
		{"running", false, true},
		{"running", true, true},
	} {
		t.Run(fmt.Sprintf("%s/kernel=%v", tt.status, tt.kernel), func(t *testing.T) {
			t.Parallel()
			d := &memberDB{t: t, audits: true, jobStatus: tt.status, accept: map[string]int64{"SetNodeUpgradeCompleted": 1}}
			// An empty node, and a reboot Proxmox accepts.
			o, client, sent := engineOn(t, d, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[]}`))
			})
			// With Reboot after update on, no SSH check of whether one is owed.
			job := db.RollingUpdateJob{ID: uuid.New(), Status: "running", DrainGuests: true, RebootAfterUpdate: !tt.kernel}
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "upgrading"}
			if tt.kernel {
				node.PackagesJson = []byte(`[{"Package":"proxmox-kernel-6.17","OldVersion":"6.17.2-1"}]`)
				stopped, cancel := context.WithCancel(context.Background())
				cancel()
				o.shutdownCtx = stopped
			}

			o.finishUpgrade(context.Background(), job, node, client, sshpkg.Config{})

			if rebooted := slices.Contains(sent(), "POST /api2/json/nodes/pve-01/status"); rebooted != tt.wantReboot {
				t.Errorf("rebooted = %v, want %v (sent %v, wrote %v)", rebooted, tt.wantReboot, sent(), d.writes)
			}
		})
	}
}
