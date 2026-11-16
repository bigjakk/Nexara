package handlers

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// qemuDestroyVolumeKey and lxcDestroyVolumeKey are the config keys whose volume a destroy frees, as Proxmox
// walks them: qemu-server destroy_vm (foreach_volume_full with include_unused
// and vmstate) and pve-container destroy_disks (rootfs, mp<n>, unused<n>).
var (
	qemuDestroyVolumeKey = regexp.MustCompile(`^((ide|sata|scsi|virtio)\d+|efidisk0|tpmstate0|unused\d+|vmstate)$`)
	lxcDestroyVolumeKey  = regexp.MustCompile(`^(rootfs|mp\d+|unused\d+)$`)
	// volumeOwner is the vmid a volume name carries (PVE::Storage parse_volname):
	// vm-, base-, subvol- or basevol-<vmid>-…, the last one in a linked clone's
	// "<base>/<clone>" name.
	volumeOwner = regexp.MustCompile(`(?:^|/)(?:vm|base|subvol|basevol)-(\d+)-`)
	// cloudinitVolume is qemu-server's drive_is_cloudinit: a CD-ROM drive that
	// destroy_vm frees all the same.
	cloudinitVolume = regexp.MustCompile(`[:/](?:vm-\d+-)?cloudinit(?:\.(?:raw|qcow2|vmdk))?$`)
)

// destroyedVolumes is the volumes a guest's destroy frees with it, read from
// its configuration before the destroy is sent: each storage volume a disk,
// mount point or unused entry names that the guest owns, as Proxmox decides
// it. A path, a bind mount, an ISO or another guest's volume is left alone by
// Proxmox, and here. Not seen: a snapshot's saved state and a pending change.
func destroyedVolumes(config proxmox.VMConfig, vmid int32, lxc bool) []string {
	keys := qemuDestroyVolumeKey
	if lxc {
		keys = lxcDestroyVolumeKey
	}
	owner := strconv.Itoa(int(vmid))
	var volumes []string
	for key, raw := range config {
		value, ok := raw.(string)
		if !ok || !keys.MatchString(key) {
			continue
		}
		parts := strings.Split(value, ",")
		volid := parts[0]
		storage, volname, found := strings.Cut(volid, ":")
		if !found || storage == "" || strings.HasPrefix(volid, "/") {
			continue
		}
		if !lxc && slices.Contains(parts[1:], "media=cdrom") && !cloudinitVolume.MatchString(volid) {
			continue
		}
		matches := volumeOwner.FindAllStringSubmatch(volname, -1)
		if len(matches) == 0 || matches[len(matches)-1][1] != owner {
			continue
		}
		if !slices.Contains(volumes, volid) {
			volumes = append(volumes, volid)
		}
	}
	slices.Sort(volumes)
	return volumes
}

// destroyAuditVolumes is destroyedVolumes for the destroy's audit row:
// "volumes" when the configuration could be read, "volumes_unknown" when not.
func destroyAuditVolumes(ctx context.Context, read func(context.Context) (proxmox.VMConfig, error), vmid int32, lxc bool) map[string]any {
	config, err := read(ctx)
	if err != nil {
		return map[string]any{"volumes_unknown": true}
	}
	volumes := destroyedVolumes(config, vmid, lxc)
	if volumes == nil {
		volumes = []string{}
	}
	return map[string]any{"volumes": volumes}
}
