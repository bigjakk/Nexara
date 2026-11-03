// Package upsfeed decides which node a upsmon login comes from, and whether a
// node a UPS powers is protected: whether a upsmon on it is logged in to the
// UPS, so the node shuts itself down before the battery runs out. The poller
// finds feeds with it and the API judges them, from one copy of the rules.
package upsfeed

import (
	"net/netip"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Node names a node the way a feed does: by cluster and name, not by row.
type Node struct {
	Cluster uuid.UUID
	Name    string
}

// HostNode is a NUT server's stored host node, nil when it has none. A name
// left behind by a deleted cluster (host_cluster_id NULL) means nothing.
func HostNode(cluster pgtype.UUID, name pgtype.Text) *Node {
	if !cluster.Valid || !name.Valid {
		return nil
	}
	return &Node{Cluster: uuid.UUID(cluster.Bytes), Name: name.String}
}

// Nodes maps each address a node can log in to upsd from to that node.
type Nodes struct {
	byAddr map[netip.Addr]Node
	known  map[Node]bool
}

// NewNodes builds the map from ListNodeLoginAddresses. An address two nodes
// share names neither, since a login from it could be either; a node left
// with no address of its own is not known.
func NewNodes(rows []db.ListNodeLoginAddressesRow) Nodes {
	byAddr := map[netip.Addr]Node{}
	shared := map[netip.Addr]bool{}
	for _, r := range rows {
		a, err := netip.ParseAddr(strings.TrimSpace(r.Address))
		if err != nil {
			continue
		}
		a = Canonical(a)
		n := Node{Cluster: r.ClusterID, Name: r.NodeName}
		if prev, ok := byAddr[a]; ok && prev != n {
			shared[a] = true
		}
		byAddr[a] = n
	}
	known := map[Node]bool{}
	for a, n := range byAddr {
		if shared[a] {
			delete(byAddr, a)
			continue
		}
		known[n] = true
	}
	return Nodes{byAddr: byAddr, known: known}
}

// Canonical makes two spellings of one address compare equal: an IPv6-mapped
// IPv4 address is unmapped and a zone dropped (upsd prints none).
func Canonical(a netip.Addr) netip.Addr {
	return a.Unmap().WithZone("")
}

// Login is the node a login from addr comes from. A loopback login is the NUT
// host's own upsmon, so it is the host node, when there is one.
func (n Nodes) Login(addr netip.Addr, host *Node) (Node, bool) {
	addr = Canonical(addr)
	if addr.IsLoopback() {
		if host == nil {
			return Node{}, false
		}
		return *host, true
	}
	node, ok := n.byAddr[addr]
	return node, ok
}

// Host is the node a NUT server runs on: the one whose address the poller's
// connection reached. A loopback address names none.
func (n Nodes) Host(remote netip.Addr) (Node, bool) {
	remote = Canonical(remote)
	if !remote.IsValid() || remote.IsLoopback() {
		return Node{}, false
	}
	node, ok := n.byAddr[remote]
	return node, ok
}

// Verdict is what Nexara can say about a node a UPS powers.
type Verdict string

const (
	// Protected: a upsmon on the node is logged in to the UPS.
	Protected Verdict = "protected"
	// NotProtected: none is, so the node would lose power without shutting down.
	NotProtected Verdict = "not_protected"
	// Elsewhere: the operator says it shuts down some way Nexara cannot see (P11).
	Elsewhere Verdict = "checked_elsewhere"
	// Offline: the node is down, so there is nothing to judge (P12).
	Offline Verdict = "node_offline"
	// Unchecked: no listing yet, or Nexara cannot tell this node's logins apart.
	Unchecked Verdict = "unchecked"
)

// MissedLimit is the number of listings in a row a login can miss and still
// count: a vanished host stays listed for up to 60 s, and a restarting upsmon
// is briefly absent (P4).
const MissedLimit = 2

// Login is one stored login of a UPS.
type Login struct {
	Addr   netip.Addr
	Missed int
}

// Judge says whether one node a UPS powers is protected. status is the node's
// status in Nexara, "" when it has no such node. listed is whether the UPS has
// had a listing; its logins hold at the last one, which the poller takes only
// on mains, because in an outage every secondary logs out as it shuts down.
func Judge(node Node, elsewhere bool, status string, listed bool, logins []Login, nodes Nodes, host *Node) (Verdict, netip.Addr) {
	isHost := host != nil && *host == node
	switch {
	case elsewhere:
		return Elsewhere, netip.Addr{}
	case status == "offline":
		return Offline, netip.Addr{}
	case status != "online", !listed, !nodes.known[node] && !isHost:
		return Unchecked, netip.Addr{}
	}
	for _, l := range logins {
		if l.Missed >= MissedLimit {
			continue
		}
		if n, ok := nodes.Login(l.Addr, host); ok && n == node {
			return Protected, Canonical(l.Addr)
		}
	}
	return NotProtected, netip.Addr{}
}
