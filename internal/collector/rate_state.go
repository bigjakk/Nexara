package collector

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// entityCounter stores the cumulative I/O counters and the timestamp at which
// they were sampled. Used to derive bytes-per-second rates against the next
// successive sample for the same entity.
type entityCounter struct {
	diskRead  int64
	diskWrite int64
	netIn     int64
	netOut    int64
	ts        time.Time
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

	prev := rs.perCluster[clusterID]
	nodeRates = make(map[uuid.UUID]entityRates, len(nodes))
	vmRates = make(map[uuid.UUID]entityRates, len(vms))

	nextNodes := make(map[uuid.UUID]entityCounter, len(nodes))
	for _, n := range nodes {
		if prev != nil {
			if p, ok := prev.nodes[n.NodeID]; ok {
				nodeRates[n.NodeID] = derive(p, n.DiskRead, n.DiskWrite, n.NetIn, n.NetOut, now)
			}
		}
		nextNodes[n.NodeID] = entityCounter{n.DiskRead, n.DiskWrite, n.NetIn, n.NetOut, now}
	}

	nextVMs := make(map[uuid.UUID]entityCounter, len(vms))
	for _, v := range vms {
		if prev != nil {
			if p, ok := prev.vms[v.VMID]; ok {
				vmRates[v.VMID] = derive(p, v.DiskRead, v.DiskWrite, v.NetIn, v.NetOut, now)
			}
		}
		nextVMs[v.VMID] = entityCounter{v.DiskRead, v.DiskWrite, v.NetIn, v.NetOut, now}
	}

	rs.perCluster[clusterID] = &clusterRates{nodes: nextNodes, vms: nextVMs}
	return nodeRates, vmRates
}

// Forget drops cached counters for a cluster. Safe to call for an unknown id.
func (rs *rateState) Forget(clusterID uuid.UUID) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.perCluster, clusterID)
}

func derive(prev entityCounter, diskRead, diskWrite, netIn, netOut int64, now time.Time) entityRates {
	dt := now.Sub(prev.ts).Seconds()
	if dt <= 0 {
		return entityRates{}
	}
	return entityRates{
		DiskReadBps:  perSecond(prev.diskRead, diskRead, dt),
		DiskWriteBps: perSecond(prev.diskWrite, diskWrite, dt),
		NetInBps:     perSecond(prev.netIn, netIn, dt),
		NetOutBps:    perSecond(prev.netOut, netOut, dt),
	}
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
