package collector

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// counterPair is a pair of cumulative counters (disk read and write, or
// network in and out), the time they were sampled — zero before the first —
// and the bytes-per-second rates last derived from them.
type counterPair struct {
	a, b       int64
	ts         time.Time
	aBps, bBps float64
}

// entityCounter stores an entity's disk and network counters, each pair a
// baseline of its own: PVE can leave a VM's disk counters unreported while
// its network ones are real. Used to derive bytes-per-second rates against
// the next successive sample for the same entity.
type entityCounter struct {
	disk, net counterPair
}

// clusterRates partitions per-entity counters by cluster so a cluster delete
// can drop its slice of state without walking every entry.
type clusterRates struct {
	nodes map[uuid.UUID]entityCounter
	vms   map[uuid.UUID]entityCounter
}

// rateState tracks the previous cumulative I/O counters per entity so the
// publisher can derive bytes-per-second rates between successive collection
// cycles. Safe for concurrent use.
type rateState struct {
	mu         sync.Mutex
	perCluster map[uuid.UUID]*clusterRates
}

func newRateState() *rateState {
	return &rateState{perCluster: make(map[uuid.UUID]*clusterRates)}
}

// entityRates holds the four bytes-per-second values published alongside the
// cumulative counters.
type entityRates struct {
	DiskReadBps  float64
	DiskWriteBps float64
	NetInBps     float64
	NetOutBps    float64
}

// Apply records the current cycle's counters and returns the rates computed
// against the prior cycle. Entities not seen this cycle are evicted, so the
// state never accumulates stale entries.
func (rs *rateState) Apply(
	clusterID uuid.UUID,
	now time.Time,
	nodes []nodeMetricSnapshot,
	vms []vmMetricSnapshot,
) (nodeRates, vmRates map[uuid.UUID]entityRates) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	var prevNodes, prevVMs map[uuid.UUID]entityCounter
	if prev := rs.perCluster[clusterID]; prev != nil {
		prevNodes, prevVMs = prev.nodes, prev.vms
	}
	nodeRates = make(map[uuid.UUID]entityRates, len(nodes))
	vmRates = make(map[uuid.UUID]entityRates, len(vms))

	nextNodes := make(map[uuid.UUID]entityCounter, len(nodes))
	for _, n := range nodes {
		p := prevNodes[n.NodeID]
		next := entityCounter{
			disk: p.disk.advance(n.DiskRead, n.DiskWrite, now, n.DiskUnreported),
			net:  p.net.advance(n.NetIn, n.NetOut, now, false),
		}
		nextNodes[n.NodeID] = next
		nodeRates[n.NodeID] = next.rates()
	}

	nextVMs := make(map[uuid.UUID]entityCounter, len(vms))
	for _, v := range vms {
		p := prevVMs[v.VMID]
		next := entityCounter{
			disk: p.disk.advance(v.DiskRead, v.DiskWrite, now, v.DiskUnreported),
			net:  p.net.advance(v.NetIn, v.NetOut, now, false),
		}
		nextVMs[v.VMID] = next
		vmRates[v.VMID] = next.rates()
	}

	rs.perCluster[clusterID] = &clusterRates{nodes: nextNodes, vms: nextVMs}
	return nodeRates, vmRates
}

// advance returns the pair after a sample. An unreported sample leaves it as
// it was, baseline and rates, so the next reported one is measured across the
// gap instead of bunching the gap's bytes into one rate.
func (p counterPair) advance(a, b int64, now time.Time, unreported bool) counterPair {
	if unreported {
		return p
	}
	next := counterPair{a: a, b: b, ts: now}
	if dt := now.Sub(p.ts).Seconds(); !p.ts.IsZero() && dt > 0 {
		next.aBps, next.bBps = perSecond(p.a, a, dt), perSecond(p.b, b, dt)
	}
	return next
}

func (c entityCounter) rates() entityRates {
	return entityRates{DiskReadBps: c.disk.aBps, DiskWriteBps: c.disk.bBps, NetInBps: c.net.aBps, NetOutBps: c.net.bBps}
}

// Forget drops cached counters for a cluster. Safe to call for an unknown id.
func (rs *rateState) Forget(clusterID uuid.UUID) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.perCluster, clusterID)
}

// perSecond floors at 0 to absorb counter resets (e.g. node reboot, Proxmox
// restart) without showing negative throughput.
func perSecond(prev, curr int64, dtSec float64) float64 {
	d := float64(curr - prev)
	if d < 0 {
		return 0
	}
	return d / dtSec
}
