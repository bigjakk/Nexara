package proxmox

import (
	"context"
	"testing"
)

// These pin the premise two declarations in internal/api rest on: an EMPTY
// value for these update fields reaches Proxmox as no field at all, so "" leaves
// the current value alone. registry_firewall.go (the alias rename) and
// registry_sdn.go (the VNet zone) accept "" through pveObjectNameOrEmptyParam
// and publish it as "keeps the current one"; the schema can only admit the
// value, and it is these methods that make the published meaning true. They are
// TestCreateACMEAccountOmitsAnEmptyName's siblings, for the two update routes.

func TestUpdateFirewallAliasOmitsAnEmptyRename(t *testing.T) {
	for _, tt := range []struct {
		name    string
		rename  string
		wantKey bool
	}{
		{"an empty rename is omitted entirely", "", false},
		{"a new name is sent", "alias02", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newFormCaptureServer(t)
			c := newTestClient(t, srv.URL)

			if err := c.UpdateFirewallAlias(context.Background(), "alias01", FirewallAliasParams{
				CIDR:   "192.0.2.0/24",
				Rename: tt.rename,
			}); err != nil {
				t.Fatalf("UpdateFirewallAlias: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("issued %d requests, want 1", len(*seen))
			}
			form := (*seen)[0]
			if _, ok := form["rename"]; ok != tt.wantKey {
				t.Errorf("form carries a rename key = %t, want %t (value %q) — an empty rename sent as "+
					"`rename=` is a value to Proxmox, not \"keep the name\"", ok, tt.wantKey, form.Get("rename"))
			}
			if tt.wantKey && form.Get("rename") != tt.rename {
				t.Errorf("rename = %q, want %q", form.Get("rename"), tt.rename)
			}
			// The control: the field that is always sent is there, so a form
			// that lost everything cannot pass the check above by being empty.
			if got := form.Get("cidr"); got != "192.0.2.0/24" {
				t.Errorf("cidr = %q, want it sent alongside", got)
			}
		})
	}
}

func TestUpdateSDNVNetOmitsAnEmptyZone(t *testing.T) {
	for _, tt := range []struct {
		name    string
		zone    string
		wantKey bool
	}{
		{"an empty zone is omitted entirely", "", false},
		{"a new zone is sent", "zone02", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newFormCaptureServer(t)
			c := newTestClient(t, srv.URL)

			if err := c.UpdateSDNVNet(context.Background(), "vnet01", UpdateSDNVNetParams{
				Zone:  tt.zone,
				Alias: "uplink",
			}); err != nil {
				t.Fatalf("UpdateSDNVNet: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("issued %d requests, want 1", len(*seen))
			}
			form := (*seen)[0]
			if _, ok := form["zone"]; ok != tt.wantKey {
				t.Errorf("form carries a zone key = %t, want %t (value %q) — an empty zone sent as "+
					"`zone=` is a value to Proxmox, not \"keep the zone\"", ok, tt.wantKey, form.Get("zone"))
			}
			if tt.wantKey && form.Get("zone") != tt.zone {
				t.Errorf("zone = %q, want %q", form.Get("zone"), tt.zone)
			}
			if got := form.Get("alias"); got != "uplink" {
				t.Errorf("alias = %q, want it sent alongside", got)
			}
		})
	}
}
