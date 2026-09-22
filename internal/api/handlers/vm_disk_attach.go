package handlers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// This file is the decision half of POST /disks/attach: everything that
// has to be known about the TARGET VM and the TARGET POOL before a volume
// spec may be written. proxmox.DiskAttachParams.Validate covers the half
// that is answerable from the parameters alone.
//
// It exists because an external consumer destroyed a live VM's boot disk
// through this endpoint. Four separate things had to be true for that:
//
//  1. The request struct held the index as a plain int, so an omitted
//     index was indistinguishable from an explicit 0 — and index 0 on a
//     VM with a disk is its boot disk. apischema's three-state read
//     (OptInt returns supplied=false) removes the ambiguity; this file
//     removes the consequence, by picking a FREE slot when nothing was
//     asked for.
//  2. Nothing checked whether the slot was already occupied. Writing
//     scsi0 on a VM that has a scsi0 does not fail — Proxmox replaces the
//     config line, and the old volume is orphaned, not attached.
//  3. The size went through as free text, so "500G" failed to parse and
//     "512000" quietly asked for 500 TiB.
//  4. Nothing compared the requested size against the pool it was
//     destined for.
//
// A fifth was not part of the incident and is reachable without it: the
// slot is chosen from a config snapshot, a second round trip happens
// before anything is written, and the write carried no compare-and-swap
// token — so two concurrent attaches that both omitted an index picked
// the same slot and the second replaced the first. That is failure mode 2
// above, arrived at by concurrency instead of by a bad parameter. The
// digest read alongside the config is carried into the write; see the
// comment on it in planDiskAttach.
//
// The checks below are ordered cheapest-first and every one of them fails
// CLOSED: a question we cannot answer is a disk we do not create.

// bytesPerGiB converts the bare GiB count apischema's "disk-size" format
// normalizes to into the byte units Proxmox reports pool capacity in.
const bytesPerGiB int64 = 1 << 30

// diskAttachProbe is the slice of *proxmox.Client that planDiskAttach
// reads. It is an interface so the planning can be driven by a fake in
// tests: every branch below is reached by what the VM's config and the
// node's storage list happen to say, and none of that is worth a live
// cluster to exercise.
type diskAttachProbe interface {
	GetVMConfig(ctx context.Context, node string, vmid int) (proxmox.VMConfig, error)
	GetStoragePools(ctx context.Context, node string) ([]proxmox.StoragePool, error)
}

// diskAttachRequest is one attach as the caller asked for it, after
// validation and normalization.
type diskAttachRequest struct {
	Bus string

	// Index and HasIndex are deliberately two fields. A single int cannot
	// say "the caller did not choose", and reading its zero value as
	// slot 0 is the first of the four defects above. HasIndex comes
	// straight from apischema's OptInt, which is why "index" is declared
	// Optional with NO default: a default would make every request look
	// like an explicit choice.
	Index    int
	HasIndex bool

	Storage string

	// SizeGiB is the normalized size. The wire form went through the
	// "disk-size" format, so 500, "500", "500G" and "1T" all arrive here
	// as a plain GiB count.
	SizeGiB int64

	Format string
}

// diskAttachRequestFrom reads one attach request out of its validated
// parameters.
//
// It is a function of its own rather than eight lines inside the handler
// because it is the SEAM: everything either side of it is covered, and
// writing HasIndex: true here, or reaching for p.Int("index") instead of
// p.OptInt, would reintroduce the incident with every other test still
// green. Its own test drives it against the endpoint's real declaration.
func diskAttachRequestFrom(p *apischema.Params) (diskAttachRequest, error) {
	// The "disk-size" format has already turned 500, "500", "500G" and
	// "1T" into a bare GiB count, so this parse cannot fail on anything a
	// caller sent; a failure would mean the declaration dropped the
	// format, which is our bug and not theirs.
	sizeGiB, err := strconv.ParseInt(p.String("size"), 10, 64)
	if err != nil {
		return diskAttachRequest{}, fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
	}

	index, hasIndex := p.OptInt("index")
	return diskAttachRequest{
		Bus:      p.String("bus"),
		Index:    int(index),
		HasIndex: hasIndex,
		Storage:  p.String("storage"),
		SizeGiB:  sizeGiB,
		Format:   p.String("format"),
	}, nil
}

// poolLookup is the answer to "how big is the pool this disk is going
// to?" — and there are three of them, not two. Collapsing the third into
// either of the others is what turns a capacity check into a formality:
// read as "it fits" it authorizes everything, and read as "it does not
// fit" it refuses a pool that is merely quiet.
type poolLookup int

const (
	// poolResolved: the node listed the pool and reported a capacity.
	poolResolved poolLookup = iota
	// poolNotListed: the node's storage list has no such pool.
	poolNotListed
	// poolNoCapacity: the pool is listed but reports a total of zero,
	// which is what an inactive or unreachable storage answers. It is
	// "we could not look", not "it has no room".
	poolNoCapacity
)

// planDiskAttach turns a request into the exact volume the attach will
// write, or into the refusal that stops it.
//
// The VM's config is mandatory: the free slot, the occupied slots and the
// boot order all come out of it, so a config we cannot read is a request
// we cannot safely serve.
func planDiskAttach(ctx context.Context, probe diskAttachProbe, node string, vmid int, req diskAttachRequest) (proxmox.DiskAttachParams, error) {
	var zero proxmox.DiskAttachParams

	maxIndex, known := proxmox.MaxDiskIndex(req.Bus)
	if !known {
		return zero, fiber.NewError(fiber.StatusBadRequest,
			"bus must be one of: "+strings.Join(proxmox.DiskBuses, ", "))
	}

	config, err := probe.GetVMConfig(ctx, node, vmid)
	if err != nil {
		return zero, describeUnverifiable(err,
			"Could not read the VM's configuration to choose a disk slot, so no disk was created")
	}

	// The CAS token for the read that is about to justify a slot choice.
	//
	// Everything below decides which slot is free FROM THIS SNAPSHOT, and
	// then a second round trip (the storage list) happens before anything
	// is written — so the config can have moved on by the time the write
	// lands. Two attaches that both omit an index read the same config,
	// both pick the same free slot, and the second write replaces the
	// first: the volume is orphaned, not attached, which is the same
	// failure mode as the incident reached by a different route. Carrying
	// the digest into the write pins the decision to the read that
	// justified it, and Proxmox refuses the stale one.
	//
	// A config with no digest is a "could not look": we cannot pin the
	// write, so we do not make it. Proxmox has always returned one here,
	// which is why this reads as a fail-closed guard rather than a
	// fallback.
	digest, _ := config["digest"].(string)
	if digest == "" {
		return zero, fiber.NewError(fiber.StatusBadGateway,
			"the VM's configuration came back without a digest, so the slot choice could not be pinned "+
				"to it and a concurrent change could not be detected; no disk was created")
	}

	occupied := diskSlotOccupants(config, req.Bus)
	boot := bootReferencedKeys(config)

	index := req.Index
	if req.HasIndex {
		if index < 0 || index > maxIndex {
			return zero, fiber.NewError(fiber.StatusBadRequest,
				fmt.Sprintf("index must be between 0 and %d for the %s bus", maxIndex, req.Bus))
		}
	} else {
		// Auto-selection SKIPS a boot-referenced slot rather than landing
		// on it and then being refused below. The two cases differ because
		// the caller does: someone who named a slot gets told no, while
		// someone who asked for "anywhere free" gets somewhere free.
		//
		// Without the skip, a boot order naming a slot nothing occupies —
		// a stale entry left behind by a detach, or a hand-edited config —
		// would dead-end EVERY automatic attach on that bus, because the
		// planner would keep picking the one slot it then refuses.
		free, ok := nextFreeDiskIndex(occupied, boot, req.Bus, maxIndex)
		if !ok {
			return zero, fiber.NewError(fiber.StatusConflict,
				fmt.Sprintf("every %s slot (0-%d) on this VM is in use or reserved for its boot disk; "+
					"detach a disk or choose another bus", req.Bus, maxIndex))
		}
		index = free
	}

	key := req.Bus + strconv.Itoa(index)

	// The boot check runs BEFORE the occupancy check, and would be
	// redundant if the occupancy check could never be reached around: a
	// slot that boots the VM is usually a slot with something in it. It is
	// kept separate because it is the refusal that matters — it names the
	// consequence rather than the collision — and because it must keep
	// holding if the rule below is ever relaxed for some class of device.
	// On the auto path it can no longer fire, by construction.
	if boot[key] {
		return zero, fiber.NewError(fiber.StatusConflict,
			key+" is in this VM's boot order; attaching here would replace the disk it boots from. "+
				"Detach it first, or pick another slot.")
	}
	if existing, taken := occupied[index]; taken {
		return zero, fiber.NewError(fiber.StatusConflict,
			key+" already holds "+existing+". Detach it first, or pick a free slot.")
	}

	// Capacity last: it is the only check that costs a second round trip,
	// and there is no point paying for it on a request the slot rules
	// have already refused.
	pools, err := probe.GetStoragePools(ctx, node)
	if err != nil {
		return zero, describeUnverifiable(err,
			"Could not read the node's storage list to size-check the new disk, so no disk was created")
	}
	total, lookup := poolTotalBytes(pools, req.Storage)
	switch lookup {
	case poolNotListed:
		return zero, fiber.NewError(fiber.StatusConflict,
			"storage "+req.Storage+" is not available on node "+node+", so the request could not be size-checked")
	case poolNoCapacity:
		return zero, fiber.NewError(fiber.StatusConflict,
			"storage "+req.Storage+" on node "+node+" reports no capacity (it is offline or disabled), "+
				"so the request could not be size-checked")
	case poolResolved:
		// The comparison is against TOTAL, not available. Thin
		// provisioning on Ceph, LVM-thin and qcow2 legitimately
		// overcommits free space — a 200 GiB volume on a pool with 20 GiB
		// free is normal and works — so gating on Avail would refuse
		// working setups. Total is the ceiling that is never legitimate
		// to cross: no pool can hold a volume larger than the pool.
		//
		// The pool's capacity is divided down rather than the request
		// multiplied up. Multiplying would overflow int64 at 8 EiB of
		// requested size and wrap to a value that compares as "fits" —
		// unreachable through the declared schema, which caps size at
		// apischema.MaxDiskSizeGiB, but this function re-checks the bus
		// and the index bounds the schema also enforces, and a capacity
		// check that only holds because of its one caller is the opt-in
		// guard shape this codebase keeps finding. The truncation is
		// harmless: a pool of 1024.9 GiB accepts 1024 and refuses 1025
		// either way.
		if req.SizeGiB > total/bytesPerGiB {
			return zero, fiber.NewError(fiber.StatusConflict,
				fmt.Sprintf("a %d GiB disk does not fit on storage %s, whose total capacity is %d GiB",
					req.SizeGiB, req.Storage, total/bytesPerGiB))
		}
	}

	return proxmox.DiskAttachParams{
		Bus:     req.Bus,
		Index:   index,
		Storage: req.Storage,
		Size:    strconv.FormatInt(req.SizeGiB, 10),
		Format:  req.Format,
		// From the read above, not from a fresh one: a digest fetched at
		// write time would pin the write to a config nobody made a
		// decision against, which is the inverted form of this check.
		Digest: digest,
	}, nil
}

// describeUnverifiable reports a lookup that failed, keeping the status
// mapProxmoxError chose (a 403 on the cluster's own credential is not a
// 502) while saying plainly that nothing was created. "The check could
// not run" and "the check passed" have to read differently, or the next
// operator will assume the attach half-succeeded.
func describeUnverifiable(err error, what string) error {
	mapped := mapProxmoxError(err)
	var fe *fiber.Error
	if errors.As(mapped, &fe) {
		return fiber.NewError(fe.Code, what+": "+fe.Message)
	}
	return mapped
}

// diskSlotOccupants maps each occupied slot index on bus to a short
// description of what is in it.
//
// The suffix is parsed strictly as digits so that neighbouring config
// keys are not mistaken for slots: "scsihw" is the controller model, not
// scsi slot "hw", and "virtiofs0" is a filesystem passthrough, not
// virtio slot 0 — attaching over which would be the same class of
// accident this whole file exists to prevent.
func diskSlotOccupants(config proxmox.VMConfig, bus string) map[int]string {
	out := map[int]string{}
	for key, raw := range config {
		suffix, found := strings.CutPrefix(key, bus)
		if !found || suffix == "" {
			continue
		}
		n, err := strconv.Atoi(suffix)
		// The round trip rejects "+1", "01" and " 1", which Atoi accepts
		// and Proxmox never writes.
		if err != nil || n < 0 || suffix != strconv.Itoa(n) {
			continue
		}
		out[n] = describeVolume(raw)
	}
	return out
}

// describeVolume renders a config value as the volume it names, dropping
// the trailing option list ("local-lvm:vm-101-disk-0,size=32G,ssd=1" →
// "local-lvm:vm-101-disk-0"). A key that is present but says nothing is
// still an occupied slot — Proxmox knows about it, so we must too.
func describeVolume(raw any) string {
	var text string
	switch v := raw.(type) {
	case string:
		text = v
	case nil:
		text = ""
	default:
		text = fmt.Sprint(v)
	}
	volume, _, _ := strings.Cut(text, ",")
	if volume = strings.TrimSpace(volume); volume == "" {
		return "an existing device"
	}
	return volume
}

// nextFreeDiskIndex returns the lowest slot on the bus that is neither
// occupied nor named in the VM's boot order, and whether there is one at
// all.
//
// A full bus is reported rather than wrapped to some other bus: which
// controller a disk hangs off changes how the guest sees it, and picking
// one on the caller's behalf is not ours to do.
func nextFreeDiskIndex(occupied map[int]string, boot map[string]bool, bus string, maxIndex int) (int, bool) {
	for i := 0; i <= maxIndex; i++ {
		if _, taken := occupied[i]; taken {
			continue
		}
		if boot[bus+strconv.Itoa(i)] {
			continue
		}
		return i, true
	}
	return 0, false
}

// bootReferencedKeys returns the config keys this VM boots from, read
// from both spellings Proxmox has used.
//
// PVE 6.0 and later write `boot: order=scsi0;ide2;net0`. Before that,
// `boot` was a letter list ("cdn" — disk, CD-ROM, network) that names no
// device, and the device itself lived in the deprecated `bootdisk:
// scsi0`. Both are read, and the letter list is harmless to split: no
// disk key is ever spelled "c", "d" or "n".
func bootReferencedKeys(config proxmox.VMConfig) map[string]bool {
	out := map[string]bool{}
	if v, ok := config["bootdisk"].(string); ok {
		if key := strings.TrimSpace(v); key != "" {
			out[key] = true
		}
	}
	if v, ok := config["boot"].(string); ok {
		spec := strings.TrimSpace(v)
		if rest, found := strings.CutPrefix(spec, "order="); found {
			spec = rest
		}
		for _, token := range strings.FieldsFunc(spec, func(r rune) bool { return r == ';' || r == ',' }) {
			if token = strings.TrimSpace(token); token != "" {
				out[token] = true
			}
		}
	}
	return out
}

// poolTotalBytes reports the named pool's total capacity, and which of
// the three poolLookup answers this is.
func poolTotalBytes(pools []proxmox.StoragePool, storage string) (int64, poolLookup) {
	for _, p := range pools {
		if p.Storage != storage {
			continue
		}
		if p.Total <= 0 {
			return 0, poolNoCapacity
		}
		return p.Total, poolResolved
	}
	return 0, poolNotListed
}

// --- Detach auditing ---

// detachProbe is the slice of *proxmox.Client that detachedVolume reads.
// Narrow on purpose, so the audit lookup is drivable by a fake.
// The two sentinels detachedVolume returns in place of a volume id. They are
// distinct because "there was nothing there" and "we could not look" send an
// incident responder in opposite directions.
const (
	configUnreadable = "unresolved: the VM config could not be read"
	notInConfig      = "unresolved: no such key in the VM config"
)

type detachProbe interface {
	GetVMConfig(ctx context.Context, node string, vmid int) (proxmox.VMConfig, error)
}

// detachedVolume names the volume a slot holds, for the audit row, and is
// called BEFORE the detach because afterwards the slot is gone.
//
// The slot name alone does not identify what a detach affected, and for one
// shape of the call what it affects is destroyed: PVE reads `delete: ide1` as
// "unhook the volume and park it in unusedN", but `delete: unused0` as
// "remove the volume from storage". An audit row saying only
// "disk_detach: unused0" cannot say which volume stopped existing.
//
// It also returns the config digest, so the caller can pin the delete to this
// same read. Empty when the read failed.
//
// CAVEAT on which view this is: GetVMConfig requests /config without
// current=1, so PVE returns the pending-MERGED view — pending values folded
// in, keys queued for pending delete already gone. The delete loop PVE runs
// reads the raw config instead. For a running VM, an ide/sata/efidisk/tpmstate
// drive cannot be hot-unplugged, so the first detach only queues a pending
// change; a second detach of the same key then reads a config that no longer
// lists it and records "no such key" for a detach PVE accepts against a real
// volume. Neither view is right for both cases (current=1 breaks the
// pending-ADD case), and GetVMConfig is shared with planDiskAttach, so this is
// documented rather than worked around.
//
// The read is best-effort — a detach Proxmox would accept must not fail
// because the audit lookup did not. But an unreadable config is recorded as
// unresolved rather than omitted: a missing value reads as "there was nothing
// there", which is the opposite of "we could not look", and those two answers
// send an incident responder in different directions.
func detachedVolume(ctx context.Context, probe detachProbe, node string, vmid int, disk string) (volume, digest string) {
	config, err := probe.GetVMConfig(ctx, node, vmid)
	if err != nil {
		return configUnreadable, ""
	}
	digest, _ = config["digest"].(string)
	raw, ok := config[disk]
	if !ok {
		return notInConfig, digest
	}
	return describeVolume(raw), digest
}

// cloudInitVolumeRe matches the volume ids PVE treats as cloud-init drives.
// Mirrors drive_is_cloudinit in qemu-server's Drive.pm: PVE FREES these on
// detach rather than parking them, via vmconfig_register_unused_drive.
var cloudInitVolumeRe = regexp.MustCompile(`[:/](?:vm-\d+-)?cloudinit(\.[a-z0-9]+)?$`)

// detachRemovesVolume reports whether detaching disk destroys the volume
// rather than parking it in an unusedN slot — the difference between a
// reversible change and an irreversible one, which cannot be recovered from
// the audit row later.
//
// It returns nil for "cannot tell", which JSON renders as null. Reporting a
// definite false when the answer is unknown is the failure this whole change
// exists to avoid: a row that asserts the reversible behaviour for a
// destructive act reads as reassurance.
//
// Three shapes destroy a volume, all by qemu-server's own decisions
// (src/PVE/API2/Qemu.pm and QemuServer.pm):
//
//   - an unusedN key, whose volume is already unhooked, so removing the key
//     is the only thing left that can happen to it. update_vm's delete loop
//     frees it on the spot, through try_deallocate_drive;
//   - vmstate, a hibernated VM's saved RAM, which the same loop frees
//     through try_deallocate_drive with force — dropping a
//     `lock: suspended` first so the delete goes through;
//   - a cloud-init drive on ANY key. A drive key is only QUEUED by that
//     loop; when the pending delete is applied, vmconfig_delete_or_detach_drive
//     hands it to vmconfig_register_unused_drive, which parks an owned volume
//     in an unusedN slot but frees a cloud-init drive.
//
// try_deallocate_drive frees only a volume the VM owns (vm_is_volid_owner);
// for any other it just drops the key. The row cannot see ownership, so it
// records what the key asked for.
//
// A key that is not set destroys nothing, whatever its name: PVE warns
// "cannot delete … not set in current configuration" and skips it, and a
// detach of unused7 on a VM with no unused7 returns 200 having done nothing.
// That is decided FIRST, so the key-name cases below cannot claim a
// destruction that did not happen.
//
// resolved is what detachedVolume found. When it could not read the config,
// the cloud-init question is unanswerable and so is this one — except for an
// unusedN or vmstate key, where the key alone decides what a detach does to
// the volume if there is one.
//
// The key-name test is only sound because PVE validates the option name
// first: API2/Qemu.pm raises "unknown option" for anything outside the config
// schema, so `unusedx` never reaches a written audit row. The declared
// pattern (^[a-z]+[0-9]*$) does NOT constrain it — do not read the schema and
// conclude otherwise.
func detachRemovesVolume(disk, resolved string) *bool {
	yes, no := true, false
	if resolved == notInConfig {
		// PVE warns and skips a key that is not set, returning 200 having
		// removed nothing. Without this the row claims a destruction that
		// did not happen, which is worse than saying nothing.
		return &no
	}
	if strings.HasPrefix(disk, "unused") || disk == "vmstate" {
		return &yes
	}
	switch {
	case resolved == configUnreadable:
		return nil
	case cloudInitVolumeRe.MatchString(resolved):
		return &yes
	default:
		return &no
	}
}
