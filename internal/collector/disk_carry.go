package collector

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// diskCarry keeps each running VM's last reported disk counters, so a sample
// in which PVE left them unreported stores those rather than the 0 it reads.
//
// PVE's full status takes a running VM's diskread and diskwrite from QEMU's
// blockstats over QMP, and when QMP does not answer in time, as under load,
// both read 0 while CPU, memory and network are real. Within one QEMU process
// a counter falls to 0 only if its last device is removed, so a 0 from a
// process that has reported more is taken as a reading PVE did not make. A
// new process (the guest restarted or moved node) starts its counters again,
// so nothing carries across one, and a counter that has read 0 all along (a
// guest with no block device) stays a reading.
type diskCarry struct {
	mu   sync.Mutex
	last map[diskCarryKey]reportedDisk
	now  func() time.Time // nil: time.Now
}

// maxDiskCarry is how long a counter is carried before its 0 is taken as a
// reading: the guest's last disk removed, or QMP gone for good. A carry marks
// the guest's node too, and this bounds how long that holds the node's disk
// rules. The longest dip in a month of a test cluster's counters was about
// two hours.
const maxDiskCarry = 6 * time.Hour

type diskCarryKey struct {
	cluster uuid.UUID
	vmid    int
}

// reportedDisk is a VM's last stored disk counters, the QEMU process, by node
// and PID, they came from, and when the carry they hold began, if they do.
type reportedDisk struct {
	node         string
	pid          int
	read, write  int64
	carriedSince time.Time
}

// counters returns the disk counters to store for vm, and whether either is
// carried forward from an earlier sample.
func (c *diskCarry) counters(cluster uuid.UUID, vm proxmox.VirtualMachine) (read, write int64, carried bool) {
	key := diskCarryKey{cluster: cluster, vmid: vm.VMID}
	c.mu.Lock()
	defer c.mu.Unlock()
	if vm.Status != "running" {
		delete(c.last, key)
		return vm.DiskRead, vm.DiskWrite, false
	}
	read, write = vm.DiskRead, vm.DiskWrite
	var since time.Time
	if prev, ok := c.last[key]; ok && prev.node == vm.Node && prev.pid == vm.PID &&
		((read == 0 && prev.read > 0) || (write == 0 && prev.write > 0)) {
		now := time.Now()
		if c.now != nil {
			now = c.now()
		}
		if since = prev.carriedSince; since.IsZero() {
			since = now
		}
		if now.Sub(since) < maxDiskCarry {
			if read == 0 {
				read = prev.read
			}
			if write == 0 {
				write = prev.write
			}
			carried = true
		} else {
			since = time.Time{}
		}
	}
	if c.last == nil {
		c.last = make(map[diskCarryKey]reportedDisk)
	}
	c.last[key] = reportedDisk{node: vm.Node, pid: vm.PID, read: read, write: write, carriedSince: since}
	return read, write, carried
}
