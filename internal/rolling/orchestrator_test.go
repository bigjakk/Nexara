package rolling

import (
	"testing"
)

func TestTaskSucceeded(t *testing.T) {
	tests := []struct {
		name       string
		exitStatus string
		want       bool
	}{
		{"empty string", "", true},
		{"OK", "OK", true},
		{"ok lowercase", "ok", true},
		{"OK with warnings", "OK (with warnings)", true},
		{"WARNINGS", "WARNINGS", true},
		{"error", "ERROR", false},
		{"failed", "FAILED", false},
		{"random text", "some error message", false},
		{"whitespace OK", "  OK  ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := taskSucceeded(tt.exitStatus)
			if got != tt.want {
				t.Errorf("taskSucceeded(%q) = %v, want %v", tt.exitStatus, got, tt.want)
			}
		})
	}
}

func TestGuestSnapshotTypes(t *testing.T) {
	// Verify struct fields are correctly defined.
	g := GuestSnapshot{
		VMID:   100,
		Name:   "test-vm",
		Type:   "qemu",
		Status: "running",
	}
	if g.VMID != 100 {
		t.Errorf("VMID = %d, want 100", g.VMID)
	}
	if g.Type != "qemu" {
		t.Errorf("Type = %s, want qemu", g.Type)
	}
}
