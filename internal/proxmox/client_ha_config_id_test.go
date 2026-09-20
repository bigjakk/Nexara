package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// haPathMethods is every client_ha.go method that interpolates a
// caller-supplied group or rule id into a Proxmox request path.
//
// Each entry sets exactly the parameters it needs to reach the wire, because
// the point of these tests is where the request GOES, not what it carries.
// UpdateHAGroup and UpdateHARule are PUTs and the two Delete methods are
// DELETEs, so every entry here is a mutating verb except GetHAGroup — which
// makes the read the mildest case rather than the representative one.
var haPathMethods = map[string]func(*Client, string) error{
	"GetHAGroup": func(c *Client, id string) error {
		_, err := c.GetHAGroup(context.Background(), id)
		return err
	},
	"UpdateHAGroup": func(c *Client, id string) error {
		comment := "x"
		return c.UpdateHAGroup(context.Background(), id, UpdateHAGroupParams{Comment: &comment})
	},
	"DeleteHAGroup": func(c *Client, id string) error {
		return c.DeleteHAGroup(context.Background(), id)
	},
	"UpdateHARule": func(c *Client, id string) error {
		comment := "x"
		return c.UpdateHARule(context.Background(), id, "node-affinity", UpdateHARuleParams{Comment: &comment})
	},
	"DeleteHARule": func(c *Client, id string) error {
		return c.DeleteHARule(context.Background(), id)
	},
}

// haCollectionFor reports which collection a method addresses, so the accept
// test can check the id landed in the slot it was meant for.
func haCollectionFor(method string) string {
	if strings.Contains(method, "Rule") {
		return "/api2/json/cluster/ha/rules/"
	}
	return "/api2/json/cluster/ha/groups/"
}

// newHAWireServer records r.RequestURI, deliberately — NOT r.URL.Path. Path is
// already percent-decoded by net/http, so "..%2F..%2F" reads back as "../../"
// and a test written against it cannot tell an escaped separator from a literal
// one. RequestURI is the bytes that actually left this process, which is the
// only thing Proxmox gets a say about.
func newHAWireServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// TestHAGroupAndRuleMethods_RejectPathTraversal is the guard's reason to exist.
//
// These five methods build their path by concatenation and hand the id to
// url.PathEscape, which is not a guard: it escapes "/" to %2F and leaves ".."
// alone, and Proxmox decodes the escape BEFORE it resolves the path — the
// capture-server run recorded on forbiddenVolumeIDChars (client_storage.go) is
// the evidence. So an escaped separator traverses exactly like a literal one.
//
// The path arithmetic, counted rather than assumed: the id sits at depth 6 of
// /api2/json/cluster/ha/{groups,rules}/{id}, so one ".." lands on /cluster/ha,
// two on /cluster, and THREE on /api2/json — the API root. Four would overshoot
// it onto /api2, off the JSON API altogether, which is why the payloads below
// use three.
//
// Every id here is refused for a reason that is about shape, not about whether
// this particular string happens to reach a live endpoint. "Whatever it lands
// on happens not to take this verb" is a fact about PVE's routing table, not
// about this code.
func TestHAGroupAndRuleMethods_RejectPathTraversal(t *testing.T) {
	payloads := []struct {
		name string
		id   string
	}{
		// Traversals. Each reaches a real PVE endpoint that the caller's HA
		// permission was never checked against.
		{"up to the API root", "../../../access/users/root@pam"},
		{"onto a guest", "../../../nodes/pve-01/qemu/100"},
		{"onto a sibling HA collection", "../../ha/resources/vm:100"},
		{"bare parent", ".."},
		{"bare self", "."},
		{"embedded traversal", "group/../other"},
		// Not a traversal on the wire — url.PathEscape re-encodes the "%" to
		// %25, so this arrives literally. Refused on shape: a Proxmox config
		// id has no "%" in it, and a value that needs one is a caller bug.
		{"encoded traversal", "%2e%2e%2faccess"},
		// Neither separator survives PathEscape as a separator either, and
		// both are refused for the same shape reason.
		{"backslash", `group\sibling`},
		{"space", "ha group01"},
		// Refused because the value is read back out of an audit row that
		// view:audit grants every Viewer — see validatePathSegment.
		{"newline", "ha-group01\nX-Injected: 1"},
		// PVE's own configid refuses a leading digit; so does the route
		// declaration. The client must not be the only layer that does not.
		{"leading digit", "1group"},
		{"empty", ""},
	}

	for method, call := range haPathMethods {
		t.Run(method, func(t *testing.T) {
			for _, p := range payloads {
				t.Run(p.name, func(t *testing.T) {
					srv, seen := newHAWireServer(t)
					c := newTestClient(t, srv.URL)

					err := call(c, p.id)
					if !errors.Is(err, ErrInvalidInput) {
						t.Errorf("%s(%q) err = %v, want ErrInvalidInput", method, p.id, err)
					}
					if len(*seen) != 0 {
						t.Errorf("%s(%q) reached the wire as %v; a rejected id must never be sent, "+
							"because Proxmox decodes the escape before it resolves the path",
							method, p.id, *seen)
					}
				})
			}
		})
	}
}

// TestHAGroupAndRuleMethods_AcceptRealIDs is the half that stops the guard from
// being tightened into something that refuses production data.
//
// The rule-shaped entries are the SHAPES of ids read out of the dev database's
// audit_log (resource_type = 'ha_rule') — every name scrubbed to the repo's
// placeholder scheme, which is why they look like a lab and not like one. The
// group-shaped entries come from the repo's own fixtures, because this estate
// runs a PVE version that migrated HA groups into rules and so has no group
// names left to read.
//
// The long entry is the deliberate one: it is 200 characters, past the route
// declaration's MaxLength of 128, and it must still be ACCEPTED here. A length
// cap at this layer would buy no safety — a string of letters, digits, "_" and
// "-" cannot leave its path segment however long it is — while giving the
// client a way to refuse a name Proxmox itself imposes no limit on, which is
// how the rolling orchestrator would lose the ability to re-enable a rule it
// had disabled.
func TestHAGroupAndRuleMethods_AcceptRealIDs(t *testing.T) {
	corpus := []struct {
		name string
		id   string
	}{
		{"guest and node", "linux02-pve01"},
		{"generated rule name", "ha-rule-4a1b2c3d-5e6f"},
		{"operator sentence", "keep-linux01-and-linux02-apart"},
		{"role with index", "vbr01"},
		{"words only", "avoid-shared-host"},
		{"repo fixture group", "ha-group01"},
		{"abbreviated group", "restricted-grp"},
		{"terse group", "g1"},
		{"single character", "a"},
		{"uppercase and underscore", "A_b-1"},
		{"200 characters", "r" + strings.Repeat("x", 199)},
	}

	for method, call := range haPathMethods {
		t.Run(method, func(t *testing.T) {
			for _, tc := range corpus {
				t.Run(tc.name, func(t *testing.T) {
					srv, seen := newHAWireServer(t)
					c := newTestClient(t, srv.URL)

					if err := call(c, tc.id); err != nil {
						t.Fatalf("%s(%q) = %v, want nil; this is a real id shape and must not be refused",
							method, tc.id, err)
					}
					if len(*seen) != 1 {
						t.Fatalf("%s(%q) issued %d requests %v, want exactly 1", method, tc.id, len(*seen), *seen)
					}

					// Round-trip the wire rather than re-deriving it with
					// url.PathEscape, which would only assert that the test
					// and the client call the same function.
					_, target, _ := strings.Cut((*seen)[0], " ")
					prefix := haCollectionFor(method)
					if !strings.HasPrefix(target, prefix) {
						t.Fatalf("%s(%q) went to %q, want it under %q", method, tc.id, target, prefix)
					}
					got, err := url.PathUnescape(strings.TrimPrefix(target, prefix))
					if err != nil {
						t.Fatalf("wire segment %q does not unescape: %v", target, err)
					}
					if got != tc.id {
						t.Errorf("%s put %q in the path, want %q", method, got, tc.id)
					}
				})
			}
		})
	}
}

// TestValidateHAConfigID_WrapsErrInvalidInput pins the half the method tests
// above take for granted: the refusal has to reach the API layer as
// ErrInvalidInput, because mapProxmoxError turns that into a 400 and everything
// else into a 500 or a 502 — a message that reads as "Proxmox is down" when the
// real answer is "that name is not a name". It also pins that the message names
// which of the two ids was wrong, since the two collections are addressed by
// methods that otherwise look identical from a log line.
func TestValidateHAConfigID_WrapsErrInvalidInput(t *testing.T) {
	for _, kind := range []string{"group", "rule"} {
		t.Run(kind, func(t *testing.T) {
			for _, id := range []string{"", "..", "a/b"} {
				err := validateHAConfigID(kind, id)
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("validateHAConfigID(%q, %q) = %v, want ErrInvalidInput", kind, id, err)
				}
				if !strings.Contains(err.Error(), "HA "+kind+" id") {
					t.Errorf("validateHAConfigID(%q, %q) message %q does not name the kind", kind, id, err)
				}
			}
			if err := validateHAConfigID(kind, "ha-group01"); err != nil {
				t.Errorf("validateHAConfigID(%q, \"ha-group01\") = %v, want nil", kind, err)
			}
		})
	}
}
