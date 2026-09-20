package proxmox

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// These cases guard the three methods that ADDRESS an existing resource pool —
// GetResourcePool, UpdateResourcePool, DeleteResourcePool — each of which
// interpolates a caller-supplied pool id into the request path.
//
// Until this change nothing in this package constrained that id at all: the
// three built "/pools/" + url.PathEscape(poolID) straight from the argument.
// The only rule anywhere was apischema's pve-poolid-segment in
// internal/api/registry_pools.go, i.e. a check in the CALLER — and that rule
// is PVE's own verify_poolname charset, which admits a leading dot, so "." and
// ".." match it exactly as any other name does. It stops a slash and nothing
// else. This is the same opt-in shape the rest of this client's path-segment
// guards were moved off (3e757d3 for task ids, 7f4d2ca for HA ids, 82d24d8 for
// node names, 4852e28 for snapshot names).
//
// Two halves, and the second is load-bearing. The refusal half alone is
// satisfiable by refusing everything, which here would be a REGRESSION rather
// than a weak test: a pool created at the PVE console can be named ".hidden"
// or "-lead", and these three are how such a pool is read, edited and deleted.
// Tightening them to a name rule is how an object becomes undeletable —
// poolIDParam's own comment documents that trap from the declaration side.
//
// Every assertion reads r.RequestURI — newCaptureServer records exactly that —
// and never r.URL.Path. net/http has already decoded Path by the time the
// handler runs, which makes "%2F.." and "/.." indistinguishable there and
// hides this whole bug class.

// poolAddressTraversal pops from the poolid slot back to the API root.
//
// The arithmetic, because an unexplained count is worse than none: the target
// these methods build is /api2/json/pools/{poolid}, so the directory holding
// the id is /api2/json/pools — two segments below /api2/json. The payload
// spends one segment ("a") and then pops twice, landing on /api2/json, and
// /access/users/root@pam is appended from there:
//
//	/api2/json/pools/a/../../access/users/root@pam
//	→ /api2/json/access/users/root@pam
//
// which is a real endpoint, reached with the cluster's own token. A third ".."
// would over-pop past /api2/json and resolve to nothing.
const poolAddressTraversal = "a/../../access/users/root@pam"

// poolAddressMethod is one of the three. wire builds the exact request target
// the method must produce for a given id, so an ACCEPTED id is checked against
// the bytes that leave rather than merely against "a request happened".
type poolAddressMethod struct {
	wire func(poolID string) string
	call func(*Client, string) error
}

var poolAddressMethods = map[string]poolAddressMethod{
	"GetResourcePool": {
		wire: func(poolID string) string { return "/api2/json/pools/" + url.PathEscape(poolID) },
		call: func(c *Client, poolID string) error {
			_, err := c.GetResourcePool(context.Background(), poolID)
			return err
		},
	},
	"UpdateResourcePool": {
		wire: func(poolID string) string { return "/api2/json/pools/" + url.PathEscape(poolID) },
		call: func(c *Client, poolID string) error {
			return c.UpdateResourcePool(context.Background(), poolID, UpdatePoolParams{VMs: "101"})
		},
	},
	"DeleteResourcePool": {
		wire: func(poolID string) string { return "/api2/json/pools/" + url.PathEscape(poolID) },
		call: func(c *Client, poolID string) error {
			return c.DeleteResourcePool(context.Background(), poolID)
		},
	},
}

// poolAddressBody is the response all three are served. Only GetResourcePool
// unmarshals it; the other two pass a nil out-parameter and ignore the body.
const poolAddressBody = `{"data":{"poolid":"infra","comment":"","members":[]}}`

// refusedPoolIDs is the corpus that must never reach the wire.
var refusedPoolIDs = []struct{ name, poolID, why string }{
	{"empty", "", "there is no pool to address, and the request would target the collection"},
	// The two live shapes, and the only two the HTTP route can actually
	// deliver here — pve-poolid-segment's charset stops everything below
	// this pair. url.PathEscape leaves a dot alone, so both arrive intact
	// and Proxmox resolves them after it decodes. The resolved targets are
	// worked out rather than recalled: /pools/. is /pools and /pools/.. is
	// /pools as well, one level further up having nothing to remove.
	{"a bare dot", ".", "a \".\" segment disappears, landing the call on the pool COLLECTION"},
	{"a bare traversal", "..", "the same, and on DELETE it addresses an endpoint the caller never named"},
	{"a traversal", poolAddressTraversal, "reaches /api2/json/access/users/root@pam with the cluster's own token"},
	{"a bare separator", "/", "not a pool id at all"},
	{"a separator", "infra/prod", "a NESTED pool id; legal to verify_poolname and unreachable through this URL shape"},
	{"an absolute path", "/cluster/log", "addresses a different tree entirely"},
	// Raw strings: a backslash and a letter, NOT an escape. Spelled the
	// obvious way, "a\b" is BACKSPACE, which this guard also refuses — for
	// the other reason — so the separator rule would go untested.
	{"a bare backslash", `\`, "not a pool id at all"},
	{"a backslash", `infra\x`, "confuses path parsers; no PVE pool id contains one"},
	{"a backslash traversal", `infra\..\..`, "the separator ban covers both slashes"},
	// Control characters. url.PathEscape encodes every one of these, so the
	// concern is not the path: PoolHandler files the pool id in an audit row
	// and a cluster event, and view:audit is granted to every Viewer by
	// default.
	{"a NUL", "infra\x00", "a truncating byte in an audit row other people read"},
	{"a newline", "infra\nprod", "forges a second line in an audit row"},
	{"a carriage return", "infra\rprod", "overwrites the line in a terminal"},
	{"an ANSI escape", "infra\x1b[2J", "clears the screen of whoever reads the audit log"},
	{"a DEL", "infra\x7f", "a control character above the printable range"},
	// A C1 control: two bytes in UTF-8, so a byte-wise scan for ASCII
	// controls misses it. hasControlChar decodes runes for this reason.
	{"a C1 control", "infra\u0085", "NEL; invisible to a byte-wise ASCII scan"},
}

// TestPoolAddressMethods_RefuseBeforeTheWire asserts the refusal happens
// before any HTTP request leaves — a capture server that saw one request is
// the vulnerability, not a detail.
//
// The refusal has to wrap ErrInvalidInput. mapProxmoxError
// (internal/api/handlers/proxmox_error.go) turns that into a 400 naming the
// value and everything else into a 500 reading "Proxmox operation failed".
func TestPoolAddressMethods_RefuseBeforeTheWire(t *testing.T) {
	for method, m := range poolAddressMethods {
		t.Run(method, func(t *testing.T) {
			for _, tc := range refusedPoolIDs {
				// PathEscape in the subtest name: a raw control byte in a
				// test name corrupts the output of the run that found the
				// bug.
				t.Run(tc.name+"/"+url.PathEscape(tc.poolID), func(t *testing.T) {
					srv, seen := newCaptureServer(t, poolAddressBody)
					c := newTestClient(t, srv.URL)

					err := m.call(c, tc.poolID)

					// Printed first and in full: this line IS the bug.
					if sent := *seen; len(sent) != 0 {
						t.Errorf("%s(%q) reached Proxmox as %q; it must never leave the client (%s)",
							method, tc.poolID, sent, tc.why)
					}
					if err == nil {
						t.Fatalf("%s(%q) succeeded, want a refusal (%s)", method, tc.poolID, tc.why)
					}
					if !errors.Is(err, ErrInvalidInput) {
						t.Errorf("%s(%q) err = %v, want one wrapping ErrInvalidInput — "+
							"anything else maps to a 500 in mapProxmoxError", method, tc.poolID, err)
					}
				})
			}
		})
	}
}

// acceptedPoolIDs is the half that stops the guard above from being satisfied
// by refusing everything.
//
// Every entry is a pool id verify_poolname (pve-access-control) accepts and
// that a pool created outside Nexara may therefore carry. A leading dash was
// once pinned as invalid in the registry tests until the upstream rule was
// actually read; it is legal, and it is here for that reason.
var acceptedPoolIDs = []struct{ name, poolID, why string }{
	{"an ordinary id", "infra", "the control that stops this test passing by refusing everything"},
	{"a leading dot", ".hidden", "legal to verify_poolname; refusing it would strand the pool"},
	{"a leading dash", "-lead", "also legal, and the case an earlier test had backwards"},
	{"one character", "p", "verify_poolname has no minimum length"},
	{"dots inside", "a.b.c", "a dot is only special as a WHOLE segment"},
	{"three dots", "...", "not \".\" and not \"..\", so nothing normalises it away"},
	{"a leading digit", "01pool", "pve-configid would refuse this; a pool id is looser"},
}

// TestPoolAddressMethods_AcceptWhatPVEMints is the non-regression half, and it
// asserts the request TARGET rather than merely that a call succeeded — the
// bytes that leave are what a guard placed one layer too high would change.
func TestPoolAddressMethods_AcceptWhatPVEMints(t *testing.T) {
	for method, m := range poolAddressMethods {
		t.Run(method, func(t *testing.T) {
			for _, tc := range acceptedPoolIDs {
				t.Run(tc.name+"/"+url.PathEscape(tc.poolID), func(t *testing.T) {
					srv, seen := newCaptureServer(t, poolAddressBody)
					c := newTestClient(t, srv.URL)

					if err := m.call(c, tc.poolID); err != nil {
						t.Fatalf("%s(%q) = %v, want it to reach Proxmox (%s)", method, tc.poolID, err, tc.why)
					}
					sent := *seen
					if len(sent) != 1 {
						t.Fatalf("%s(%q) made %d requests, want 1", method, tc.poolID, len(sent))
					}
					if want := m.wire(tc.poolID); sent[0] != want {
						t.Errorf("%s(%q) requested %q, want %q", method, tc.poolID, sent[0], want)
					}
				})
			}
		})
	}
}

// TestCreateResourcePoolStillTakesANestedID records the asymmetry the guard
// above deliberately does not close.
//
// POST /pools sends the id as a form FIELD, so nothing about it has to survive
// a path segment: "infra/prod" is a pool PVE will create and the three
// addressing methods can never reach. Giving the create the same guard would
// refuse a pool name Proxmox accepts, which is the invented-strictness mistake
// poolCreateIDParam (internal/api/registry_pools.go) exists to avoid.
//
// It is a test rather than a comment because the tempting "make all four
// consistent" edit is one line and nothing else would fail.
func TestCreateResourcePoolStillTakesANestedID(t *testing.T) {
	srv, seen := newCaptureServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)

	if err := c.CreateResourcePool(context.Background(), CreatePoolParams{PoolID: "infra/prod"}); err != nil {
		t.Fatalf("CreateResourcePool(%q) = %v, want it to reach Proxmox", "infra/prod", err)
	}
	if sent := *seen; len(sent) != 1 || sent[0] != "/api2/json/pools" {
		t.Errorf("CreateResourcePool requested %q, want one POST to /api2/json/pools — the id is a "+
			"form field, not a path segment", sent)
	}
}
