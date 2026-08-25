package handlers

import "testing"

// TestVmidFromUPID_OnlyGuestTaskTypes is the Go half of the guard that
// migrations/000084's is_guest_upid() is the SQL half of. Both answer the same
// question about the same field, and they must agree.
//
// The case that motivates it: a UPID's worker id (field 7) is a plain integer
// for plenty of non-guest task types. cephdestroyosd's is the OSD number, and
// ceph_osd.go dispatches exactly that through TrackTask — so a digits-only
// check records "destroy OSD 113" as an action on VM 113, answers a ?vmids=113
// query with it, and forwards it to a SIEM under that guest's identity.
func TestVmidFromUPID_OnlyGuestTaskTypes(t *testing.T) {
	tests := []struct {
		name string
		upid string
		want int
	}{
		// Guest task families.
		{"qemu start", "UPID:HV01:001316BE:00B8B463:6A8CE407:qmstart:110:root@pam!nexara:", 110},
		{"qemu destroy", "UPID:HV03:001316BE:00B8B463:6A8CE407:qmdestroy:121:root@pam:", 121},
		{"lxc start", "UPID:HV01:001316BE:00B8B463:6A8CE407:vzstart:104:root@pam:", 104},
		{"guest backup", "UPID:HV01:001316BE:00B8B463:6A8CE407:vzdump:101:root@pam:", 101},
		{"ha migrate", "UPID:HV01:001316BE:00B8B463:6A8CE407:hamigrate:125:root@pam:", 125},
		// Carry a VMID without a guest prefix.
		{"disk resize", "UPID:HV01:001316BE:00B8B463:6A8CE407:resize:102:root@pam:", 102},
		{"volume move", "UPID:HV01:001316BE:00B8B463:6A8CE407:move_volume:102:root@pam:", 102},

		// Numeric worker id that is NOT a VMID — the whole point.
		{"ceph osd destroy", "UPID:HV01:001316BE:00B8B463:6A8CE407:cephdestroyosd:113:root@pam:", 0},

		// No guest, for assorted reasons.
		{"node apt update has an empty id", "UPID:HV01:0000A1B2:00000001:6A8CE407:aptupdate::root@pam:", 0},
		{"node-wide backup has an empty id", "UPID:HV01:0000A1B2:00000001:6A8CE407:vzdump::root@pam:", 0},
		{"storage id is not numeric", "UPID:HV01:0000A1B2:00000001:6A8CE407:imgdel:105@proxmox-hdd:root@pam:", 0},
		{"service restart names a unit", "UPID:HV01:0000A1B2:00000001:6A8CE407:srvrestart:osd.1:root@pam:", 0},
		{"truncated upid", "UPID:HV01:0000A1B2", 0},
		{"empty", "", 0},
		// Bounded so the ::int cast on the SQL side cannot overflow.
		{"absurd id is rejected, not truncated", "UPID:HV01:0000A1B2:00000001:6A8CE407:qmstart:1234567890:root@pam:", 0},
		{"zero is not a vmid", "UPID:HV01:0000A1B2:00000001:6A8CE407:qmstart:0:root@pam:", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vmidFromUPID(tt.upid); got != tt.want {
				t.Errorf("vmidFromUPID(%q) = %d, want %d", tt.upid, got, tt.want)
			}
		})
	}
}
