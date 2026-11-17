package rolling

import (
	"errors"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestCephCheck: "clean" is every PG active and clean and the OSDs back to the
// baseline, never HEALTH_OK, and a failed read is could-not-look, never clean.
func TestCephCheck(t *testing.T) {
	t.Parallel()
	status := func(up int, pools int, states ...proxmox.PGStateCount) *proxmox.CephStatus {
		s := &proxmox.CephStatus{Health: proxmox.CephHealth{Status: "HEALTH_WARN"}}
		s.OSDMap.NumUpOSDs = up
		s.PGMap.NumPools = pools
		s.PGMap.PGsPerState = states
		for _, st := range states {
			s.PGMap.NumPGs += st.Count
		}
		return s
	}
	pg := func(state string, n int) proxmox.PGStateCount { return proxmox.PGStateCount{StateName: state, Count: n} }
	for _, tt := range []struct {
		name     string
		status   *proxmox.CephStatus
		err      error
		baseline int
		want     CephOutcome
		reason   string
	}{
		{"all clean", status(3, 2, pg("active+clean", 128)), nil, 3, CephClean, ""},
		{"scrubbing", status(3, 2, pg("active+clean", 120), pg("active+clean+scrubbing+deep", 8)), nil, 3, CephClean, ""},
		{"degraded", status(2, 2, pg("active+clean", 100), pg("active+undersized+degraded", 28)), nil, 2, CephNotClean,
			"28 of 128 placement groups are not active+clean"},
		{"peering", status(3, 2, pg("peering", 4), pg("active+clean", 124)), nil, 3, CephNotClean,
			"4 of 128 placement groups are not active+clean"},
		{"an OSD short of the baseline", status(2, 2, pg("active+clean", 128)), nil, 3, CephNotClean, "2 OSDs up, 3 expected"},
		{"an OSD already down at the baseline", status(2, 2, pg("active+clean", 128)), nil, 2, CephClean, ""},
		{"no PG entries", status(3, 2), nil, 3, CephNotClean, "Ceph reports no placement group states"},
		{"read failed", nil, errors.New("connection refused"), 3, CephUnknown, "Could not read the Ceph status from Proxmox"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cephCheck(tt.status, tt.err, tt.baseline)
			if got.Outcome != tt.want || got.Reason != tt.reason {
				t.Errorf("cephCheck = %s %q, want %s %q", got.Outcome, got.Reason, tt.want, tt.reason)
			}
		})
	}
}
