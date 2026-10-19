package collector

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// A disk counter that reads 0 after its QEMU process reported more is one PVE
// did not report, and is carried forward; a 0 from a new process, a counter
// that has read 0 all along, and a stopped guest's are readings.
func TestDiskCarry(t *testing.T) {
	t.Parallel()
	vm := func(node string, pid int, read, write int64) proxmox.VirtualMachine {
		return proxmox.VirtualMachine{VMID: 100, Status: "running", Node: node, PID: pid, DiskRead: read, DiskWrite: write}
	}
	stopped := proxmox.VirtualMachine{VMID: 100, Status: "stopped", Node: "pve-01"}
	tests := []struct {
		name                string
		samples             []proxmox.VirtualMachine // oldest first; the last is checked
		wantRead, wantWrite int64
		wantCarried         bool
	}{
		{"a reported reading is stored", []proxmox.VirtualMachine{vm("pve-01", 7, 500, 700)}, 500, 700, false},
		{"a 0 after more is carried", []proxmox.VirtualMachine{vm("pve-01", 7, 500, 700), vm("pve-01", 7, 0, 0)}, 500, 700, true},
		{"a run of 0s stays carried", []proxmox.VirtualMachine{vm("pve-01", 7, 500, 700), vm("pve-01", 7, 0, 0), vm("pve-01", 7, 0, 0)}, 500, 700, true},
		{"one counter's 0 is carried alone", []proxmox.VirtualMachine{vm("pve-01", 7, 500, 700), vm("pve-01", 7, 0, 800)}, 500, 800, true},
		{"a new process starts again", []proxmox.VirtualMachine{vm("pve-01", 7, 500, 700), vm("pve-01", 9, 0, 0)}, 0, 0, false},
		{"so does one on another node", []proxmox.VirtualMachine{vm("pve-01", 7, 500, 700), vm("pve-02", 7, 0, 0)}, 0, 0, false},
		{"a counter that read 0 all along is a reading", []proxmox.VirtualMachine{vm("pve-01", 7, 0, 700), vm("pve-01", 7, 0, 750)}, 0, 750, false},
		{"a stopped guest's zeros are readings, PID or none", []proxmox.VirtualMachine{vm("pve-01", 0, 500, 700), stopped}, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var c diskCarry
			cluster := uuid.New()
			var read, write int64
			var carried bool
			for _, s := range tt.samples {
				read, write, carried = c.counters(cluster, s)
			}
			if read != tt.wantRead || write != tt.wantWrite || carried != tt.wantCarried {
				t.Errorf("counters = (%d, %d, %v), want (%d, %d, %v)", read, write, carried, tt.wantRead, tt.wantWrite, tt.wantCarried)
			}
		})
	}
}

// A carry gives up after maxDiskCarry: a counter still 0 by then (its disk
// unplugged, say) is a reading, so the node it marks is not held for good.
func TestDiskCarryGivesUp(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := diskCarry{now: func() time.Time { return now }}
	cluster := uuid.New()
	vm := proxmox.VirtualMachine{VMID: 100, Status: "running", Node: "pve-01", PID: 7, DiskRead: 500, DiskWrite: 700}
	c.counters(cluster, vm)
	vm.DiskRead, vm.DiskWrite = 0, 0
	if _, _, carried := c.counters(cluster, vm); !carried {
		t.Fatal("the first 0 after more was not carried")
	}
	now = now.Add(maxDiskCarry)
	if read, write, carried := c.counters(cluster, vm); read != 0 || write != 0 || carried {
		t.Errorf("after maxDiskCarry counters = (%d, %d, %v), want (0, 0, false)", read, write, carried)
	}
}
