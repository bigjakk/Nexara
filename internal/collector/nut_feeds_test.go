package collector

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut/nuttest"
)

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
	// The verdicts are judged as of the last listing, so each one is stamped.
	if got := q.listedAt["ups01"]; !got.Equal(clock.Add(-time.Minute)) {
		t.Errorf("ups01 listed at %v, want the previous minute's listing", got)
	}
}
