package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	spec := DiskMoveSpec{
		Disk: "scsi0", TargetStorage: "nfs", Format: "qcow2",
		DeleteSource: true, BWLimitKiB: 100,
	}
	extra := spec.AuditExtra()

	for _, key := range []string{"disk", "storage", "format", "delete", "bwlimit_kib"} {
		if _, ok := extra[key]; !ok {
			t.Errorf("audit extra is missing %q", key)
		}
	}
	// The storage the disk leaves is recorded only when it is known, so a
	// History of that storage can show the move; it is never sent to Proxmox.
	if _, ok := extra["from_storage"]; ok {
		t.Error("audit extra names a source storage nobody supplied")
	}
	spec.FromStorage = "store01"
	if got := spec.AuditExtra()["from_storage"]; got != "store01" {
		t.Errorf("from_storage = %v, want store01", got)
	}
}

// VolumeStorage reads the storage id off a disk or volume config value; a value
// that names none (a bind mount, an empty drive) has none.
func TestVolumeStorage(t *testing.T) {
	tests := []struct{ in, want string }{
		{"local-lvm:vm-100-disk-0,size=32G", "local-lvm"},
		{"store01:vm-101-disk-0", "store01"},
		// A colon in an option is not the storage separator.
		{"none,media=cdrom,note=a:b", ""},
		{"/srv/data,mp=/data", ""},
		// A passed-through device path has a colon, and no storage before it.
		{"/dev/disk/by-path/pci-0000:00:17.0-ata-1,backup=0", ""},
		{"none,media=cdrom", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := VolumeStorage(tt.in); got != tt.want {
			t.Errorf("VolumeStorage(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// VMDiskStorage and CTVolumeStorage read the guest's config for the audit row of
// a move; whatever they cannot tell, they leave empty rather than fail the move.
func TestDiskStorageLookup(t *testing.T) {
	ctx := context.Background()

	vmSrv, vmSeen := newCaptureServer(t, `{"data":{"scsi0":"store01:vm-100-disk-0,size=32G"}}`)
	vm := newTestClient(t, vmSrv.URL)
	for _, tt := range []struct{ name, key, want string }{
		{"a disk on a storage", "scsi0", "store01"},
		{"a key the guest does not have", "scsi9", ""},
	} {
		if got := vm.VMDiskStorage(ctx, "pve-01", 100, tt.key); got != tt.want {
			t.Errorf("%s: VMDiskStorage = %q, want %q", tt.name, got, tt.want)
		}
	}
	// current=1: the config without the changes still pending on it.
	if want := "/api2/json/nodes/pve-01/qemu/100/config?current=1"; (*vmSeen)[0] != want {
		t.Errorf("a VM's disk was looked up at %s, want %s", (*vmSeen)[0], want)
	}

	ctSrv, ctSeen := newCaptureServer(t, `{"data":{"rootfs":"store01:subvol-101-disk-0,size=8G"}}`)
	if got := newTestClient(t, ctSrv.URL).CTVolumeStorage(ctx, "pve-01", 101, "rootfs"); got != "store01" {
		t.Errorf("CTVolumeStorage = %q, want store01", got)
	}
	if want := "/api2/json/nodes/pve-01/lxc/101/config?current=1"; (*ctSeen)[0] != want {
		t.Errorf("a container's volume was looked up at %s, want %s", (*ctSeen)[0], want)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"data":null}`, http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	if got := newTestClient(t, failing.URL).VMDiskStorage(ctx, "pve-01", 100, "scsi0"); got != "" {
		t.Errorf("a config that cannot be read gave %q, want none", got)
	}
}
