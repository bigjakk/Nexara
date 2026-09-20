package handlers

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestExtractNodeFromUPID(t *testing.T) {
	tests := []struct {
		upid string
		want string
	}{
		{"UPID:pve1:000012:00AB:65000000:qmstart:100:user@pam:", "pve1"},
		{"UPID:node-02:000012:00AB:65000000:qmstop:101:admin@pve:", "node-02"},
		{"UPID:my-node:00FF:0A0B:65123456:qmshutdown:200:user@pam:", "my-node"},
		{"invalid", ""},
		{"", ""},
		{"UPID:", ""},
	}

	for _, tt := range tests {
		got := extractNodeFromUPID(tt.upid)
		if got != tt.want {
			t.Errorf("extractNodeFromUPID(%q) = %q, want %q", tt.upid, got, tt.want)
		}
	}
}

func TestSplitUPID(t *testing.T) {
	parts := splitUPID("UPID:pve1:000012:00AB:65000000:qmstart:100:user@pam:")
	if len(parts) != 8 {
		t.Fatalf("expected 8 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "UPID" {
		t.Errorf("parts[0] = %q, want UPID", parts[0])
	}
	if parts[1] != "pve1" {
		t.Errorf("parts[1] = %q, want pve1", parts[1])
	}
}

// TestSnapshotNameErrorAttributesTheFailure pins WHO each refusal blames.
//
// A bad name is the caller's mistake and earns a 400 that names it. An
// unregistered guest kind is Nexara's mistake, and billing it to the
// caller as a 400 would tell them their snapshot name was wrong when no
// name of theirs could have worked — and would echo an internal enum value
// back at them while doing it.
func TestSnapshotNameErrorAttributesTheFailure(t *testing.T) {
	tests := []struct {
		name       string
		kind       snapshotGuestKind
		snap       string
		wantStatus int
		why        string
	}{
		{"a valid name passes", qemuSnapshot, "snap01", 0, ""},
		{"a reserved name is the caller's mistake", qemuSnapshot, "current", fiber.StatusBadRequest,
			"the caller can fix this by choosing another name"},
		{"an over-long name is the caller's mistake", lxcSnapshot, strings.Repeat("a", SnapshotMaxNameLen+1),
			fiber.StatusBadRequest, "the caller can fix this by choosing a shorter name"},
		{"an unregistered kind is ours", "not-a-guest-kind", "snap01", fiber.StatusInternalServerError,
			"no request the caller could send would make this succeed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := snapshotNameError(tt.kind, tt.snap)
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("snapshotNameError(%s, %q) = %v, want nil", tt.kind, tt.snap, err)
				}
				return
			}
			var fe *fiber.Error
			if !errors.As(err, &fe) {
				t.Fatalf("snapshotNameError(%s, %q) = %v, want a *fiber.Error", tt.kind, tt.snap, err)
			}
			if fe.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d — %s", fe.Code, tt.wantStatus, tt.why)
			}
			// The internal enum value must not travel to the caller.
			if tt.wantStatus == fiber.StatusInternalServerError && strings.Contains(fe.Message, string(tt.kind)) {
				t.Errorf("message = %q, want it not to echo the internal guest kind %q", fe.Message, tt.kind)
			}
		})
	}
}

// captureSlog redirects the default logger for the duration of a test and
// returns an accessor for what was written to it. It mirrors the helper of
// the same name in internal/api.
//
// A test using it must stay SEQUENTIAL. bytes.Buffer is not safe for
// concurrent use and nothing here guards it, so a t.Parallel() test
// logging through the default logger while this one holds it is a data
// race — and a second concurrent captureSlog would swap the default out
// from under this one's assertion, atomically and just as fatally.
// Today that cannot happen for three separate reasons: the caller below
// does not call t.Parallel(), Go resumes top-level parallel tests only
// once the sequential ones have finished, and none of this package's
// t.Parallel() tests log through slog at all. Adding t.Parallel() to the
// caller removes all three at once.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return buf.String
}

// TestSnapshotNameErrorLogsTheUnknownKind is the other half of keeping the
// guest kind out of the response.
//
// Withholding it from the caller is only correct if it goes somewhere.
// errorHandler (internal/api/errors.go) renders the envelope without
// logging, so if snapshotNameError dropped the cause the operator would
// get a bare 500 whose reason exists nowhere at all — which is worse than
// the 400 this replaced, not better. That is the moved-swallow shape:
// attribution moves up a layer, the payload gets dropped, and a comment
// says it is fixed.
func TestSnapshotNameErrorLogsTheUnknownKind(t *testing.T) {
	logged := captureSlog(t)

	if err := snapshotNameError("not-a-guest-kind", "snap01"); err == nil {
		t.Fatal("snapshotNameError returned nil for an unknown guest kind")
	}

	out := logged()
	for _, want := range []string{"not-a-guest-kind", "reserved set"} {
		if !strings.Contains(out, want) {
			t.Errorf("server log = %q, want it to record %q — the caller is deliberately not "+
				"told which kind, so the log is the only place it exists", out, want)
		}
	}

	// A refusal the caller CAN act on must not be logged as a server fault.
	quiet := captureSlog(t)
	if err := snapshotNameError(qemuSnapshot, "current"); err == nil {
		t.Fatal(`snapshotNameError returned nil for the reserved name "current"`)
	}
	if out := quiet(); strings.Contains(out, "reserved set") {
		t.Errorf("server log = %q; a caller's bad name is a 400 and must not be logged as "+
			"Nexara's own misconfiguration", out)
	}
}

func TestSnapshotBlockingVolumes(t *testing.T) {
	storageTypes := map[string]string{
		"nas":       "nfs",
		"store01":   "nfs",
		"local":     "dir",
		"test":      "rbd",
		"local-lvm": "lvmthin",
	}

	tests := []struct {
		name   string
		config proxmox.VMConfig
		want   []string
	}{
		{
			name: "win11: qcow2 disk fine, raw tpm state on nfs flagged",
			config: proxmox.VMConfig{
				"scsi0":     "store01:102/vm-102-disk-0.qcow2,discard=on,size=125G",
				"tpmstate0": "store01:102/vm-102-disk-1.raw,size=4M,version=v2.0",
				"ide2":      "none,media=cdrom",
				"bios":      "ovmf",
			},
			want: []string{"tpmstate0 on store01"},
		},
		{
			name: "raw disk on nfs flagged",
			config: proxmox.VMConfig{
				"scsi0": "nas:121/vm-121-disk-0.raw,discard=on,size=81G",
			},
			want: []string{"scsi0 on nas"},
		},
		{
			name: "rbd and lvmthin volumes are fine",
			config: proxmox.VMConfig{
				"scsi0":   "test:vm-101-disk-0,size=32G",
				"virtio1": "local-lvm:vm-101-disk-1,size=8G",
			},
			want: []string{},
		},
		{
			name: "cdrom iso on file storage is ignored",
			config: proxmox.VMConfig{
				"ide2":  "nas:iso/WinXPSP3.iso,media=cdrom,size=637568K",
				"scsi0": "test:vm-101-disk-0,size=32G",
			},
			want: []string{},
		},
		{
			name: "passthrough device flagged",
			config: proxmox.VMConfig{
				"scsi1": "/dev/disk/by-id/ata-Foo123,size=500G",
			},
			want: []string{"scsi1 (passthrough device)"},
		},
		{
			name: "container rootfs and mount point raw on nfs flagged",
			config: proxmox.VMConfig{
				"rootfs": "store01:105/vm-105-disk-0.raw,size=8G",
				"mp0":    "test:vm-105-disk-1,mp=/data,size=10G",
			},
			want: []string{"rootfs on store01"},
		},
		{
			name: "unknown storage is not accused",
			config: proxmox.VMConfig{
				"scsi0": "mystery:vm-1-disk-0.raw,size=1G",
			},
			want: []string{},
		},
		{
			name: "non-string and non-volume keys are ignored",
			config: proxmox.VMConfig{
				"cores":  float64(4),
				"name":   "linux01",
				"scsihw": "virtio-scsi-pci",
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := snapshotBlockingVolumes(tt.config, storageTypes)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("snapshotBlockingVolumes() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestVMStatusActions pins the list POST .../vms/:vm_id/status accepts.
// It is now the endpoint's declared enum as well as the set PerformAction
// switches on, so an entry added here without a matching switch case
// would dispatch nothing and track a task with no UPID.
func TestVMStatusActions(t *testing.T) {
	valid := map[string]bool{}
	for _, action := range VMStatusActions {
		valid[action] = true
	}

	for _, action := range []string{"start", "stop", "shutdown", "reboot", "reset", "suspend", "resume"} {
		if !valid[action] {
			t.Errorf("expected %q to be a valid status action", action)
		}
	}
	for _, action := range []string{"delete", "migrate", "snapshot", ""} {
		if valid[action] {
			t.Errorf("expected %q not to be a valid status action", action)
		}
	}
	if len(VMStatusActions) != 7 {
		t.Errorf("VMStatusActions has %d entries, want 7 — add the switch case in PerformAction too", len(VMStatusActions))
	}
}
