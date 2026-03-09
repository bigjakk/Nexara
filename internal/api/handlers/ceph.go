package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
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
	Status string `json:"status"`
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
func (h *CephHandler) GetStatus(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
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
		Health: cephHealthResponse{Status: status.Health.Status},
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
func (h *CephHandler) ListOSDs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
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
	return c.JSON(osds)
}

// flattenOSDTree walks the OSD tree and returns flat OSD entries.
func flattenOSDTree(node *proxmox.CephOSDTreeNode) []cephOSDResponse {
	var result []cephOSDResponse
	if node.Type == "osd" {
		result = append(result, cephOSDResponse{
			ID:          int(node.ID),
			Name:        node.Name,
			Host:        node.Host,
			Up:          boolToInt(node.Status == "up"),
			In:          1,
			Status:      node.Status,
			CrushWeight: node.CrushWeight,
		})
	}
	for i := range node.Children {
		if node.Type == "host" {
			for j := range node.Children {
				if node.Children[j].Host == "" {
					node.Children[j].Host = node.Name
				}
			}
		}
		result = append(result, flattenOSDTree(&node.Children[i])...)
	}
	return result
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListPools handles GET /api/v1/clusters/:cluster_id/ceph/pools
func (h *CephHandler) ListPools(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
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
	return c.JSON(resp)
}

// ListMonitors handles GET /api/v1/clusters/:cluster_id/ceph/monitors
func (h *CephHandler) ListMonitors(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
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
	return c.JSON(resp)
}

// ListFS handles GET /api/v1/clusters/:cluster_id/ceph/fs
func (h *CephHandler) ListFS(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
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
	return c.JSON(resp)
}

// ListCrushRules handles GET /api/v1/clusters/:cluster_id/ceph/rules
func (h *CephHandler) ListCrushRules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
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
	return c.JSON(resp)
}

// CreatePool handles POST /api/v1/clusters/:cluster_id/ceph/pools
func (h *CephHandler) CreatePool(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ceph"); err != nil {
		return err
	}

	var req createPoolRequest
	if err := c.BodyParser(&req); err != nil {
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

	clusterID, _ := uuid.Parse(c.Params("cluster_id"))
	h.auditLog(c, clusterID, "ceph_pool", req.Name, "create")

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status": "created",
		"name":   req.Name,
	})
}

// DeletePool handles DELETE /api/v1/clusters/:cluster_id/ceph/pools/:pool_name
func (h *CephHandler) DeletePool(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ceph"); err != nil {
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

	clusterID, _ := uuid.Parse(c.Params("cluster_id"))
	h.auditLog(c, clusterID, "ceph_pool", poolName, "delete")

	return c.JSON(fiber.Map{
		"status": "deleted",
		"name":   poolName,
	})
}

// --- Database metric endpoints ---

// GetHistorical handles GET /api/v1/clusters/:cluster_id/ceph/metrics
func (h *CephHandler) GetHistorical(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	timeframe := c.Query("timeframe", "1h")
	now := time.Now()
	var start time.Time

	switch timeframe {
	case "1h":
		start = now.Add(-1 * time.Hour)
	case "6h":
		start = now.Add(-6 * time.Hour)
	case "24h":
		start = now.Add(-24 * time.Hour)
	case "7d":
		start = now.Add(-7 * 24 * time.Hour)
	default:
		start = now.Add(-1 * time.Hour)
	}

	metrics, err := h.queries.GetCephClusterMetricsHistory(c.Context(), db.GetCephClusterMetricsHistoryParams{
		ClusterID: clusterID,
		Time:      start,
		Time_2:    now,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get ceph metrics")
	}

	return c.JSON(metrics)
}

// GetOSDMetrics handles GET /api/v1/clusters/:cluster_id/ceph/osds/metrics
func (h *CephHandler) GetOSDMetrics(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	metrics, err := h.queries.GetLatestCephOSDMetrics(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OSD metrics")
	}

	return c.JSON(metrics)
}

// GetPoolMetrics handles GET /api/v1/clusters/:cluster_id/ceph/pools/metrics
func (h *CephHandler) GetPoolMetrics(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ceph"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	metrics, err := h.queries.GetLatestCephPoolMetrics(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get pool metrics")
	}

	return c.JSON(metrics)
}

// --- Helpers ---

// resolveClusterNode picks the first online node for Ceph API calls.
func (h *CephHandler) resolveClusterNode(c *fiber.Ctx) (*proxmox.Client, string, error) {
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
func (h *CephHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// auditLog writes an audit log entry.
func (h *CephHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, nil)
}
