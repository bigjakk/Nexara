package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// cephReadAttemptTimeout bounds one attempt of a Ceph read, in place of the
// cached client's five-minute request bound. Thirty seconds is what pveproxy
// itself gives a proxied request (pve-http-server AnyEvent.pm, proxy_request:
// timeout => 30). It must stay above the cache's ten-second connect bound: a
// silent endpoint then fails its dial inside the attempt, the cache drops it,
// and the one repeat goes to the member the cache picks instead, so the worst
// case is about ten seconds plus the repeat's own answer.
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

// noCephMemoWindow is how long the status read trusts a member walk that found
// no member running Ceph: during it, the endpoint's own "binary not installed"
// is the answer, one request a poll instead of one per member.
const noCephMemoWindow = 10 * time.Minute

// noCephMemo remembers, per cluster and in this process, when a walk found no
// member running Ceph. One entry per cluster, expired entries pruned on write.
type noCephMemo struct {
	mu    sync.Mutex
	now   func() time.Time
	until map[uuid.UUID]time.Time
}

func newNoCephMemo(now func() time.Time) *noCephMemo {
	return &noCephMemo{now: now, until: map[uuid.UUID]time.Time{}}
}

func (m *noCephMemo) holds(cluster uuid.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now().Before(m.until[cluster])
}

func (m *noCephMemo) set(cluster uuid.UUID, absent bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, t := range m.until {
		if !now.Before(t) {
			delete(m.until, id)
		}
	}
	if absent {
		m.until[cluster] = now.Add(noCephMemoWindow)
	} else {
		delete(m.until, cluster)
	}
}

// readCephStatus is the Ceph page's status read: the cluster route, and on the
// endpoint's "binary not installed" the members' node routes
// (proxmox.ReadCephStatusDetail), once per request, with cephRead's attempt
// bound and its one refetch through the cache after a lost connection. A walk
// that finds no member with Ceph, the rest only unreachable, answers with the
// endpoint's "binary not installed", so the page reads "no Ceph" while a node is
// down; one that asked every member is remembered for noCephMemoWindow.
func (h *CephHandler) readCephStatus(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.CephStatus, error) {
	px, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(c.Context(), cephReadAttemptTimeout)
		status, rerr := h.cephStatusOnce(ctx, px, clusterID)
		cancel()
		if rerr == nil {
			return status, nil
		}
		if !errors.Is(rerr, proxmox.ErrConnectionFailed) || attempt > 0 {
			return nil, mapProxmoxError(rerr)
		}
		if px, err = h.createProxmoxClient(c, clusterID); err != nil {
			return nil, mapProxmoxError(rerr)
		}
	}
}

func (h *CephHandler) cephStatusOnce(ctx context.Context, px *proxmox.Client, clusterID uuid.UUID) (*proxmox.CephStatus, error) {
	if h.noCeph.holds(clusterID) {
		status, err := px.GetClusterCephStatus(ctx)
		if err == nil {
			h.noCeph.set(clusterID, false)
		}
		return status, err
	}
	r := px.ReadCephStatusDetail(ctx)
	switch {
	case r.Status != nil:
		h.noCeph.set(clusterID, false)
		return r.Status, nil
	case r.NotInstalled != nil && r.Presence == proxmox.CephAbsent:
		h.noCeph.set(clusterID, true)
		return nil, r.NotInstalled
	case r.NotInstalled != nil && r.Failed == 0:
		return nil, r.NotInstalled
	}
	return nil, r.Err
}
