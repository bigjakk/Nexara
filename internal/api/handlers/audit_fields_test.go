package handlers

import (
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
