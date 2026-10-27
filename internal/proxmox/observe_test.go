package proxmox

import (
	"context"
	"testing"
)

// Each observe target reads the path it names, the guest configuration in its
// pending-applied view (no current=1), and a target built from an unusable
// segment is refused before anything is sent.
func TestObserveTargets(t *testing.T) {
	t.Parallel()
	must := func(target ObserveTarget, err error) ObserveTarget {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	cases := []struct {
		target    ObserveTarget
		wantPath  string
		wantQuery string
	}{
		{ObservePermissionsRoot(), "/api2/json/access/permissions", "path=%2F"},
		{must(ObservePool("team/web")), "/api2/json/pools", "poolid=team%2Fweb"},
		{must(ObserveMetricServer("influx01")), "/api2/json/cluster/metrics/server/influx01", ""},
		{must(ObserveNodeNetwork("pve-01")), "/api2/json/nodes/pve-01/network", ""},
		{must(ObserveGuestConfig("pve-01", "qemu", 100)), "/api2/json/nodes/pve-01/qemu/100/config", ""},
		{must(ObserveGuestConfig("pve-01", "lxc", 101)), "/api2/json/nodes/pve-01/lxc/101/config", ""},
	}
	for _, tc := range cases {
		got := requireSentOnce(t, func(c *Client) error {
			_, err := c.ObserveRead(context.Background(), tc.target)
			return err
		})
		if got.method != "GET" || got.path != tc.wantPath || got.query.Encode() != tc.wantQuery {
			t.Errorf("%s: sent %s %s?%s, want GET %s?%s", tc.target, got.method, got.path, got.query.Encode(),
				tc.wantPath, tc.wantQuery)
		}
	}

	for name, build := range map[string]func() (ObserveTarget, error){
		"dot node":     func() (ObserveTarget, error) { return ObserveNodeDNS(".") },
		"slashed node": func() (ObserveTarget, error) { return ObserveGuestConfig("pve-01/..", "qemu", 100) },
		"guest type":   func() (ObserveTarget, error) { return ObserveGuestConfig("pve-01", "openvz", 100) },
		"empty pool":   func() (ObserveTarget, error) { return ObservePool("") },
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s: built", name)
		}
	}
	requireRefusedUnsent(t, func(c *Client) error {
		_, err := c.ObserveRead(context.Background(), ObserveTarget{})
		return err
	}, ErrInvalidInput)
}

// ObserveRead hands back Proxmox's data untouched, numbers and order as sent.
func TestObserveReadKeepsTheRawData(t *testing.T) {
	t.Parallel()
	srv, _ := newRecordingServer(t, `{"data":[{"storage":"store01","shared":1,"content":"images,iso"}]}`)
	got, err := newTestClient(t, srv.URL).ObserveRead(context.Background(), ObserveStorage())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[{"storage":"store01","shared":1,"content":"images,iso"}]` {
		t.Fatalf("data = %s", got)
	}
}
