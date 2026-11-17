package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// cephReadAttemptTimeout bounds one attempt of a Ceph read. The cached client's
// own bound is proxmox.CachedClientTimeout (five minutes), which a dial to a
// member that went down without a reset would sit out before the next attempt.
// Thirty seconds is what pveproxy itself gives a proxied request
// (pve-http-server AnyEvent.pm, proxy_request: timeout => 30).
const cephReadAttemptTimeout = 30 * time.Second

// cephRead runs a Ceph read against the node resolveClusterNode would pick,
// and tries again only when the failure says something could not be reached.
// Reads only: a write keeps its single attempt. read is a client method
// expression such as (*proxmox.Client).GetCephOSDs, hence the receiver before
// the context.
//
//   - A 595 or 596 is the endpoint's pveproxy failing to forward to the node in
//     the path (cephNodeUnreachable), so the same endpoint is asked about the
//     next node, once per node.
//   - A failed connection is the endpoint itself gone. The cache has dropped
//     that client by then and holds the endpoint unreachable, so the client is
//     taken from the cache once more — which member it now points at, if any,
//     is the cache's choice (proxmox.SelectClusterEndpoint) — and the read is
//     repeated once.
func cephRead[T any](h *CephHandler, c fiber.Ctx, clusterID uuid.UUID,
	read func(client *proxmox.Client, ctx context.Context, node string) (T, error),
) (T, error) {
	var zero T
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return zero, err
	}
	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil || len(nodes) == 0 {
		return zero, fiber.NewError(fiber.StatusNotFound, "No nodes found in cluster")
	}

	order := cephReadOrder(nodes)
	refetched := false
	var lastErr error
	for i := 0; i < len(order); {
		ctx, cancel := context.WithTimeout(c.Context(), cephReadAttemptTimeout)
		v, rerr := read(pxClient, ctx, order[i].Name)
		cancel()
		switch {
		case rerr == nil:
			return v, nil
		case errors.Is(rerr, proxmox.ErrConnectionFailed) && !refetched:
			refetched = true
			if pxClient, err = h.createProxmoxClient(c, clusterID); err != nil {
				return zero, mapProxmoxError(rerr)
			}
		case cephNodeUnreachable(rerr):
			i++
		default:
			return zero, mapProxmoxError(rerr)
		}
		lastErr = rerr
	}
	return zero, mapProxmoxError(lastErr)
}

// cephReadOrder is the node resolveClusterNode picks — the first one the
// database calls online, else the first — followed by the rest, online first.
func cephReadOrder(nodes []db.Node) []db.Node {
	order := make([]db.Node, 0, len(nodes))
	for _, online := range []bool{true, false} {
		for _, n := range nodes {
			if (n.Status == "online") == online {
				order = append(order, n)
			}
		}
	}
	return order
}

// cephNodeUnreachable reports whether the endpoint answered that it could not
// forward the read to the node in its path. 595 and 596 are AnyEvent::HTTP's
// own failures, which pveproxy relays as they are: 595 while connecting, 596
// during TLS or the request (see vzdumpRefused in
// internal/proxmox/client_backup.go).
func cephNodeUnreachable(err error) bool {
	var apiErr *proxmox.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 595 || apiErr.StatusCode == 596)
}
