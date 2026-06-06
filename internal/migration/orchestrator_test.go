package migration

import (
	"testing"
)

func TestStatusConstants(t *testing.T) {
	// Verify status constants are distinct.
	statuses := []string{
		StatusPending, StatusChecking, StatusMigrating,
		StatusCompleted, StatusFailed, StatusCancelled,
	}
	seen := make(map[string]bool)
	for _, s := range statuses {
		if s == "" {
			t.Error("status constant should not be empty")
		}
		if seen[s] {
			t.Errorf("duplicate status constant: %s", s)
		}
		seen[s] = true
	}
}

func TestMigrationTypeConstants(t *testing.T) {
	if TypeIntraCluster == TypeCrossCluster {
		t.Error("migration type constants should be distinct")
	}
	if TypeIntraCluster != "intra-cluster" {
		t.Errorf("TypeIntraCluster = %q, want %q", TypeIntraCluster, "intra-cluster")
	}
	if TypeCrossCluster != "cross-cluster" {
		t.Errorf("TypeCrossCluster = %q, want %q", TypeCrossCluster, "cross-cluster")
	}
}

func TestVMTypeConstants(t *testing.T) {
	if VMTypeQEMU != "qemu" {
		t.Errorf("VMTypeQEMU = %q, want %q", VMTypeQEMU, "qemu")
	}
	if VMTypeLXC != "lxc" {
		t.Errorf("VMTypeLXC = %q, want %q", VMTypeLXC, "lxc")
	}
}

func TestVolumeStorage(t *testing.T) {
	cases := map[string]string{
		"local-lvm:vm-100-disk-0,size=32G": "local-lvm",
		"ceph:vm-101-disk-0":               "ceph",
		"test:subvol-102-disk-0,size=8G":   "test",
		"":                                 "",
		"none,media=cdrom":                 "",
	}
	for in, want := range cases {
		if got := volumeStorage(in); got != want {
			t.Errorf("volumeStorage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDiskTargets(t *testing.T) {
	disks := []movableDisk{
		{Key: "scsi0", Storage: "local-lvm"},
		{Key: "scsi1", Storage: "ceph"},
	}

	t.Run("single target moves every disk, skipping no-ops", func(t *testing.T) {
		// Fallback target = ceph: scsi0 moves (local-lvm->ceph), scsi1 is already
		// on ceph so it's skipped.
		moves := resolveDiskTargets(disks, map[string]string{}, "ceph")
		if len(moves) != 1 || moves[0].Disk != "scsi0" || moves[0].Target != "ceph" {
			t.Fatalf("got %+v, want only scsi0 -> ceph", moves)
		}
	})

	t.Run("per-disk map keeps unmapped disks in place", func(t *testing.T) {
		// Only scsi0 is mapped; scsi1 has no entry -> stays put.
		moves := resolveDiskTargets(disks, map[string]string{"scsi0": "proxmox-ssd"}, "")
		if len(moves) != 1 || moves[0].Disk != "scsi0" || moves[0].Target != "proxmox-ssd" {
			t.Fatalf("got %+v, want only scsi0 -> proxmox-ssd", moves)
		}
	})

	t.Run("per-disk map sends each disk to its own target", func(t *testing.T) {
		moves := resolveDiskTargets(disks, map[string]string{"scsi0": "fast", "scsi1": "bulk"}, "")
		got := map[string]string{}
		for _, m := range moves {
			got[m.Disk] = m.Target
		}
		if len(got) != 2 || got["scsi0"] != "fast" || got["scsi1"] != "bulk" {
			t.Fatalf("got %+v, want scsi0->fast, scsi1->bulk", got)
		}
	})

	t.Run("per-disk map skips a disk mapped to its current storage", func(t *testing.T) {
		// scsi1 mapped to ceph where it already lives -> no-op skipped.
		moves := resolveDiskTargets(disks, map[string]string{"scsi0": "fast", "scsi1": "ceph"}, "")
		if len(moves) != 1 || moves[0].Disk != "scsi0" {
			t.Fatalf("got %+v, want only scsi0 (scsi1 already on ceph)", moves)
		}
	})

	t.Run("nothing to move yields empty", func(t *testing.T) {
		moves := resolveDiskTargets(disks, map[string]string{}, "")
		if len(moves) != 0 {
			t.Fatalf("got %+v, want no moves (empty fallback, empty map)", moves)
		}
	})
}
