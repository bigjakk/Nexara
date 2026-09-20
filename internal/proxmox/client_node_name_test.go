package proxmox

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// These cases guard the node half of a Proxmox request path, which is the
// half validateNodeName was NOT guarding until it was changed to delegate to
// validatePathSegment.
//
// The rule it carried before refused "", any "/", and ".." as a SUBSTRING,
// which let three things through: a bare "." (a path segment that disappears,
// so /nodes/./tasks/{upid}/status resolves onto /nodes/tasks/{upid}/status), a
// backslash, and every control character. It also refused "pve..01", a name
// the node-name format itself accepts, with an error that did not wrap
// ErrInvalidInput and so surfaced as a 500.
//
// The reachable path is the one taskUPID (internal/api/handlers/vms.go) opens:
// it percent-DECODES the :upid parameter and hands colon-field 1 to the client
// as the node, so "UPID:.:0:0:0:x::root@pam:" names the node ".". The UPID
// guard cannot see it — that string holds no separator at all — and the two
// GET task routes are gated on view:task, which the built-in Viewer role
// holds. POST /api/v1/tasks is the second way in: it declares "node" as an
// optional string with no pattern, and reconcileRunningTasks
// (internal/collector/task_reconcile.go) replays the stored value through
// GetTaskStatus unattended.
//
// Every case drives an EXPORTED method rather than the validator. Asserting on
// validateNodeName directly would still pass on the day someone drops the call
// from one of its 144 sites.
//
// Every assertion reads r.RequestURI (newCaptureServer records exactly that),
// never r.URL.Path. net/http has already decoded Path by the time a handler
// runs, so "%2F.." and "/.." are indistinguishable there and the whole bug
// class is invisible.

// validNodeTaskUPID is a UPID the UPID guard accepts, so that in the
// two-argument calls below only the NODE guard can be the thing that refuses.
const validNodeTaskUPID = "UPID:pve-01:0:0:0:qmstart:110:root@pam:"

// refusedNodeNames is the corpus that must never reach the wire.
//
// Each is written so the bytes arrive unmodified: a backslash is a raw string,
// because in Go "a\b" is BACKSPACE and would silently test a control character
// where a separator was meant.
var refusedNodeNames = []struct{ name, node string }{
	{"empty", ""},
	// The live gap. A "." segment is removed when the path is normalised, so
	// the request lands one level up from where the route addressed.
	{"a bare dot", "."},
	{"a bare traversal", ".."},
	// Raw strings: these are a backslash followed by a letter, NOT an escape.
	{"a backslash", `a\b`},
	{"a backslash traversal", `a\..\status`},
	// Control characters. url.PathEscape encodes all of these, so the concern
	// is not the path — it is that the value is also written into a TrackTask
	// description and an audit row with %s, and view:audit is granted to every
	// Viewer by default.
	{"a NUL", "a\x00b"},
	{"a newline", "a\nb"},
	{"a carriage return", "a\rb"},
	{"an ANSI escape", "a\x1bb"},
	{"a DEL", "a\x7fb"},
	// "\b" here IS the escape: U+0008, BACKSPACE. Spelled deliberately,
	// because it is the mistake the raw strings above exist to avoid, and it
	// is a control character in its own right.
	{"a backspace", "a\bb"},
	// A C1 control. It encodes as two bytes in UTF-8, so a byte-wise scan for
	// ASCII controls misses it; hasControlChar decodes runes for this reason.
	{"a C1 control", "a\u0085b"},
	{"a separator", "a/b"},
	{"a traversal", "../etc"},
	{"an absolute path", "/cluster/log"},
}

// TestNodeNameRefusalsReachTheWireAsNothing drives the guard through three
// exported methods, chosen for where the node sits in the path each builds.
//
// GetNodeStatus puts the node second-to-last, so a traversal moves the request
// within /nodes. GetTaskStatus passes a VALID UPID alongside, so only the node
// guard can refuse. StopNodeTask is the DELETE, and it appends nothing after
// the UPID — the caller owns the whole tail there, which makes it the worst of
// the three.
//
// The refusal has to wrap ErrInvalidInput: mapProxmoxError
// (internal/api/handlers/proxmox_error.go) turns that into a 400 and anything
// else into a 500 that reads like a Proxmox outage rather than like the
// caller's own input.
func TestNodeNameRefusalsReachTheWireAsNothing(t *testing.T) {
	for _, tc := range refusedNodeNames {
		// url.PathEscape in the subtest name: a control byte in a test name
		// corrupts the output of the run that found the bug.
		label := tc.name + "/" + url.PathEscape(tc.node)

		t.Run("status/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			_, err := c.GetNodeStatus(context.Background(), tc.node)
			assertNodeNameRefused(t, err, seen)
		})

		t.Run("taskstatus/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			_, err := c.GetTaskStatus(context.Background(), tc.node, validNodeTaskUPID)
			assertNodeNameRefused(t, err, seen)
		})

		t.Run("stoptask/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.StopNodeTask(context.Background(), tc.node, validNodeTaskUPID)
			assertNodeNameRefused(t, err, seen)
		})
	}
}

// TestNodeNameAcceptCorpusStillReachesProxmoxUnchanged is the control without
// which the test above is vacuous: a guard that refused everything would pass
// every refusal case.
//
// It is also the ratchet against re-tightening. "pve..01" is here because the
// old substring ban refused it; the 100-character name is here because there
// is deliberately no length cap at the client — see validateNodeName's own
// note, and validateHAConfigID's, on why a cap here would only hand the client
// a way to refuse a name Proxmox minted.
func TestNodeNameAcceptCorpusStillReachesProxmoxUnchanged(t *testing.T) {
	// One golden literal. Every expectation below is built with the same
	// url.PathEscape the production code uses, which pins "escaped once" but
	// would follow an encoder swap without complaining; this pins what
	// actually goes on the wire. What it fixes in particular is that
	// PathEscape leaves a DOT alone — that is why "pve..01" arrives as itself
	// rather than as "pve%2E%2E01".
	const golden = "pve-01.example.com"
	if want, got := "/api2/json/nodes/pve-01.example.com/status",
		"/api2/json/nodes/"+url.PathEscape(golden)+"/status"; want != got {
		t.Fatalf("url.PathEscape(%q) builds %q, want %q — the wire form has changed", golden, got, want)
	}

	corpus := []struct{ name, node string }{
		{"the ordinary form", "pve-01"},
		{"no separator", "pve1"},
		{"a single character", "n"},
		{"digits in the middle of a word", "node1"},
		// Dots: PVE's own pve_verify_node_name does not allow one, but this
		// package's node-name format does and a cluster can carry an FQDN-ish
		// node name. The client is not the layer that decides.
		{"a dot", "pve.01"},
		{"an FQDN", golden},
		// An underscore is refused by apischema's node-name format and by
		// PVE's own rule. It is accepted HERE on purpose: the client's job is
		// to keep the value inside its path segment, not to re-litigate the
		// declaration.
		{"an underscore", "pve_01"},
		// The name the old ".."-as-a-substring ban refused, as a 500.
		{"two dots", "pve..01"},
		// No length cap at the client, deliberately.
		{"a hundred characters", strings.Repeat("n", 100)},
	}

	for _, tc := range corpus {
		t.Run(tc.name+"/"+url.PathEscape(tc.node), func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if _, err := c.GetNodeStatus(context.Background(), tc.node); err != nil {
				t.Fatalf("GetNodeStatus(%q): %v", tc.node, err)
			}
			want := "/api2/json/nodes/" + url.PathEscape(tc.node) + "/status"
			got := *seen
			if len(got) != 1 {
				t.Fatalf("issued %d requests %q, want exactly 1", len(got), got)
			}
			if got[0] != want {
				t.Errorf("request target = %q, want %q", got[0], want)
			}
		})
	}
}

// TestPathGuardFamilyShareTheirCoreRefusals holds this client's path-segment
// guards to the refusals they all have to make.
//
// validateNodeName is why this table exists. It was a member of the family by
// position — same call shape, same job, same file even — and not by rule, and
// nothing in the tree compared the two until a reader happened to. A table
// that names the members is a table that fails when the next one drifts.
//
// Every CHARACTER probe is derived by injecting into that member's own
// baseline, which is the only way those rows mean anything. Written as
// standalone strings they pass for the wrong reason and the test goes quietly
// vacuous: `a\b` is refused by validateVolumeID for having no colon in it, not
// for the backslash. Injecting into a value the member ACCEPTS isolates the
// character.
//
// The three WHOLE-VALUE probes — "", "." and ".." — are necessarily absolute,
// and several members do refuse them on a rule other than the one being
// probed: validateHAResourceID answers "should be a VMID", validateVolumeID
// answers "is not in \"storage:name\" form". That is tolerated rather than
// hidden. The property those rows carry is the weaker "no member accepts a
// bare traversal", which is still worth pinning; they are not evidence that a
// member has a dot-segment rule of its own.
//
// The baseline itself is asserted as accepted on every row. Without that a
// member replaced by a blanket refusal would pass the whole table.
func TestPathGuardFamilyShareTheirCoreRefusals(t *testing.T) {
	family := []struct {
		name     string
		validate func(string) error
		// baseline is a value this member accepts, and the value every
		// injected probe below is built from.
		baseline string
		// skipSeparator exempts a member whose accepted values legitimately
		// carry a "/", so baseline+"/x" is not a refusal it can make.
		skipSeparator bool
		// why records the exemption, so it cannot be copied onto a member
		// that has not earned it.
		why string
	}{
		{
			name:     "validateNodeName",
			validate: validateNodeName,
			baseline: "pve-01",
		},
		{
			name:     "validatePathSegment",
			validate: func(v string) error { return validatePathSegment("thing", v) },
			baseline: "vmbr0",
		},
		{
			name:     "validatePathSegmentAllowingSlash",
			validate: func(v string) error { return validatePathSegmentAllowingSlash("thing", v) },
			baseline: "192.0.2.0/24",
			// Its whole reason for existing: a firewall IP set entry id is a
			// CIDR, so ONE slash is part of the name. The injected probe would
			// be refused here anyway — but on the component COUNT, which is a
			// different rule from the separator ban this column is about, and
			// a probe that passes for a different reason than the column
			// claims is the vacuity this table is trying to avoid.
			skipSeparator: true,
			why:           "one slash is legitimate; see its doc comment",
		},
		{
			name:     "validateTaskUPID",
			validate: validateTaskUPID,
			baseline: validNodeTaskUPID,
		},
		{
			name:     "validatePBSTaskUPID",
			validate: validatePBSTaskUPID,
			baseline: validNodeTaskUPID,
		},
		{
			name:     "validateHAConfigID",
			validate: func(v string) error { return validateHAConfigID("rule", v) },
			baseline: "rule01",
		},
		{
			name:     "validateHAResourceID",
			validate: validateHAResourceID,
			baseline: "vm:100",
		},
		{
			name:     "validateVolumeID",
			validate: validateVolumeID,
			baseline: "store01:iso/x.iso",
			// A volume id's name half is a slash-delimited path —
			// "local:iso/debian-12.iso" — so a single separator is ordinary
			// here. It refuses the TRAVERSAL probe per component instead,
			// which is the row above it in the table and is asserted.
			// Contrast validatePathSegmentAllowingSlash, whose traversal
			// probe fires its component-COUNT rule rather than its
			// per-component one: also a refusal for a different reason than
			// the column names, and tolerated on the same terms as the
			// whole-value probes in the doc above.
			skipSeparator: true,
			why:           "the name half is legitimately slash-delimited",
		},
		// The access guards are the same shape and the same job, and were
		// absent from this table on its first pass — which is the drift the
		// table exists to catch, reproduced while writing it. They pass
		// unchanged; the list was incomplete, not the guards.
		{
			name:     "validateUserID",
			validate: validateUserID,
			baseline: "admin@pam",
		},
		{
			name:     "validateTokenID",
			validate: validateTokenID,
			baseline: "monitoring",
		},
		{
			name:     "validateRealm",
			validate: validateRealm,
			baseline: "pam",
		},
		{
			name:     "validateAccessName",
			validate: func(v string) error { return validateAccessName("group id", v, 64) },
			baseline: "group01",
		},
	}

	for _, member := range family {
		t.Run(member.name, func(t *testing.T) {
			// The control. Everything below is a refusal, so without this a
			// member swapped for `func(string) error { return errors.New("no") }`
			// would pass every case.
			if err := member.validate(member.baseline); err != nil {
				t.Fatalf("baseline %q is refused: %v — every probe below is derived from it, "+
					"so the rest of this row would pass for the wrong reason", member.baseline, err)
			}

			probes := []struct{ name, value string }{
				{"empty", ""},
				{"a bare dot", "."},
				{"a bare traversal", ".."},
				{"a NUL", member.baseline + "\x00"},
				{"a newline", member.baseline + "\n"},
				// Raw string: a backslash and an "x", not an escape.
				{"a backslash", member.baseline + `\x`},
				{"a traversal", member.baseline + "/../.."},
			}
			if !member.skipSeparator {
				probes = append(probes, struct{ name, value string }{"a separator", member.baseline + "/x"})
			} else if member.why == "" {
				t.Error("skipSeparator with no reason recorded")
			}

			for _, p := range probes {
				t.Run(p.name+"/"+url.PathEscape(p.value), func(t *testing.T) {
					err := member.validate(p.value)
					if err == nil {
						t.Fatalf("%s(%q) = nil; every member of this family refuses it", member.name, p.value)
					}
					if !errors.Is(err, ErrInvalidInput) {
						t.Errorf("%s(%q) = %v, want one wrapping ErrInvalidInput — "+
							"anything else maps to a 500 in mapProxmoxError", member.name, p.value, err)
					}
				})
			}
		})
	}
}

func assertNodeNameRefused(t *testing.T, err error, seen *[]string) {
	t.Helper()
	if sent := *seen; len(sent) != 0 {
		// Printed first and in full: this line IS the vulnerability. A "."
		// segment vanishes when the path is normalised and a "%2F" is decoded
		// before the path is resolved, so either one lands the request
		// somewhere the route never addressed.
		t.Errorf("the request reached Proxmox as %q; it must never leave the client", sent)
	}
	if err == nil {
		t.Fatal("the call succeeded; the node name must be refused before it is sent")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want one wrapping ErrInvalidInput — anything else maps to a 500", err)
	}
}
