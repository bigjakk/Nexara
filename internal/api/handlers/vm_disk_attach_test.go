package handlers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// fakeDiskProbe stands in for *proxmox.Client. Every branch in
// planDiskAttach is reached through what a VM's config and a node's
// storage list say, so the two reads are all that has to be faked — and
// faking them is what lets the boot-disk and capacity rules be tested
// without a cluster to break.
type fakeDiskProbe struct {
	config    proxmox.VMConfig
	configErr error
	pools     []proxmox.StoragePool
	poolsErr  error

	// omitDigest drops the CAS token Proxmox always sends with a config,
	// which is the one branch that cannot be reached by setting config
	// alone.
	omitDigest bool
	// afterRead runs once the config has been handed over, so a test can
	// stand in for another writer changing the guest between the read that
	// chose a slot and the write that takes it.
	afterRead func()

	configCalls int
	poolCalls   int
}

// testDigest is the SHA-1 Proxmox returns with a config. Synthetic hex.
const testDigest = "aabbccddeeff00112233445566778899aabbccdd"

func (f *fakeDiskProbe) GetVMConfig(_ context.Context, _ string, _ int) (proxmox.VMConfig, error) {
	f.configCalls++
	if f.configErr != nil {
		return nil, f.configErr
	}
	// A copy, so a test that mutates f.config after the read is changing
	// the guest rather than the answer already given — which is the whole
	// point of the afterRead hook.
	out := maps.Clone(f.config)
	if out == nil {
		out = proxmox.VMConfig{}
	}
	if !f.omitDigest {
		if _, ok := out["digest"]; !ok {
			out["digest"] = testDigest
		}
	}
	if f.afterRead != nil {
		f.afterRead()
	}
	return out, nil
}

func (f *fakeDiskProbe) GetStoragePools(_ context.Context, _ string) ([]proxmox.StoragePool, error) {
	f.poolCalls++
	if f.poolsErr != nil {
		return nil, f.poolsErr
	}
	return f.pools, nil
}

const (
	testAttachNode    = "pve-01"
	testAttachStorage = "store01"
	testAttachVMID    = 101
	gib               = int64(1) << 30
)

// onePool is a 1 TiB pool with almost nothing free, which is the shape
// thin provisioning produces and which the capacity rule must still
// accept for anything under Total.
func onePool(total, avail int64) []proxmox.StoragePool {
	return []proxmox.StoragePool{
		{Storage: "local", Type: "dir", Total: 50 * gib, Avail: 20 * gib},
		{Storage: testAttachStorage, Type: "rbd", Total: total, Avail: avail},
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var fe *fiber.Error
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a *fiber.Error, so it carries no status", err)
	}
	return fe.Code
}

func attachRequest(bus string) diskAttachRequest {
	return diskAttachRequest{
		Bus:     bus,
		Storage: testAttachStorage,
		SizeGiB: 32,
	}
}

func withIndex(req diskAttachRequest, index int) diskAttachRequest {
	req.Index = index
	req.HasIndex = true
	return req
}

// TestPlanDiskAttachChoosesAFreeSlot covers the defect the incident
// turned on: an omitted index used to mean slot 0.
func TestPlanDiskAttachChoosesAFreeSlot(t *testing.T) {
	tests := []struct {
		name   string
		bus    string
		config proxmox.VMConfig
		want   string
	}{
		{
			name:   "empty VM takes slot 0",
			bus:    "scsi",
			config: proxmox.VMConfig{},
			want:   "scsi0",
		},
		{
			name:   "skips the occupied prefix",
			bus:    "scsi",
			config: proxmox.VMConfig{"scsi0": "store01:vm-101-disk-0,size=32G", "scsi1": "store01:vm-101-disk-1"},
			want:   "scsi2",
		},
		{
			name:   "fills a hole rather than appending",
			bus:    "scsi",
			config: proxmox.VMConfig{"scsi0": "store01:vm-101-disk-0", "scsi2": "store01:vm-101-disk-2"},
			want:   "scsi1",
		},
		{
			name: "scsihw is the controller model, not scsi slot hw",
			bus:  "scsi",
			// If the suffix were parsed loosely, "hw" could be read as a
			// slot and shift the answer; it must simply not be a slot.
			config: proxmox.VMConfig{"scsihw": "virtio-scsi-single"},
			want:   "scsi0",
		},
		{
			name: "virtiofs0 is a filesystem passthrough, not virtio slot 0",
			bus:  "virtio",
			// The dangerous direction: reading virtiofs0 as virtio0 would
			// make the planner SKIP a genuinely free slot, but reading
			// virtio0 as free when virtiofs0 exists is fine — they are
			// different devices.
			config: proxmox.VMConfig{"virtiofs0": "shared-dir"},
			want:   "virtio0",
		},
		{
			name:   "an ide CD-ROM occupies its slot",
			bus:    "ide",
			config: proxmox.VMConfig{"ide2": "local:iso/debian.iso,media=cdrom"},
			want:   "ide0",
		},
		{
			name:   "leading zeros are not a slot",
			bus:    "sata",
			config: proxmox.VMConfig{"sata00": "store01:vm-101-disk-9"},
			want:   "sata0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &fakeDiskProbe{config: tt.config, pools: onePool(1024*gib, 1*gib)}
			got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest(tt.bus))
			if err != nil {
				t.Fatalf("planDiskAttach: %v", err)
			}
			if got.DiskKey() != tt.want {
				t.Errorf("disk key = %q, want %q", got.DiskKey(), tt.want)
			}
		})
	}
}

// TestPlanDiskAttachRefusesAFullBus pins each bus's ceiling, and that a
// full one is reported rather than silently wrapped onto another
// controller.
func TestPlanDiskAttachRefusesAFullBus(t *testing.T) {
	for _, bus := range proxmox.DiskBuses {
		t.Run(bus, func(t *testing.T) {
			maxIndex, ok := proxmox.MaxDiskIndex(bus)
			if !ok {
				t.Fatalf("MaxDiskIndex(%q) reports the bus is unknown", bus)
			}
			config := proxmox.VMConfig{}
			for i := 0; i <= maxIndex; i++ {
				config[fmt.Sprintf("%s%d", bus, i)] = testAttachStorage + ":vm-101-disk-" + fmt.Sprint(i)
			}

			probe := &fakeDiskProbe{config: config, pools: onePool(1024*gib, 1*gib)}
			_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest(bus))
			if err == nil {
				t.Fatal("a full bus was accepted")
			}
			if code := statusOf(t, err); code != fiber.StatusConflict {
				t.Errorf("status = %d, want 409", code)
			}
			if !strings.Contains(err.Error(), bus) {
				t.Errorf("message %q does not name the bus", err.Error())
			}
			if probe.poolCalls != 0 {
				t.Errorf("the storage list was read %d time(s) for a request the slot rules refused", probe.poolCalls)
			}

			// One slot short of full is still servable, which is what
			// makes the ceiling a ceiling rather than an off-by-one.
			delete(config, fmt.Sprintf("%s%d", bus, maxIndex))
			got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest(bus))
			if err != nil {
				t.Fatalf("the last free slot was refused: %v", err)
			}
			if want := fmt.Sprintf("%s%d", bus, maxIndex); got.DiskKey() != want {
				t.Errorf("disk key = %q, want %q", got.DiskKey(), want)
			}
		})
	}
}

// TestPlanDiskAttachRefusesAnOccupiedSlot is the fix that makes the
// incident unreachable: the caller named a slot, and something is in it.
func TestPlanDiskAttachRefusesAnOccupiedSlot(t *testing.T) {
	config := proxmox.VMConfig{
		"scsi1": "store01:vm-101-disk-1,size=32G,ssd=1",
		"ide2":  "local:iso/debian.iso,media=cdrom",
		"sata0": "",
	}

	tests := []struct {
		name     string
		bus      string
		index    int
		occupant string
	}{
		{"a data disk", "scsi", 1, "store01:vm-101-disk-1"},
		{"a mounted CD-ROM", "ide", 2, "local:iso/debian.iso"},
		{"a slot whose value says nothing", "sata", 0, "an existing device"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &fakeDiskProbe{config: config, pools: onePool(1024*gib, 1*gib)}
			_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID,
				withIndex(attachRequest(tt.bus), tt.index))
			if err == nil {
				t.Fatal("an occupied slot was accepted")
			}
			if code := statusOf(t, err); code != fiber.StatusConflict {
				t.Errorf("status = %d, want 409", code)
			}
			if !strings.Contains(err.Error(), tt.occupant) {
				t.Errorf("message %q does not name what is in the slot (%q)", err.Error(), tt.occupant)
			}
		})
	}
}

// TestPlanDiskAttachRefusesTheBootDisk covers the belt-and-braces rule,
// in both spellings Proxmox has used for the boot order and on both the
// explicit and the auto-selected path.
func TestPlanDiskAttachRefusesTheBootDisk(t *testing.T) {
	tests := []struct {
		name     string
		config   proxmox.VMConfig
		req      diskAttachRequest
		wantKey  string
		explicit bool
	}{
		{
			name:    "modern boot order, explicit slot",
			config:  proxmox.VMConfig{"boot": "order=scsi0;ide2;net0", "scsi0": "store01:vm-101-disk-0"},
			req:     withIndex(attachRequest("scsi"), 0),
			wantKey: "scsi0",
		},
		{
			name:    "deprecated bootdisk key, explicit slot",
			config:  proxmox.VMConfig{"bootdisk": "scsi0", "scsi0": "store01:vm-101-disk-0"},
			req:     withIndex(attachRequest("scsi"), 0),
			wantKey: "scsi0",
		},
		{
			name: "a boot order naming a slot nothing occupies still protects it",
			// The slot is FREE, so the occupancy rule has nothing to say.
			// This is the case the separate boot check exists for — and it
			// only applies when the caller NAMED the slot; see
			// TestPlanDiskAttachSkipsABootReferencedFreeSlot for what
			// happens when they did not.
			config:  proxmox.VMConfig{"boot": "order=scsi0"},
			req:     withIndex(attachRequest("scsi"), 0),
			wantKey: "scsi0",
		},
		{
			name:    "comma-separated boot order",
			config:  proxmox.VMConfig{"boot": "order=virtio0,net0"},
			req:     withIndex(attachRequest("virtio"), 0),
			wantKey: "virtio0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &fakeDiskProbe{config: tt.config, pools: onePool(1024*gib, 1*gib)}
			_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, tt.req)
			if err == nil {
				t.Fatal("a slot the VM boots from was accepted")
			}
			if code := statusOf(t, err); code != fiber.StatusConflict {
				t.Errorf("status = %d, want 409", code)
			}
			if !strings.Contains(err.Error(), tt.wantKey) || !strings.Contains(err.Error(), "boot") {
				t.Errorf("message %q does not say which slot boots the VM", err.Error())
			}
		})
	}
}

// TestPlanDiskAttachSkipsABootReferencedFreeSlot is the other half of the
// boot rule, and the one that keeps it from becoming a dead end.
//
// A caller who named a slot gets refused; a caller who asked for
// "anywhere free" gets somewhere free. Without the skip, a config whose
// boot order names a slot nothing occupies — a stale entry left by a
// detach, or a hand-edited config — would refuse every automatic attach
// on that bus forever, because the planner would keep choosing the one
// slot it then rejects.
func TestPlanDiskAttachSkipsABootReferencedFreeSlot(t *testing.T) {
	tests := []struct {
		name    string
		config  proxmox.VMConfig
		wantKey string
	}{
		{
			name:    "a stale boot entry on an empty bus",
			config:  proxmox.VMConfig{"boot": "order=scsi0"},
			wantKey: "scsi1",
		},
		{
			name:    "the deprecated bootdisk key too",
			config:  proxmox.VMConfig{"bootdisk": "scsi0"},
			wantKey: "scsi1",
		},
		{
			name:    "two reserved slots in a row",
			config:  proxmox.VMConfig{"boot": "order=scsi0;scsi1;net0"},
			wantKey: "scsi2",
		},
		{
			name: "occupied and reserved slots together",
			config: proxmox.VMConfig{
				"boot":  "order=scsi2",
				"scsi0": "store01:vm-101-disk-0",
			},
			wantKey: "scsi1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &fakeDiskProbe{config: tt.config, pools: onePool(1024*gib, 512*gib)}
			got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("scsi"))
			if err != nil {
				t.Fatalf("auto-selection dead-ended on a boot-referenced slot: %v", err)
			}
			if got.DiskKey() != tt.wantKey {
				t.Errorf("disk key = %q, want %q", got.DiskKey(), tt.wantKey)
			}
		})
	}

	// When the reservation is what makes the bus full, that is still a
	// 409 — and the message has to say so, or the operator goes looking
	// for a disk to detach that is not there.
	config := proxmox.VMConfig{"boot": "order=ide3", "ide0": "a", "ide1": "b", "ide2": "c"}
	probe := &fakeDiskProbe{config: config, pools: onePool(1024*gib, 512*gib)}
	_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("ide"))
	if err == nil {
		t.Fatal("a bus whose only free slot is boot-reserved was accepted")
	}
	if code := statusOf(t, err); code != fiber.StatusConflict {
		t.Errorf("status = %d, want 409", code)
	}
	if !strings.Contains(err.Error(), "boot") {
		t.Errorf("message %q does not mention the boot reservation", err.Error())
	}
}

// TestPlanDiskAttachIgnoresALegacyLetterBootOrder proves the permissive
// boot parse does not turn a pre-6.0 "boot: cdn" into a refusal of
// everything.
func TestPlanDiskAttachIgnoresALegacyLetterBootOrder(t *testing.T) {
	probe := &fakeDiskProbe{
		config: proxmox.VMConfig{"boot": "cdn"},
		pools:  onePool(1024*gib, 1*gib),
	}
	got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("scsi"))
	if err != nil {
		t.Fatalf("a legacy letter boot order blocked the attach: %v", err)
	}
	if got.DiskKey() != "scsi0" {
		t.Errorf("disk key = %q, want scsi0", got.DiskKey())
	}
}

// TestPlanDiskAttachCapacity is the third defect: nothing compared the
// requested size against the pool it was going to.
func TestPlanDiskAttachCapacity(t *testing.T) {
	tests := []struct {
		name     string
		sizeGiB  int64
		pools    []proxmox.StoragePool
		poolsErr error
		wantErr  bool
		wantCode int
		wantSaid string
	}{
		{
			name:    "comfortably under the pool's total",
			sizeGiB: 32,
			pools:   onePool(1024*gib, 512*gib),
		},
		{
			name:    "exactly the pool's total still fits",
			sizeGiB: 1024,
			pools:   onePool(1024*gib, 512*gib),
		},
		{
			name: "overcommitting free space is allowed",
			// Thin provisioning on Ceph and LVM-thin does this routinely:
			// 900 GiB requested on a pool with 1 GiB free is a working
			// setup, and gating on Avail would break it.
			sizeGiB: 900,
			pools:   onePool(1024*gib, 1*gib),
		},
		{
			name:     "one GiB over the total is refused",
			sizeGiB:  1025,
			pools:    onePool(1024*gib, 512*gib),
			wantErr:  true,
			wantCode: fiber.StatusConflict,
			wantSaid: "does not fit",
		},
		{
			name: "the incident's 512000",
			// disk-size normalizes "512000" to 512000 GiB — 500 TiB —
			// without complaint, because it cannot know the target pool.
			// This is the check that catches it.
			sizeGiB:  512000,
			pools:    onePool(1024*gib, 512*gib),
			wantErr:  true,
			wantCode: fiber.StatusConflict,
			wantSaid: "512000 GiB",
		},
		{
			name:     "a pool the node does not list",
			sizeGiB:  32,
			pools:    []proxmox.StoragePool{{Storage: "local", Total: 50 * gib}},
			wantErr:  true,
			wantCode: fiber.StatusConflict,
			wantSaid: "not available on node",
		},
		{
			name: "a pool reporting no capacity",
			// An inactive or unreachable storage answers 0, which is "we
			// could not look", not "it has no room".
			sizeGiB:  32,
			pools:    onePool(0, 0),
			wantErr:  true,
			wantCode: fiber.StatusConflict,
			wantSaid: "reports no capacity",
		},
		{
			name:     "the storage list could not be read",
			sizeGiB:  32,
			poolsErr: fmt.Errorf("listing storages: %w", proxmox.ErrConnectionFailed),
			wantErr:  true,
			wantCode: fiber.StatusBadGateway,
			wantSaid: "no disk was created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &fakeDiskProbe{config: proxmox.VMConfig{}, pools: tt.pools, poolsErr: tt.poolsErr}
			req := attachRequest("scsi")
			req.SizeGiB = tt.sizeGiB

			got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, req)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("planDiskAttach: %v", err)
				}
				if got.Size != fmt.Sprint(tt.sizeGiB) {
					t.Errorf("size = %q, want the bare GiB count %d", got.Size, tt.sizeGiB)
				}
				return
			}
			if err == nil {
				t.Fatal("the request was accepted")
			}
			if code := statusOf(t, err); code != tt.wantCode {
				t.Errorf("status = %d, want %d (%v)", code, tt.wantCode, err)
			}
			if !strings.Contains(err.Error(), tt.wantSaid) {
				t.Errorf("message %q does not contain %q", err.Error(), tt.wantSaid)
			}
		})
	}
}

// TestPlanDiskAttachRefusesWhenTheConfigCannotBeRead pins the other
// fail-closed branch. Without the config there is no way to know which
// slot is free or which one boots the VM, so there is nothing safe to do.
func TestPlanDiskAttachRefusesWhenTheConfigCannotBeRead(t *testing.T) {
	probe := &fakeDiskProbe{configErr: fmt.Errorf("get VM config: %w", proxmox.ErrConnectionFailed)}
	_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("scsi"))
	if err == nil {
		t.Fatal("an unreadable VM config was accepted")
	}
	if code := statusOf(t, err); code != fiber.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
	if !strings.Contains(err.Error(), "no disk was created") {
		t.Errorf("message %q does not say the attach did not happen", err.Error())
	}
	if probe.poolCalls != 0 {
		t.Errorf("the storage list was read although the config read failed")
	}
}

// TestPlanDiskAttachRejectsAnImpossibleSlot covers the bounds on an
// explicitly requested index. The schema caps it at the widest bus (30),
// so the per-bus ceiling is this function's to enforce.
func TestPlanDiskAttachRejectsAnImpossibleSlot(t *testing.T) {
	tests := []struct {
		name  string
		bus   string
		index int
	}{
		{"ide has four slots", "ide", 4},
		{"sata has six", "sata", 6},
		{"virtio has sixteen", "virtio", 16},
		{"scsi stops at 30", "scsi", 31},
		{"negative", "scsi", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &fakeDiskProbe{config: proxmox.VMConfig{}, pools: onePool(1024*gib, 512*gib)}
			_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID,
				withIndex(attachRequest(tt.bus), tt.index))
			if err == nil {
				t.Fatal("an out-of-range slot was accepted")
			}
			if code := statusOf(t, err); code != fiber.StatusBadRequest {
				t.Errorf("status = %d, want 400", code)
			}
		})
	}
}

func TestPlanDiskAttachRejectsAnUnknownBus(t *testing.T) {
	probe := &fakeDiskProbe{config: proxmox.VMConfig{}}
	_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("nvme"))
	if err == nil {
		t.Fatal("an unknown bus was accepted")
	}
	if code := statusOf(t, err); code != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if probe.configCalls != 0 {
		t.Error("the VM config was read for a request that could never be served")
	}
}

// TestPlanDiskAttachPassesTheWholeSpecThrough pins what a successful plan
// hands to Proxmox — in particular that the size reaches it as the bare
// GiB count the "storage:N" form requires.
func TestPlanDiskAttachPassesTheWholeSpecThrough(t *testing.T) {
	probe := &fakeDiskProbe{
		config: proxmox.VMConfig{"scsi0": "store01:vm-101-disk-0"},
		pools:  onePool(1024*gib, 512*gib),
	}
	req := attachRequest("scsi")
	req.SizeGiB = 500
	req.Format = proxmox.ImageFormatQcow2

	got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, req)
	if err != nil {
		t.Fatalf("planDiskAttach: %v", err)
	}
	want := proxmox.DiskAttachParams{
		Bus:     "scsi",
		Index:   1,
		Storage: testAttachStorage,
		Size:    "500",
		Format:  proxmox.ImageFormatQcow2,
		Digest:  testDigest,
	}
	if got != want {
		t.Errorf("params = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the plan does not satisfy the client's own validation: %v", err)
	}
}

// TestPlanDiskAttachPinsTheWriteToTheReadThatChoseTheSlot is the
// compare-and-swap half.
//
// The slot is decided from a config snapshot, and a second round trip
// (the storage list) happens before anything is written — so between the
// two, another attach can take the slot this one picked. Without a digest
// the write would not fail, it would REPLACE the other attach's config
// line and orphan its volume: failure mode 2 from this file's header,
// reached by concurrency rather than by a bad parameter.
//
// The digest that goes out must be the one from the read that made the
// decision, NOT a fresh one — a token fetched at write time pins the
// write to a config nobody decided anything against, which is the
// inverted form of the check.
func TestPlanDiskAttachPinsTheWriteToTheReadThatChoseTheSlot(t *testing.T) {
	probe := &fakeDiskProbe{
		config: proxmox.VMConfig{"digest": "digest-at-read-time", "scsi0": "store01:vm-101-disk-0"},
		pools:  onePool(1024*gib, 512*gib),
	}
	// Another writer lands between the read and the write: it takes scsi1
	// — the slot this attach is about to choose — and the config's digest
	// moves on.
	probe.afterRead = func() {
		probe.config = proxmox.VMConfig{
			"digest": "digest-after-the-other-writer",
			"scsi0":  "store01:vm-101-disk-0",
			"scsi1":  "store01:vm-102-disk-0",
		}
	}

	got, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("scsi"))
	if err != nil {
		t.Fatalf("planDiskAttach: %v", err)
	}
	if got.DiskKey() != "scsi1" {
		t.Fatalf("disk key = %q, want scsi1 — the plan is made from the snapshot it read", got.DiskKey())
	}
	if got.Digest != "digest-at-read-time" {
		t.Errorf("digest = %q, want \"digest-at-read-time\" — the token must come from the read that chose "+
			"the slot; a later one pins the write to a configuration nothing was decided against", got.Digest)
	}
	// Proxmox is what refuses the stale write. The contract this side has
	// to keep is that the token travels at all.
	if err := got.Validate(); err != nil {
		t.Errorf("the plan does not satisfy the client's own validation: %v", err)
	}
}

// TestPlanDiskAttachRefusesAConfigWithNoDigest covers the fail-closed
// branch: with no CAS token there is no way to detect a concurrent
// change, so there is nothing safe to write.
func TestPlanDiskAttachRefusesAConfigWithNoDigest(t *testing.T) {
	probe := &fakeDiskProbe{
		config:     proxmox.VMConfig{},
		omitDigest: true,
		pools:      onePool(1024*gib, 512*gib),
	}
	_, err := planDiskAttach(context.Background(), probe, testAttachNode, testAttachVMID, attachRequest("scsi"))
	if err == nil {
		t.Fatal("a configuration with no digest was accepted")
	}
	if code := statusOf(t, err); code != fiber.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
	if !strings.Contains(err.Error(), "no disk was created") {
		t.Errorf("message %q does not say the attach did not happen", err.Error())
	}
	if probe.poolCalls != 0 {
		t.Error("the storage list was read although the config could not be pinned")
	}
}

func TestDiskSlotOccupants(t *testing.T) {
	config := proxmox.VMConfig{
		"scsi0":     "store01:vm-101-disk-0,size=32G",
		"scsi10":    "store01:vm-101-disk-10",
		"scsihw":    "virtio-scsi-single",
		"scsi":      "not a slot",
		"scsi 1":    "not a slot either",
		"virtiofs0": "shared",
		"virtio0":   "store01:vm-101-disk-2",
	}

	scsi := diskSlotOccupants(config, "scsi")
	if len(scsi) != 2 {
		t.Fatalf("scsi slots = %v, want exactly 0 and 10", scsi)
	}
	if scsi[0] != "store01:vm-101-disk-0" {
		t.Errorf("scsi0 = %q, want the volume without its option list", scsi[0])
	}
	if _, ok := scsi[10]; !ok {
		t.Error("scsi10 was not read as slot 10")
	}

	virtio := diskSlotOccupants(config, "virtio")
	if len(virtio) != 1 {
		t.Fatalf("virtio slots = %v, want only slot 0 (virtiofs0 is not a disk)", virtio)
	}
}

func TestBootReferencedKeys(t *testing.T) {
	tests := []struct {
		name   string
		config proxmox.VMConfig
		want   []string
		absent []string
	}{
		{
			name:   "modern order",
			config: proxmox.VMConfig{"boot": "order=scsi0;ide2;net0"},
			want:   []string{"scsi0", "ide2", "net0"},
			absent: []string{"scsi1"},
		},
		{
			name:   "deprecated bootdisk",
			config: proxmox.VMConfig{"bootdisk": "virtio0"},
			want:   []string{"virtio0"},
		},
		{
			name:   "both spellings at once",
			config: proxmox.VMConfig{"boot": "order=scsi1", "bootdisk": "scsi0"},
			want:   []string{"scsi0", "scsi1"},
		},
		{
			name:   "legacy letter list names no device",
			config: proxmox.VMConfig{"boot": "cdn"},
			absent: []string{"scsi0", "ide0", "c", "n"},
		},
		{
			name:   "no boot keys at all",
			config: proxmox.VMConfig{"scsi0": "store01:vm-101-disk-0"},
			absent: []string{"scsi0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bootReferencedKeys(tt.config)
			for _, key := range tt.want {
				if !got[key] {
					t.Errorf("%q is not reported as a boot device (got %v)", key, got)
				}
			}
			for _, key := range tt.absent {
				if got[key] {
					t.Errorf("%q is reported as a boot device but should not be (got %v)", key, got)
				}
			}
		})
	}
}

func TestPoolTotalBytes(t *testing.T) {
	pools := onePool(1024*gib, 512*gib)

	if total, lookup := poolTotalBytes(pools, testAttachStorage); lookup != poolResolved || total != 1024*gib {
		t.Errorf("resolved pool = (%d, %v), want (%d, poolResolved)", total, lookup, 1024*gib)
	}
	if _, lookup := poolTotalBytes(pools, "store02"); lookup != poolNotListed {
		t.Errorf("missing pool = %v, want poolNotListed", lookup)
	}
	if _, lookup := poolTotalBytes(onePool(0, 0), testAttachStorage); lookup != poolNoCapacity {
		t.Errorf("zero-total pool = %v, want poolNoCapacity", lookup)
	}
	if _, lookup := poolTotalBytes(nil, testAttachStorage); lookup != poolNotListed {
		t.Errorf("empty list = %v, want poolNotListed", lookup)
	}
}

func TestNextFreeDiskIndex(t *testing.T) {
	noBoot := map[string]bool{}

	tests := []struct {
		name     string
		occupied map[int]string
		boot     map[string]bool
		want     int
		wantOK   bool
	}{
		{name: "empty bus", occupied: map[int]string{}, boot: noBoot, want: 0, wantOK: true},
		{name: "occupied prefix", occupied: map[int]string{0: "a", 1: "b"}, boot: noBoot, want: 2, wantOK: true},
		{name: "hole in the middle", occupied: map[int]string{0: "a", 2: "c"}, boot: noBoot, want: 1, wantOK: true},
		{name: "full bus", occupied: map[int]string{0: "a", 1: "b", 2: "c", 3: "d"}, boot: noBoot, wantOK: false},
		{
			name:     "a boot-reserved slot is skipped",
			occupied: map[int]string{},
			boot:     map[string]bool{"scsi0": true},
			want:     1,
			wantOK:   true,
		},
		{
			name:     "a boot entry for another bus is irrelevant",
			occupied: map[int]string{},
			boot:     map[string]bool{"ide2": true, "net0": true},
			want:     0,
			wantOK:   true,
		},
		{
			name:     "occupied and reserved together can fill the bus",
			occupied: map[int]string{0: "a", 1: "b", 2: "c"},
			boot:     map[string]bool{"scsi3": true},
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := nextFreeDiskIndex(tt.occupied, tt.boot, "scsi", 3)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("index = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestDiskAttachRequestFrom covers the seam between the declared schema
// and the planner: the eight lines that turn validated parameters into
// the struct planDiskAttach reads.
//
// It is here rather than in package api because diskAttachRequestFrom is
// unexported, and the schema below is a local mirror of the endpoint's
// declaration for the same reason. The real declaration is pinned by
// TestAttachDiskIndexIsThreeState (internal/api), so between the two the
// whole chain is covered: real declaration -> three-state read, then
// three-state read -> request struct.
func TestDiskAttachRequestFrom(t *testing.T) {
	schema := apischema.Properties{
		"bus":     {Type: apischema.String, Enum: proxmox.DiskBuses},
		"storage": {Type: apischema.String, Format: "storage-id"},
		"size":    {Type: apischema.String, Format: "disk-size"},
		"index":   {Type: apischema.Integer, Optional: true, Minimum: apischema.Ptr(0.0), Maximum: apischema.Ptr(30.0)},
		"format":  {Type: apischema.String, Optional: true},
	}
	if err := schema.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}

	tests := []struct {
		name string
		in   map[string]any
		want diskAttachRequest
	}{
		{
			name: "an omitted index is not slot 0",
			in:   map[string]any{"bus": "scsi", "storage": "store01", "size": "32"},
			want: diskAttachRequest{Bus: "scsi", Storage: "store01", SizeGiB: 32, HasIndex: false},
		},
		{
			name: "an explicit slot 0 is",
			in:   map[string]any{"bus": "scsi", "storage": "store01", "size": "32", "index": 0},
			want: diskAttachRequest{Bus: "scsi", Storage: "store01", SizeGiB: 32, Index: 0, HasIndex: true},
		},
		{
			name: "everything else is carried through",
			in:   map[string]any{"bus": "virtio", "storage": "store02", "size": "500G", "index": 7, "format": "qcow2"},
			want: diskAttachRequest{Bus: "virtio", Storage: "store02", SizeGiB: 500, Index: 7, HasIndex: true, Format: "qcow2"},
		},
		{
			name: "the incident's 512000 survives as a GiB count",
			in:   map[string]any{"bus": "scsi", "storage": "store01", "size": "512000"},
			want: diskAttachRequest{Bus: "scsi", Storage: "store01", SizeGiB: 512000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := schema.Validate(tt.in)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got, err := diskAttachRequestFrom(params)
			if err != nil {
				t.Fatalf("diskAttachRequestFrom: %v", err)
			}
			if got != tt.want {
				t.Errorf("request = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestDiskAttachRequestFromRejectsAnUnnormalizedSize proves the branch
// that exists for a declaration bug rather than a request: if the
// endpoint ever dropped the "disk-size" format, the size would arrive
// unnormalized and this must 500 rather than pass a suffixed value on to
// Proxmox.
func TestDiskAttachRequestFromRejectsAnUnnormalizedSize(t *testing.T) {
	unformatted := apischema.Properties{
		"bus":     {Type: apischema.String},
		"storage": {Type: apischema.String},
		"size":    {Type: apischema.String}, // the missing Format is the point
		"index":   {Type: apischema.Integer, Optional: true},
		"format":  {Type: apischema.String, Optional: true},
	}
	params, err := unformatted.Validate(map[string]any{"bus": "scsi", "storage": "store01", "size": "500G"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	_, err = diskAttachRequestFrom(params)
	if err == nil {
		t.Fatal(`an unnormalized size of "500G" was accepted`)
	}
	if code := statusOf(t, err); code != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — a missing format is our declaration bug, not the caller's request", code)
	}
}

// TestRequireGuestKindPerm is the mutation-proof for the permission
// crossover a security review found: convert-to-template and
// clone-to-template are reached on manage:vm, but they call
// ConvertCTToTemplate / CloneCT for a container — and manage:container is
// a separate permission row a cluster-scoped or custom role can withhold.
//
// It is driven through a real Fiber request rather than called directly,
// because requireClusterPerm resolves the engine out of c.Locals and a
// direct call would prove nothing about what a request actually gets. The
// engine is newGrantEngine (audit_redaction_test.go) because the shared
// admin/viewer stub answers every permission the same way and cannot
// express "holds manage:vm, does not hold manage:container" — which is
// the only interesting state here.
func TestRequireGuestKindPerm(t *testing.T) {
	clusterID := uuid.New()

	tests := []struct {
		name       string
		guestType  string
		grants     []string
		wantStatus int
		wantPass   bool
	}{
		{
			name:       "a VM never needs container rights",
			guestType:  "qemu",
			grants:     []string{"manage:vm"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:      "an empty type is not a container either",
			guestType: "",
			// The collector writes "qemu"/"lxc"; a blank type is a row we
			// cannot classify, and the conservative reading is that it is
			// not an LXC, because the VM half has already been checked.
			grants:     []string{"manage:vm"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "a container with container rights passes",
			guestType:  "lxc",
			grants:     []string{"manage:vm", "manage:container"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:      "a container WITHOUT container rights is refused",
			guestType: "lxc",
			// This is the hole: manage:vm alone got the caller to the
			// handler, and without this check it would irreversibly convert
			// someone else's container.
			grants:     []string{"manage:vm"},
			wantStatus: http.StatusForbidden,
			wantPass:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := func(c fiber.Ctx) error {
				if err := requireGuestKindPerm(c, "manage", tt.guestType, clusterID); err != nil {
					return err
				}
				return c.Next()
			}
			app, reached := probeApp(gate, func(c fiber.Ctx) {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", newGrantEngine(tt.grants...))
			})

			if got := probe(t, app, clusterID.String()); got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
			if *reached != tt.wantPass {
				t.Errorf("handler reached = %v, want %v", *reached, tt.wantPass)
			}
		})
	}
}

// --- Detach auditing ---

type detachProbeStub struct {
	config proxmox.VMConfig
	err    error
}

func (s detachProbeStub) GetVMConfig(context.Context, string, int) (proxmox.VMConfig, error) {
	return s.config, s.err
}

// The audit row for a detach has to distinguish three answers: the volume, "no
// such slot", and "we could not look". Collapsing the last two is what makes an
// audit row lie by omission.
func TestDetachedVolume(t *testing.T) {
	config := proxmox.VMConfig{
		"digest":  "abc123",
		"ide0":    "store01:vm-101-disk-0,size=32G",
		"unused0": "store01:vm-101-disk-2",
		"ide2":    "none,media=cdrom",
	}

	tests := []struct {
		name       string
		probe      detachProbe
		disk       string
		want       string
		wantDigest string
	}{
		{"a live slot names its volume", detachProbeStub{config: config}, "ide0", "store01:vm-101-disk-0", "abc123"},
		{"an unused slot names its volume", detachProbeStub{config: config}, "unused0", "store01:vm-101-disk-2", "abc123"},
		{"an empty cdrom still resolves", detachProbeStub{config: config}, "ide2", "none", "abc123"},
		{"a missing key says so, and still pins", detachProbeStub{config: config}, "scsi5", notInConfig, "abc123"},
		{"an unreadable config says so, and differently", detachProbeStub{err: errors.New("boom")}, "ide0", configUnreadable, ""},
		{"a digestless config pins nothing", detachProbeStub{config: proxmox.VMConfig{"ide0": "store01:vm-101-disk-0"}}, "ide0", "store01:vm-101-disk-0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, digest := detachedVolume(context.Background(), tt.probe, "pve-01", 101, tt.disk)
			if got != tt.want {
				t.Errorf("volume = %q, want %q", got, tt.want)
			}
			if digest != tt.wantDigest {
				t.Errorf("digest = %q, want %q", digest, tt.wantDigest)
			}
		})
	}
}

// removes_volume is the difference between a reversible change and an
// irreversible one, so it has three answers too. Asserting a definite false
// when the answer is unknown reads as reassurance, which is the failure this
// whole change exists to avoid.
func TestDetachRemovesVolume(t *testing.T) {
	tests := []struct {
		name     string
		disk     string
		resolved string
		want     *bool
	}{
		{"an unusedN key destroys, whatever the volume", "unused0", "store01:vm-101-disk-2", boolPtr(true)},
		{"an unusedN key destroys even unresolved", "unused11", configUnreadable, boolPtr(true)},
		{"a live slot parks its volume", "ide0", "store01:vm-101-disk-0", boolPtr(false)},
		{"a cloud-init drive is freed, not parked", "ide2", "store01:vm-101-cloudinit", boolPtr(true)},
		{"a cloud-init drive with a format suffix too", "ide2", "store01:vm-101-cloudinit.qcow2", boolPtr(true)},
		{"a path-style cloud-init volume", "ide2", "/mnt/pve/store01/vm-101-cloudinit", boolPtr(true)},
		{"a plain volume that merely mentions cloudinit is not one", "scsi1", "store01:vm-101-cloudinit-backup", boolPtr(false)},
		{"a key that was not set destroyed nothing", "scsi5", notInConfig, boolPtr(false)},
		{"an unreadable config cannot answer", "ide0", configUnreadable, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detachRemovesVolume(tt.disk, tt.resolved)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("got %v, want nil (unknown)", *got)
			case tt.want != nil && got == nil:
				t.Errorf("got nil (unknown), want %v", *tt.want)
			case tt.want != nil && got != nil && *got != *tt.want:
				t.Errorf("got %v, want %v", *got, *tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
