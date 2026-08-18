package proxmox

import "fmt"

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
	return map[string]any{
		"disk":        s.Disk,
		"storage":     s.TargetStorage,
		"format":      s.Format,
		"delete":      s.DeleteSource,
		"bwlimit_kib": s.BWLimitKiB,
	}
}
