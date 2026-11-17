package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// cephReadAttemptTimeout bounds one attempt of a Ceph read. The cached client's
// own bound is proxmox.CachedClientTimeout (five minutes), which a dial to a
// member that went down without a reset would sit out before the next member
// is tried. Thirty seconds is what pveproxy itself gives a proxied request
// (pve-http-server AnyEvent.pm, proxy_request: timeout => 30).
const cephReadAttemptTimeout = 30 * time.Second

// cephRead runs a Ceph read against the node resolveClusterNode would pick
// and, when the failure says a node could not be reached, once more on each
// other node of the cluster. Reads only: a write keeps its single attempt.
// read is a client method expression such as (*proxmox.Client).GetCephOSDs,
// hence the receiver before the context.
//
// Two failures count as unreachable (cephNodeUnreachable). A 595 or 596 is the
// endpoint's pveproxy failing to forward to the node in the path, so the same
// client is asked about the next node. A failed connection is the endpoint
// itself gone — the cached client stays pinned to it until the collector marks
// it offline and the cache is rebuilt — so from then on each node is asked
// directly at its own address and pinned certificate, as the collector's
// failoverCluster does. A member with no recorded address or certificate is
// skipped rather than reached unpinned.
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

	var (
		cluster      *db.Cluster
		endpointDown bool
		lastErr      error
	)
	for i, n := range cephReadOrder(nodes) {
		client := pxClient
		if i > 0 && endpointDown {
			if cluster == nil {
				row, cerr := h.queries.GetCluster(c.Context(), clusterID)
				if cerr != nil {
					break
				}
				cluster = &row
			}
			if n.Address == "" || n.SslFingerprint == "" ||
				strings.EqualFold(n.Address, proxmox.APIURLHost(cluster.ApiUrl)) {
				continue
			}
			direct := *cluster
			direct.ApiUrl = proxmox.FailoverBaseURL(cluster.ApiUrl, n.Address)
			direct.TlsFingerprint = n.SslFingerprint
			if client, err = clusterRowClient(direct, h.encryptionKey, cephReadAttemptTimeout); err != nil {
				continue
			}
		}

		ctx, cancel := context.WithTimeout(c.Context(), cephReadAttemptTimeout)
		v, rerr := read(client, ctx, n.Name)
		cancel()
		if rerr == nil {
			return v, nil
		}
		if !cephNodeUnreachable(rerr) {
			return zero, mapProxmoxError(rerr)
		}
		if errors.Is(rerr, proxmox.ErrConnectionFailed) {
			endpointDown = true
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

// cephNodeUnreachable reports whether a read failed for want of a node rather
// than because of what it asked. 595 and 596 are AnyEvent::HTTP's own failures,
// which pveproxy relays as they are when it cannot forward a request to the
// node in its path: 595 while connecting, 596 during TLS or the request (see
// vzdumpRefused in internal/proxmox/client_backup.go).
func cephNodeUnreachable(err error) bool {
	if errors.Is(err, proxmox.ErrConnectionFailed) {
		return true
	}
	var apiErr *proxmox.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 595 || apiErr.StatusCode == 596)
}
