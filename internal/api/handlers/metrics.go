package handlers

import (
	"maps"
	"math"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// All three routes are declared in internal/api/registry_metrics.go, which
// states their cluster-scoped permission (view:cluster, view:vm, view:node
// respectively) and their parameters; nothing below re-checks either.
//
// What stays here is what the declaration cannot see: the per-guest and
// per-node queries key on the GUEST's or NODE's row id alone, so each handler
// re-reads the row and refuses it when its cluster_id is not the one the gate
// authorized.

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

// rangeDurations is the accepted ?range= vocabulary and the window each
// member means. It is the same set metricRangeParam's Enum declares in
// internal/api/registry_metrics.go — TestMetricRangeVocabulary pins the two
// key sets against each other, because they are two copies of one list and a
// copy nothing compares is a copy that rots.
var rangeDurations = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// MetricRangeKeys returns the accepted ?range= values, sorted. Exported for
// the guard in package api that compares them against the declared Enum;
// package handlers cannot import package api, so the comparison has to read
// this from the other side.
func MetricRangeKeys() []string { return slices.Sorted(maps.Keys(rangeDurations)) }

// metricWindow resolves a validated ?range= to its duration.
//
// The declaration's Enum is what rejects an unknown value, so a miss here can
// only mean the Enum and this map have drifted — our bug, not the caller's.
// Answering 500 rather than falling through to a zero duration is the same
// split parseParamUUID makes: a zero window would return 200 with an empty
// series, which reads as "this guest has no metrics" instead of as a fault.
func metricWindow(rangeParam string) (time.Duration, error) {
	d, ok := rangeDurations[rangeParam]
	if !ok {
		return 0, fiber.NewError(fiber.StatusInternalServerError, "Unsupported metric range")
	}
	return d, nil
}

// rawRow is a generic container for rows from any metric query.
type rawRow struct {
	bucket                                                     time.Time
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
func (h *MetricsHandler) GetClusterHistorical(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	rangeParam := p.String("range")
	window, err := metricWindow(rangeParam)
	if err != nil {
		return err
	}
	since := time.Now().Add(-window)

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

	return RespondItems(c, computeRates(rows))
}

// GetVMHistorical handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/metrics.
func (h *MetricsHandler) GetVMHistorical(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vmID, err := parseParamUUID(p.String("vm_id"))
	if err != nil {
		return err
	}
	// The metric query keys on vm_id alone, so verify the VM actually belongs to
	// the authorized cluster — otherwise a user with view on cluster X could read
	// a VM that lives in cluster Y by passing its id.
	if vm, vErr := h.queries.GetVM(c.Context(), vmID); vErr != nil || vm.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "VM not found")
	}

	rangeParam := p.String("range")
	window, err := metricWindow(rangeParam)
	if err != nil {
		return err
	}
	since := time.Now().Add(-window)

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

	return RespondItems(c, computeRates(rows))
}

// GetNodeHistorical handles GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics.
func (h *MetricsHandler) GetNodeHistorical(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeID, err := parseParamUUID(p.String("node_id"))
	if err != nil {
		return err
	}
	// Verify the node belongs to the authorized cluster (query keys on node_id).
	if node, nErr := h.queries.GetNode(c.Context(), nodeID); nErr != nil || node.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Node not found")
	}

	rangeParam := p.String("range")
	window, err := metricWindow(rangeParam)
	if err != nil {
		return err
	}
	since := time.Now().Add(-window)

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

	return RespondItems(c, computeRates(rows))
}
