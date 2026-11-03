package collector

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut/nuttest"
)

func TestNodeAddrs(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	nodes := newNodeAddrs([]db.ListNodeLoginAddressesRow{
		{ClusterID: a, NodeName: "pve-01", Address: "192.0.2.11"},    // its cluster address
		{ClusterID: a, NodeName: "pve-01", Address: "198.51.100.11"}, // an interface
		{ClusterID: a, NodeName: "pve-02", Address: "2001:db8::12"},  // a cluster address over IPv6
		{ClusterID: a, NodeName: "pve-02", Address: "192.0.2.50"},    // shared with
		{ClusterID: b, NodeName: "pve-03", Address: "192.0.2.50"},    // a node of another cluster
		{ClusterID: b, NodeName: "pve-01", Address: "192.0.2.21"},    // the same name, another cluster
		{ClusterID: a, NodeName: "pve-04", Address: "pve-04.example.com"},
		{ClusterID: a, NodeName: "pve-04", Address: "127.0.1.1"}, // Debian's hostname address
	})
	pve01, pve02, otherPve01 := nodeRef{a, "pve-01"}, nodeRef{a, "pve-02"}, nodeRef{b, "pve-01"}
	for _, tc := range []struct {
		login string
		host  *nodeRef
		want  *nodeRef
	}{
		{"192.0.2.11", nil, &pve01},
		{"198.51.100.11", nil, &pve01},
		{"::ffff:192.0.2.11", nil, &pve01},
		{"2001:db8::12", nil, &pve02},
		{"192.0.2.21", nil, &otherPve01},
		{"192.0.2.50", nil, nil}, // shared: names neither
		{"192.0.2.99", nil, nil},
		{"127.0.0.1", &pve01, &pve01}, // the NUT host's own upsmon
		{"::1", &pve01, &pve01},
		{"127.0.0.1", nil, nil}, // and no host known
	} {
		got, ok := nodes.loginNode(netip.MustParseAddr(tc.login), tc.host)
		if ok != (tc.want != nil) || ok && got != *tc.want {
			t.Errorf("loginNode(%s, host %v) = %v, %v; want %v", tc.login, tc.host, got, ok, tc.want)
		}
	}
	for _, tc := range []struct {
		remote netip.Addr
		want   *nodeRef
	}{
		{netip.MustParseAddr("192.0.2.11"), &pve01},
		{netip.MustParseAddr("192.0.2.50"), nil},
		{netip.MustParseAddr("127.0.1.1"), nil}, // a loopback address names no node
		{netip.Addr{}, nil},
	} {
		got, ok := nodes.hostNode(tc.remote)
		if ok != (tc.want != nil) || ok && got != *tc.want {
			t.Errorf("hostNode(%v) = %v, %v; want %v", tc.remote, got, ok, tc.want)
		}
	}
}

// Logins are listed once a minute, and only for a UPS read fresh on mains: the
// logouts of an outage must not count. A login from a node's address, or the
// NUT host's own over loopback, finds the node, once.
func TestNutPoller_FindsWhatEachUpsPowers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := nuttest.NewServer(t)
	fake.SetDevice("ups01", nuttest.Device{Vars: map[string]string{"ups.status": "OL"},
		Clients: []string{"192.0.2.11", "192.0.2.11", "127.0.0.1", "192.0.2.99"}})
	fake.SetDevice("ups02", nuttest.Device{Vars: map[string]string{"ups.status": "OB DISCHRG"},
		Clients: []string{"192.0.2.12"}})
	host, port := fake.Addr()
	cluster := uuid.New()
	q := newFakeNutQueries(db.NutServer{ID: uuid.New(), Name: "nut01", Host: host, Port: int32(port), TlsMode: "off", Enabled: true,
		HostClusterID: pgtype.UUID{Bytes: cluster, Valid: true}, HostNodeName: pgtype.Text{String: "pve-03", Valid: true}})
	q.nodeAddrs = []db.ListNodeLoginAddressesRow{
		{ClusterID: cluster, NodeName: "pve-01", Address: "192.0.2.11"},
		{ClusterID: cluster, NodeName: "pve-02", Address: "192.0.2.12"},
	}
	p := NewNutPoller(q, "", NutPollerConfig{Interval: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := time.Date(2026, 11, 3, 3, 12, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	st := p.loadServerState(ctx, q.server)
	defer st.closeConn()
	tick := func(d time.Duration) { clock = clock.Add(d); p.poll(ctx, st) }

	tick(0)
	tick(time.Second) // not due again yet
	if got := q.listings["ups01"]; len(got) != 1 || !slices.Equal(got[0], []string{"192.0.2.11", "192.0.2.11", "127.0.0.1", "192.0.2.99"}) {
		t.Fatalf("ups01 listings = %q; want one, every connection", got)
	}
	if got := q.listings["ups02"]; got != nil {
		t.Fatalf("ups02 on battery was listed: %q", got)
	}
	if got := slices.Sorted(slices.Values(q.feeds["ups01"])); !slices.Equal(got, []string{"pve-01", "pve-03"}) {
		t.Fatalf("ups01 feeds = %q; want pve-01 by address and pve-03, the host, by loopback", got)
	}

	fake.SetVar("ups02", "ups.status", "OL")
	tick(time.Minute)
	if len(q.listings["ups01"]) != 2 || q.marked["ups01"] != 2 || !slices.Equal(q.feeds["ups02"], []string{"pve-02"}) || q.feedCalls != 3 {
		t.Errorf("a minute later: %d ups01 listings, %d marked, ups02 feeds %q, %d feed inserts; want 2, 2, pve-02, 3",
			len(q.listings["ups01"]), q.marked["ups01"], q.feeds["ups02"], q.feedCalls)
	}

	// A stale driver keeps its last state, but is not read fresh, so it holds.
	fake.Fail("LIST VAR ups01", "DATA-STALE")
	tick(time.Minute)
	if n := len(q.listings["ups01"]); n != 2 {
		t.Errorf("stale ups01 was listed: %d listings, want still 2", n)
	}
}
