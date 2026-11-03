package collector

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut"
)

// nutClientRetention is how long a login no listing has shown stays stored,
// so a UPS page can say when a host was last logged in.
const nutClientRetention = 7 * 24 * time.Hour

// nodeRef names a node the way a feed does: by cluster and name, not by row.
type nodeRef struct {
	cluster uuid.UUID
	name    string
}

// nodeAddrs maps each address a node can log in to upsd from to that node. An
// address two nodes share names neither, so it is left out.
type nodeAddrs map[netip.Addr]nodeRef

func newNodeAddrs(rows []db.ListNodeLoginAddressesRow) nodeAddrs {
	out := nodeAddrs{}
	shared := map[netip.Addr]bool{}
	for _, r := range rows {
		a, err := netip.ParseAddr(strings.TrimSpace(r.Address))
		if err != nil {
			continue
		}
		a = canonicalAddr(a)
		ref := nodeRef{cluster: r.ClusterID, name: r.NodeName}
		if prev, ok := out[a]; ok && prev != ref {
			shared[a] = true
		}
		out[a] = ref
	}
	for a := range shared {
		delete(out, a)
	}
	return out
}

// canonicalAddr makes two spellings of one address compare equal: an
// IPv6-mapped IPv4 address is unmapped and a zone dropped (upsd prints none).
func canonicalAddr(a netip.Addr) netip.Addr {
	return a.Unmap().WithZone("")
}

// loginNode is the node a login from addr comes from. A loopback login is the
// NUT host's own upsmon, so it is the host node, when there is one.
func (n nodeAddrs) loginNode(addr netip.Addr, host *nodeRef) (nodeRef, bool) {
	addr = canonicalAddr(addr)
	if addr.IsLoopback() {
		if host == nil {
			return nodeRef{}, false
		}
		return *host, true
	}
	ref, ok := n[addr]
	return ref, ok
}

// hostNode is the node a NUT server runs on: the one whose address the
// poller's connection reached. A loopback address names none.
func (n nodeAddrs) hostNode(remote netip.Addr) (nodeRef, bool) {
	remote = canonicalAddr(remote)
	if !remote.IsValid() || remote.IsLoopback() {
		return nodeRef{}, false
	}
	ref, ok := n[remote]
	return ref, ok
}

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
func (p *NutPoller) loginNodes(ctx context.Context, st *nutServerState) (nodeAddrs, *nodeRef) {
	rows, err := p.q.ListNodeLoginAddresses(ctx)
	if err != nil {
		p.logger.Warn("nut: listing node addresses failed", "server", st.srv.Name, "error", err)
	}
	nodes := newNodeAddrs(rows)
	srv, err := p.q.GetNutServer(ctx, st.srv.ID)
	if err != nil {
		p.logger.Warn("nut: reading the server's host node failed", "server", st.srv.Name, "error", err)
		return nodes, nil
	}
	if srv.HostClusterID.Valid && srv.HostNodeName.Valid {
		return nodes, &nodeRef{cluster: uuid.UUID(srv.HostClusterID.Bytes), name: srv.HostNodeName.String}
	}
	ref, ok := nodes.hostNode(st.conn.RemoteAddr())
	if !ok {
		return nodes, nil
	}
	if err := p.q.SetNutServerHostNode(ctx, db.SetNutServerHostNodeParams{ID: st.srv.ID, ClusterID: ref.cluster, NodeName: ref.name}); err != nil {
		p.logger.Warn("nut: storing the server's host node failed", "server", st.srv.Name, "error", err)
	}
	return nodes, &ref
}

// recordClients stores one UPS's listing and the feeds it finds. A node already
// fed, or one the operator removed, is left as it is.
func (p *NutPoller) recordClients(ctx context.Context, d *upsDeviceState, addrs []netip.Addr, nodes nodeAddrs, host *nodeRef, now time.Time) bool {
	// One entry per connection, as upsd lists them; the query counts repeats.
	raw := make([]string, len(addrs))
	count := map[netip.Addr]int16{}
	for i, a := range addrs {
		a = canonicalAddr(a)
		raw[i] = a.String()
		count[a]++
	}
	if err := p.q.UpsertUpsClients(ctx, db.UpsertUpsClientsParams{UpsID: d.id, Addresses: raw, SeenAt: now}); err != nil {
		p.logger.Warn("nut: storing logins failed", "ups", d.name, "error", err)
		return false
	}
	if err := p.q.MarkUpsClientsMissed(ctx, db.MarkUpsClientsMissedParams{UpsID: d.id, Addresses: raw}); err != nil {
		p.logger.Warn("nut: storing missed logins failed", "ups", d.name, "error", err)
	}
	if err := p.q.PruneUpsClients(ctx, db.PruneUpsClientsParams{UpsID: d.id, Before: now.Add(-nutClientRetention)}); err != nil {
		p.logger.Warn("nut: pruning old logins failed", "ups", d.name, "error", err)
	}
	changed := !maps.Equal(d.clients, count)
	d.clients = count
	for _, a := range slices.SortedFunc(maps.Keys(count), netip.Addr.Compare) {
		ref, ok := nodes.loginNode(a, host)
		if !ok || d.fed[ref] {
			continue
		}
		n, err := p.q.InsertFoundUpsFeed(ctx, db.InsertFoundUpsFeedParams{UpsID: d.id, ClusterID: ref.cluster, NodeName: ref.name})
		if err != nil {
			p.logger.Warn("nut: storing a found feed failed", "ups", d.name, "node", ref.name, "error", err)
			continue
		}
		d.fed[ref] = true
		if n > 0 {
			p.logger.Info("nut: found a node a UPS powers", "ups", d.name, "node", ref.name)
			changed = true
		}
	}
	return changed
}
