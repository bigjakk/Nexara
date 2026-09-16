package handlers

import (
	"reflect"
	"strings"
	"testing"

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

func TestValidateSnapshotName(t *testing.T) {
	valid := []string{"ab", "before-upgrade", "Snap_2026-07-30", "a1", strings.Repeat("a", 40)}
	for _, name := range valid {
		if err := validateSnapshotName(name); err != nil {
			t.Errorf("validateSnapshotName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []struct {
		name string
		why  string
	}{
		{"", "empty"},
		{"a", "too short"},
		{"my snap", "contains space"},
		{" ab", "leading space"},
		{"1abc", "starts with digit"},
		{"-abc", "starts with dash"},
		{"_abc", "starts with underscore"},
		{"ab.c", "invalid character"},
		{"ab/c", "path separator"},
		{"current", "reserved by Proxmox"},
		{strings.Repeat("a", 41), "too long"},
	}
	for _, tt := range invalid {
		if err := validateSnapshotName(tt.name); err == nil {
			t.Errorf("validateSnapshotName(%q) = nil, want error (%s)", tt.name, tt.why)
		}
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

func TestValidVMActions(t *testing.T) {
	valid := []string{"start", "stop", "shutdown", "reboot", "reset", "suspend", "resume"}
	for _, action := range valid {
		if !validVMActions[action] {
			t.Errorf("expected %q to be valid", action)
		}
	}

	invalid := []string{"delete", "migrate", "snapshot", ""}
	for _, action := range invalid {
		if validVMActions[action] {
			t.Errorf("expected %q to be invalid", action)
		}
	}
}
