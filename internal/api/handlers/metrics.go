package handlers

import (
	"maps"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

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

// metricPoint is one point of a chart series. A nil value is no measurement —
// nothing was sampled there, or a counter fell or went unreported — so a chart
// breaks its line instead of drawing a zero that would read as a stall.
type metricPoint struct {
	Timestamp    int64    `json:"timestamp"`
	CPUPercent   *float64 `json:"cpuPercent"`
	MemPercent   *float64 `json:"memPercent"`
	DiskReadBps  *float64 `json:"diskReadBps"`
	DiskWriteBps *float64 `json:"diskWriteBps"`
	NetInBps     *float64 `json:"netInBps"`
	NetOutBps    *float64 `json:"netOutBps"`
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

// The two short ranges are served from the raw samples, bucketed to
// window/seriesPoints (1h at 10 s, 6h at 1 minute), so a stall of a minute or
// two shows; 24h reads the 5-minute rollup and 7d the hourly one.
const (
	rollup5m = 5 * time.Minute
	rollup1h = time.Hour
)

// rawWidth is a raw-sample range's bucket width.
func rawWidth(window time.Duration) time.Duration { return window / seriesPoints }

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

	// Each node is rated on its own, then the nodes merged: see mergeNodes.
	perNode := map[uuid.UUID][]ratedSample{}
	var width time.Duration

	switch rangeParam {
	case "1h", "6h":
		dbRows, qErr := h.queries.GetClusterNodeMetricsRaw(c.Context(), db.GetClusterNodeMetricsRawParams{
			ClusterID: clusterID,
			Since:     rawFetchSince(since),
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		samples := map[uuid.UUID][]rawSample{}
		for _, r := range dbRows {
			samples[r.NodeID] = append(samples[r.NodeID], rawSample{
				at: r.Time, cpu: r.CpuUsage, memUsed: float64(r.MemUsed), memTotal: float64(r.MemTotal),
				diskRead: float64(r.DiskRead), diskWrite: float64(r.DiskWrite),
				netIn: float64(r.NetIn), netOut: float64(r.NetOut), diskUnreported: r.DiskUnreported,
			})
		}
		for id, s := range samples {
			perNode[id] = rateSamples(s, false)
		}
		width = rawWidth(window)
	case "24h":
		dbRows, qErr := h.queries.GetClusterNodeMetrics5m(c.Context(), db.GetClusterNodeMetrics5mParams{
			ClusterID: clusterID,
			Since:     since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		rows := map[uuid.UUID][]rollupRow{}
		for _, r := range dbRows {
			rows[r.NodeID] = append(rows[r.NodeID], rollupRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut})
		}
		for id, r := range rows {
			perNode[id] = rollupSamples(r)
		}
		width = rollup5m
	case "7d":
		dbRows, qErr := h.queries.GetClusterNodeMetrics1h(c.Context(), db.GetClusterNodeMetrics1hParams{
			ClusterID: clusterID,
			Since:     since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query metrics")
		}
		rows := map[uuid.UUID][]rollupRow{}
		for _, r := range dbRows {
			rows[r.NodeID] = append(rows[r.NodeID], rollupRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut})
		}
		for id, r := range rows {
			perNode[id] = rollupSamples(r)
		}
		width = rollup1h
	}

	return RespondItems(c, withGaps(bucketSamples(mergeNodes(perNode), since, width)))
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

	var rated []ratedSample
	var width time.Duration

	switch rangeParam {
	case "1h", "6h":
		dbRows, qErr := h.queries.GetVMRecentMetrics(c.Context(), db.GetVMRecentMetricsParams{
			VmID: vmID,
			Time: rawFetchSince(since),
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		rated = rateSamples(oldestFirst(dbRows, func(r db.GetVMRecentMetricsRow) rawSample {
			return rawSample{
				at: r.Bucket, cpu: r.CpuUsage, memUsed: float64(r.MemUsed), memTotal: float64(r.MemTotal),
				diskRead: float64(r.DiskRead), diskWrite: float64(r.DiskWrite),
				netIn: float64(r.NetIn), netOut: float64(r.NetOut), diskUnreported: r.DiskUnreported,
			}
		}), true)
		width = rawWidth(window)
	case "24h":
		dbRows, qErr := h.queries.GetVMMetrics5m(c.Context(), db.GetVMMetrics5mParams{
			VmID:   vmID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		rows := make([]rollupRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rollupRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
		rated = rollupSamples(rows)
		width = rollup5m
	case "7d":
		dbRows, qErr := h.queries.GetVMMetrics1h(c.Context(), db.GetVMMetrics1hParams{
			VmID:   vmID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query VM metrics")
		}
		rows := make([]rollupRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rollupRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
		rated = rollupSamples(rows)
		width = rollup1h
	}

	return RespondItems(c, withGaps(bucketSamples(rated, since, width)))
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

	var rated []ratedSample
	var width time.Duration

	switch rangeParam {
	case "1h", "6h":
		dbRows, qErr := h.queries.GetNodeRecentMetrics(c.Context(), db.GetNodeRecentMetricsParams{
			NodeID: nodeID,
			Time:   rawFetchSince(since),
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		rated = rateSamples(oldestFirst(dbRows, func(r db.GetNodeRecentMetricsRow) rawSample {
			return rawSample{
				at: r.Bucket, cpu: r.CpuUsage, memUsed: float64(r.MemUsed), memTotal: float64(r.MemTotal),
				diskRead: float64(r.DiskRead), diskWrite: float64(r.DiskWrite),
				netIn: float64(r.NetIn), netOut: float64(r.NetOut), diskUnreported: r.DiskUnreported,
			}
		}), false)
		width = rawWidth(window)
	case "24h":
		dbRows, qErr := h.queries.GetNodeMetrics5m(c.Context(), db.GetNodeMetrics5mParams{
			NodeID: nodeID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		rows := make([]rollupRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rollupRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
		rated = rollupSamples(rows)
		width = rollup5m
	case "7d":
		dbRows, qErr := h.queries.GetNodeMetrics1h(c.Context(), db.GetNodeMetrics1hParams{
			NodeID: nodeID,
			Bucket: since,
		})
		if qErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to query node metrics")
		}
		rows := make([]rollupRow, len(dbRows))
		for i, r := range dbRows {
			rows[i] = rollupRow{r.Bucket, r.Cpu, r.MemUsed, r.MemTotal, r.DiskRead, r.DiskWrite, r.NetIn, r.NetOut}
		}
		rated = rollupSamples(rows)
		width = rollup1h
	}

	return RespondItems(c, withGaps(bucketSamples(rated, since, width)))
}
