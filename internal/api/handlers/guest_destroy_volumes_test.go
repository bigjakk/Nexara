package handlers

import (
	"slices"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The volumes a destroy frees are the guest's own, as Proxmox picks them: a
// cloud-init drive yes, an ISO, a path, a bind mount or another guest's volume
// (a linked clone's base) no.
func TestDestroyedVolumes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config proxmox.VMConfig
		lxc    bool
		want   []string
	}{
		{"a VM", proxmox.VMConfig{
			"scsi0":     "store01:vm-101-disk-0,size=32G",
			"scsi1":     "store02:base-100-disk-0/vm-101-disk-1,size=8G",
			"efidisk0":  "store01:vm-101-disk-2,efitype=4m",
			"unused0":   "store01:vm-101-disk-3",
			"ide2":      "store01:vm-101-cloudinit,media=cdrom",
			"ide0":      "store03:iso/linux.iso,media=cdrom",
			"sata0":     "store01:vm-100-disk-0,size=4G",
			"virtio0":   "/dev/disk/by-id/ata-example,size=100G",
			"net0":      "virtio=02:00:00:00:00:01,bridge=vmbr0",
			"cores":     float64(2),
			"tpmstate0": "store01:vm-101-disk-4,version=v2.0",
		}, false, []string{"store01:vm-101-cloudinit", "store01:vm-101-disk-0", "store01:vm-101-disk-2",
			"store01:vm-101-disk-3", "store01:vm-101-disk-4", "store02:base-100-disk-0/vm-101-disk-1"}},
		{"a container", proxmox.VMConfig{
			"rootfs":  "store01:subvol-101-disk-0,size=8G",
			"mp0":     "store01:vm-101-disk-1,mp=/data",
			"mp1":     "/srv/share,mp=/share",
			"unused0": "store02:subvol-101-disk-2",
		}, true, []string{"store01:subvol-101-disk-0", "store01:vm-101-disk-1", "store02:subvol-101-disk-2"}},
	} {
		if got := destroyedVolumes(tc.config, 101, tc.lxc); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
