package collector

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A sample whose disk counters PVE left unreported is no disk baseline: node
// and VM republish their last disk rates while their network rates move on,
// the next reported sample is measured across the gap, and an unreported first
// sample leaves nothing to measure a burst from.
func TestRateStateAcrossUnreported(t *testing.T) {
	t.Parallel()
	rs := newRateState()
	cluster, node, vm := uuid.New(), uuid.New(), uuid.New()
	start := time.Now()
	for _, step := range []struct {
		name              string
		at                time.Duration
		disk, net         int64
		unreported        bool
		wantDisk, wantNet float64
	}{
		{"an unreported first sample", 0, 0, 0, true, 0, 0},
		{"is no disk baseline", 20 * time.Second, 1000, 1000, false, 0, 50},
		{"a reported pair", 40 * time.Second, 2000, 2000, false, 50, 50},
		{"an unreported sample holds disk alone", 60 * time.Second, 2000, 4000, true, 50, 100},
		{"the next is measured across the gap", 100 * time.Second, 5000, 6000, false, 50, 50},
	} {
		// Write and out run at twice read and in, so each rate's counter is pinned.
		n, v := rs.Apply(cluster, start.Add(step.at),
			[]nodeMetricSnapshot{{NodeID: node, DiskRead: step.disk, DiskWrite: 2 * step.disk, NetIn: step.net, NetOut: 2 * step.net, DiskUnreported: step.unreported}},
			[]vmMetricSnapshot{{VMID: vm, DiskRead: step.disk, DiskWrite: 2 * step.disk, NetIn: step.net, NetOut: 2 * step.net, DiskUnreported: step.unreported}})
		for who, r := range map[string]entityRates{"node": n[node], "VM": v[vm]} {
			got := []float64{r.DiskReadBps, r.DiskWriteBps, r.NetInBps, r.NetOutBps}
			want := []float64{step.wantDisk, 2 * step.wantDisk, step.wantNet, 2 * step.wantNet}
			if !slices.Equal(got, want) {
				t.Errorf("%s, %s: disk read/write, net in/out = %v B/s, want %v", step.name, who, got, want)
			}
		}
	}
}
