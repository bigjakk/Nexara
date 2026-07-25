package handlers

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// OSD lifecycle actions. Two different Proxmox mechanisms sit behind these:
//
//   - in/out are mon commands (POST /nodes/{node}/ceph/osd/{id}/{in,out}) that
//     change CRUSH membership synchronously and return no task UPID. Any node in
//     the cluster can serve them.
//   - start/stop/restart are systemd operations on the daemon
//     (POST /nodes/{node}/ceph/{action}?service=osd.N) that DO return a UPID, so
//     they must be addressed to the OSD's own host and recorded via TrackTask.

// cephOSDActions are the OSD actions the API accepts, mirroring the routes
// registered in the router.
var cephOSDActions = map[string]bool{
	"in": true, "out": true, "start": true, "stop": true, "restart": true,
}

// cephDisruptiveOSDActions are the actions that can reduce data redundancy, and
// so get a pre-flight assessment in the UI before the operator confirms.
var cephDisruptiveOSDActions = map[string]bool{
	"out":     true,
	"stop":    true,
	"restart": true,
}

const (
	cephPreflightOK       = "ok"
	cephPreflightWarning  = "warning"
	cephPreflightCritical = "critical"
)

// osdActionResponse is returned by the OSD action endpoints. UPID is empty for
// in/out, which Ceph applies without spawning a Proxmox task.
type osdActionResponse struct {
	Status string `json:"status"`
	OSD    string `json:"osd"`
	Action string `json:"action"`
	Node   string `json:"node,omitempty"`
	UPID   string `json:"upid,omitempty"`
}

// cephPoolConstraint is the redundancy contract of a single pool.
type cephPoolConstraint struct {
	PoolName string `json:"pool_name"`
	Size     int    `json:"size"`
	MinSize  int    `json:"min_size"`
}

// cephOSDPreflight is the advisory safety assessment for a disruptive OSD
// action. It is deliberately advisory only: the action endpoints do not consult
// it and never refuse on its basis. Operators of small or unusual clusters
// routinely need to do things this assessment flags, so the contract is
// "show the cost, then let them confirm" rather than a hard block.
type cephOSDPreflight struct {
	OSDID  int    `json:"osd_id"`
	Action string `json:"action"`
	// Disruptive is false for in/start, which only add capacity back.
	Disruptive bool   `json:"disruptive"`
	Severity   string `json:"severity"`

	OSDsTotal int `json:"osds_total"`
	// Serving counts OSDs that are both up and in — only those hold a copy of
	// live data, so they are what redundancy is actually measured against.
	OSDsServing       int `json:"osds_serving"`
	OSDsServingAfter  int `json:"osds_serving_after"`
	HostsServing      int `json:"hosts_serving"`
	HostsServingAfter int `json:"hosts_serving_after"`

	MaxPoolSize    int                  `json:"max_pool_size"`
	MaxPoolMinSize int                  `json:"max_pool_min_size"`
	Pools          []cephPoolConstraint `json:"pools"`
	Warnings       []string             `json:"warnings"`
}

// GetOSDPreflight handles
// GET /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/preflight?action=out
func (h *CephHandler) GetOSDPreflight(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	osdID, err := osdIDFromParam(c)
	if err != nil {
		return err
	}
	action := c.Query("action", "out")
	if !cephOSDActions[action] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid action")
	}

	octx, err := h.loadOSDContext(c, osdID)
	if err != nil {
		return err
	}

	// Pool constraints are what turn OSD counts into a redundancy verdict, but a
	// failure to read them must not block the pre-flight: report what we have
	// and let evaluateOSDPreflight flag the gap.
	pools, poolErr := octx.client.GetCephPools(c.Context(), octx.queryNode)
	if poolErr != nil {
		pools = nil
	}

	constraints := make([]cephPoolConstraint, 0, len(pools))
	for _, p := range pools {
		constraints = append(constraints, cephPoolConstraint{
			PoolName: p.PoolName,
			Size:     int(p.Size),
			MinSize:  int(p.MinSize),
		})
	}

	return c.JSON(evaluateOSDPreflight(osdID, action, octx.osds, constraints, poolErr != nil))
}

// evaluateOSDPreflight projects the cluster's redundancy state forward across
// the requested action. It is a pure function so the (fiddly) counting rules can
// be unit-tested without a Proxmox cluster.
//
// The host-level reasoning assumes Ceph's default host failure domain: with
// size=3 across 3 hosts, a pool keeps one replica per host, so the number of
// hosts still serving data — not the raw OSD count — is what bounds redundancy.
// Clusters using an osd or rack failure domain will see a conservative estimate.
func evaluateOSDPreflight(osdID int, action string, osds []cephOSDResponse, pools []cephPoolConstraint, poolsUnavailable bool) cephOSDPreflight {
	pf := cephOSDPreflight{
		OSDID:      osdID,
		Action:     action,
		Disruptive: cephDisruptiveOSDActions[action],
		Severity:   cephPreflightOK,
		OSDsTotal:  len(osds),
		Pools:      pools,
		Warnings:   []string{},
	}

	servingHosts := make(map[string]struct{})
	hostsAfter := make(map[string]struct{})
	var alreadyDegraded []string

	for _, o := range osds {
		bucket := osdHostBucket(o)
		serving := o.Up == 1 && o.In == 1
		if serving {
			pf.OSDsServing++
			servingHosts[bucket] = struct{}{}
		} else if o.ID != osdID {
			alreadyDegraded = append(alreadyDegraded, fmt.Sprintf("osd.%d (%s)", o.ID, osdStateLabel(o)))
		}

		if o.ID == osdID {
			serving = osdServingAfter(o, action)
		}
		if serving {
			pf.OSDsServingAfter++
			hostsAfter[bucket] = struct{}{}
		}
	}
	pf.HostsServing = len(servingHosts)
	pf.HostsServingAfter = len(hostsAfter)

	for _, p := range pools {
		if p.Size > pf.MaxPoolSize {
			pf.MaxPoolSize = p.Size
		}
		if p.MinSize > pf.MaxPoolMinSize {
			pf.MaxPoolMinSize = p.MinSize
		}
	}

	// Grade the action's delta, not the cluster's current state. Stopping a
	// daemon that is already out — the second step of the ordinary
	// drain-then-swap-the-disk path — costs no redundancy, and must not inherit
	// the alarm for degradation it did not cause. An advisory check that cries
	// wolf on the safe steps gets clicked through on the dangerous one.
	if !pf.Disruptive || pf.HostsServingAfter >= pf.HostsServing {
		return pf
	}

	// A successful read that yields no usable size/min_size is as uninformative
	// as a failed one; only a cluster with genuinely no pools is safely "ok".
	if len(pools) > 0 && pf.MaxPoolSize == 0 {
		poolsUnavailable = true
	}

	switch {
	case pf.MaxPoolMinSize > 0 && pf.HostsServingAfter < pf.MaxPoolMinSize:
		pf.Severity = cephPreflightCritical
		pf.Warnings = append(pf.Warnings, fmt.Sprintf(
			"Only %s would still be serving data, below the highest pool min_size (%d). "+
				"PGs can drop under min_size and guest I/O on affected pools will stall until an OSD returns.",
			pluralHosts(pf.HostsServingAfter), pf.MaxPoolMinSize))
	case pf.MaxPoolSize > 0 && pf.HostsServingAfter < pf.MaxPoolSize:
		pf.Severity = cephPreflightWarning
		pf.Warnings = append(pf.Warnings, fmt.Sprintf(
			"%s would still be serving data, below the highest pool size (%d). "+
				"Pools will run degraded and Ceph cannot restore full redundancy until another host has an OSD in.",
			pluralHosts(pf.HostsServingAfter), pf.MaxPoolSize))
	}

	if poolsUnavailable {
		pf.Severity = maxSeverity(pf.Severity, cephPreflightWarning)
		pf.Warnings = append(pf.Warnings,
			"Pool size/min_size could not be read, so the redundancy impact of this action is unknown.")
	}

	if len(alreadyDegraded) > 0 {
		sort.Strings(alreadyDegraded)
		pf.Severity = maxSeverity(pf.Severity, cephPreflightWarning)
		pf.Warnings = append(pf.Warnings, fmt.Sprintf(
			"The cluster is already degraded: %s. Recovery is likely still in progress.",
			joinList(alreadyDegraded)))
	}

	if action == "restart" && pf.Severity != cephPreflightOK {
		pf.Warnings = append(pf.Warnings,
			"A restart takes the OSD down only briefly, but the impact above applies for its duration.")
	}

	return pf
}

// osdServingAfter reports whether the target OSD would still be holding live
// data once the action completes. restart counts as not serving: the daemon does
// go down, just briefly.
func osdServingAfter(o cephOSDResponse, action string) bool {
	switch action {
	case "out", "stop", "restart":
		return false
	case "in":
		return o.Up == 1
	case "start":
		return o.In == 1
	}
	return o.Up == 1 && o.In == 1
}

// addRedundancySnapshot records how much of the cluster was serving data when
// the action was dispatched. The feature deliberately lets operators proceed
// past a critical pre-flight, so the audit row is the only place that can later
// answer whether osd.4 was stopped on a healthy cluster or on the last host
// still holding a replica. The counts come from the OSD tree already fetched —
// no extra Proxmox round-trip, and no pool data, so they are exact either way.
func addRedundancySnapshot(dst map[string]any, osdID int, action string, osds []cephOSDResponse) {
	snapshot := evaluateOSDPreflight(osdID, action, osds, nil, false)
	dst["osds_serving"] = snapshot.OSDsServing
	dst["osds_serving_after"] = snapshot.OSDsServingAfter
	dst["hosts_serving"] = snapshot.HostsServing
	dst["hosts_serving_after"] = snapshot.HostsServingAfter
}

// osdHostBucket keys an OSD by its failure domain. An OSD whose host Ceph did
// not report cannot be shown to share a host with any other, so it counts as its
// own bucket — collapsing every unreported OSD into a single empty-string host
// would fabricate a redundancy cliff on a healthy cluster.
func osdHostBucket(o cephOSDResponse) string {
	if o.Host == "" {
		return "\x00unknown-host/osd." + strconv.Itoa(o.ID)
	}
	return o.Host
}

func osdStateLabel(o cephOSDResponse) string {
	switch {
	case o.Up != 1 && o.In != 1:
		return "down and out"
	case o.Up != 1:
		return "down"
	default:
		return "out"
	}
}

func pluralHosts(n int) string {
	if n == 1 {
		return "1 host"
	}
	return fmt.Sprintf("%d hosts", n)
}

// joinList renders "a", "a and b", or "a, b and c".
func joinList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	head := items[:len(items)-1]
	out := ""
	for i, s := range head {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out + " and " + items[len(items)-1]
}

var cephSeverityRank = map[string]int{
	cephPreflightOK:       0,
	cephPreflightWarning:  1,
	cephPreflightCritical: 2,
}

func maxSeverity(a, b string) string {
	if cephSeverityRank[b] > cephSeverityRank[a] {
		return b
	}
	return a
}

// --- Action endpoints ---

// SetOSDIn handles POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/in
func (h *CephHandler) SetOSDIn(c fiber.Ctx) error {
	return h.osdMembershipAction(c, "in")
}

// SetOSDOut handles POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/out
func (h *CephHandler) SetOSDOut(c fiber.Ctx) error {
	return h.osdMembershipAction(c, "out")
}

// osdMembershipAction marks an OSD in or out. Ceph applies these immediately and
// returns no UPID, so they are recorded with AuditLog rather than TrackTask.
func (h *CephHandler) osdMembershipAction(c fiber.Ctx, action string) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ceph", clusterID); err != nil {
		return err
	}

	osdID, err := osdIDFromParam(c)
	if err != nil {
		return err
	}

	octx, err := h.loadOSDContext(c, osdID)
	if err != nil {
		return err
	}

	switch action {
	case "in":
		err = octx.client.SetCephOSDIn(c.Context(), octx.queryNode, osdID)
	case "out":
		err = octx.client.SetCephOSDOut(c.Context(), octx.queryNode, osdID)
	default:
		return fiber.NewError(fiber.StatusBadRequest, "Invalid action")
	}
	if err != nil {
		return mapProxmoxError(err)
	}

	osdName := osdDisplayName(octx.target)
	detail := map[string]any{
		"osd_id": osdID,
		"osd":    osdName,
		"host":   octx.target.Host,
		"node":   octx.queryNode,
	}
	addRedundancySnapshot(detail, osdID, action, octx.osds)
	details, _ := json.Marshal(detail)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ceph_osd", strconv.Itoa(osdID), "osd_"+action, details)

	return c.JSON(osdActionResponse{
		Status: "applied",
		OSD:    osdName,
		Action: action,
		Node:   octx.queryNode,
	})
}

// StartOSD handles POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/start
func (h *CephHandler) StartOSD(c fiber.Ctx) error {
	return h.osdDaemonAction(c, "start")
}

// StopOSD handles POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/stop
func (h *CephHandler) StopOSD(c fiber.Ctx) error {
	return h.osdDaemonAction(c, "stop")
}

// RestartOSD handles POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/restart
func (h *CephHandler) RestartOSD(c fiber.Ctx) error {
	return h.osdDaemonAction(c, "restart")
}

// osdDaemonAction starts, stops or restarts an OSD daemon. Proxmox returns a
// UPID for these, so the result is recorded via TrackTask.
func (h *CephHandler) osdDaemonAction(c fiber.Ctx, action string) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ceph", clusterID); err != nil {
		return err
	}

	osdID, err := osdIDFromParam(c)
	if err != nil {
		return err
	}

	octx, err := h.loadOSDContext(c, osdID)
	if err != nil {
		return err
	}

	// systemd runs on the OSD's own host, so unlike in/out this cannot be sent
	// to whichever node answered the read.
	node, err := h.daemonNode(c, clusterID, octx.target)
	if err != nil {
		return err
	}

	osdName := osdDisplayName(octx.target)
	upid, err := octx.client.CephServiceAction(c.Context(), node, "osd."+strconv.Itoa(osdID), action)
	if err != nil {
		return mapProxmoxError(err)
	}

	extra := map[string]any{"osd_id": osdID}
	addRedundancySnapshot(extra, osdID, action, octx.osds)

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "ceph_osd",
		ResourceID:   strconv.Itoa(osdID),
		ResourceName: osdName,
		Action:       "osd_" + action,
		UPID:         upid,
		Description:  fmt.Sprintf("%s Ceph %s on %s", actionVerb(action), osdName, node),
		Extra:        extra,
	})

	return c.JSON(osdActionResponse{
		Status: "dispatched",
		OSD:    osdName,
		Action: action,
		Node:   node,
		UPID:   upid,
	})
}

// --- Helpers ---

// osdContext bundles what the OSD lifecycle handlers need: a client for the
// cluster, the OSD tree as of this request, and the targeted OSD.
type osdContext struct {
	client *proxmox.Client
	// queryNode is an online node used to address cluster-wide (mon) commands.
	queryNode string
	osds      []cephOSDResponse
	target    cephOSDResponse
}

// loadOSDContext resolves the cluster's OSD tree and locates the target OSD.
// Reading the tree first is what lets the handlers route daemon actions to the
// right host and reject IDs that do not exist, instead of forwarding them to
// Proxmox and surfacing its error.
func (h *CephHandler) loadOSDContext(c fiber.Ctx, osdID int) (*osdContext, error) {
	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return nil, err
	}

	osdResp, err := pxClient.GetCephOSDs(c.Context(), nodeName)
	if err != nil {
		return nil, mapProxmoxError(err)
	}

	osds := flattenOSDTree(&osdResp.Root)
	for _, o := range osds {
		if o.ID == osdID {
			return &osdContext{client: pxClient, queryNode: nodeName, osds: osds, target: o}, nil
		}
	}
	return nil, fiber.NewError(fiber.StatusNotFound, fmt.Sprintf("OSD %d not found in this cluster", osdID))
}

// daemonNode resolves the cluster node running the target OSD's daemon. The host
// comes from the CRUSH map — operator-controlled data that would land in the
// request path — so it is matched against the cluster's known nodes rather than
// forwarded on trust.
func (h *CephHandler) daemonNode(c fiber.Ctx, clusterID uuid.UUID, osd cephOSDResponse) (string, error) {
	if osd.Host == "" {
		return "", fiber.NewError(fiber.StatusUnprocessableEntity, fmt.Sprintf(
			"Cannot determine which node runs osd.%d — Ceph reported no host for it", osd.ID))
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to list cluster nodes")
	}
	for _, n := range nodes {
		if n.Name == osd.Host {
			return n.Name, nil
		}
	}
	return "", fiber.NewError(fiber.StatusUnprocessableEntity, fmt.Sprintf(
		"Ceph reports osd.%d on host %q, which is not a known node in this cluster", osd.ID, osd.Host))
}

func osdIDFromParam(c fiber.Ctx) (int, error) {
	osdID, err := strconv.Atoi(c.Params("osd_id"))
	if err != nil || osdID < 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "Invalid OSD ID")
	}
	return osdID, nil
}

// osdDisplayName prefers the CRUSH name ("osd.3") and falls back to composing
// one, since the tree occasionally omits it for down OSDs.
func osdDisplayName(o cephOSDResponse) string {
	if o.Name != "" {
		return o.Name
	}
	return "osd." + strconv.Itoa(o.ID)
}

func actionVerb(action string) string {
	switch action {
	case "start":
		return "Start"
	case "stop":
		return "Stop"
	case "restart":
		return "Restart"
	}
	return action
}
