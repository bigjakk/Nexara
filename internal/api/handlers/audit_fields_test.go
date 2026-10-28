package handlers

import (
	"encoding/json"
	"net/url"
	"slices"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// An update's fields are the names it sends and clears, never their values:
// a set field of the request, embedded ones included, a form's keys, the
// names its delete list takes out and those named beside it — never digest.
func TestAuditFieldNames(t *testing.T) {
	t.Parallel()
	port := 0
	for _, tc := range []struct {
		name   string
		params any
		also   []string
		want   []string
	}{
		{"a set field, an unset one, a pointer to zero, a delete list", proxmox.UpdateMetricServerParams{
			Server: "192.0.2.30", Port: &port, Delete: "mtu, timeout;path"}, nil,
			[]string{"mtu", "path", "port", "server", "timeout"}},
		{"embedded options and a delete slice", proxmox.UpdateNetworkInterfaceParams{Type: "bridge",
			NetworkInterfaceOptions: proxmox.NetworkInterfaceOptions{CIDR: "192.0.2.10/24"}, Delete: []string{"gateway"}},
			nil, []string{"cidr", "gateway", "type"}},
		{"the digest is no field", proxmox.UpdateACMEPluginParams{API: "cf", Digest: "d1"}, nil, []string{"api"}},
		{"a form and what is cleared beside it", url.Values{"content": {"images"}, "delete": {"nodes"}, "digest": {"d1"}},
			[]string{"shared"}, []string{"content", "nodes", "shared"}},
		{"nothing sent", proxmox.UpdateSDNZoneParams{}, nil, []string{}},
	} {
		if got := auditFieldNames(tc.params, tc.also...); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// An update that read its object first names only what its write changes: a
// field sent with another value than the one read, or one cleared that the
// object had. A comma list is compared in any order, a read value without the
// space around it, a field the object leaves out as Proxmox's default; with
// nothing read, every name.
func TestAuditSentChanged(t *testing.T) {
	t.Parallel()
	read := map[string]json.RawMessage{"mtu": json.RawMessage(`1450`), "nodes": json.RawMessage(`"pve-02,pve-01"`),
		"comments": json.RawMessage(`"uplink\n"`), "gateway": json.RawMessage(`"192.0.2.1"`), "vlan-aware": json.RawMessage(`null`)}
	sent := auditSent{values: map[string]string{"mtu": "1450", "nodes": "pve-01,pve-02", "comments": "uplink",
		"bridge_ports": "eno1", "autostart": "0"}, cleared: []string{"gateway", "vlan-aware", "slaves"}}
	for _, tc := range []struct {
		name   string
		before map[string]json.RawMessage
		absent map[string]string
		want   []string
	}{
		{"read", read, map[string]string{"autostart": "0"}, []string{"bridge_ports", "gateway"}},
		{"no default for a field left out", read, nil, []string{"autostart", "bridge_ports", "gateway"}},
		{"nothing read", nil, nil, []string{"autostart", "bridge_ports", "comments", "gateway", "mtu", "nodes", "slaves", "vlan-aware"}},
	} {
		if got := sent.changed(tc.before, tc.absent); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := auditSentOf(proxmox.UpdateNetworkInterfaceParams{Type: "bridge"}, "autostart").values; got["autostart"] != "0" ||
		got["type"] != "bridge" || len(got) != 2 {
		t.Errorf("a field always sent: %v, want autostart 0 beside type", got)
	}
}
