package handlers

import (
	"net/netip"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/upsfeed"
)

// upsFeedResponse is one thing a UPS powers: a node, with whether it would
// shut down cleanly, or a load that is not a node, such as a switch.
type upsFeedResponse struct {
	ID                 uuid.UUID  `json:"id"`
	Kind               string     `json:"kind"` // node | label
	ClusterID          *uuid.UUID `json:"cluster_id"`
	ClusterName        string     `json:"cluster_name"`
	NodeName           string     `json:"node_name"`
	NodeID             *uuid.UUID `json:"node_id"`
	Label              string     `json:"label"`
	Source             string     `json:"source"` // found | manual
	ProtectedElsewhere bool       `json:"protected_elsewhere"`
	// Protection is upsfeed's verdict for a node, "" for a label.
	Protection   string    `json:"protection"`
	LoginAddress string    `json:"login_address"`
	CreatedAt    time.Time `json:"created_at"`
}

// upsClientResponse is one address logged in to the UPS, and the node it
// comes from when Nexara can tell.
type upsClientResponse struct {
	Address     string     `json:"address"`
	Connections int        `json:"connections"`
	Missed      int        `json:"missed"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	ClusterID   *uuid.UUID `json:"cluster_id"`
	NodeName    string     `json:"node_name"`
}

type upsNodeRef struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeName  string    `json:"node_name"`
}

// upsPower is what one request knows about what UPSes power: every device's
// feeds and logins, the node addresses logins are matched against, and the
// clusters whose nodes the caller may see. The UPS routes are global, but a
// node belongs to a cluster, so a view:ups holder sees a node only where they
// hold view:node.
type upsPower struct {
	feeds   map[uuid.UUID][]db.ListUpsFeedsRow
	clients map[uuid.UUID][]db.UpsClient
	nodes   upsfeed.Nodes
	access  clusterAccess
}

func (h *NutHandler) loadPower(c fiber.Ctx) (*upsPower, error) {
	access, err := accessibleClusters(c, "view", "node")
	if err != nil {
		return nil, err
	}
	ctx := c.Context()
	// Three missed full syncs, two minutes at least, before a node's last
	// status stops counting as current.
	feeds, err := h.queries.ListUpsFeeds(ctx, time.Now().Add(-max(2*time.Minute, 3*h.nodeSyncInterval)))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to list what UPS devices power")
	}
	clients, err := h.queries.ListUpsClients(ctx)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to list UPS logins")
	}
	addrs, err := h.queries.ListNodeLoginAddresses(ctx)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to list node addresses")
	}
	pw := &upsPower{
		feeds: map[uuid.UUID][]db.ListUpsFeedsRow{}, clients: map[uuid.UUID][]db.UpsClient{},
		nodes: upsfeed.NewNodes(addrs), access: access,
	}
	for _, f := range feeds {
		pw.feeds[f.UpsID] = append(pw.feeds[f.UpsID], f)
	}
	for _, cl := range clients {
		pw.clients[cl.UpsID] = append(pw.clients[cl.UpsID], cl)
	}
	return pw, nil
}

// apply fills a device's feeds, logins and host node, judging each node feed,
// and leaves out what belongs to a cluster whose nodes the caller cannot see.
func (pw *upsPower) apply(d *upsDeviceResponse, r db.ListUpsDevicesRow) {
	host := upsfeed.HostNode(r.ServerHostClusterID, r.ServerHostNodeName)
	if host != nil && pw.access.PermitsCluster(host.Cluster) {
		d.HostNode = &upsNodeRef{ClusterID: host.Cluster, NodeName: host.Name}
	}
	if r.ClientsListedAt.Valid {
		t := r.ClientsListedAt.Time
		d.ClientsListedAt = &t
	}
	logins := make([]upsfeed.Login, 0, len(pw.clients[r.ID]))
	for _, cl := range pw.clients[r.ID] {
		a, err := netip.ParseAddr(cl.Address)
		if err != nil {
			continue
		}
		logins = append(logins, upsfeed.Login{Addr: a, Missed: int(cl.Missed)})
		resp := upsClientResponse{
			Address: cl.Address, Connections: int(cl.Connections), Missed: int(cl.Missed),
			FirstSeenAt: cl.FirstSeenAt, LastSeenAt: cl.LastSeenAt,
		}
		node, placed := pw.nodes.Login(a, host)
		switch {
		case placed && pw.access.PermitsCluster(node.Cluster):
			resp.ClusterID, resp.NodeName = &node.Cluster, node.Name
		case !pw.access.HasGlobal:
			// A caller who cannot view every cluster's nodes sees only logins
			// placed on nodes they can: hiding just the hidden nodes' logins
			// would itself say which addresses are nodes they cannot see.
			continue
		}
		d.Clients = append(d.Clients, resp)
	}
	listed := r.ClientsListedAt.Valid
	for _, f := range pw.feeds[r.ID] {
		resp := upsFeedResponse{ID: f.ID, Kind: "label", Label: f.Label, Source: f.Source, CreatedAt: f.CreatedAt}
		if f.ClusterID.Valid {
			node := upsfeed.Node{Cluster: uuid.UUID(f.ClusterID.Bytes), Name: f.NodeName.String}
			if !pw.access.PermitsCluster(node.Cluster) {
				continue
			}
			resp.Kind, resp.ClusterID, resp.ClusterName, resp.NodeName = "node", &node.Cluster, f.ClusterName.String, node.Name
			if f.NodeID.Valid {
				id := uuid.UUID(f.NodeID.Bytes)
				resp.NodeID = &id
			}
			resp.ProtectedElsewhere = f.ProtectedElsewhere
			status := f.NodeStatus.String
			if !f.NodeStatusCurrent {
				status = "unknown" // the last status of a cluster Nexara cannot reach
			}
			verdict, addr := upsfeed.Judge(node, f.ProtectedElsewhere, status, listed, logins, pw.nodes, host)
			resp.Protection = string(verdict)
			if addr.IsValid() {
				resp.LoginAddress = addr.String()
			}
		}
		d.Feeds = append(d.Feeds, resp)
	}
}
