package collector

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/upsfeed"
)

// nutClientRetention is how long a login no listing has shown stays stored,
// so a UPS page can say when a host was last logged in.
const nutClientRetention = 7 * 24 * time.Hour

// listClients records who is logged in to each UPS, and the nodes they log in
// from, once a listing interval. A UPS not read fresh and on mains this poll
// keeps its last listing: in an outage every secondary logs out on purpose as
// it shuts down, so only logins seen on mains say whether a node is protected.
func (p *NutPoller) listClients(ctx context.Context, st *nutServerState, now time.Time) bool {
	if !st.clientsAt.IsZero() && now.Sub(st.clientsAt) < nutListInterval {
		return false
	}
	var due []*upsDeviceState
	for _, name := range sortedDeviceNames(st.devices) {
		if d := st.devices[name]; d.lastFresh.Equal(now) && d.prev.State == nut.StateOnline {
			due = append(due, d)
		}
	}
	if len(due) == 0 {
		return false
	}
	st.clientsAt = now
	nodes, host := p.loginNodes(ctx, st)
	changed := false
	for _, d := range due {
		addrs, err := st.conn.ListClients(ctx, d.name)
		var pe *nut.ProtocolError
		switch {
		case errors.As(err, &pe):
			continue
		case err != nil:
			// The readings came through, so the server is not unreachable. If
			// the connection broke, the next poll redials.
			return changed
		}
		changed = p.recordClients(ctx, d, addrs, nodes, host, now) || changed
	}
	return changed
}

// loginNodes loads the addresses logins are matched against, and the node the
// NUT server runs on: as stored, or found now from the address the connection
// reached and stored for next time.
func (p *NutPoller) loginNodes(ctx context.Context, st *nutServerState) (upsfeed.Nodes, *upsfeed.Node) {
	rows, err := p.q.ListNodeLoginAddresses(ctx)
	if err != nil {
		p.logger.Warn("nut: listing node addresses failed", "server", st.srv.Name, "error", err)
	}
	nodes := upsfeed.NewNodes(rows)
	srv, err := p.q.GetNutServer(ctx, st.srv.ID)
	if err != nil {
		p.logger.Warn("nut: reading the server's host node failed", "server", st.srv.Name, "error", err)
		return nodes, nil
	}
	if host := upsfeed.HostNode(srv.HostClusterID, srv.HostNodeName); host != nil {
		return nodes, host
	}
	ref, ok := nodes.Host(st.conn.RemoteAddr())
	if !ok {
		return nodes, nil
	}
	// With the address this worker dialled: if an edit has replaced it, the
	// store is refused, and the restarted worker finds the new host.
	if err := p.q.SetNutServerHostNode(ctx, db.SetNutServerHostNodeParams{
		ID: st.srv.ID, ClusterID: ref.Cluster, NodeName: ref.Name, Host: st.srv.Host, Port: st.srv.Port,
	}); err != nil {
		p.logger.Warn("nut: storing the server's host node failed", "server", st.srv.Name, "error", err)
	}
	return nodes, &ref
}

// recordClients stores one UPS's listing and the feeds it finds. A node already
// fed, or one the operator removed, is left as it is.
func (p *NutPoller) recordClients(ctx context.Context, d *upsDeviceState, addrs []netip.Addr, nodes upsfeed.Nodes, host *upsfeed.Node, now time.Time) bool {
	// One entry per connection, as upsd lists them; the query counts repeats.
	raw := make([]string, len(addrs))
	count := map[netip.Addr]int16{}
	for i, a := range addrs {
		a = upsfeed.Canonical(a)
		raw[i] = a.String()
		count[a]++
	}
	if err := p.q.UpsertUpsClients(ctx, db.UpsertUpsClientsParams{UpsID: d.id, Addresses: raw, SeenAt: now}); err != nil {
		p.logger.Warn("nut: storing logins failed", "ups", d.name, "error", err)
		return false
	}
	if err := p.q.MarkUpsClientsMissed(ctx, db.MarkUpsClientsMissedParams{UpsID: d.id, Addresses: raw}); err != nil {
		p.logger.Warn("nut: storing missed logins failed", "ups", d.name, "error", err)
		return false
	}
	// Only a listing stored whole stamps the time the verdicts are judged at.
	if err := p.q.SetUpsClientsListed(ctx, db.SetUpsClientsListedParams{ID: d.id, ListedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		p.logger.Warn("nut: storing the listing time failed", "ups", d.name, "error", err)
	}
	if err := p.q.PruneUpsClients(ctx, db.PruneUpsClientsParams{UpsID: d.id, Before: now.Add(-nutClientRetention)}); err != nil {
		p.logger.Warn("nut: pruning old logins failed", "ups", d.name, "error", err)
	}
	changed := !maps.Equal(d.clients, count)
	d.clients = count
	for _, a := range slices.SortedFunc(maps.Keys(count), netip.Addr.Compare) {
		ref, ok := nodes.Login(a, host)
		if !ok || d.fed[ref] {
			continue
		}
		n, err := p.q.InsertFoundUpsFeed(ctx, db.InsertFoundUpsFeedParams{UpsID: d.id, ClusterID: ref.Cluster, NodeName: ref.Name})
		if err != nil {
			p.logger.Warn("nut: storing a found feed failed", "ups", d.name, "node", ref.Name, "error", err)
			continue
		}
		d.fed[ref] = true
		if n > 0 {
			p.logger.Info("nut: found a node a UPS powers", "ups", d.name, "node", ref.Name)
			changed = true
		}
	}
	return changed
}
