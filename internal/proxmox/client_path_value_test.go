package proxmox

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestValidatePathSegmentAllowingSlash(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		// The whole reason this exists: an IP set entry id is a CIDR, and the
		// slash is part of the name rather than a path separator.
		{"cidr", "192.0.2.0/24", false},
		{"ipv6 cidr", "2001:db8::/32", false},
		{"bare address", "192.0.2.10", false},
		{"bare ipv6", "2001:db8::1", false},
		{"alias name", "trusted-hosts", false},
		// A percent needs no exclusion — the caller escapes on the way out, so
		// it arrives at Proxmox as the character the operator meant.
		{"percent", "100%", false},
		{"dotted name", "a.b.c", false},

		{"empty", "", true},
		{"dot", ".", true},
		{"dotdot", "..", true},
		{"traversal", "../..", true},
		{"traversal component", "192.0.2.0/../24", true},
		{"leading slash", "/24", true},
		{"trailing slash", "192.0.2.0/", true},
		{"doubled slash", "192.0.2.0//24", true},
		{"backslash", `a\b`, true},
		{"control character", "a\x00b", true},
		{"newline", "a\nb", true},
		// The shape rule rather than a traversal one: every component is a
		// real name, so the per-component check takes it, and pveproxy would
		// join it back into one cidr value anyway (see the function's doc).
		// One slash is the whole allowance.
		{"two slashes", "a/b/c", true},
		{"many slashes", "a/b/c/d", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePathSegmentAllowingSlash("thing", tt.value)
			if tt.wantErr && err == nil {
				t.Errorf("validatePathSegmentAllowingSlash(%q) = nil, want an error", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validatePathSegmentAllowingSlash(%q) = %v, want nil", tt.value, err)
			}
		})
	}
}

// TestIPSetEntryPathEscapesTheValueExactlyOnce pins the outbound path for an
// entry id the caller hands in decoded.
//
// The API layer decodes the path parameter (handlers.accessParam), so what
// arrives here is "192.0.2.0/24" and not "192.0.2.0%2F24". Escaping it once is
// what puts the entry in a single path segment, which is the form PVE's own UI
// sends; escaping it twice — the shape this route shipped with, because nothing
// decoded — asks Proxmox for an entry whose id literally contains "%2F", and no
// such entry exists.
func TestIPSetEntryPathEscapesTheValueExactlyOnce(t *testing.T) {
	const set = "storeset"

	// One golden literal, because every other case below builds its expectation
	// with the same url.PathEscape the production code uses — which pins "once,
	// not twice" but would follow an encoder swap without complaining.
	if want := "/api2/json/cluster/firewall/ipset/storeset/192.0.2.0%2F24"; want !=
		"/api2/json/cluster/firewall/ipset/"+set+"/"+url.PathEscape("192.0.2.0/24") {
		t.Fatalf("url.PathEscape no longer produces the wire form PVE's own UI sends; want %q", want)
	}

	for _, entry := range []string{"192.0.2.0/24", "2001:db8::/32", "192.0.2.10"} {
		t.Run(entry, func(t *testing.T) {
			want := "/api2/json/cluster/firewall/ipset/" + set + "/" + url.PathEscape(entry)

			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			if err := c.DeleteFirewallIPSetEntry(context.Background(), set, entry); err != nil {
				t.Fatalf("DeleteFirewallIPSetEntry(%q): %v", entry, err)
			}
			if err := c.UpdateFirewallIPSetEntry(context.Background(), set, entry, FirewallIPSetEntryParams{Comment: "x"}); err != nil {
				t.Fatalf("UpdateFirewallIPSetEntry(%q): %v", entry, err)
			}

			if len(*seen) != 2 {
				t.Fatalf("issued %d requests %v, want 2", len(*seen), *seen)
			}
			for _, got := range *seen {
				if got != want {
					t.Errorf("request target = %q, want %q", got, want)
				}
				if strings.Contains(got, "%25") {
					t.Errorf("request target %q carries a doubly-escaped percent", got)
				}
			}
		})
	}
}

// TestIPSetEntryRefusesTraversalThroughTheSlashItAllows is the counterweight:
// tolerating a slash must not tolerate a traversal built out of one.
//
// pveproxy decodes a percent-escape before it routes, but it removes no dot
// segment, and at this position its IP-set subclass joins everything after
// the set name into the one cidr value (see validatePathSegmentAllowingSlash).
// A proxy in front of it that decoded "../.." out of its escaped segment and
// then normalised would pop "storeset" and "ipset" both, landing on
// /cluster/firewall. Nothing may be sent.
func TestIPSetEntryRefusesTraversalThroughTheSlashItAllows(t *testing.T) {
	tests := []struct{ set, entry string }{
		// The entry half.
		{"storeset", ".."},
		{"storeset", "."},
		{"storeset", "../.."},
		{"storeset", "192.0.2.0/../24"},
		{"storeset", "a/b/c"},
		{"storeset", `a\b`},
		{"storeset", "a\nb"},
		{"storeset", ""},
		// The SET-NAME half, which went unchecked until this change even though
		// it sits in the same path expression. Behind a normalising proxy ".."
		// there drops the ipset segment and addresses /cluster/firewall/<entry>,
		// and "." addresses the set NAMED by the entry.
		{"..", "192.0.2.10"},
		{".", "192.0.2.10"},
		{"a/b", "192.0.2.10"},
		{"", "192.0.2.10"},
	}

	for _, tt := range tests {
		t.Run("set="+tt.set+",entry="+tt.entry, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			if err := c.DeleteFirewallIPSetEntry(context.Background(), tt.set, tt.entry); err == nil {
				t.Errorf("DeleteFirewallIPSetEntry(%q, %q) succeeded, want rejection", tt.set, tt.entry)
			}
			if err := c.UpdateFirewallIPSetEntry(context.Background(), tt.set, tt.entry, FirewallIPSetEntryParams{}); err == nil {
				t.Errorf("UpdateFirewallIPSetEntry(%q, %q) succeeded, want rejection", tt.set, tt.entry)
			}
			if len(*seen) != 0 {
				t.Errorf("issued %d request(s) %v, want none — the refusal must come before the request", len(*seen), *seen)
			}
		})
	}
}

// TestPathSegmentRefusesControlCharacters is the regression for a hole the
// decode at the API layer opened.
//
// A declared pattern matches the value AS IT ARRIVES, and no regex in the
// catalogue can see a control byte through a "%0A" — so once the handler
// percent-decodes, a name really can carry one. url.PathEscape puts it back on
// the wire harmlessly, but the same string is also written into a TrackTask
// description and an audit row with %s, and view:audit is a Viewer-level
// permission. The guard is what keeps a newline or an ANSI escape out of text
// other people read.
func TestPathSegmentRefusesControlCharacters(t *testing.T) {
	for _, value := range []string{"a\x00b", "a\nb", "a\rb", "a\x1bb", "a\x7fb"} {
		t.Run(url.PathEscape(value), func(t *testing.T) {
			if err := validatePathSegment("thing", value); err == nil {
				t.Errorf("validatePathSegment(%q) = nil, want an error", value)
			}
			if err := validatePathSegmentAllowingSlash("thing", value); err == nil {
				t.Errorf("validatePathSegmentAllowingSlash(%q) = nil, want an error", value)
			}

			// And through the exported surface, which is where it matters:
			// nothing may be sent.
			srv, seen := newCaptureServer(t, `{"data":"UPID:pve-01:0:0:0:cephdestroypool::root@pam:"}`)
			c := newTestClient(t, srv.URL)
			if _, err := c.DeleteCephPool(context.Background(), "pve-01", value); err == nil {
				t.Errorf("DeleteCephPool(%q) succeeded, want rejection", value)
			}
			if len(*seen) != 0 {
				t.Errorf("issued %d request(s) %v, want none", len(*seen), *seen)
			}
		})
	}
}

// TestCephPoolNamePathEscapesTheNameExactlyOnce is the same pin on the Ceph
// side. Every name here is one PVE's own rule admits and this API can now
// create, and each contains a character url.PathEscape encodes — which is
// exactly the set that used to be escaped a second time.
func TestCephPoolNamePathEscapesTheNameExactlyOnce(t *testing.T) {
	const node = "pve-01"

	// The golden anchor, for the reason given on the IP set test above.
	if want := "/api2/json/nodes/pve-01/ceph/pool/a%23b"; want !=
		"/api2/json/nodes/"+node+"/ceph/pool/"+url.PathEscape("a#b") {
		t.Fatalf("url.PathEscape no longer encodes \"#\" as %%23; want %q", want)
	}

	for _, pool := range []string{"a#b", "a%b", "a?b", "a b", "100%", "rbd", ".mgr"} {
		t.Run(pool, func(t *testing.T) {
			want := "/api2/json/nodes/" + node + "/ceph/pool/" + url.PathEscape(pool)

			srv, seen := newCaptureServer(t, `{"data":"UPID:pve-01:0:0:0:cephdestroypool::root@pam:"}`)
			c := newTestClient(t, srv.URL)

			if _, err := c.DeleteCephPool(context.Background(), node, pool); err != nil {
				t.Fatalf("DeleteCephPool(%q): %v", pool, err)
			}
			if len(*seen) != 1 || (*seen)[0] != want {
				t.Fatalf("request target = %v, want [%s]", *seen, want)
			}
		})
	}
}
