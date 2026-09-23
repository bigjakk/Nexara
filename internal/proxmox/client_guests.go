package proxmox

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (c *Client) vmStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s VM %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) StartVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "start")
}
func (c *Client) StopVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "stop")
}
func (c *Client) ShutdownVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "shutdown")
}
func (c *Client) RebootVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reboot")
}
func (c *Client) ResetVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reset")
}
func (c *Client) SuspendVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "suspend")
}
func (c *Client) ResumeVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "resume")
}
func (c *Client) CloneVM(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DestroyVM(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ConvertVMToTemplate(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/template"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("convert VM %d to template on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ctStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s CT %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) StartCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "start")
}
func (c *Client) StopCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "stop")
}
func (c *Client) ShutdownCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "shutdown")
}
func (c *Client) RebootCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "reboot")
}
func (c *Client) SuspendCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "suspend")
}
func (c *Client) ResumeCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "resume")
}
func (c *Client) CloneCT(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("hostname", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DestroyCT(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ConvertCTToTemplate(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/template"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("convert CT %d to template on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) MigrateCT(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("restart", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) MigrateVM(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("online", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ResizeDisk(ctx context.Context, node string, vmid int, params DiskResizeParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("size", params.Size)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/resize"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("resize disk on VM %d: %w", vmid, err)
	}
	return nil
}
func (c *Client) ResizeCTDisk(ctx context.Context, node string, vmid int, params DiskResizeParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("size", params.Size)
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/resize"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("resize disk on CT %d: %w", vmid, err)
	}
	return nil
}
func (c *Client) MoveDisk(ctx context.Context, node string, vmid int, params DiskMoveParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("storage", params.Storage)
	if params.Format != "" {
		form.Set("format", params.Format)
	}
	if params.Delete {
		form.Set("delete", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/move_disk"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("move disk on VM %d: %w", vmid, err)
	}
	return upid, nil
}
func (c *Client) MoveCTVolume(ctx context.Context, node string, vmid int, params CTVolumeMoveParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("volume", params.Volume)
	form.Set("storage", params.Storage)
	if params.Delete {
		form.Set("delete", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/move_volume"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("move volume on CT %d: %w", vmid, err)
	}
	return upid, nil
}

// DiskBuses are the VM disk buses Proxmox exposes, in the order the UI
// offers them.
var DiskBuses = []string{"scsi", "sata", "virtio", "ide"}

// maxDiskIndex is the highest slot index each bus has, i.e. the
// controller's slot count minus one.
var maxDiskIndex = map[string]int{
	"ide":    3,
	"sata":   5,
	"virtio": 15,
	"scsi":   30,
}

// MaxDiskIndex reports the highest index Proxmox accepts on bus, and
// whether bus is a disk bus at all.
//
// It is exported because the API layer picks a free slot before calling
// AttachDisk and has to agree with this package about where each bus ends
// — two copies of "scsi goes up to 30" is one copy too many.
func MaxDiskIndex(bus string) (int, bool) {
	limit, ok := maxDiskIndex[bus]
	return limit, ok
}

// DetachableDiskKeyPattern is every VM config key DetachDisk will remove, and
// nothing else: the drive and unused-disk keys of qemu-server's config schema,
// plus vmstate.
//
// Transcribed from qemu-server rather than guessed:
//
//   - src/PVE/QemuServer/Drive.pm, valid_drive_names_with_unused: ide0-ide3,
//     scsi0-scsi30, virtio0-virtio15 and sata0-sata5 ($MAX_IDE_DISKS = 4,
//     $MAX_SCSI_DISKS = 31, $MAX_VIRTIO_DISKS = 16, $MAX_SATA_DISKS = 6 — the
//     same four ceilings maxDiskIndex carries for AttachDisk), efidisk0,
//     tpmstate0, and unused0-unused255 ($MAX_UNUSED_DISKS = 256).
//   - src/PVE/QemuServer.pm, $confdesc->{vmstate}: a hibernated VM's saved
//     RAM, which $update_vm_api's delete loop (src/PVE/API2/Qemu.pm) frees
//     through try_deallocate_drive just as it frees an unusedN volume.
//
// Proxmox's own `delete` parameter is far wider. The only check
// $update_vm_api makes on a deleted key's name is option_exists, and its
// delete loop queues any other option for removal, so without this a detach
// could take net0, boot, cores or onboot out of the VM's config. option_exists
// is an exact hash lookup, which is why no number here carries a leading zero:
// "scsi01" is an "unknown option" upstream as well.
//
// ide2 is here although it conventionally holds the CD-ROM: upstream it is a
// drive key like any other, a VM can keep a data disk on it, and it is where a
// cloud-init drive usually lives. "cdrom" is NOT here, although
// $update_vm_api accepts it in `delete` as an alias it rewrites to ide2. It is
// not a config key, so the audit lookup in front of the detach
// (detachedVolume in internal/api/handlers) would find no "cdrom" in the
// config and record that nothing was removed for a request that removed ide2.
// A caller sends ide2 instead.
//
// Exported so the API declaration can publish and pre-check the same set
// (the detach route's disk parameter in internal/api/registry_vms.go);
// DetachDisk enforces it itself either way.
const DetachableDiskKeyPattern = `^(?:ide[0-3]|sata[0-5]|scsi(?:[12]?[0-9]|30)|virtio(?:1[0-5]|[0-9])|` +
	`efidisk0|tpmstate0|unused(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])|vmstate)$`

var detachableDiskKeyRe = regexp.MustCompile(DetachableDiskKeyPattern)

// bareGiBRe matches the only spelling of a size Proxmox's "storage:N"
// allocation form accepts: a bare integer count of gibibytes.
var bareGiBRe = regexp.MustCompile(`^[1-9]\d*$`)

// Validate rejects every DiskAttachParams that would produce a volume
// spec other than the one the caller meant.
//
// It lives HERE, at the choke point, rather than in the handler that
// happens to call AttachDisk today: a validator placed in one caller is a
// validator the next caller silently skips. The handler still makes the
// decisions this layer cannot — which slot is free, whether the target
// slot already holds the boot disk, whether the pool is big enough —
// because none of those are answerable from the parameters alone.
//
// The shape checks matter because Size, Storage and Format are
// concatenated into "storage:size[,format=fmt]", where a stray ":" or ","
// does not fail: it re-parses into a DIFFERENT, valid spec. "20G" is the
// one this package documented for years, and PVE answers it with a parse
// error; a value meant as megabytes is worse, because PVE accepts it and
// allocates gigabytes.
func (p DiskAttachParams) Validate() error {
	maxIndex, known := MaxDiskIndex(p.Bus)
	if !known {
		return fmt.Errorf("%w: bus must be one of: %s", ErrInvalidInput, strings.Join(DiskBuses, ", "))
	}
	if p.Index < 0 || p.Index > maxIndex {
		return fmt.Errorf("%w: %s index must be between 0 and %d", ErrInvalidInput, p.Bus, maxIndex)
	}
	if p.Storage == "" {
		return fmt.Errorf("%w: storage is required", ErrInvalidInput)
	}
	if strings.ContainsAny(p.Storage, ":,=") {
		return fmt.Errorf("%w: storage %q contains a character that would restructure the volume spec", ErrInvalidInput, p.Storage)
	}
	if !bareGiBRe.MatchString(p.Size) {
		return fmt.Errorf("%w: size must be a bare count of gibibytes such as \"20\", not %q", ErrInvalidInput, p.Size)
	}
	if !ValidImageFormat(p.Format) {
		return fmt.Errorf("%w: format must be one of: %s", ErrInvalidInput, strings.Join(ImageFormats, ", "))
	}
	if p.Digest == "" {
		return fmt.Errorf("%w: digest is required — pass the digest from the GET /config that chose this slot, "+
			"so Proxmox refuses the write if the configuration changed underneath it", ErrInvalidInput)
	}
	return nil
}

// DiskKey is the VM config key this attach writes, e.g. "scsi1".
func (p DiskAttachParams) DiskKey() string {
	return p.Bus + strconv.Itoa(p.Index)
}

func (c *Client) AttachDisk(ctx context.Context, node string, vmid int, params DiskAttachParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if err := params.Validate(); err != nil {
		return err
	}

	// Build the volume spec: "storage:size[,format=fmt]", where size is a
	// bare GiB count — see DiskAttachParams.Size.
	volume := params.Storage + ":" + params.Size
	if params.Format != "" {
		volume += ",format=" + params.Format
	}

	// "digest" is not a config key: PVE reads it off the config PUT as the
	// compare-and-swap token for the whole file, and answers 400 if the
	// configuration has changed since it was read. See
	// DiskAttachParams.Digest for what that prevents.
	fields := map[string]string{
		params.DiskKey(): volume,
		"digest":         params.Digest,
	}

	return c.SetVMConfig(ctx, node, vmid, fields)
}

// DetachDisk removes a disk key from a VM's config. PVE parks an owned
// volume in an unusedN slot for a regular drive key, and removes an owned
// volume from storage for an unusedN key, for vmstate (a hibernated VM's saved
// RAM) or for a cloud-init drive on any key.
//
// digest pins the write to a config read, the way AttachDisk does: PVE
// refuses the PUT if the config moved underneath, which stops a concurrent
// edit being silently clobbered and stops the audit row naming a volume that
// a racing caller already replaced. It is optional here rather than required,
// because the caller's read is best-effort — a detach Proxmox would accept
// must not start failing because the audit lookup could not reach the API.
// Pass "" to write unpinned.
//
// disk must be a key DetachableDiskKeyPattern admits. The check is here, not
// only on the HTTP route, because `delete` is whatever this method is handed
// and Proxmox removes any config option named in it: a validator in the one
// caller is a validator the next caller skips.
func (c *Client) DetachDisk(ctx context.Context, node string, vmid int, disk, digest string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if !detachableDiskKeyRe.MatchString(disk) {
		return fmt.Errorf("%w: %q is not a disk a detach can remove: want a drive key such as scsi1, "+
			"efidisk0 or tpmstate0, an unusedN key, or vmstate", ErrInvalidInput, disk)
	}

	fields := map[string]string{
		"delete": disk,
	}
	if digest != "" {
		fields["digest"] = digest
	}

	return c.SetVMConfig(ctx, node, vmid, fields)
}
func (c *Client) RestoreVM(ctx context.Context, node string, params RestoreParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("restore requires a positive VMID")
	}
	if params.Archive == "" {
		return "", fmt.Errorf("restore requires an archive path")
	}

	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("archive", params.Archive)
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Unique {
		form.Set("unique", "1")
	}
	if params.Force {
		form.Set("force", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore VM %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) RestoreCT(ctx context.Context, node string, params RestoreParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("restore requires a positive VMID")
	}
	if params.Archive == "" {
		return "", fmt.Errorf("restore requires an archive path")
	}

	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("ostemplate", params.Archive)
	form.Set("restore", "1")
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Unique {
		form.Set("unique", "1")
	}
	if params.Force {
		form.Set("force", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}

// --- Snapshot names ---
//
// The rule below lives at the CLIENT rather than in internal/api/handlers,
// because the handlers are not the only caller and the next one will not
// remember: internal/scheduler fires stored snapshot tasks and
// internal/guesttools snapshots a guest before updating it, and neither goes
// through a handler. That is the same reasoning that put validateHAConfigID in
// client_ha.go, and it is what makes the two Create*Snapshot methods the choke
// point instead of a convention two packages happen to follow.
//
// Putting it here also dissolves the "which guest kind is this?" third state at
// the sites that matter. At the client the kind is structural — CreateVMSnapshot
// is qemu and CreateCTSnapshot is lxc — so there is nothing to pass wrong.

// SnapshotMaxNameLen is PROXMOX's cap on a snapshot name, not one Nexara
// invented — this was unverified for a long time and the answer is that
// upstream states it outright.
//
// pve-common registers the standard option every snapname parameter uses:
//
//	register_standard_option('pve-snapshot-name', {
//	    description => "The name of the snapshot.",
//	    type => 'string', format => 'pve-configid', maxLength => 40,
//	});
//
// (pve-common src/PVE/JSONSchema.pm.) Its validator answers "value may
// only be 40 characters long" at 41, and every snapname in qemu-server
// src/PVE/API2/Qemu.pm and pve-container src/PVE/API2/LXC/Snapshot.pm
// uses that option — two of them add `optional => 1` and none of them
// override maxLength. Proxmox's own published API reference agrees: all
// 16 snapname parameters in pve-docs' apidoc.js carry maxLength 40.
//
// It is NOT pve-configid's bound, and conflating the two is the mistake to
// avoid here. $CONFIGID_RE is `^[a-z][a-z0-9_-]+\z` case-insensitively — a
// minimum of two characters and NO maximum — which is why the catalogue's
// pve-configid rule runs to 128. 40 is a snapshot-only cap layered on top
// of that shape. The storage plugins add nothing further: pve-storage
// validates no snapshot name of its own, so a ZFS/RBD/LVM object name is
// not what bounds this.
//
// The cap applies to the ADDRESSING endpoints too — delete, rollback and
// the config reads all take pve-snapshot-name — so a snapshot whose name
// exceeds 40 could not be reached through PVE's API even if some other
// tool managed to create one. The delete and rollback methods below
// deliberately do NOT call ValidateSnapshotName: they address a snapshot
// Proxmox already minted, and a validator stricter than whatever produced
// that name would strand it. That does not leave them unguarded — see
// "Addressing an existing snapshot" below for the weaker path-segment rule
// they carry instead.
const SnapshotMaxNameLen = 40

// snapshotNameRE is pve-configid's shape bounded by SnapshotMaxNameLen: a
// leading letter, then letters, digits, '-' or '_'. It is built from the
// constant so the two cannot drift.
var snapshotNameRE = regexp.MustCompile(
	fmt.Sprintf(`^[A-Za-z][A-Za-z0-9_-]{1,%d}$`, SnapshotMaxNameLen-1),
)

// SnapshotGuestKind selects which reserved names apply, because Proxmox
// reserves a DIFFERENT set for VMs and for containers and checks each with
// its own case sensitivity:
//
//   - "current" — both kinds, exact match. It is the pseudo-name the
//     snapshot listing gives the live config, so a real snapshot called
//     that could never be addressed. (qemu-server src/PVE/API2/Qemu.pm and
//     pve-container src/PVE/API2/LXC/Snapshot.pm: `die … if $snapname eq
//     'current'`.)
//   - "pending" — VMs only, CASE-INSENSITIVELY. A VM config keeps staged
//     changes in a `[PENDING]` section, and parse_vm_config matches that
//     header with /i ahead of the snapshot-section branch, so a snapshot
//     named "Pending" would be read back as pending changes rather than as
//     a snapshot. (qemu-server src/PVE/API2/Qemu.pm: `die … if
//     lc($snapname) eq 'pending'`; the collision is in
//     src/PVE/QemuServer.pm.)
//   - "vzdump" — containers only, exact match. vzdump takes a snapshot
//     literally named that when it backs a container up, and
//     pve-guest-common's __snapshot_prepare special-cases it.
//     (pve-container src/PVE/API2/LXC/Snapshot.pm: `die … if $snapname eq
//     'vzdump'`.)
//
// Containers deliberately do NOT reserve "pending", and that asymmetry is
// upstream's design rather than an oversight to paper over: an LXC config
// spells its pending section `[pve:pending]` (pve-container
// src/PVE/LXC/Config.pm), and a colon is not a legal configid character,
// so there is nothing to collide with. Rejecting it here would refuse a
// name Proxmox accepts.
type SnapshotGuestKind string

const (
	QemuSnapshot SnapshotGuestKind = "qemu"
	LXCSnapshot  SnapshotGuestKind = "lxc"
)

// ErrUnknownSnapshotGuestKind is returned when ValidateSnapshotName is
// handed a kind it has no reserved set for. It is OUR bug, not the
// caller's, which is why it is deliberately NOT wrapped in ErrInvalidInput:
// mapProxmoxError turns ErrInvalidInput into a 400, and billing an
// unregistered enum value to the caller would tell them their snapshot name
// was wrong when no name of theirs could have worked.
//
// Unreachable through the two Create*Snapshot methods, which each pass a
// literal.
var ErrUnknownSnapshotGuestKind = errors.New("snap_name cannot be validated for an unknown guest kind")

// ReservedSnapshotName reports whether Proxmox refuses name outright for
// this guest kind. The second result is false when kind is not one of the
// two above — the caller must treat that as "could not decide" rather than
// as "not reserved", so an unhandled kind cannot silently borrow the wrong
// reserved set.
func ReservedSnapshotName(kind SnapshotGuestKind, name string) (reserved, known bool) {
	switch kind {
	case QemuSnapshot:
		return name == "current" || strings.EqualFold(name, "pending"), true
	case LXCSnapshot:
		return name == "current" || name == "vzdump", true
	}
	return false, false
}

// ValidateSnapshotName rejects names Proxmox would refuse, with an
// actionable message instead of PVE's "invalid configuration ID",
// "value may only be 40 characters long" or "reserved name".
//
// The three caller-fault refusals wrap ErrInvalidInput, which is what tells
// the API layer this never reached Proxmox: mapProxmoxError answers it with
// a 400 naming the value, where an unwrapped error falls through to a 500
// reading "Proxmox operation failed". The fourth — an unregistered guest
// kind — is Nexara's own bug and carries ErrUnknownSnapshotGuestKind
// instead; see handlers.snapshotNameError for the attribution that hangs
// off that split.
func ValidateSnapshotName(kind SnapshotGuestKind, name string) error {
	reserved, known := ReservedSnapshotName(kind, name)
	if !known {
		// Refusing beats guessing a reserved set, because guessing wrong is
		// silent: a new guest kind would inherit whichever set happened to
		// be the fallback and let a reserved name through.
		return fmt.Errorf("%w %q", ErrUnknownSnapshotGuestKind, kind)
	}
	switch {
	case name == "":
		return fmt.Errorf("%w: snap_name is required", ErrInvalidInput)
	case reserved:
		return fmt.Errorf("%w: snap_name %q is reserved by Proxmox", ErrInvalidInput, name)
	case !snapshotNameRE.MatchString(name):
		return fmt.Errorf("%w: snap_name must start with a letter and contain only letters, digits, '-' and '_' (no spaces), 2-%d characters", ErrInvalidInput, SnapshotMaxNameLen)
	}
	return nil
}

// --- Addressing an existing snapshot ---
//
// DeleteVMSnapshot, RollbackVMSnapshot, DeleteCTSnapshot and
// RollbackCTSnapshot each interpolate a caller-supplied snapshot name into the
// request PATH, so they take validatePathSegment: the same guard
// validateNodeName delegates to, with the same deliberate looseness — no
// charset, no length cap, no minimum.
//
// They must NOT take ValidateSnapshotName. That rule describes what Nexara is
// willing to MINT. These four address something Proxmox already minted, and a
// name a human took at the PVE console, or an older Proxmox, or vzdump itself
// produced is one the operator now has to be able to delete. Refusing it here
// is how an object becomes undeletable — the invented-strictness shape, and
// the reason registry_vms.go declares snapshotNameParam with MaxLength 128 and
// no two-character minimum rather than the create rule's 40 and 2.
//
// Escaping is not the guard, which is what made the emptiness check these
// replaced insufficient. url.PathEscape encodes "/" but leaves "." and ".."
// entirely alone, so those two travel raw. pveproxy takes them literally, as a
// snapshot name its pve-snapshot-name format refuses (see validatePathSegment),
// but a normalising proxy in front of it resolves them upward, with no decode
// needed at all. A separator PathEscape does escape, to "%2F", pveproxy itself
// decodes before it routes, so that one re-routes the request even with no
// proxy in between. So on the two DELETE methods, behind such a proxy:
//
//	snapname="."   DELETE /nodes/{node}/qemu/{vmid}/snapshot
//	               the snapshot COLLECTION rather than one snapshot
//	snapname=".."  DELETE /nodes/{node}/qemu/{vmid}
//	               the GUEST — the same target as DestroyVM
//
// The second one is the reason this is not a cosmetic tightening: destroy_vm
// takes node and vmid from the path and needs nothing else, so a caller asking
// to delete a snapshot would destroy a stopped, unprotected, unlocked VM (a
// "suspended" lock does not stop it) that is in neither HA nor replication and
// is not a template whose base image a linked clone still uses — the refusals
// in qemu-server's API destroy_vm and QemuServer::destroy_vm — and the audit
// row and the task description still read "snapshot_delete" because both are
// built from the arguments rather than from the path. Commit 3e757d3 closed
// the same shape for task ids and 7f4d2ca for HA ids, on the rationale "guard
// at the client, not at whichever caller remembers".
//
// Whichever caller remembers is what this was until now: the only thing
// constraining these names was snapshotNameParam's Pattern in
// internal/api/registry_vms.go — a check in the CALLER, which a non-HTTP
// caller inherits nothing from. Today there is no such caller, and that is
// worth recording rather than assuming: the four handlers in
// internal/api/handlers are the only call sites in the tree, internal/scheduler
// and internal/guesttools reach the CREATE methods only, and the central
// snapshots "prune" (handlers/guest_snapshots.go) deletes cache ROWS, not
// snapshots. So this closes a gap nothing reaches yet, on the same terms as
// UpdateFirewallIPSetEntry, which is guarded with no route reaching it at all:
// the next caller inherits the check instead of having to remember it.
//
// A "%" is deliberately NOT refused, unlike in validateVolumeID. These four
// write url.PathEscape(snapname), which re-encodes a percent to %25, so
// "%2e%2e%2f" arrives as the literal name the caller meant rather than as
// "../". A volume id is interpolated raw, which is the whole difference.
//
// The control-character refusal is not about the path — url.PathEscape encodes
// those — but about where the name goes afterwards: all four handlers file
// snap_name in a TrackTask Extra map, and view:audit is granted to every
// Viewer by default.

func (c *Client) ListVMSnapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot"
	var snaps []Snapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("list VM %d snapshots on %s: %w", vmid, node, err)
	}
	return snaps, nil
}
func (c *Client) CreateVMSnapshot(ctx context.Context, node string, vmid int, params SnapshotParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if err := ValidateSnapshotName(QemuSnapshot, params.SnapName); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("snapname", params.SnapName)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.VMState {
		form.Set("vmstate", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create snapshot on VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DeleteVMSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	// Addressing, not minting: the path-segment rule and deliberately NOT
	// ValidateSnapshotName — see "Addressing an existing snapshot" above.
	if err := validatePathSegment("snapshot name", snapname); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete snapshot %s on VM %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) RollbackVMSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	// Addressing, not minting: the path-segment rule and deliberately NOT
	// ValidateSnapshotName — see "Addressing an existing snapshot" above.
	if err := validatePathSegment("snapshot name", snapname); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname) + "/rollback"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("rollback snapshot %s on VM %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}

// featureCheck is the response of GET /nodes/{node}/{qemu|lxc}/{vmid}/feature.
type featureCheck struct {
	HasFeature FlexBool `json:"hasFeature"`
}

// GetVMSnapshotFeature reports whether a VM's current configuration supports
// taking snapshots — the same check the native PVE UI uses to enable its
// snapshot button (false e.g. for raw disks or TPM state on file storage).
func (c *Client) GetVMSnapshotFeature(ctx context.Context, node string, vmid int) (bool, error) {
	if err := validateNodeName(node); err != nil {
		return false, err
	}
	if err := validateVMID(vmid); err != nil {
		return false, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/feature?feature=snapshot"
	var res featureCheck
	if err := c.do(ctx, path, &res); err != nil {
		return false, fmt.Errorf("get snapshot feature for VM %d on %s: %w", vmid, node, err)
	}
	return bool(res.HasFeature), nil
}

// GetCTSnapshotFeature is the container counterpart of GetVMSnapshotFeature.
func (c *Client) GetCTSnapshotFeature(ctx context.Context, node string, vmid int) (bool, error) {
	if err := validateNodeName(node); err != nil {
		return false, err
	}
	if err := validateVMID(vmid); err != nil {
		return false, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/feature?feature=snapshot"
	var res featureCheck
	if err := c.do(ctx, path, &res); err != nil {
		return false, fmt.Errorf("get snapshot feature for CT %d on %s: %w", vmid, node, err)
	}
	return bool(res.HasFeature), nil
}

func (c *Client) ListCTSnapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot"
	var snaps []Snapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("list CT %d snapshots on %s: %w", vmid, node, err)
	}
	return snaps, nil
}
func (c *Client) CreateCTSnapshot(ctx context.Context, node string, vmid int, params SnapshotParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if err := ValidateSnapshotName(LXCSnapshot, params.SnapName); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("snapname", params.SnapName)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create snapshot on CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DeleteCTSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	// Addressing, not minting: the path-segment rule and deliberately NOT
	// ValidateSnapshotName — see "Addressing an existing snapshot" above.
	if err := validatePathSegment("snapshot name", snapname); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete snapshot %s on CT %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) RollbackCTSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	// Addressing, not minting: the path-segment rule and deliberately NOT
	// ValidateSnapshotName — see "Addressing an existing snapshot" above.
	if err := validatePathSegment("snapshot name", snapname); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname) + "/rollback"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("rollback snapshot %s on CT %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) CreateVM(ctx context.Context, node string, params CreateVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("VMID is required and must be positive")
	}
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Memory > 0 {
		form.Set("memory", strconv.Itoa(params.Memory))
	}
	if params.Cores > 0 {
		form.Set("cores", strconv.Itoa(params.Cores))
	}
	if params.Sockets > 0 {
		form.Set("sockets", strconv.Itoa(params.Sockets))
	}
	if params.SCSI0 != "" {
		form.Set("scsi0", params.SCSI0)
	}
	if params.IDE2 != "" {
		form.Set("ide2", params.IDE2)
	}
	if params.Net0 != "" {
		form.Set("net0", params.Net0)
	}
	if params.OSType != "" {
		form.Set("ostype", params.OSType)
	}
	if params.Boot != "" {
		form.Set("boot", params.Boot)
	}
	if params.CDRom != "" {
		form.Set("cdrom", params.CDRom)
	}
	if params.Start {
		form.Set("start", "1")
	}
	if params.CIUser != "" {
		form.Set("ciuser", params.CIUser)
	}
	if params.CIPassword != "" {
		form.Set("cipassword", params.CIPassword)
	}
	if params.IPConfig0 != "" {
		form.Set("ipconfig0", params.IPConfig0)
	}
	if params.SSHKeys != "" {
		form.Set("sshkeys", url.QueryEscape(params.SSHKeys))
	}
	if params.CIType != "" {
		form.Set("citype", params.CIType)
	}
	if params.Nameserver != "" {
		form.Set("nameserver", params.Nameserver)
	}
	if params.Searchdomain != "" {
		form.Set("searchdomain", params.Searchdomain)
	}
	// System
	if params.BIOS != "" {
		form.Set("bios", params.BIOS)
	}
	if params.Machine != "" {
		form.Set("machine", params.Machine)
	}
	if params.ScsiHW != "" {
		form.Set("scsihw", params.ScsiHW)
	} else {
		form.Set("scsihw", "virtio-scsi-pci")
	}
	if params.EFIDisk0 != "" {
		form.Set("efidisk0", params.EFIDisk0)
	}
	if params.TPMState0 != "" {
		form.Set("tpmstate0", params.TPMState0)
	}
	if params.Agent != "" {
		form.Set("agent", params.Agent)
	}
	// CPU
	if params.CPUType != "" {
		form.Set("cpu", params.CPUType)
	}
	if params.Numa != nil {
		if *params.Numa {
			form.Set("numa", "1")
		} else {
			form.Set("numa", "0")
		}
	}
	// Memory
	if params.Balloon != nil {
		form.Set("balloon", strconv.Itoa(*params.Balloon))
	}
	// Display
	if params.VGA != "" {
		form.Set("vga", params.VGA)
	}
	// Boot / Options
	if params.OnBoot != nil {
		if *params.OnBoot {
			form.Set("onboot", "1")
		} else {
			form.Set("onboot", "0")
		}
	}
	if params.Hotplug != "" {
		form.Set("hotplug", params.Hotplug)
	}
	if params.Tablet != nil {
		if *params.Tablet {
			form.Set("tablet", "1")
		} else {
			form.Set("tablet", "0")
		}
	}
	// Description / Tags / Pool
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.Tags != "" {
		form.Set("tags", params.Tags)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	// Forward any extra fields (additional disks, CD-ROMs, etc.)
	for k, v := range params.Extra {
		if v != "" {
			form.Set(k, v)
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create VM %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) CreateCT(ctx context.Context, node string, params CreateCTParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("VMID is required and must be positive")
	}
	if params.OSTemplate == "" {
		return "", fmt.Errorf("ostemplate is required")
	}
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("ostemplate", params.OSTemplate)
	if params.Hostname != "" {
		form.Set("hostname", params.Hostname)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.RootFS != "" {
		form.Set("rootfs", params.RootFS)
	}
	if params.Memory > 0 {
		form.Set("memory", strconv.Itoa(params.Memory))
	}
	if params.Swap > 0 {
		form.Set("swap", strconv.Itoa(params.Swap))
	}
	if params.Cores > 0 {
		form.Set("cores", strconv.Itoa(params.Cores))
	}
	if params.Net0 != "" {
		form.Set("net0", params.Net0)
	}
	if params.Password != "" {
		form.Set("password", params.Password)
	}
	if params.SSHKeys != "" {
		form.Set("ssh-public-keys", url.QueryEscape(params.SSHKeys))
	}
	if params.Unprivileged {
		form.Set("unprivileged", "1")
	}
	if params.Start {
		form.Set("start", "1")
	}
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.Tags != "" {
		form.Set("tags", params.Tags)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	if params.Nameserver != "" {
		form.Set("nameserver", params.Nameserver)
	}
	if params.Searchdomain != "" {
		form.Set("searchdomain", params.Searchdomain)
	}
	for k, v := range params.Extra {
		if v != "" {
			form.Set(k, v)
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) RemoteMigrateVM(ctx context.Context, node string, vmid int, params RemoteMigrateVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("target-endpoint", params.TargetEndpoint.PropertyString())
	form.Set("target-bridge", params.TargetBridge)
	if params.TargetStorage != "" {
		form.Set("target-storage", params.TargetStorage)
	}
	if params.TargetVMID > 0 {
		form.Set("target-vmid", strconv.Itoa(params.TargetVMID))
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	if params.Online {
		form.Set("online", "1")
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/remote_migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("remote migrate VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) RemoteMigrateCT(ctx context.Context, node string, vmid int, params RemoteMigrateCTParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("target-endpoint", params.TargetEndpoint.PropertyString())
	form.Set("target-bridge", params.TargetBridge)
	if params.TargetStorage != "" {
		form.Set("target-storage", params.TargetStorage)
	}
	if params.TargetVMID > 0 {
		form.Set("target-vmid", strconv.Itoa(params.TargetVMID))
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	if params.Restart {
		form.Set("restart", "1")
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/remote_migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("remote migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) GetVMConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get VM %d config on %s: %w", vmid, node, err)
	}
	return config, nil
}
func (c *Client) GetGuestAgentOSInfo(ctx context.Context, node string, vmid int) (*GuestOSInfo, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/get-osinfo"
	var wrapper struct {
		Result GuestOSInfo `json:"result"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get guest agent OS info for VM %d on %s: %w", vmid, node, err)
	}
	return &wrapper.Result, nil
}
func (c *Client) GetGuestAgentNetworkInterfaces(ctx context.Context, node string, vmid int) ([]GuestNetworkInterface, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/network-get-interfaces"
	var wrapper struct {
		Result []GuestNetworkInterface `json:"result"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get guest agent network interfaces for VM %d on %s: %w", vmid, node, err)
	}
	return wrapper.Result, nil
}

// maxGuestFileWriteBytes bounds a single agent/file-write. Proxmox validates
// the content parameter at 61440 characters, so anything larger is rejected by
// the API rather than by the guest. Callers that need more must chunk; nothing
// here does, because the only thing written into a guest is a short script.
const maxGuestFileWriteBytes = 60 * 1024

// GuestAgentExec starts a command inside the guest via the QEMU guest agent and
// returns its PID, to be polled with GuestAgentExecStatus.
//
// Returns (0, nil) when the agent is not running, matching the other agent
// helpers: an absent agent is a state, not a failure.
//
// The PID is deliberately an int rather than a string. Proxmox returns a
// process id here, not a UPID, and typing it as a string would both invite that
// confusion and drag the method into the UPID/TrackTask guard list.
//
// IMPORTANT: whatever is started here is a child of the guest agent. A command
// that restarts the agent's own service — which installing virtio-win guest
// tools does — kills the process tree and loses the PID table with it. Run such
// things detached (a scheduled task), not directly through this.
func (c *Client) GuestAgentExec(ctx context.Context, node string, vmid int, command []string) (int, error) {
	if err := validateNodeName(node); err != nil {
		return 0, err
	}
	if err := validateVMID(vmid); err != nil {
		return 0, err
	}
	if len(command) == 0 {
		return 0, fmt.Errorf("command is required")
	}

	form := url.Values{}
	// Proxmox takes the command as a repeated parameter, one element per
	// argument — NOT a single shell string. Passing it as one value makes the
	// agent try to execute the whole line as a program name.
	for _, arg := range command {
		form.Add("command", arg)
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/exec"
	var wrapper struct {
		PID int `json:"pid"`
	}
	if err := c.doPost(ctx, path, form, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("guest agent exec on VM %d on %s: %w", vmid, node, err)
	}
	return wrapper.PID, nil
}

// GuestAgentExecStatus polls a command started by GuestAgentExec.
// Returns (nil, nil) when the agent is not running.
func (c *Client) GuestAgentExecStatus(ctx context.Context, node string, vmid, pid int) (*GuestExecStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	if pid <= 0 {
		return nil, fmt.Errorf("pid must be positive")
	}

	q := url.Values{}
	q.Set("pid", strconv.Itoa(pid))
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) +
		"/agent/exec-status?" + q.Encode()

	var status GuestExecStatus
	if err := c.do(ctx, path, &status); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("guest agent exec-status for pid %d on VM %d on %s: %w", pid, vmid, node, err)
	}
	return &status, nil
}

// GuestAgentExecWait runs a command in the guest and blocks until it exits,
// returning its final status. It is GuestAgentExec followed by a poll of
// GuestAgentExecStatus every poll interval until the command exits or timeout
// elapses.
//
// A command that exits non-zero returns both the status and an error naming the
// exit code and whatever the guest wrote — stderr if there is any, stdout
// otherwise, since PowerShell routinely reports failures on stdout.
//
// ErrGuestAgentUnavailable comes back unwrapped when the agent is not running,
// either at exec time (pid 0) or when it disappears mid-poll, so callers can
// errors.Is it without unwrapping. The same caveat as GuestAgentExec applies:
// a command that restarts the agent's own service loses its PID table, and must
// be run detached rather than waited on here.
func (c *Client) GuestAgentExecWait(ctx context.Context, node string, vmid int, argv []string, timeout, poll time.Duration) (*GuestExecStatus, error) {
	pid, err := c.GuestAgentExec(ctx, node, vmid, argv)
	if err != nil {
		return nil, err
	}
	if pid == 0 {
		return nil, ErrGuestAgentUnavailable
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("guest %d: %s did not finish within %s", vmid, argv[0], timeout)
		}
		status, err := c.GuestAgentExecStatus(ctx, node, vmid, pid)
		if err != nil {
			return nil, err
		}
		if status == nil {
			return nil, ErrGuestAgentUnavailable
		}
		if status.Exited {
			if status.ExitCode != 0 {
				return status, fmt.Errorf("guest %d: %s exited %d: %s", vmid, argv[0], int(status.ExitCode),
					cmp.Or(strings.TrimSpace(status.ErrData), strings.TrimSpace(status.OutData)))
			}
			return status, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// GuestAgentFileWrite writes content to a file inside the guest.
//
// Proxmox base64-encodes the content on our behalf (encode=1), so callers pass
// plain bytes. The size cap is the API's, not the agent's.
func (c *Client) GuestAgentFileWrite(ctx context.Context, node string, vmid int, file string, content []byte) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if file == "" {
		return fmt.Errorf("file path is required")
	}
	if len(content) > maxGuestFileWriteBytes {
		return fmt.Errorf("content is %d bytes, over the %d-byte limit Proxmox accepts for a single write",
			len(content), maxGuestFileWriteBytes)
	}

	form := url.Values{}
	form.Set("file", file)
	form.Set("content", string(content))
	form.Set("encode", "1")

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/file-write"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		if isAgentNotRunning(err) {
			return ErrGuestAgentUnavailable
		}
		return fmt.Errorf("guest agent file-write %s on VM %d on %s: %w", file, vmid, node, err)
	}
	return nil
}

// GuestAgentFileRead reads a file from inside the guest.
// Returns (nil, nil) when the agent is not running.
//
// Proxmox decodes the agent's base64 for us (decode=1). A file larger than the
// agent's own limit comes back truncated, which is surfaced as an error rather
// than silently handing back a partial file — callers here parse JSON, and half
// a JSON document is worse than none.
func (c *Client) GuestAgentFileRead(ctx context.Context, node string, vmid int, file string) ([]byte, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	if file == "" {
		return nil, fmt.Errorf("file path is required")
	}

	q := url.Values{}
	q.Set("file", file)
	q.Set("decode", "1")
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) +
		"/agent/file-read?" + q.Encode()

	var wrapper struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("guest agent file-read %s on VM %d on %s: %w", file, vmid, node, err)
	}
	if wrapper.Truncated {
		return nil, fmt.Errorf("guest agent file-read %s on VM %d on %s: file was truncated", file, vmid, node)
	}
	return []byte(wrapper.Content), nil
}

func (c *Client) GetCTConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/config"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get CT %d config on %s: %w", vmid, node, err)
	}
	return config, nil
}

// SetVMConfig applies configuration changes to a VM and returns once Proxmox
// has actually applied them.
//
// PUT is Proxmox's synchronous config API. POST on the same path is the
// asynchronous one: it forks a worker and answers with a task UPID, so it
// returns the moment the work is QUEUED. Both verbs run the identical update,
// hotplug included — the only difference is whether the change has happened
// when the call returns.
//
// Every caller here needs it to have happened: they go straight on to act on
// the new config — mount the disc that was just attached, record the media
// change as done. A queued worker gives them none of that, and its failure
// would never reach them.
//
// qemu-server's own PUT description does steer callers to POST "for any
// actions involving hotplug or storage allocation", and that advice is about
// duration, not behaviour: the sync verb runs the whole update inside the HTTP
// request, so a slow allocation can outlive it. Nexara accepts that. A cached
// client allows 5 minutes (proxmox.CachedClientTimeout), which is far past any
// config write that is going to succeed, and every other config-writing method
// here already waits the same way. Trading a bounded wait for an answer the
// caller can act on is the right side of that deal.
//
// If an asynchronous variant is genuinely needed, it belongs in a separate
// method returning (string, error) whose UPID the handler records via
// TrackTask; upid_signature_guard_test.go enforces that shape for a POST to
// this path.
func (c *Client) SetVMConfig(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set VM %d config on %s: %w", vmid, node, err)
	}
	return nil
}
func (c *Client) SetContainerConfig(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set container %d config on %s: %w", vmid, node, err)
	}
	return nil
}
