package handlers

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nodemember"
)

// A node name a caller supplies must be one of the cluster's nodes before it
// reaches Proxmox, and not only a well-formed one.
//
// pveproxy picks the host to forward a /nodes/{node}/… call to from the name
// BEFORE anything validates it — pve-manager PVE/HTTPServer.pm rest_handler →
// resolve_proxyto → PVE::Cluster::remote_node_ip, whose get_ip_from_hostname
// fallback resolves a name that is not in the cluster's node list through DNS
// or /etc/hosts — and then opens TLS to that host on port 8006. pveproxy's
// own check of the peer's certificate during that handshake
// (PVE::APIServer::AnyEvent proxy_request → PVE::HTTPServer
// check_cert_fingerprint → PVE::Cluster::check_cert_fingerprint, against the
// certificates pinned for the cluster's nodes) keeps the forwarded credentials
// from any host that does not hold a member node's key, but what the node
// found out comes back: the lookup failure, the refused connection, the
// timeout or the certificate mismatch arrives as a 502 carrying Proxmox's own
// sentence (mapProxmoxError). That is a DNS and port-8006 reachability oracle
// from the node's network position, and the node-name format, which admits
// dots, lets an IP address or an FQDN through to it.
//
// The nodes table is the authority for "one of the cluster's", as it is for
// every node list the SPA shows. A node that joined the Proxmox cluster since
// the collector's last slow pass (every METRICS_COLLECT_INTERVAL) is refused
// until that pass records it; a cluster being added has its nodes recorded by
// the create itself when its connectivity check reaches it, and by the first
// pass otherwise. The collector never deletes a node row, so a node since
// removed from Proxmox still passes — a name the cluster really held, rather
// than one the caller chose.

// errNodeNotMember answers a request naming a node the cluster does not have.
// It is deliberately not the bare "Not Found" Fiber answers for a path that
// routes nowhere, which the registry's route sweep reads as a routing miss.
var errNodeNotMember = fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")

// NodeLookup is the query a membership check asks. *db.Queries satisfies it.
type NodeLookup = nodemember.Lookup

// nodeMembership says whether a node is one of clusterID's, by
// nodemember.Check — the one definition of "member" the API and the background
// engines share. A lookup that fails is a logged 500, never a yes.
func nodeMembership(q NodeLookup, clusterID uuid.UUID) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, node string) (bool, error) {
		member, err := nodemember.Check(ctx, q, clusterID, node)
		if err != nil {
			// The error handler logs nothing, so without this a database
			// outage would read as an unexplained 500 on every route that
			// names a node.
			slog.Error("node membership lookup failed", "cluster_id", clusterID, "error", err)
			return false, fiber.NewError(fiber.StatusInternalServerError, "Failed to look up the node")
		}
		return member, nil
	}
}

// RequireNodesInCluster refuses a request naming a node that is not one of the
// cluster its path names, with 404 before anything is sent to Proxmox.
//
// The registry runs it for every route that names a node — in its URL or its
// body (Endpoint.serve and Endpoint.checkedNodeParams,
// internal/api/registry.go) — on the values the handler is about to be
// handed, and the two task routes run it on the node their UPID carries
// (VMHandler.taskUPID). A body node on a
// route whose permission is Deferred is the handler's to check, with
// requireNodeInCluster. The cluster is resolved with clusterIDFromParam,
// the permission gate's own resolution, so the node is checked against the
// cluster the caller was authorized on. That gate runs first, as route
// middleware, so a caller it refuses learns nothing about which nodes exist.
func RequireNodesInCluster(c fiber.Ctx, q NodeLookup, nodes []string) error {
	if len(nodes) == 0 {
		return nil
	}
	if q == nil {
		// mountRegistry refuses to mount a route naming a node without a
		// lookup, so this is unreachable there; it fails closed anyway.
		return fiber.NewError(fiber.StatusInternalServerError, "Node lookup not configured")
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	isMember := nodeMembership(q, clusterID)
	// Each distinct name is asked about once: a node list (a rolling job's,
	// a DRS rule's) may repeat one, and asking again cannot change the
	// answer. That bounds the lookups one request can cost at the cluster's
	// own node count, plus the first stranger, which ends the loop.
	asked := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if asked[node] {
			continue
		}
		asked[node] = true
		member, err := isMember(c.Context(), node)
		if err != nil {
			return err
		}
		if !member {
			return errNodeNotMember
		}
	}
	return nil
}

// requireNodeInCluster refuses a node that is not one of clusterID's with the
// same 404 RequireNodesInCluster answers. It is for the handlers that check a
// body node themselves (Endpoint.NodesCheckedByHandler): each calls it AFTER
// its own permission check, against the cluster the node belongs to — which
// for a migration's target is the TARGET cluster. "" names no node.
func requireNodeInCluster(c fiber.Ctx, q NodeLookup, clusterID uuid.UUID, node string) error {
	if node == "" {
		return nil
	}
	if q == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Node lookup not configured")
	}
	member, err := nodeMembership(q, clusterID)(c.Context(), node)
	if err != nil {
		return err
	}
	if !member {
		return errNodeNotMember
	}
	return nil
}

// lookupOf is q as a NodeLookup. A nil *db.Queries would be a non-nil
// NodeLookup, and a method call on it a panic; nil answers "not configured"
// instead.
func lookupOf(q *db.Queries) NodeLookup {
	if q == nil {
		return nil
	}
	return q
}
