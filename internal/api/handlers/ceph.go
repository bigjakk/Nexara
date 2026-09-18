package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
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

// cephPoolActionResponse is returned by pool create and delete. Both are
// dispatched as Proxmox tasks, so the UPID is the caller's handle on the
// outcome; neither operation has finished when the response is written.
type cephPoolActionResponse struct {
	Status string `json:"status"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	UPID   string `json:"upid"`
}

// --- Live Proxmox proxy endpoints ---

// GetStatus handles GET /api/v1/clusters/:cluster_id/ceph/status
func (h *CephHandler) GetStatus(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
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
func (h *CephHandler) ListOSDs(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
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
func (h *CephHandler) ListPools(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
	if err != nil {
		return err
	}

	pools, err := pxClient.GetCephPools(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]cephPoolResponse, len(pools))
	for i, pool := range pools {
		resp[i] = cephPoolResponse{
			PoolName:     pool.PoolName,
			Pool:         int(pool.Pool),
			Size:         int(pool.Size),
			MinSize:      int(pool.MinSize),
			PGNum:        int(pool.PGNum),
			PGAutoScale:  pool.PGAutoScale,
			CrushRule:    int(pool.CrushRule),
			BytesUsed:    pool.BytesUsed,
			PercentUsed:  pool.PercentUsed,
			ReadBytesSec: pool.ReadBytesSec,
			WritBytesSec: pool.WritBytesSec,
			ReadOpPerSec: pool.ReadOpPerSec,
			WritOpPerSec: pool.WritOpPerSec,
		}
	}
	return RespondItems(c, resp)
}

// ListMonitors handles GET /api/v1/clusters/:cluster_id/ceph/monitors
func (h *CephHandler) ListMonitors(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
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
func (h *CephHandler) ListFS(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
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
func (h *CephHandler) ListCrushRules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
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
func (h *CephHandler) CreatePool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	poolName := p.String("name")

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateCephPool(c.Context(), nodeName, proxmox.CephPoolCreateParams{
		Name:    poolName,
		Size:    int(p.Int("size")),
		MinSize: int(p.Int("min_size")),
		PGNum:   int(p.Int("pg_num")),
		// crush_rule_name is Nexara's own wire name for this field and
		// differs from PVE's, which is crush_rule. Do not "align" it — the
		// client translates, and the comment on that translation in
		// client_storage.go explains why sending PVE's spelling was the bug.
		Application: p.String("application"),
		CrushRule:   p.String("crush_rule_name"),
		PGAutoScale: p.String("pg_autoscale_mode"),
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "ceph_pool",
		ResourceID:   poolName,
		ResourceName: poolName,
		Action:       "create",
		UPID:         upid,
		Description:  fmt.Sprintf("Create Ceph pool %s on %s", poolName, nodeName),
		Extra: map[string]any{
			"size":   p.Int("size"),
			"pg_num": p.Int("pg_num"),
		},
	})

	// 202, not 201: PVE creates the pool in a background worker, so nothing is
	// created yet at this point. Reporting 201 "created" meant a pool that
	// failed inside the worker was still reported to the operator as a success.
	// The UPID is the handle on the real outcome.
	return c.Status(fiber.StatusAccepted).JSON(cephPoolActionResponse{
		Status: "dispatched",
		Name:   poolName,
		Node:   nodeName,
		UPID:   upid,
	})
}

// DeletePool handles DELETE /api/v1/clusters/:cluster_id/ceph/pools/:pool_name
func (h *CephHandler) DeletePool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	poolName := p.String("pool_name")

	pxClient, nodeName, err := h.resolveClusterNode(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DeleteCephPool(c.Context(), nodeName, poolName)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "ceph_pool",
		ResourceID:   poolName,
		ResourceName: poolName,
		Action:       "delete",
		UPID:         upid,
		Description:  fmt.Sprintf("Destroy Ceph pool %s on %s", poolName, nodeName),
	})

	// Accepted rather than "deleted" — see CreatePool. The pool and its data are
	// removed by a background worker.
	return c.Status(fiber.StatusAccepted).JSON(cephPoolActionResponse{
		Status: "dispatched",
		Name:   poolName,
		Node:   nodeName,
		UPID:   upid,
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

// CephMetricsTimeframes is the ?timeframe= vocabulary
// GET .../ceph/metrics accepts, ordered shortest window first.
//
// It is exported so the endpoint's declaration in
// internal/api/registry_ceph.go can use it as the parameter's enum: the
// list that validates the request and the table that maps a timeframe to
// its window are then held together by
// TestCephMetricsTimeframesCoverTheTable rather than by whoever remembers
// to edit both.
//
// A slice rather than the map's keys because the docs render it in order,
// and a map gives a different order on every run.
var CephMetricsTimeframes = []string{"1h", "6h", "24h", "7d"}

// CephMetricsDefaultTimeframe is both the documented default for a missing
// ?timeframe= and the fallback cephMetricsWindow applies to an
// unrecognised one; it must be a key of cephMetricsTimeframes.
//
// The declaration states it as the parameter's Default, so the fallback
// below is no longer reachable from an HTTP request — the enum refuses
// anything that is not a key. It stays because cephMetricsWindow is
// called with a stored value elsewhere, and because a lookup that returns
// a zero window on a miss would ask the database for a window ending
// before it started.
const CephMetricsDefaultTimeframe = "1h"

// cephMetricsWindow maps a requested timeframe to its window and bucket width,
// falling back to the default for anything unrecognised.
func cephMetricsWindow(timeframe string) (window time.Duration, bucketSeconds int32) {
	tf, ok := cephMetricsTimeframes[timeframe]
	if !ok {
		tf = cephMetricsTimeframes[CephMetricsDefaultTimeframe]
	}
	return tf.window, tf.bucketSeconds
}

// GetHistorical handles GET /api/v1/clusters/:cluster_id/ceph/metrics
func (h *CephHandler) GetHistorical(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	now := time.Now()
	window, bucketSeconds := cephMetricsWindow(p.String("timeframe"))

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
func (h *CephHandler) GetOSDMetrics(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	metrics, err := h.queries.GetLatestCephOSDMetrics(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OSD metrics")
	}

	return RespondItems(c, metrics)
}

// GetPoolMetrics handles GET /api/v1/clusters/:cluster_id/ceph/pools/metrics
func (h *CephHandler) GetPoolMetrics(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
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
//
// clusterID is passed in rather than re-read from the path. It used to
// parse c.Params("cluster_id") itself, which meant every handler resolved
// the same value twice and — more to the point — a second reader of the
// path that the route declaration does not describe. The caller has the
// id the schema validated; there is nothing here to re-derive.
func (h *CephHandler) resolveClusterNode(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, string, error) {
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
