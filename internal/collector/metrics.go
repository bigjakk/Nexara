package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// nodeMetricSnapshot holds metric data collected from a Proxmox node.
type nodeMetricSnapshot struct {
	NodeID    uuid.UUID `json:"node_id"`
	CPUUsage  float64   `json:"cpu_usage"`
	MemUsed   int64     `json:"mem_used"`
	MemTotal  int64     `json:"mem_total"`
	DiskRead  int64     `json:"disk_read"`
	DiskWrite int64     `json:"disk_write"`
	NetIn     int64     `json:"net_in"`
	NetOut    int64     `json:"net_out"`
}

// vmMetricSnapshot holds metric data collected from a VM or container.
type vmMetricSnapshot struct {
	VMID      uuid.UUID `json:"vm_id"`
	CPUUsage  float64   `json:"cpu_usage"`
	MemUsed   int64     `json:"mem_used"`
	MemTotal  int64     `json:"mem_total"`
	DiskRead  int64     `json:"disk_read"`
	DiskWrite int64     `json:"disk_write"`
	NetIn     int64     `json:"net_in"`
	NetOut    int64     `json:"net_out"`
}

// nodeCollectionResult holds all data collected from syncing a single node.
type nodeCollectionResult struct {
	Node       uuid.UUID
	NodeMetric nodeMetricSnapshot
	VMMetrics  []vmMetricSnapshot
}

// clusterMetricResult holds all metric data from a single cluster sync cycle.
type clusterMetricResult struct {
	ClusterID   uuid.UUID
	CollectedAt time.Time
	NodeMetrics []nodeMetricSnapshot
	VMMetrics   []vmMetricSnapshot
}

// MetricCollector orchestrates batch insertion and publishing of collected metrics.
type MetricCollector struct {
	copier    CopyFromer
	publisher *Publisher
	logger    *slog.Logger
}

// NewMetricCollector creates a MetricCollector.
func NewMetricCollector(copier CopyFromer, publisher *Publisher, logger *slog.Logger) *MetricCollector {
	return &MetricCollector{
		copier:    copier,
		publisher: publisher,
		logger:    logger,
	}
}

// ProcessResults batch-inserts metrics and publishes summaries for each cluster result.
func (mc *MetricCollector) ProcessResults(ctx context.Context, results []*clusterMetricResult) {
	for _, result := range results {
		if result == nil {
			continue
		}

		inserted, err := batchInsertNodeMetrics(ctx, mc.copier, result.CollectedAt, result.NodeMetrics)
		if err != nil {
			mc.logger.Error("failed to insert node metrics",
				"cluster_id", result.ClusterID,
				"error", err,
			)
		} else {
			mc.logger.Debug("inserted node metrics",
				"cluster_id", result.ClusterID,
				"count", inserted,
			)
		}

		inserted, err = batchInsertVMMetrics(ctx, mc.copier, result.CollectedAt, result.VMMetrics)
		if err != nil {
			mc.logger.Error("failed to insert VM metrics",
				"cluster_id", result.ClusterID,
				"error", err,
			)
		} else {
			mc.logger.Debug("inserted VM metrics",
				"cluster_id", result.ClusterID,
				"count", inserted,
			)
		}

		if mc.publisher != nil {
			mc.publisher.PublishClusterMetrics(ctx, result)
		}
	}
}
