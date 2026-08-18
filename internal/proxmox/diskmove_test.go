package proxmox

import "testing"

func TestDiskMoveSpecValidate(t *testing.T) {
	base := DiskMoveSpec{Disk: "scsi0", TargetStorage: "ceph"}

	tests := []struct {
		name    string
		mutate  func(*DiskMoveSpec)
		wantErr bool
	}{
		{"minimal", func(*DiskMoveSpec) {}, false},
		{"empty format means storage decides", func(s *DiskMoveSpec) { s.Format = "" }, false},
		{"qcow2", func(s *DiskMoveSpec) { s.Format = "qcow2" }, false},
		{"raw", func(s *DiskMoveSpec) { s.Format = "raw" }, false},
		{"vmdk", func(s *DiskMoveSpec) { s.Format = "vmdk" }, false},
		{"unknown format", func(s *DiskMoveSpec) { s.Format = "subvol" }, true},
		{"missing disk", func(s *DiskMoveSpec) { s.Disk = "" }, true},
		{"missing storage", func(s *DiskMoveSpec) { s.TargetStorage = "" }, true},
		{"negative bwlimit", func(s *DiskMoveSpec) { s.BWLimitKiB = -1 }, true},
		{"zero bwlimit", func(s *DiskMoveSpec) { s.BWLimitKiB = 0 }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := base
			tt.mutate(&spec)
			err := spec.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// The whole point of the shared spec: one intent renders to both guest types,
// with the container form dropping the format LXC has no concept of.
func TestDiskMoveSpecRendersBothGuestTypes(t *testing.T) {
	spec := DiskMoveSpec{
		Disk:          "rootfs",
		TargetStorage: "ceph",
		Format:        "qcow2",
		DeleteSource:  true,
		BWLimitKiB:    2048,
	}

	vm := spec.VMParams()
	if vm.Disk != "rootfs" || vm.Storage != "ceph" || vm.Format != "qcow2" ||
		!vm.Delete || vm.BWLimit != 2048 {
		t.Errorf("unexpected VM params: %+v", vm)
	}

	ct := spec.CTParams()
	if ct.Volume != "rootfs" || ct.Storage != "ceph" || !ct.Delete || ct.BWLimit != 2048 {
		t.Errorf("unexpected CT params: %+v", ct)
	}
}

func TestDiskMoveSpecSummary(t *testing.T) {
	tests := []struct {
		name string
		spec DiskMoveSpec
		want string
	}{
		{
			name: "without format",
			spec: DiskMoveSpec{Disk: "scsi0", TargetStorage: "ceph"},
			want: "scsi0 → ceph",
		},
		{
			name: "with format",
			spec: DiskMoveSpec{Disk: "scsi0", TargetStorage: "nfs", Format: "qcow2"},
			want: "scsi0 → nfs (qcow2)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.Summary(); got != tt.want {
				t.Errorf("Summary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiskMoveSpecAuditExtra(t *testing.T) {
	extra := DiskMoveSpec{
		Disk: "scsi0", TargetStorage: "nfs", Format: "qcow2",
		DeleteSource: true, BWLimitKiB: 100,
	}.AuditExtra()

	for _, key := range []string{"disk", "storage", "format", "delete", "bwlimit_kib"} {
		if _, ok := extra[key]; !ok {
			t.Errorf("audit extra is missing %q", key)
		}
	}
}
