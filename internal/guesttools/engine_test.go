package guesttools

import (
	"errors"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestSnapshotNameIsAcceptedByTheClient pins the PREFIX BUDGET.
//
// snapshotName pastes an 18-character prefix in front of an upstream ISO
// version with its dots turned into dashes, and the result goes straight to
// proxmox.CreateVMSnapshot. Proxmox caps a snapshot name at 40 characters
// (pve-snapshot-name's maxLength), so the prefix leaves 22 for the version.
//
// A real version — "0.1.302" becoming "0-1-302" — spends 7 of those, so the
// budget is not close and never has been. What this test holds is that the two
// halves stay in a ratio where that remains true: lengthening the prefix, or
// widening what counts as a version, would break the update path on the
// snapshot step with a raw PVE rejection and nothing upstream of it to explain
// why.
//
// The refusal being asserted is the CLIENT's, not this package's. guesttools
// does no validation of its own and should not start: the rule lives on
// CreateVMSnapshot (internal/proxmox/client_guests.go) precisely so a caller
// like this one cannot forget it.
func TestSnapshotNameIsAcceptedByTheClient(t *testing.T) {
	t.Parallel()

	// Upstream virtio-win ISO versions, as internal/virtiowin parses them out
	// of the download index.
	realVersions := []string{"0.1.96", "0.1.126", "0.1.240", "0.1.262", "0.1.285", "0.1.302"}
	for _, v := range realVersions {
		name := snapshotName(v)
		if err := proxmox.ValidateSnapshotName(proxmox.QemuSnapshot, name); err != nil {
			t.Errorf("snapshotName(%q) = %q (%d chars), which the client refuses: %v",
				v, name, len(name), err)
		}
	}

	// The budget itself, asserted directly rather than inferred from "a real
	// version still fits". Inference would not hold the line: lengthening the
	// prefix by eight characters leaves "0.1.302" comfortably inside the cap
	// and every case above still green, while quietly cutting the room a
	// longer version has by more than a third.
	const wantPrefix = "nexara-guesttools-"
	const wantBudget = proxmox.SnapshotMaxNameLen - len(wantPrefix)
	if prefix := snapshotName(""); prefix != wantPrefix {
		t.Fatalf("snapshotName mints the prefix %q, want %q — that leaves %d characters for the "+
			"version rather than %d, and the version is the half nothing bounds",
			prefix, wantPrefix, proxmox.SnapshotMaxNameLen-len(prefix), wantBudget)
	}
	if err := proxmox.ValidateSnapshotName(proxmox.QemuSnapshot, snapshotName(strings.Repeat("a", wantBudget))); err != nil {
		t.Errorf("a %d-character version fills the budget exactly and must be accepted: %v",
			wantBudget, err)
	}
	if err := proxmox.ValidateSnapshotName(proxmox.QemuSnapshot, snapshotName(strings.Repeat("a", wantBudget+1))); err == nil {
		t.Errorf("a %d-character version is one past the budget and must be refused", wantBudget+1)
	}

	// versionPattern (internal/virtiowin/version.go) is
	// `^\d+(?:\.\d+)*(?:-\d+)?$` — the `(?:\.\d+)*` is UNBOUNDED, the
	// iso_version column is TEXT with no cap, and the download root an
	// operator points the catalog at is configurable. Nothing between that
	// input and here shortens the string, so the only thing standing between a
	// long version and a Proxmox rejection is the client's check.
	long := strings.Repeat("1.", 12) + "1"
	name := snapshotName(long)
	if len(name) <= proxmox.SnapshotMaxNameLen {
		t.Fatalf("snapshotName(%q) = %q is only %d chars; this case has to exceed the %d-character "+
			"cap or it proves nothing", long, name, len(name), proxmox.SnapshotMaxNameLen)
	}
	err := proxmox.ValidateSnapshotName(proxmox.QemuSnapshot, name)
	if err == nil {
		t.Errorf("snapshotName(%q) = %q (%d chars) was accepted; Proxmox caps the name at %d",
			long, name, len(name), proxmox.SnapshotMaxNameLen)
	}
	if !errors.Is(err, proxmox.ErrInvalidInput) {
		t.Errorf("err = %v, want it to wrap proxmox.ErrInvalidInput so the API layer reports the "+
			"refusal as a bad value rather than as Proxmox being unreachable", err)
	}
}
