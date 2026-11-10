package rolling

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestConfirmUpgrade_OnlyARunningJobsNodeIsConfirmed: a failed, cancelled or
// paused job leaves its awaiting node where it was, and confirming that node
// must not reboot it, so nothing is looked up, written or sent to Proxmox. A
// running job whose confirm a cancel overtook (the guarded write matches no
// row) is refused too, rather than going on to the reboot.
func TestConfirmUpgrade_OnlyARunningJobsNodeIsConfirmed(t *testing.T) {
	t.Parallel()
	cluster := uuid.New()
	for _, tt := range []struct {
		status     string
		reboot     bool
		wantAsked  int // node-membership lookups
		wantWrites []string
	}{
		{status: "failed", reboot: true},
		{status: "cancelled", reboot: true},
		{status: "paused", reboot: true},
		// Overtaken. Without a reboot the engine needs no client to get there.
		{status: "running", wantAsked: 1, wantWrites: []string{"ConfirmNodeUpgrade"}},
	} {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}, accept: map[string]int64{"ConfirmNodeUpgrade": 0}}
			o, _, sent := engineOn(t, d, nil)
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: tt.status, RebootAfterUpdate: tt.reboot}
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "awaiting_upgrade"}

			if err := o.ConfirmUpgrade(context.Background(), job, node); err == nil {
				t.Fatal("the node was confirmed")
			}
			if len(d.asked) != tt.wantAsked || !slices.Equal(d.writes, tt.wantWrites) || len(sent()) != 0 {
				t.Errorf("asked %v, wrote %v, sent %v", d.asked, d.writes, sent())
			}
		})
	}
}
