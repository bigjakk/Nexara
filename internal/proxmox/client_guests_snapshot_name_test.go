package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- The rule on its own ---
//
// These four tests moved here from internal/api/handlers/vms_test.go with the
// rule itself. They are the rule's unit tests; the wire tests further down are
// what prove the two Create*Snapshot methods actually consult it.

func TestValidateSnapshotName(t *testing.T) {
	// The shape rules are the same for both guest kinds, so they are
	// asserted against both rather than against whichever one was handy.
	kinds := []SnapshotGuestKind{QemuSnapshot, LXCSnapshot}

	valid := []string{
		"ab", "before-upgrade", "Snap_2026-07-30", "a1",
		strings.Repeat("a", SnapshotMaxNameLen),
	}
	for _, kind := range kinds {
		for _, name := range valid {
			if err := ValidateSnapshotName(kind, name); err != nil {
				t.Errorf("ValidateSnapshotName(%s, %q) = %v, want nil", kind, name, err)
			}
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
		{"current", "reserved by Proxmox for both guest kinds"},
		{strings.Repeat("a", SnapshotMaxNameLen+1), "one over Proxmox's own maxLength"},
	}
	for _, kind := range kinds {
		for _, tt := range invalid {
			err := ValidateSnapshotName(kind, tt.name)
			if err == nil {
				t.Errorf("ValidateSnapshotName(%s, %q) = nil, want error (%s)", kind, tt.name, tt.why)
				continue
			}
			// The sentinel is what makes this a 400 at the API layer
			// (mapProxmoxError). Unwrapped it falls through to a 500 reading
			// "Proxmox operation failed" — a caller's bad name reported as an
			// outage.
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("ValidateSnapshotName(%s, %q) = %v, want it to wrap ErrInvalidInput (%s)",
					kind, tt.name, err, tt.why)
			}
		}
	}
}

// TestValidateSnapshotNameReservedPerKind pins the ASYMMETRY between the
// two guest kinds, which a table shared across both cannot express.
//
// Proxmox reserves "pending" for VMs (case-insensitively, because a VM
// config's `[PENDING]` section header is matched with /i) and "vzdump" for
// containers, and neither reserves the other's. Collapsing the two sets
// into one union would be the easy mistake: it would refuse a container
// snapshot named "pending" that Proxmox accepts, and nothing else in this
// suite would notice.
func TestValidateSnapshotNameReservedPerKind(t *testing.T) {
	tests := []struct {
		kind       SnapshotGuestKind
		name       string
		wantRefuse bool
		why        string
	}{
		{QemuSnapshot, "current", true, "reserved for both kinds"},
		{LXCSnapshot, "current", true, "reserved for both kinds"},

		{QemuSnapshot, "pending", true, "collides with the VM config's [PENDING] section"},
		{QemuSnapshot, "PENDING", true, "PVE compares with lc(), so the check is case-insensitive"},
		{QemuSnapshot, "Pending", true, "PVE compares with lc(), so the check is case-insensitive"},
		{LXCSnapshot, "pending", false, "an LXC config spells it [pve:pending]; PVE accepts this name"},
		{LXCSnapshot, "PENDING", false, "an LXC config spells it [pve:pending]; PVE accepts this name"},

		{LXCSnapshot, "vzdump", true, "vzdump names a container's backup snapshot this"},
		{QemuSnapshot, "vzdump", false, "PVE's VM snapshot endpoint accepts it"},

		// "current" is compared with eq upstream, not lc(), for both kinds.
		// Refusing "Current" would refuse a name Proxmox takes.
		{QemuSnapshot, "Current", false, "PVE compares 'current' with eq, not lc()"},
		{LXCSnapshot, "Current", false, "PVE compares 'current' with eq, not lc()"},
		// Same for "vzdump": pve-container compares it with eq as well.
		{LXCSnapshot, "VZDump", false, "PVE compares 'vzdump' with eq, not lc()"},
	}

	for _, tt := range tests {
		err := ValidateSnapshotName(tt.kind, tt.name)
		if tt.wantRefuse && err == nil {
			t.Errorf("ValidateSnapshotName(%s, %q) = nil, want a refusal (%s)", tt.kind, tt.name, tt.why)
		}
		if !tt.wantRefuse && err != nil {
			t.Errorf("ValidateSnapshotName(%s, %q) = %v, want nil (%s)", tt.kind, tt.name, err, tt.why)
		}
	}
}

// TestValidateSnapshotNameUnknownKindRefuses covers the third outcome the
// reserved-name lookup can produce: not "reserved" and not "free", but
// "this kind has no reserved set recorded".
//
// Without it, adding a third guest kind would silently take whichever set
// the fallback happened to be — the failure mode is a reserved name
// getting through, and nothing observable would change until Proxmox
// refused the request.
//
// Unreachable through the two Create*Snapshot methods, which each pass a
// literal — which is the point of siting the rule at the client. The
// function is still exported, so the state still has to answer for itself.
func TestValidateSnapshotNameUnknownKindRefuses(t *testing.T) {
	if _, known := ReservedSnapshotName("", "snap01"); known {
		t.Error(`ReservedSnapshotName("", …) reported a known kind; the zero value must not resolve to a reserved set`)
	}
	if err := ValidateSnapshotName("", "snap01"); err == nil {
		t.Error(`ValidateSnapshotName("", "snap01") = nil; an unknown guest kind must refuse rather than guess a reserved set`)
	}
	// A name that is fine under every known kind must still be refused,
	// which is what distinguishes "refuses because unknown" from "refuses
	// because the name is bad".
	err := ValidateSnapshotName("not-a-guest-kind", "snap01")
	if err == nil {
		t.Fatal(`ValidateSnapshotName("not-a-guest-kind", "snap01") = nil, want a refusal naming the unknown kind`)
	}
	// And it must NOT be ErrInvalidInput: that sentinel means "the caller
	// sent something bad" and maps to a 400, while an unregistered kind is
	// Nexara's own bug that no request could fix.
	if !errors.Is(err, ErrUnknownSnapshotGuestKind) {
		t.Errorf("err = %v, want it to wrap ErrUnknownSnapshotGuestKind", err)
	}
	if errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want it NOT to wrap ErrInvalidInput — that would bill our own "+
			"misconfiguration to the caller as a 400", err)
	}
}

// TestSnapshotNameREMatchesTheDeclaredCap pins that the shape regex
// enforces exactly SnapshotMaxNameLen, so the constant cannot be raised
// while the regex quietly keeps the old bound. The regex is built from the
// constant, and this is what proves that build is right rather than
// merely present.
func TestSnapshotNameREMatchesTheDeclaredCap(t *testing.T) {
	atCap := strings.Repeat("a", SnapshotMaxNameLen)
	if !snapshotNameRE.MatchString(atCap) {
		t.Errorf("snapshotNameRE rejects a %d-character name; the cap is %d, so this one is legal",
			len(atCap), SnapshotMaxNameLen)
	}
	overCap := strings.Repeat("a", SnapshotMaxNameLen+1)
	if snapshotNameRE.MatchString(overCap) {
		t.Errorf("snapshotNameRE accepts a %d-character name; Proxmox's pve-snapshot-name maxLength is %d",
			len(overCap), SnapshotMaxNameLen)
	}
}

// --- The rule at the choke point ---

// snapshotWireRequest is one request the stub PVE saw: where it went and what
// snapshot name it carried.
type snapshotWireRequest struct {
	target   string
	snapname string
}

// newSnapshotWireServer records r.RequestURI and the posted form.
//
// Both halves matter. The name goes into the POST BODY, not the path, so this
// is not a traversal guard and nothing here pretends it is — the assertion is
// that a refused name never left the process at all, which is what turns a
// per-fire Proxmox rejection into an error the caller gets synchronously.
func newSnapshotWireServer(t *testing.T) (*httptest.Server, *[]snapshotWireRequest) {
	t.Helper()
	var seen []snapshotWireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, snapshotWireRequest{
			target:   r.Method + " " + r.RequestURI,
			snapname: r.PostForm.Get("snapname"),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"UPID:pve-01:0000A1B2:00C3D4E5:65000000:qmsnapshot:100:nexara@pve!api:"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// snapshotCreateMethod is one of the two client methods that mint a snapshot
// name. collection is the path prefix the request must land under, so an
// accepted name can be checked against the slot it was meant for.
type snapshotCreateMethod struct {
	collection string
	call       func(*Client, string) (string, error)
}

// snapshotCreateMethods is every *Client method that puts a caller-supplied
// snapshot name into a `snapname` form field.
//
// The delete and rollback methods are deliberately absent. They ADDRESS a
// snapshot Proxmox already minted, and a create-side validator applied there
// would strand any snapshot whose name this rule would not have produced —
// the invented-strictness shape. TestGuard_SnapshotNameValidatedAtEveryCreate
// keeps that boundary honest by scoping itself to the form field rather than
// to the path.
var snapshotCreateMethods = map[string]snapshotCreateMethod{
	"CreateVMSnapshot": {
		collection: "/api2/json/nodes/pve-01/qemu/100/snapshot",
		call: func(c *Client, name string) (string, error) {
			return c.CreateVMSnapshot(context.Background(), "pve-01", 100, SnapshotParams{SnapName: name})
		},
	},
	"CreateCTSnapshot": {
		collection: "/api2/json/nodes/pve-01/lxc/101/snapshot",
		call: func(c *Client, name string) (string, error) {
			return c.CreateCTSnapshot(context.Background(), "pve-01", 101, SnapshotParams{SnapName: name})
		},
	},
}

// TestCreateSnapshotMethods_RefuseBadNamesBeforeTheWire is the reason the rule
// moved down here.
//
// Two callers built a snapshot name and never passed it through the validator:
// internal/scheduler decodes snap_name out of a scheduled_tasks.params jsonb
// blob that no schema describes, and internal/guesttools derives one from an
// upstream ISO version. The scheduler path was live — a manage:schedule holder
// could store "current" or 200 characters, and the task then failed on EVERY
// fire with a raw PVE error in last_error, so a recurring snapshot the operator
// believed was protecting a guest silently never ran.
//
// A validator in each caller would have been the opt-in shape: the third caller
// skips it. This asserts the refusal happens at the method, which is the one
// place all of them go through.
func TestCreateSnapshotMethods_RefuseBadNamesBeforeTheWire(t *testing.T) {
	payloads := []struct {
		name string
		snap string
		why  string
	}{
		{"empty", "", "the caller supplied nothing; PVE requires snapname"},
		{"one over the cap", strings.Repeat("a", SnapshotMaxNameLen+1), "pve-snapshot-name is maxLength 40"},
		{"reserved for both kinds", "current", `PVE dies on $snapname eq 'current'`},
		{"space", "my snap", "not a legal pve-configid"},
		{"dot", "snap.1", "not a legal pve-configid"},
		{"leading digit", "1snap", "a configid must start with a letter"},
		{"single character", "a", "$CONFIGID_RE needs at least two characters"},
	}

	for method, m := range snapshotCreateMethods {
		t.Run(method, func(t *testing.T) {
			for _, p := range payloads {
				t.Run(p.name, func(t *testing.T) {
					srv, seen := newSnapshotWireServer(t)
					c := newTestClient(t, srv.URL)

					upid, err := m.call(c, p.snap)
					if !errors.Is(err, ErrInvalidInput) {
						t.Errorf("%s(%q) err = %v, want ErrInvalidInput (%s)", method, p.snap, err, p.why)
					}
					if upid != "" {
						t.Errorf("%s(%q) returned upid %q; a refused name must produce no task", method, p.snap, upid)
					}
					if len(*seen) != 0 {
						t.Errorf("%s(%q) reached the wire as %v; the whole point is that the caller "+
							"is told now rather than on every fire, so the request must not be sent",
							method, p.snap, *seen)
					}
				})
			}
		})
	}
}

// TestCreateSnapshotMethods_AcceptTheOtherKindsReservedName is the half that
// stops the guard above from being satisfied by a naive union of the two
// reserved sets.
//
// Proxmox's reserved names are asymmetric and its comparisons are not uniformly
// case-folded: "pending" is refused for a VM case-INsensitively, "vzdump" is
// refused for a container with `eq`, and "current" is refused for both with
// `eq`. Refusing the other kind's name, or folding the case of one that
// upstream compares exactly, would refuse a snapshot PVE would have taken.
func TestCreateSnapshotMethods_AcceptTheOtherKindsReservedName(t *testing.T) {
	accepted := map[string][]struct {
		name string
		snap string
		why  string
	}{
		"CreateVMSnapshot": {
			{"the container's reserved name", "vzdump", "only pve-container reserves vzdump"},
			{"Current", "Current", "PVE compares 'current' with eq, not lc()"},
			{"VZDump", "VZDump", "not reserved for a VM at all"},
			{"an ordinary name", "before-upgrade", "a plain pve-configid"},
			{"at the cap", strings.Repeat("a", SnapshotMaxNameLen), "40 is legal; 41 is not"},
			{"the scheduler's auto name", "auto-20260920-020000", "what executeSnapshot mints when snap_name is empty"},
			{"the guest-tools name", "nexara-guesttools-0-1-302", "what guesttools.snapshotName mints"},
		},
		"CreateCTSnapshot": {
			{"the VM's reserved name", "pending", "an LXC config spells it [pve:pending]; PVE accepts this"},
			{"PENDING", "PENDING", "not reserved for a container in any casing"},
			{"Current", "Current", "PVE compares 'current' with eq, not lc()"},
			{"VZDump", "VZDump", "PVE compares 'vzdump' with eq, not lc()"},
			{"an ordinary name", "before-upgrade", "a plain pve-configid"},
			{"at the cap", strings.Repeat("a", SnapshotMaxNameLen), "40 is legal; 41 is not"},
			{"the scheduler's auto name", "auto-20260920-020000", "what executeSnapshot mints when snap_name is empty"},
		},
	}

	for method, cases := range accepted {
		m, ok := snapshotCreateMethods[method]
		if !ok {
			t.Fatalf("%q is not one of the create methods %v; a typo here would otherwise run "+
				"every case against a nil call and panic rather than report anything",
				method, snapshotCreateMethods)
		}
		t.Run(method, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					srv, seen := newSnapshotWireServer(t)
					c := newTestClient(t, srv.URL)

					if _, err := m.call(c, tc.snap); err != nil {
						t.Fatalf("%s(%q) = %v, want nil; PVE accepts this name (%s)", method, tc.snap, err, tc.why)
					}
					if len(*seen) != 1 {
						t.Fatalf("%s(%q) issued %d requests %v, want exactly 1", method, tc.snap, len(*seen), *seen)
					}
					got := (*seen)[0]
					if !strings.HasSuffix(got.target, m.collection) {
						t.Errorf("%s(%q) went to %q, want it under %q", method, tc.snap, got.target, m.collection)
					}
					// Round-trip the body rather than re-deriving it: the name
					// travels as a form field, so this is where a mangling
					// would show up.
					if got.snapname != tc.snap {
						t.Errorf("%s posted snapname=%q, want %q", method, got.snapname, tc.snap)
					}
				})
			}
		})
	}
}
