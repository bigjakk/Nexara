// Package nodemember answers one question: is a node name one of a cluster's
// nodes? It is the single definition of "member" shared by the API, which
// refuses a caller's node that is not one, and by the background engines that
// replay a node a caller stored earlier (a schedule, a migration, a rolling
// job, the virtio-win config, a registered task).
//
// Why it matters: pveproxy picks the host to forward a /nodes/{node}/… call to
// from the name before it validates it (pve-manager PVE/HTTPServer.pm
// rest_handler → resolve_proxyto → PVE::Cluster::remote_node_ip, whose
// get_ip_from_hostname fallback resolves a name outside the cluster's node
// list through DNS or /etc/hosts) and then dials that host on port 8006. The
// lookup, connection or certificate error it gets back reaches whoever reads
// the call's error. handlers.RequireNodesInCluster has the full account.
//
// The nodes table is the authority, as it is for every node list the SPA
// shows. Only names a CALLER supplied are checked — at the API when they
// arrive, and again by each engine that replays one (a scheduled run, a
// migration job, a rolling job's steps, the virtio-win config and its
// in-flight downloads, the collector's poll of a registered task). Names the
// collector and the engines take from Proxmox's own node list (GET /nodes) are
// not, so a node that has just joined keeps working there before the
// collector records it.
//
// The collector never deletes a node row, so a node since removed from the
// Proxmox cluster still passes: what this refuses is a name Nexara never
// recorded for the cluster — an address, an FQDN, another cluster's node.
package nodemember

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Lookup is the query a membership check asks. *db.Queries satisfies it.
type Lookup interface {
	GetNodeByClusterAndName(ctx context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error)
}

// Check says whether node is one of clusterID's nodes. Only "no such row" is
// "not a member"; a lookup that fails is an error, never a yes.
//
// The name is compared exactly, as Proxmox reported it to the collector:
// pveproxy's own node-list lookup is exact too, so a spelling in another case
// is not a member to either of them.
func Check(ctx context.Context, q Lookup, clusterID uuid.UUID, node string) (bool, error) {
	_, err := q.GetNodeByClusterAndName(ctx, db.GetNodeByClusterAndNameParams{ClusterID: clusterID, Name: node})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up node %q: %w", node, err)
	}
	return true, nil
}

// NotMemberError is Require's answer for a node the cluster does not hold.
// Its text is written where the operator reads a failed run (a schedule's
// last_error, a migration's error), so it says what was NOT done.
type NotMemberError struct {
	Node string
}

func (e *NotMemberError) Error() string {
	return fmt.Sprintf("node %q is not one of this cluster's nodes; it was not contacted", e.Node)
}

// LookupError is Require's answer when the database could not say. Its text
// is as generic as NotMemberError's, because it lands in the same
// operator-readable fields; the cause is kept for the log (Unwrap).
type LookupError struct {
	Node string
	Err  error
}

func (e *LookupError) Error() string {
	return fmt.Sprintf("could not confirm node %q is one of this cluster's nodes; it was not contacted", e.Node)
}

func (e *LookupError) Unwrap() error { return e.Err }

// Require is Check for a caller that replays a stored node: nil for a member,
// a *NotMemberError for a name the cluster does not hold, and a *LookupError
// when the database could not tell. Anything but nil means "do not dial it".
func Require(ctx context.Context, q Lookup, clusterID uuid.UUID, node string) error {
	member, err := Check(ctx, q, clusterID, node)
	if err != nil {
		return &LookupError{Node: node, Err: err}
	}
	if !member {
		return &NotMemberError{Node: node}
	}
	return nil
}
