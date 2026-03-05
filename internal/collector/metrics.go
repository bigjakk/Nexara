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

// cephClusterMetricSnapshot holds cluster-wide Ceph metrics.
type cephClusterMetricSnapshot struct {
	ClusterID    uuid.UUID
	HealthStatus string
	OSDsTotal    int
	OSDsUp       int
	OSDsIn       int
	PGsTotal     int
	BytesUsed    int64
	BytesAvail   int64
	BytesTotal   int64
	ReadOpsSec   int64
	WriteOpsSec  int64
	ReadBytesSec int64
	WritBytesSec int64
}

// cephOSDMetricSnapshot holds per-OSD metrics.
type cephOSDMetricSnapshot struct {
	ClusterID   uuid.UUID
	OSDID       int
	OSDName     string
	Host        string
	StatusUp    bool
	StatusIn    bool
	CrushWeight float64
}

// cephPoolMetricSnapshot holds per-pool metrics.
type cephPoolMetricSnapshot struct {
	ClusterID    uuid.UUID
	PoolID       int
	PoolName     string
	Size         int
	MinSize      int
	PGNum        int
	BytesUsed    int64
	PercentUsed  float64
	ReadOpsSec   int64
	WriteOpsSec  int64
	ReadBytesSec int64
	WritBytesSec int64
}

// pbsDatastoreMetricSnapshot holds metric data from a PBS datastore.
type pbsDatastoreMetricSnapshot struct {
	PBSServerID uuid.UUID
	Datastore   string
	Total       int64
	Used        int64
	Avail       int64
}

// pbsMetricResult holds all metric data from a single PBS server sync cycle.
type pbsMetricResult struct {
	PBSServerID      uuid.UUID
	CollectedAt      time.Time
	DatastoreMetrics []pbsDatastoreMetricSnapshot
}

// clusterMetricResult holds all metric data from a single cluster sync cycle.
type clusterMetricResult struct {
	ClusterID   uuid.UUID
	CollectedAt time.Time
	NodeMetrics []nodeMetricSnapshot
	VMMetrics   []vmMetricSnapshot
	CephCluster *cephClusterMetricSnapshot
	CephOSDs    []cephOSDMetricSnapshot
	CephPools   []cephPoolMetricSnapshot
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

// ProcessPBSResults batch-inserts PBS datastore metrics for each PBS server result.
func (mc *MetricCollector) ProcessPBSResults(ctx context.Context, results []*pbsMetricResult) {
	for _, result := range results {
		if result == nil || len(result.DatastoreMetrics) == 0 {
			continue
		}

		inserted, err := batchInsertPBSDatastoreMetrics(ctx, mc.copier, result.CollectedAt, result.DatastoreMetrics)
		if err != nil {
			mc.logger.Error("failed to insert PBS datastore metrics",
				"pbs_id", result.PBSServerID,
				"error", err,
			)
		} else {
			mc.logger.Debug("inserted PBS datastore metrics",
				"pbs_id", result.PBSServerID,
				"count", inserted,
			)
		}
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

		// Batch insert Ceph metrics.
		if result.CephCluster != nil {
			inserted, err = batchInsertCephClusterMetrics(ctx, mc.copier, result.CollectedAt, []cephClusterMetricSnapshot{*result.CephCluster})
			if err != nil {
				mc.logger.Error("failed to insert ceph cluster metrics",
					"cluster_id", result.ClusterID,
					"error", err,
				)
			} else {
				mc.logger.Debug("inserted ceph cluster metrics",
					"cluster_id", result.ClusterID,
					"count", inserted,
				)
			}
		}

		if len(result.CephOSDs) > 0 {
			inserted, err = batchInsertCephOSDMetrics(ctx, mc.copier, result.CollectedAt, result.CephOSDs)
			if err != nil {
				mc.logger.Error("failed to insert ceph OSD metrics",
					"cluster_id", result.ClusterID,
					"error", err,
				)
			} else {
				mc.logger.Debug("inserted ceph OSD metrics",
					"cluster_id", result.ClusterID,
					"count", inserted,
				)
			}
		}

		if len(result.CephPools) > 0 {
			inserted, err = batchInsertCephPoolMetrics(ctx, mc.copier, result.CollectedAt, result.CephPools)
			if err != nil {
				mc.logger.Error("failed to insert ceph pool metrics",
					"cluster_id", result.ClusterID,
					"error", err,
				)
			} else {
				mc.logger.Debug("inserted ceph pool metrics",
					"cluster_id", result.ClusterID,
					"count", inserted,
				)
			}
		}

		if mc.publisher != nil {
			mc.publisher.PublishClusterMetrics(ctx, result)
		}
	}
}
