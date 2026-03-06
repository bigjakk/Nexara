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
