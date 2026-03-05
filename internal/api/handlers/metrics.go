package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

// MetricsHandler handles historical metric endpoints.
type MetricsHandler struct {
	queries *db.Queries
}

// NewMetricsHandler creates a new metrics handler.
func NewMetricsHandler(queries *db.Queries) *MetricsHandler {
	return &MetricsHandler{queries: queries}
}

type metricPoint struct {
	Timestamp    int64   `json:"timestamp"`
	CpuPercent   float64 `json:"cpuPercent"`
	MemPercent   float64 `json:"memPercent"`
	DiskReadBps  float64 `json:"diskReadBps"`
	DiskWriteBps float64 `json:"diskWriteBps"`
	NetInBps     float64 `json:"netInBps"`
	NetOutBps    float64 `json:"netOutBps"`
}

var rangeDurations = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// GetClusterHistorical handles GET /api/v1/clusters/:cluster_id/metrics.
func (h *MetricsHandler) GetClusterHistorical(c *fiber.Ctx) error {
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	rangeParam := c.Query("range", "1h")
	duration, ok := rangeDurations[rangeParam]
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid range: must be 1h, 6h, 24h, or 7d")
	}

	since := time.Now().Add(-duration)

	var points []metricPoint

	switch rangeParam {
	case "1h", "6h":
		rows, err := h.queries.GetClusterMetrics5m(c.Context(), db.GetClusterMetrics5mParams{
			ClusterID: clusterID,
			Bucket:    since,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		points = make([]metricPoint, len(rows))
		for i, r := range rows {
			points[i] = toMetricPoint(r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut)
		}
	case "24h", "7d":
		rows, err := h.queries.GetClusterMetrics1h(c.Context(), db.GetClusterMetrics1hParams{
			ClusterID: clusterID,
			Bucket:    since,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		points = make([]metricPoint, len(rows))
		for i, r := range rows {
			points[i] = toMetricPoint(r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut)
		}
	}

	return c.JSON(points)
}

// GetVMHistorical handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/metrics.
func (h *MetricsHandler) GetVMHistorical(c *fiber.Ctx) error {
	vmID, err := uuid.Parse(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	rangeParam := c.Query("range", "1h")
	duration, ok := rangeDurations[rangeParam]
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid range: must be 1h, 6h, 24h, or 7d")
	}

	since := time.Now().Add(-duration)

	var points []metricPoint

	switch rangeParam {
	case "1h", "6h":
		rows, qErr := h.queries.GetVMMetrics5m(c.Context(), db.GetVMMetrics5mParams{
			VmID:   vmID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		points = make([]metricPoint, len(rows))
		for i, r := range rows {
			points[i] = toMetricPoint(r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut)
		}
	case "24h", "7d":
		rows, qErr := h.queries.GetVMMetrics1h(c.Context(), db.GetVMMetrics1hParams{
			VmID:   vmID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		points = make([]metricPoint, len(rows))
		for i, r := range rows {
			points[i] = toMetricPoint(r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut)
		}
	}

	return c.JSON(points)
}

// GetNodeHistorical handles GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics.
func (h *MetricsHandler) GetNodeHistorical(c *fiber.Ctx) error {
	nodeID, err := uuid.Parse(c.Params("node_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
	}

	rangeParam := c.Query("range", "1h")
	duration, ok := rangeDurations[rangeParam]
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid range: must be 1h, 6h, 24h, or 7d")
	}

	since := time.Now().Add(-duration)

	var points []metricPoint

	switch rangeParam {
	case "1h", "6h":
		rows, qErr := h.queries.GetNodeMetrics5m(c.Context(), db.GetNodeMetrics5mParams{
			NodeID: nodeID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		points = make([]metricPoint, len(rows))
		for i, r := range rows {
			points[i] = toMetricPoint(r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut)
		}
	case "24h", "7d":
		rows, qErr := h.queries.GetNodeMetrics1h(c.Context(), db.GetNodeMetrics1hParams{
			NodeID: nodeID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		points = make([]metricPoint, len(rows))
		for i, r := range rows {
			points[i] = toMetricPoint(r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut)
		}
	}

	return c.JSON(points)
}

func toMetricPoint(bucket time.Time, cpu, memUsed, memTotal, diskRead, diskWrite, netIn, netOut float64) metricPoint {
	var memPercent float64
	if memTotal > 0 {
		memPercent = (memUsed / memTotal) * 100
	}
	return metricPoint{
		Timestamp:    bucket.UnixMilli(),
		CpuPercent:   cpu * 100,
		MemPercent:   memPercent,
		DiskReadBps:  diskRead,
		DiskWriteBps: diskWrite,
		NetInBps:     netIn,
		NetOutBps:    netOut,
	}
}
