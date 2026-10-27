package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ImageFormat values a disk move may request. An empty format means "let the
// target storage decide", which is the only valid choice for block-backed
// storages (LVM, LVM-thin, ZFS, RBD, iSCSI).
const (
	ImageFormatRaw   = "raw"
	ImageFormatQcow2 = "qcow2"
	ImageFormatVMDK  = "vmdk"
)

// ImageFormats lists every format a disk move may request, in the order the UI
// offers them.
var ImageFormats = []string{ImageFormatQcow2, ImageFormatRaw, ImageFormatVMDK}

// ValidImageFormat reports whether f is an image format a disk move may
// request. The empty string is valid and means "target storage decides".
func ValidImageFormat(f string) bool {
	if f == "" {
		return true
	}
	for _, valid := range ImageFormats {
		if f == valid {
			return true
		}
	}
	return false
}

// DiskMoveSpec is one "move this volume to that storage" intent. It is the
// single description of a storage move shared by the per-disk API handlers and
// the migration orchestrator, so the rules below — which formats exist, that
// LXC has no format choice, that a bandwidth limit can't be negative — are
// stated once instead of per caller.
type DiskMoveSpec struct {
	// Disk is the config key for a VM ("scsi0") or the volume key for a
	// container ("rootfs", "mp0").
	Disk string
	// TargetStorage is the storage to move onto. Proxmox rejects moving a
	// volume onto the storage it already lives on.
	TargetStorage string
	// Format converts the image on the way over. Honored only for VMs, and
	// only when the target storage is file-based; leave empty otherwise.
	Format string
	// DeleteSource drops the original volume once the copy lands. With it
	// false PVE keeps the original as an unusedN entry on the guest.
	DeleteSource bool
	// BWLimitKiB throttles the copy in KiB/s. 0 means the storage default.
	BWLimitKiB int
	// FromStorage is the storage the volume lives on now. It is never sent to
	// Proxmox, which does not ask: it is for the audit row, so that a storage's
	// History can show the disks that left it as well as the ones that arrived.
	// Empty when it is not known.
	FromStorage string
}

// Validate checks the spec against what Proxmox will accept. It deliberately
// does not check format against the target storage type — that needs the
// cluster's storage config, so callers gate it in the UI and PVE rejects the
// rest.
func (s DiskMoveSpec) Validate() error {
	if s.Disk == "" {
		return fmt.Errorf("disk is required")
	}
	if s.TargetStorage == "" {
		return fmt.Errorf("storage is required")
	}
	if !ValidImageFormat(s.Format) {
		return fmt.Errorf("format must be one of: raw, qcow2, vmdk")
	}
	if s.BWLimitKiB < 0 {
		return fmt.Errorf("bandwidth limit must not be negative")
	}
	return nil
}

// VMParams renders the spec as QEMU move_disk parameters.
func (s DiskMoveSpec) VMParams() DiskMoveParams {
	return DiskMoveParams{
		Disk:    s.Disk,
		Storage: s.TargetStorage,
		Format:  s.Format,
		Delete:  s.DeleteSource,
		BWLimit: s.BWLimitKiB,
	}
}

// CTParams renders the spec as LXC move_volume parameters. Format is dropped:
// container volumes are subvol or raw as decided by the target storage, and
// sending a format is an error.
func (s DiskMoveSpec) CTParams() CTVolumeMoveParams {
	return CTVolumeMoveParams{
		Volume:  s.Disk,
		Storage: s.TargetStorage,
		Delete:  s.DeleteSource,
		BWLimit: s.BWLimitKiB,
	}
}

// Summary describes the move for a task or audit description, e.g.
// "scsi0 → ceph (qcow2)". Callers prefix their own context.
func (s DiskMoveSpec) Summary() string {
	out := s.Disk + " → " + s.TargetStorage
	if s.Format != "" {
		out += " (" + s.Format + ")"
	}
	return out
}

// AuditExtra renders the spec for a task-history or audit detail row, so a
// move recorded by the per-disk API and one recorded by a migration job carry
// the same keys. These land in audit details, which every Viewer can read —
// storage names and formats only, no credentials.
func (s DiskMoveSpec) AuditExtra() map[string]any {
	extra := map[string]any{
		"disk":        s.Disk,
		"storage":     s.TargetStorage,
		"format":      s.Format,
		"delete":      s.DeleteSource,
		"bwlimit_kib": s.BWLimitKiB,
	}
	if s.FromStorage != "" {
		extra["from_storage"] = s.FromStorage
	}
	return extra
}

// VolumeStorage returns the storage id of a disk or volume config value such as
// "local-lvm:vm-100-disk-0,size=32G" — "local-lvm" — or "" when the value names
// none: a bind mount ("/srv/data,mp=/data"), an empty drive ("none,media=cdrom"),
// a device path ("/dev/disk/by-path/pci-0000:00:17.0-ata-1"), which holds a
// colon but no storage id (an id never holds a slash).
func VolumeStorage(val string) string {
	first, _, _ := strings.Cut(val, ",")
	storage, _, found := strings.Cut(first, ":")
	if !found || strings.Contains(storage, "/") {
		return ""
	}
	return storage
}

// diskStorageLookupTimeout bounds the config read VMDiskStorage and
// CTVolumeStorage make before a move, so that a node slow to answer cannot hold
// the move up behind a read whose only use is an audit row.
const diskStorageLookupTimeout = 5 * time.Second

// VMDiskStorage returns the storage a VM's disk lives on right now, as its
// config says, or "" when that cannot be told: the VM has no such key, the key
// holds no volume, or the config could not be read. Failing to read is not an
// error to the caller on purpose. This feeds the audit row of a move, and a
// move does not fail for it.
func (c *Client) VMDiskStorage(ctx context.Context, node string, vmid int, disk string) string {
	ctx, cancel := context.WithTimeout(ctx, diskStorageLookupTimeout)
	defer cancel()
	config, err := c.currentGuestConfig(ctx, "qemu", node, vmid)
	if err != nil {
		return ""
	}
	return configVolumeStorage(config, disk)
}

// CTVolumeStorage is VMDiskStorage for a container's volume ("rootfs", "mp0").
func (c *Client) CTVolumeStorage(ctx context.Context, node string, vmid int, volume string) string {
	ctx, cancel := context.WithTimeout(ctx, diskStorageLookupTimeout)
	defer cancel()
	config, err := c.currentGuestConfig(ctx, "lxc", node, vmid)
	if err != nil {
		return ""
	}
	return configVolumeStorage(config, volume)
}

// currentGuestConfig reads a guest's config as it is now (kind is the API's
// "qemu" or "lxc"). Without current=1 Proxmox merges the changes still pending
// on a running guest into the answer, and a pending change to a disk would name
// a storage the disk is not on yet.
func (c *Client) currentGuestConfig(ctx context.Context, kind, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/" + kind + "/" + strconv.Itoa(vmid) + "/config?current=1"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get %s %d current config on %s: %w", kind, vmid, node, err)
	}
	return config, nil
}

func configVolumeStorage(config VMConfig, key string) string {
	val, ok := config[key].(string)
	if !ok {
		return ""
	}
	return VolumeStorage(val)
}
