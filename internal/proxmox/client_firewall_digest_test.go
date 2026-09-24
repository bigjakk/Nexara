package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// fwDigestRequest is one request the capture server saw.
type fwDigestRequest struct {
	method, path string
	query, form  url.Values
}

func newFWDigestCaptureServer(t *testing.T) (*httptest.Server, *[]fwDigestRequest) {
	t.Helper()
	var seen []fwDigestRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		seen = append(seen, fwDigestRequest{r.Method, r.URL.Path, r.URL.Query(), r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// TestFirewallRuleWritesSendTheDigestOnlyWhenSet covers the eight positional
// rule writes. pve-firewall's update_rule reads `digest` from the PUT's form
// and delete_rule from the DELETE's parameters (src/PVE/API2/Firewall/Rules.pm);
// PVE::Tools::assert_if_modified compares only when both digests are
// non-empty, so an empty one must be no key at all — the unconditional write
// these methods made before they took a digest.
func TestFirewallRuleWritesSendTheDigestOnlyWhenSet(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef01234567"
	rule := FirewallRuleParams{Type: "in", Action: "ACCEPT", Enable: 1}
	for _, tt := range []struct {
		name   string
		method string
		path   string
		call   func(c *Client, digest string) error
	}{
		{"cluster update", http.MethodPut, "/api2/json/cluster/firewall/rules/3",
			func(c *Client, d string) error { return c.UpdateClusterFirewallRule(context.Background(), 3, rule, d) }},
		{"cluster delete", http.MethodDelete, "/api2/json/cluster/firewall/rules/3",
			func(c *Client, d string) error { return c.DeleteClusterFirewallRule(context.Background(), 3, d) }},
		{"node update", http.MethodPut, "/api2/json/nodes/pve-01/firewall/rules/3",
			func(c *Client, d string) error {
				return c.UpdateNodeFirewallRule(context.Background(), "pve-01", 3, rule, d)
			}},
		{"node delete", http.MethodDelete, "/api2/json/nodes/pve-01/firewall/rules/3",
			func(c *Client, d string) error { return c.DeleteNodeFirewallRule(context.Background(), "pve-01", 3, d) }},
		{"guest update", http.MethodPut, "/api2/json/nodes/pve-01/qemu/101/firewall/rules/3",
			func(c *Client, d string) error {
				return c.UpdateVMFirewallRule(context.Background(), "pve-01", "qemu", 101, 3, rule, d)
			}},
		{"guest delete", http.MethodDelete, "/api2/json/nodes/pve-01/qemu/101/firewall/rules/3",
			func(c *Client, d string) error {
				return c.DeleteVMFirewallRule(context.Background(), "pve-01", "qemu", 101, 3, d)
			}},
		{"security group update", http.MethodPut, "/api2/json/cluster/firewall/groups/sg01/3",
			func(c *Client, d string) error {
				return c.UpdateSecurityGroupRule(context.Background(), "sg01", 3, rule, d)
			}},
		{"security group delete", http.MethodDelete, "/api2/json/cluster/firewall/groups/sg01/3",
			func(c *Client, d string) error { return c.DeleteSecurityGroupRule(context.Background(), "sg01", 3, d) }},
	} {
		for _, sent := range []string{digest, ""} {
			t.Run(tt.name+"/digest="+sent, func(t *testing.T) {
				srv, seen := newFWDigestCaptureServer(t)
				if err := tt.call(newTestClient(t, srv.URL), sent); err != nil {
					t.Fatalf("call: %v", err)
				}
				if len(*seen) != 1 {
					t.Fatalf("issued %d requests, want 1", len(*seen))
				}
				req := (*seen)[0]
				if req.method != tt.method || req.path != tt.path {
					t.Fatalf("sent %s %s, want %s %s", req.method, req.path, tt.method, tt.path)
				}
				// Where Proxmox reads it, and never the other place.
				where, other := req.form, req.query
				if tt.method == http.MethodDelete {
					where, other = req.query, req.form
				}
				if _, ok := other["digest"]; ok {
					t.Errorf("the digest travelled in the wrong place: query %v, form %v", req.query, req.form)
				}
				got, ok := where["digest"]
				switch {
				case sent == "" && ok:
					t.Errorf("an empty digest was sent as %q; it must be no key at all", got)
				case sent != "" && (!ok || len(got) != 1 || got[0] != sent):
					t.Errorf("digest = %q (present %t), want exactly %q", got, ok, sent)
				}
				// The control: an update still carries the rule it was
				// given, so a form that lost everything cannot pass the
				// absence check above by being empty.
				if tt.method == http.MethodPut && req.form.Get("action") != "ACCEPT" {
					t.Errorf("the update's form lost the rule: %v", req.form)
				}
			})
		}
	}
}

// TestFirewallRuleCarriesTheListDigest: the listing's per-rule digest reaches
// FirewallRule, which is how it gets to the SPA to be sent back.
func TestFirewallRuleCarriesTheListDigest(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/cluster/firewall/rules": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]any{
				{"pos": 0, "type": "in", "action": "ACCEPT", "enable": 1, "digest": "aa11"},
				{"pos": 1, "type": "out", "action": "DROP", "enable": 0, "digest": "aa11"},
			})
		},
	})
	t.Cleanup(srv.Close)
	rules, err := newTestClient(t, srv.URL).GetClusterFirewallRules(context.Background())
	if err != nil {
		t.Fatalf("GetClusterFirewallRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	for _, r := range rules {
		if r.Digest != "aa11" {
			t.Errorf("rule %d digest = %q, want aa11", r.Pos, r.Digest)
		}
	}
}
