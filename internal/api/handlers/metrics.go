package handlers

import (
	"math"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
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
	CPUPercent   float64 `json:"cpuPercent"`
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

// rawRow is a generic container for rows from any metric query.
type rawRow struct {
	bucket                                                      time.Time
	cpu, memUsed, memTotal, diskRead, diskWrite, netIn, netOut float64
}

// computeRates converts cumulative I/O counters into per-second rates
// by computing deltas between consecutive time-ordered points.
// CPU and memory are passed through unchanged.
func computeRates(rows []rawRow) []metricPoint {
	if len(rows) == 0 {
		return nil
	}

	points := make([]metricPoint, len(rows))

	// First point has no predecessor — emit zero I/O rates.
	points[0] = toMetricPoint(rows[0], 0, 0, 0, 0)

	for i := 1; i < len(rows); i++ {
		elapsed := rows[i].bucket.Sub(rows[i-1].bucket).Seconds()

		drBps := ioRate(rows[i].diskRead, rows[i-1].diskRead, elapsed)
		dwBps := ioRate(rows[i].diskWrite, rows[i-1].diskWrite, elapsed)
		niBps := ioRate(rows[i].netIn, rows[i-1].netIn, elapsed)
		noBps := ioRate(rows[i].netOut, rows[i-1].netOut, elapsed)

		points[i] = toMetricPoint(rows[i], drBps, dwBps, niBps, noBps)
	}

	return points
}

// ioRate computes (cur - prev) / elapsed, clamped to zero on counter reset or reboot.
func ioRate(cur, prev, elapsed float64) float64 {
	if elapsed <= 0 {
		return 0
	}
	delta := cur - prev
	if delta < 0 {
		// Counter reset (reboot) — return zero rather than a negative spike.
		return 0
	}
	return math.Max(delta/elapsed, 0)
}

func toMetricPoint(r rawRow, diskReadBps, diskWriteBps, netInBps, netOutBps float64) metricPoint {
	var memPercent float64
	if r.memTotal > 0 {
		memPercent = (r.memUsed / r.memTotal) * 100
	}
	return metricPoint{
		Timestamp:    r.bucket.UnixMilli(),
		CPUPercent:   r.cpu * 100,
		MemPercent:   memPercent,
		DiskReadBps:  diskReadBps,
		DiskWriteBps: diskWriteBps,
		NetInBps:     netInBps,
		NetOutBps:    netOutBps,
	}
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

	var rows []rawRow

	switch rangeParam {
	case "1h", "6h":
		dbRows, qErr := h.queries.GetClusterMetrics5m(c.Context(), db.GetClusterMetrics5mParams{
			ClusterID: clusterID,
			Bucket:    since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		rows = make([]rawRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rawRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
	case "24h", "7d":
		dbRows, qErr := h.queries.GetClusterMetrics1h(c.Context(), db.GetClusterMetrics1hParams{
			ClusterID: clusterID,
			Bucket:    since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		rows = make([]rawRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rawRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
	}

	return c.JSON(computeRates(rows))
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

	var rows []rawRow

	switch rangeParam {
	case "1h", "6h":
		dbRows, qErr := h.queries.GetVMMetrics5m(c.Context(), db.GetVMMetrics5mParams{
			VmID:   vmID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		rows = make([]rawRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rawRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
	case "24h", "7d":
		dbRows, qErr := h.queries.GetVMMetrics1h(c.Context(), db.GetVMMetrics1hParams{
			VmID:   vmID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		rows = make([]rawRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rawRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
	}

	return c.JSON(computeRates(rows))
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

	var rows []rawRow

	switch rangeParam {
	case "1h", "6h":
		dbRows, qErr := h.queries.GetNodeMetrics5m(c.Context(), db.GetNodeMetrics5mParams{
			NodeID: nodeID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		rows = make([]rawRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rawRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
	case "24h", "7d":
		dbRows, qErr := h.queries.GetNodeMetrics1h(c.Context(), db.GetNodeMetrics1hParams{
			NodeID: nodeID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		rows = make([]rawRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rawRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
	}

	return c.JSON(computeRates(rows))
}
