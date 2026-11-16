package proxmox

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// These guard the four methods that address an existing snapshot (Delete and Rollback, VM and
// CT), which put a caller's name in the path and once checked only `snapname == ""`. The
// accepting half is load-bearing: addressing is declared looser than create (MaxLength 128, no
// two-character minimum) so a snapshot minted outside Nexara stays deletable. Assertions read
// r.RequestURI; r.URL.Path is already decoded.

const (
	snapshotAddressNode = "pve-01"
	snapshotAddressVMID = 100
	snapshotAddressCTID = 101
)

// snapshotAddressTraversal pops from the snapshot slot behind a proxy that decodes %2F and
// removes dot segments (pveproxy does not). The name sits five below /api2/json (qemu and lxc
// alike), so five ".." land the DELETEs on /api2/json/access/users/root@pam, a real endpoint;
// the rollbacks append "/rollback" and land on nothing. The depth is shared, not the target.
const snapshotAddressTraversal = "../../../../../access/users/root@pam"

// snapshotAddressMethod is one of the four. wire builds the exact request
// target the method must produce for a given name, so an ACCEPTED name is
// checked against the bytes that leave rather than merely against "a request
// happened".
type snapshotAddressMethod struct {
	wire func(name string) string
	call func(*Client, string) (string, error)
}

// snapshotAddressSlot is the collection the name is positioned in, built from
// the same constants the calls use so the expectation cannot drift away from
// the arguments. TestSnapshotAddressPercentIsDoubleEscaped carries the one
// fully-spelled golden literal, which is what pins the wire form itself.
func snapshotAddressSlot(kind string, vmid int) string {
	return "/api2/json/nodes/" + snapshotAddressNode + "/" + kind + "/" + strconv.Itoa(vmid) + "/snapshot/"
}

var snapshotAddressMethods = map[string]snapshotAddressMethod{
	"DeleteVMSnapshot": {
		wire: func(name string) string {
			return snapshotAddressSlot("qemu", snapshotAddressVMID) + url.PathEscape(name)
		},
		call: func(c *Client, name string) (string, error) {
			return c.DeleteVMSnapshot(context.Background(), snapshotAddressNode, snapshotAddressVMID, name)
		},
	},
	"RollbackVMSnapshot": {
		wire: func(name string) string {
			return snapshotAddressSlot("qemu", snapshotAddressVMID) + url.PathEscape(name) + "/rollback"
		},
		call: func(c *Client, name string) (string, error) {
			return c.RollbackVMSnapshot(context.Background(), snapshotAddressNode, snapshotAddressVMID, name)
		},
	},
	"DeleteCTSnapshot": {
		wire: func(name string) string {
			return snapshotAddressSlot("lxc", snapshotAddressCTID) + url.PathEscape(name)
		},
		call: func(c *Client, name string) (string, error) {
			return c.DeleteCTSnapshot(context.Background(), snapshotAddressNode, snapshotAddressCTID, name)
		},
	},
	"RollbackCTSnapshot": {
		wire: func(name string) string {
			return snapshotAddressSlot("lxc", snapshotAddressCTID) + url.PathEscape(name) + "/rollback"
		},
		call: func(c *Client, name string) (string, error) {
			return c.RollbackCTSnapshot(context.Background(), snapshotAddressNode, snapshotAddressCTID, name)
		},
	},
}

// snapshotAddressUPID is the response body all four methods are served.
//
// One fixture for all four, so the worker type is wrong for three of them: a
// real PVE answers qmrollback, vzdelsnapshot and vzrollback in turn. Nothing
// here parses it — these methods unmarshal the UPID into a string and hand it
// back — so the only property that matters is that it is non-empty, which is
// what lets the accept half assert the caller got a task to track.
const snapshotAddressUPID = `{"data":"UPID:pve-01:0000A1B2:00C3D4E5:65000000:qmdelsnapshot:100:nexara@pve!api:"}`

// refusedSnapshotAddressNames is the corpus that must never reach the wire.
//
// A backslash is written as a raw string on purpose: in Go "a\b" is BACKSPACE,
// so spelling it the obvious way would silently probe a control character where
// a separator was meant, and the separator rule would go untested.
var refusedSnapshotAddressNames = []struct{ name, snap, why string }{
	{"empty", "", "there is no snapshot to address"},
	// The two live shapes. url.PathEscape leaves a dot alone, so both arrive
	// intact. pveproxy would read either as a snapshot name and refuse it (see
	// validatePathSegment); a normalising proxy in front of it resolves them,
	// and the targets there are worked out rather than asserted from memory:
	// /nodes/pve-01/qemu/100/snapshot/. is /nodes/pve-01/qemu/100/snapshot and
	// /nodes/pve-01/qemu/100/snapshot/.. is /nodes/pve-01/qemu/100.
	{"a bare dot", ".", "behind a normalising proxy the \".\" segment disappears, landing a DELETE on the snapshot COLLECTION"},
	{"a bare traversal", "..", "behind a normalising proxy it lands a DELETE on the GUEST — the same target as DestroyVM"},
	{"a traversal", snapshotAddressTraversal, "behind a proxy that decodes %2F and normalises, it reaches /api2/json/access/users/root@pam with the cluster's own token"},
	{"a bare separator", "/", "not a name at all"},
	{"a separator", "snap01/rollback", "descends out of the slot the method positioned it in"},
	{"an absolute path", "/cluster/log", "addresses a different tree entirely"},
	// Raw strings: a backslash and a letter, NOT an escape.
	{"a bare backslash", `\`, "not a name at all"},
	{"a backslash", `snap01\x`, "confuses path parsers; no PVE snapshot name contains one"},
	{"a backslash traversal", `snap01\..\..`, "the separator ban covers both slashes"},
	// Control characters. url.PathEscape encodes every one of these, so the
	// concern is not the path: all four handlers file snap_name in a TrackTask
	// Extra map, and view:audit is granted to every Viewer by default.
	{"a NUL", "snap01\x00", "a truncating byte in an audit row other people read"},
	{"a newline", "snap01\nsnap02", "forges a second line in an audit row"},
	{"a carriage return", "snap01\rsnap02", "overwrites the line in a terminal"},
	{"an ANSI escape", "snap01\x1b[2J", "clears the screen of whoever reads the audit log"},
	{"a DEL", "snap01\x7f", "a control character above the printable range"},
	// "\b" here IS the escape — U+0008, BACKSPACE — spelled deliberately,
	// because it is the mistake the raw strings above exist to avoid.
	{"a backspace", "snap01\bx", "rubs out the character before it in a rendered row"},
	// A C1 control: two bytes in UTF-8, so a byte-wise scan for ASCII controls
	// misses it. hasControlChar decodes runes for this reason.
	{"a C1 control", "snap01\u0085", "NEL; invisible to a byte-wise ASCII scan"},
}

// TestSnapshotAddressMethods_RefuseBeforeTheWire asserts the refusal happens
// before any HTTP request leaves — a capture server that saw one request is the
// vulnerability, not a detail.
//
// The refusal has to wrap ErrInvalidInput. mapProxmoxError
// (internal/api/handlers/proxmox_error.go) turns that into a 400 naming the
// value and everything else into a 500 reading "Proxmox operation failed"; the
// `fmt.Errorf("snapshot name is required")` these replaced was the second kind,
// so an empty name used to surface as what looks like a Proxmox outage.
func TestSnapshotAddressMethods_RefuseBeforeTheWire(t *testing.T) {
	for method, m := range snapshotAddressMethods {
		t.Run(method, func(t *testing.T) {
			for _, tc := range refusedSnapshotAddressNames {
				// PathEscape in the subtest name: a raw control byte in a test
				// name corrupts the output of the run that found the bug.
				t.Run(tc.name+"/"+url.PathEscape(tc.snap), func(t *testing.T) {
					srv, seen := newCaptureServer(t, snapshotAddressUPID)
					c := newTestClient(t, srv.URL)

					upid, err := m.call(c, tc.snap)

					// Printed first and in full: this line IS the bug.
					if sent := *seen; len(sent) != 0 {
						t.Errorf("%s(%q) reached Proxmox as %q; it must never leave the client (%s)",
							method, tc.snap, sent, tc.why)
					}
					if err == nil {
						t.Fatalf("%s(%q) succeeded, want a refusal (%s)", method, tc.snap, tc.why)
					}
					if !errors.Is(err, ErrInvalidInput) {
						t.Errorf("%s(%q) err = %v, want one wrapping ErrInvalidInput — "+
							"anything else maps to a 500 in mapProxmoxError", method, tc.snap, err)
					}
					if upid != "" {
						t.Errorf("%s(%q) returned upid %q; a refused name must produce no task",
							method, tc.snap, upid)
					}
				})
			}
		})
	}
}

// acceptedSnapshotAddressNames keeps the test above from passing vacuously and ratchets against
// re-tightening: every entry but the first is a name ValidateSnapshotName refuses. PVE's delete
// and rollback check snapname themselves; refusing here would only hide upstream's clear error,
// or strand a snapshot PVE accepts (snapshotNameParam records the same decision).
var acceptedSnapshotAddressNames = []struct{ name, snap, why string }{
	{"the ordinary form", "snap01", "a plain pve-configid"},
	{"a single character", "x", "$CONFIGID_RE needs two; the addressing side drops the minimum"},
	{"a hundred characters", strings.Repeat("s", 100), "the create rule caps at 40; the route caps at 128; the client caps at nothing"},
	{"a dot inside", "Snap.1", "not a legal pve-configid, and not this client's call"},
	// The substring ban validateNodeName removed, refused here too: ".."
	// traverses only as a WHOLE segment, and the substring form refuses a name
	// that is merely unusual. Do not put it back.
	{"dots as a substring", "snap..01", "\"..\" traverses only as a whole segment"},
	{"a leading digit", "1snap", "a configid must start with a letter; an existing one need not"},
	{"a space", "my snap", "url.PathEscape sends it as %20; it stays in its own segment"},
	// The one an operator actually hits: vzdump mints a snapshot literally
	// named "vzdump" when it backs a container up, and pve-container refuses
	// that name at CREATE. A stale one left behind by an interrupted backup is
	// exactly the object that has to be deletable.
	{"the name vzdump itself mints", "vzdump", "reserved at create precisely because vzdump creates it"},
	{"a name reserved for both kinds", "current", "PVE answers for this one; pre-empting it is not the client's job"},
	// The headline row. See TestSnapshotAddressPercentIsDoubleEscaped for the
	// golden literal that pins why this is safe rather than merely tolerated.
	{"a percent-encoded traversal", "%2e%2e%2f", "url.PathEscape re-encodes the percent, so it arrives as a literal name"},
}

// TestSnapshotAddressMethods_AcceptWhatTheCreateRuleRefuses pins the accept
// half, target and all: a name that must be addressable has to arrive at
// Proxmox as itself, in the slot the method positioned it in.
func TestSnapshotAddressMethods_AcceptWhatTheCreateRuleRefuses(t *testing.T) {
	for method, m := range snapshotAddressMethods {
		t.Run(method, func(t *testing.T) {
			for _, tc := range acceptedSnapshotAddressNames {
				t.Run(tc.name+"/"+url.PathEscape(tc.snap), func(t *testing.T) {
					srv, seen := newCaptureServer(t, snapshotAddressUPID)
					c := newTestClient(t, srv.URL)

					upid, err := m.call(c, tc.snap)
					if err != nil {
						t.Fatalf("%s(%q) = %v, want nil; refusing it here strands the snapshot (%s)",
							method, tc.snap, err, tc.why)
					}
					if upid == "" {
						t.Errorf("%s(%q) returned no upid; the caller has no task to track", method, tc.snap)
					}

					got := *seen
					if len(got) != 1 {
						t.Fatalf("%s(%q) issued %d requests %q, want exactly 1", method, tc.snap, len(got), got)
					}
					if want := m.wire(tc.snap); got[0] != want {
						t.Errorf("%s(%q) went to %q, want %q", method, tc.snap, got[0], want)
					}
				})
			}
		})
	}
}

// TestSnapshotAddressPercentIsDoubleEscaped is this file's one golden literal: the cases above
// build expectations with the production url.PathEscape, so they would follow an encoder swap.
// "%" is absent from these guards (unlike validateVolumeID) only because the name is escaped:
// pveproxy decodes "%2e%2e%2f" to "../" before routing. If this prints "%2e%2e%2f" rather than
// "%252e%252e%252f", the escape is gone and "%" must go back into a refusal list.
func TestSnapshotAddressPercentIsDoubleEscaped(t *testing.T) {
	const payload = "%2e%2e%2f"
	const want = "/api2/json/nodes/pve-01/qemu/100/snapshot/%252e%252e%252f"

	srv, seen := newCaptureServer(t, snapshotAddressUPID)
	c := newTestClient(t, srv.URL)

	if _, err := c.DeleteVMSnapshot(context.Background(), snapshotAddressNode, snapshotAddressVMID, payload); err != nil {
		t.Fatalf("DeleteVMSnapshot(%q): %v", payload, err)
	}

	got := *seen
	if len(got) != 1 {
		t.Fatalf("issued %d requests %q, want exactly 1", len(got), got)
	}
	if got[0] != want {
		t.Errorf("request target = %q, want %q — the percent must arrive re-encoded, "+
			"or Proxmox decodes it to \"../\": a separator and a dot segment the caller never sent", got[0], want)
	}
}
