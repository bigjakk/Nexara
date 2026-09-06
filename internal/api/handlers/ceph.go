package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// CephHandler handles Ceph monitoring endpoints.
type CephHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewCephHandler creates a new Ceph handler.
func NewCephHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *CephHandler {
	return &CephHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

// --- Response types ---

type cephStatusResponse struct {
	Health cephHealthResponse `json:"health"`
	PGMap  cephPGMapResponse  `json:"pgmap"`
	OSDMap cephOSDMapResponse `json:"osdmap"`
	MonMap cephMonMapResponse `json:"monmap"`
}

type cephHealthResponse struct {
	Status string                        `json:"status"`
	Checks []proxmox.CephHealthCheckItem `json:"checks"`
}

type cephPGMapResponse struct {
	BytesUsed    int64 `json:"bytes_used"`
	BytesAvail   int64 `json:"bytes_avail"`
	BytesTotal   int64 `json:"bytes_total"`
	ReadBytesSec int64 `json:"read_bytes_sec"`
	WritBytesSec int64 `json:"write_bytes_sec"`
	ReadOpPerSec int64 `json:"read_op_per_sec"`
	WritOpPerSec int64 `json:"write_op_per_sec"`
	NumPGs       int   `json:"num_pgs"`
}

type cephOSDMapResponse struct {
	NumOSDs   int  `json:"num_osds"`
	NumUpOSDs int  `json:"num_up_osds"`
	NumInOSDs int  `json:"num_in_osds"`
	Full      bool `json:"full"`
	NearFull  bool `json:"nearfull"`
}

type cephMonMapResponse struct {
	NumMons int `json:"num_mons"`
}

type cephOSDResponse struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Host        string  `json:"host"`
	Up          int     `json:"up"`
	In          int     `json:"in"`
	Status      string  `json:"status"`
	CrushWeight float64 `json:"crush_weight"`
}

type cephPoolResponse struct {
	PoolName     string  `json:"pool_name"`
	Pool         int     `json:"pool"`
	Size         int     `json:"size"`
	MinSize      int     `json:"min_size"`
	PGNum        int     `json:"pg_num"`
	PGAutoScale  string  `json:"pg_autoscale_mode"`
	CrushRule    int     `json:"crush_rule"`
	BytesUsed    int64   `json:"bytes_used"`
	PercentUsed  float64 `json:"percent_used"`
	ReadBytesSec int64   `json:"read_bytes_sec"`
	WritBytesSec int64   `json:"write_bytes_sec"`
	ReadOpPerSec int64   `json:"read_op_per_sec"`
	WritOpPerSec int64   `json:"write_op_per_sec"`
}

type cephMonResponse struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
	Host string `json:"host"`
	Rank int    `json:"rank"`
}

type cephFSResponse struct {
	Name     string `json:"name"`
	MetaPool string `json:"metadata_pool"`
	DataPool string `json:"data_pool"`
}

type cephCrushRuleResponse struct {
	RuleID   int    `json:"rule_id"`
	RuleName string `json:"rule_name"`
	Type     int    `json:"type"`
	MinSize  int    `json:"min_size"`
	MaxSize  int    `json:"max_size"`
}

type createPoolRequest struct {
	Name        string `json:"name"`
	Size        int    `json:"size"`
	MinSize     int    `json:"min_size,omitempty"`
	PGNum       int    `json:"pg_num"`
	Application string `json:"application,omitempty"`
	CrushRule   string `json:"crush_rule_name,omitempty"`
	PGAutoScale string `json:"pg_autoscale_mode,omitempty"`
}

// --- Live Proxmox proxy endpoints ---

// GetStatus handles GET /api/v1/clusters/:cluster_id/ceph/status
func (h *CephHandler) GetStatus(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	status, err := pxClient.GetCephStatus(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(cephStatusResponse{
		Health: cephHealthResponse{
			Status: status.Health.Status,
			Checks: status.Health.NormalizedChecks(),
		},
		PGMap: cephPGMapResponse{
			BytesUsed:    status.PGMap.BytesUsed,
			BytesAvail:   status.PGMap.BytesAvail,
			BytesTotal:   status.PGMap.BytesTotal,
			ReadBytesSec: status.PGMap.ReadBytesSec,
			WritBytesSec: status.PGMap.WritBytesSec,
			ReadOpPerSec: status.PGMap.ReadOpPerSec,
			WritOpPerSec: status.PGMap.WritOpPerSec,
			NumPGs:       status.PGMap.NumPGs,
		},
		OSDMap: cephOSDMapResponse{
			NumOSDs:   status.OSDMap.NumOSDs,
			NumUpOSDs: status.OSDMap.NumUpOSDs,
			NumInOSDs: status.OSDMap.NumInOSDs,
			Full:      status.OSDMap.Full,
			NearFull:  status.OSDMap.NearFull,
		},
		MonMap: cephMonMapResponse{NumMons: status.MonMap.MonCount()},
	})
}

// ListOSDs handles GET /api/v1/clusters/:cluster_id/ceph/osds
func (h *CephHandler) ListOSDs(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	osdResp, err := pxClient.GetCephOSDs(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	osds := flattenOSDTree(&osdResp.Root)
	return RespondItems(c, osds)
}

// flattenOSDTree walks the OSD tree and returns flat OSD entries.
func flattenOSDTree(node *proxmox.CephOSDTreeNode) []cephOSDResponse {
	return appendTreeOSDs(nil, node, "")
}

// appendTreeOSDs walks the CRUSH tree carrying the enclosing host bucket's name
// down, so OSDs that don't repeat it inline still resolve to a node — the OSD
// lifecycle handlers address daemon actions by host, so this must be populated.
func appendTreeOSDs(dst []cephOSDResponse, node *proxmox.CephOSDTreeNode, host string) []cephOSDResponse {
	if node.Type == "host" && node.Name != "" {
		host = node.Name
	}
	if node.Type == "osd" {
		osdHost := node.Host
		if osdHost == "" {
			osdHost = host
		}
		dst = append(dst, cephOSDResponse{
			ID:          int(node.ID),
			Name:        node.Name,
			Host:        osdHost,
			Up:          boolToInt(node.Status == "up"),
			In:          boolToInt(node.IsIn()),
			Status:      node.Status,
			CrushWeight: node.CrushWeight,
		})
	}
	for i := range node.Children {
		dst = appendTreeOSDs(dst, &node.Children[i], host)
	}
	return dst
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListPools handles GET /api/v1/clusters/:cluster_id/ceph/pools
func (h *CephHandler) ListPools(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	pools, err := pxClient.GetCephPools(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]cephPoolResponse, len(pools))
	for i, p := range pools {
		resp[i] = cephPoolResponse{
			PoolName:     p.PoolName,
			Pool:         int(p.Pool),
			Size:         int(p.Size),
			MinSize:      int(p.MinSize),
			PGNum:        int(p.PGNum),
			PGAutoScale:  p.PGAutoScale,
			CrushRule:    int(p.CrushRule),
			BytesUsed:    p.BytesUsed,
			PercentUsed:  p.PercentUsed,
			ReadBytesSec: p.ReadBytesSec,
			WritBytesSec: p.WritBytesSec,
			ReadOpPerSec: p.ReadOpPerSec,
			WritOpPerSec: p.WritOpPerSec,
		}
	}
	return RespondItems(c, resp)
}

// ListMonitors handles GET /api/v1/clusters/:cluster_id/ceph/monitors
func (h *CephHandler) ListMonitors(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	mons, err := pxClient.GetCephMonitors(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]cephMonResponse, len(mons))
	for i, m := range mons {
		resp[i] = cephMonResponse{
			Name: m.Name,
			Addr: m.Addr,
			Host: m.Host,
			Rank: int(m.Rank),
		}
	}
	return RespondItems(c, resp)
}

// ListFS handles GET /api/v1/clusters/:cluster_id/ceph/fs
func (h *CephHandler) ListFS(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	fs, err := pxClient.GetCephFS(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]cephFSResponse, len(fs))
	for i, f := range fs {
		resp[i] = cephFSResponse{
			Name:     f.Name,
			MetaPool: f.MetaPool,
			DataPool: f.DataPool,
		}
	}
	return RespondItems(c, resp)
}

// ListCrushRules handles GET /api/v1/clusters/:cluster_id/ceph/rules
func (h *CephHandler) ListCrushRules(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	rules, err := pxClient.GetCephCrushRules(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]cephCrushRuleResponse, len(rules))
	for i, r := range rules {
		resp[i] = cephCrushRuleResponse{
			RuleID:   r.RuleID,
			RuleName: r.RuleName,
			Type:     r.Type,
			MinSize:  r.MinSize,
			MaxSize:  r.MaxSize,
		}
	}
	return RespondItems(c, resp)
}

// CreatePool handles POST /api/v1/clusters/:cluster_id/ceph/pools
func (h *CephHandler) CreatePool(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ceph", clusterID); err != nil {
		return err
	}

	var req createPoolRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool name is required")
	}
	if req.Size <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "Pool size must be positive")
	}
	if req.PGNum <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "pg_num must be positive")
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	if err := pxClient.CreateCephPool(c.Context(), nodeName, proxmox.CephPoolCreateParams{
		Name:        req.Name,
		Size:        req.Size,
		MinSize:     req.MinSize,
		PGNum:       req.PGNum,
		Application: req.Application,
		CrushRule:   req.CrushRule,
		PGAutoScale: req.PGAutoScale,
	}); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ceph_pool", req.Name, "create", nil)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status": "created",
		"name":   req.Name,
	})
}

// DeletePool handles DELETE /api/v1/clusters/:cluster_id/ceph/pools/:pool_name
func (h *CephHandler) DeletePool(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ceph", clusterID); err != nil {
		return err
	}

	poolName := c.Params("pool_name")
	if poolName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool name is required")
	}

	pxClient, nodeName, err := h.resolveClusterNode(c)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteCephPool(c.Context(), nodeName, poolName); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ceph_pool", poolName, "delete", nil)

	return c.JSON(fiber.Map{
		"status": "deleted",
		"name":   poolName,
	})
}

// --- Database metric endpoints ---

// cephClusterMetricResponse mirrors a ceph_cluster_metrics sample for the
// history endpoint. It deliberately omits the per-sample health_checks JSONB:
// only the latest health needs reasons, so sending checks for every historical
// row (up to 7 days of samples) would bloat the response for no consumer.
type cephClusterMetricResponse struct {
	Time          time.Time `json:"time"`
	ClusterID     uuid.UUID `json:"cluster_id"`
	HealthStatus  string    `json:"health_status"`
	OSDsTotal     int32     `json:"osds_total"`
	OSDsUp        int32     `json:"osds_up"`
	OSDsIn        int32     `json:"osds_in"`
	PGsTotal      int32     `json:"pgs_total"`
	BytesUsed     int64     `json:"bytes_used"`
	BytesAvail    int64     `json:"bytes_avail"`
	BytesTotal    int64     `json:"bytes_total"`
	ReadOpsSec    int64     `json:"read_ops_sec"`
	WriteOpsSec   int64     `json:"write_ops_sec"`
	ReadBytesSec  int64     `json:"read_bytes_sec"`
	WriteBytesSec int64     `json:"write_bytes_sec"`
}

func toCephClusterMetricResponses(rows []db.GetCephClusterMetricsHistoryRow) []cephClusterMetricResponse {
	out := make([]cephClusterMetricResponse, len(rows))
	for i, m := range rows {
		out[i] = cephClusterMetricResponse{
			// The query aliases the time_bucket expression `bucket`; the DTO
			// is what keeps the JSON key "time".
			Time:          m.Bucket,
			ClusterID:     m.ClusterID,
			HealthStatus:  m.HealthStatus,
			OSDsTotal:     m.OsdsTotal,
			OSDsUp:        m.OsdsUp,
			OSDsIn:        m.OsdsIn,
			PGsTotal:      m.PgsTotal,
			BytesUsed:     m.BytesUsed,
			BytesAvail:    m.BytesAvail,
			BytesTotal:    m.BytesTotal,
			ReadOpsSec:    m.ReadOpsSec,
			WriteOpsSec:   m.WriteOpsSec,
			ReadBytesSec:  m.ReadBytesSec,
			WriteBytesSec: m.WriteBytesSec,
		}
	}
	return out
}

// cephMetricsTimeframes pairs each supported ?timeframe= with its history
// window and the bucket width GetCephClusterMetricsHistory downsamples to.
//
// The two are chosen together on purpose. Samples land once per
// METRICS_COLLECT_INTERVAL — 10s in docker-compose.yml and .env.example — so
// an un-bucketed 7-day window runs to tens of thousands of rows: megabytes of
// JSON, and a browser main thread that stops answering while it parses and
// lays them out. Every pairing here holds window/bucket to a couple of hundred
// points, already finer than the pixels available to draw them.
//
// Deliberately a separate table from datastoreMetricsTimeframes in backup.go
// rather than a shared one, even though the pairings currently agree: the two
// feed different collectors and different charts, and Ceph additionally has
// the ceph_cluster_metrics_5m/_1h rollups it could switch to. Unify them only
// if a third caller appears.
//
// This is a table rather than a switch so TestCephMetricsWindowBounded can
// range over it, and a timeframe added later is covered by that bound without
// anyone remembering to add a test row.
var cephMetricsTimeframes = map[string]struct {
	window        time.Duration
	bucketSeconds int32
}{
	"1h":  {time.Hour, 60},
	"6h":  {6 * time.Hour, 300},
	"24h": {24 * time.Hour, 600},
	"7d":  {7 * 24 * time.Hour, 3600},
}

// cephMetricsDefaultTimeframe is both the documented default for a missing
// ?timeframe= and the fallback for an unrecognised one; it must be a key of
// cephMetricsTimeframes.
const cephMetricsDefaultTimeframe = "1h"

// cephMetricsWindow maps a requested timeframe to its window and bucket width,
// falling back to the default for anything unrecognised.
func cephMetricsWindow(timeframe string) (window time.Duration, bucketSeconds int32) {
	tf, ok := cephMetricsTimeframes[timeframe]
	if !ok {
		tf = cephMetricsTimeframes[cephMetricsDefaultTimeframe]
	}
	return tf.window, tf.bucketSeconds
}

// GetHistorical handles GET /api/v1/clusters/:cluster_id/ceph/metrics
func (h *CephHandler) GetHistorical(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	timeframe := c.Query("timeframe", cephMetricsDefaultTimeframe)
	now := time.Now()
	window, bucketSeconds := cephMetricsWindow(timeframe)

	metrics, err := h.queries.GetCephClusterMetricsHistory(c.Context(), db.GetCephClusterMetricsHistoryParams{
		BucketSeconds: bucketSeconds,
		ClusterID:     clusterID,
		StartTime:     now.Add(-window),
		EndTime:       now,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get ceph metrics")
	}

	return RespondItems(c, toCephClusterMetricResponses(metrics))
}

// GetOSDMetrics handles GET /api/v1/clusters/:cluster_id/ceph/osds/metrics
func (h *CephHandler) GetOSDMetrics(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	metrics, err := h.queries.GetLatestCephOSDMetrics(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OSD metrics")
	}

	return RespondItems(c, metrics)
}

// GetPoolMetrics handles GET /api/v1/clusters/:cluster_id/ceph/pools/metrics
func (h *CephHandler) GetPoolMetrics(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "ceph", clusterID); err != nil {
		return err
	}

	metrics, err := h.queries.GetLatestCephPoolMetrics(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get pool metrics")
	}

	return RespondItems(c, metrics)
}

// --- Helpers ---

// resolveClusterNode picks the first online node for Ceph API calls.
func (h *CephHandler) resolveClusterNode(c fiber.Ctx) (*proxmox.Client, string, error) {
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return nil, "", fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return nil, "", err
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil || len(nodes) == 0 {
		return nil, "", fiber.NewError(fiber.StatusNotFound, "No nodes found in cluster")
	}

	// Prefer the first online node.
	nodeName := nodes[0].Name
	for _, n := range nodes {
		if n.Status == "online" {
			nodeName = n.Name
			break
		}
	}

	return pxClient, nodeName, nil
}

// createProxmoxClient creates a Proxmox client for the given cluster.
func (h *CephHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}
