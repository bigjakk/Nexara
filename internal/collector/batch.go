package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CopyFromer wraps pgx.CopyFrom for testability.
type CopyFromer interface {
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

var nodeMetricColumns = []string{
	"time", "node_id", "cpu_usage", "mem_used", "mem_total",
	"disk_read", "disk_write", "net_in", "net_out",
}

var vmMetricColumns = []string{
	"time", "vm_id", "cpu_usage", "mem_used", "mem_total",
	"disk_read", "disk_write", "net_in", "net_out",
}

func batchInsertNodeMetrics(ctx context.Context, copier CopyFromer, ts time.Time, snapshots []nodeMetricSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	src := pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
		s := snapshots[i]
		return []any{ts, s.NodeID, s.CPUUsage, s.MemUsed, s.MemTotal, s.DiskRead, s.DiskWrite, s.NetIn, s.NetOut}, nil
	})

	n, err := copier.CopyFrom(ctx, pgx.Identifier{"node_metrics"}, nodeMetricColumns, src)
	if err != nil {
		return 0, fmt.Errorf("copy node metrics: %w", err)
	}
	return n, nil
}

func batchInsertVMMetrics(ctx context.Context, copier CopyFromer, ts time.Time, snapshots []vmMetricSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	src := pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
		s := snapshots[i]
		return []any{ts, s.VMID, s.CPUUsage, s.MemUsed, s.MemTotal, s.DiskRead, s.DiskWrite, s.NetIn, s.NetOut}, nil
	})

	n, err := copier.CopyFrom(ctx, pgx.Identifier{"vm_metrics"}, vmMetricColumns, src)
	if err != nil {
		return 0, fmt.Errorf("copy VM metrics: %w", err)
	}
	return n, nil
}

// --- PBS batch inserts ---

var pbsDatastoreMetricColumns = []string{
	"time", "pbs_server_id", "datastore", "total", "used", "avail",
}

func batchInsertPBSDatastoreMetrics(ctx context.Context, copier CopyFromer, ts time.Time, snapshots []pbsDatastoreMetricSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	src := pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
		s := snapshots[i]
		return []any{ts, s.PBSServerID, s.Datastore, s.Total, s.Used, s.Avail}, nil
	})

	n, err := copier.CopyFrom(ctx, pgx.Identifier{"pbs_datastore_metrics"}, pbsDatastoreMetricColumns, src)
	if err != nil {
		return 0, fmt.Errorf("copy PBS datastore metrics: %w", err)
	}
	return n, nil
}

// --- Ceph batch inserts ---

var cephClusterMetricColumns = []string{
	"time", "cluster_id", "health_status",
	"osds_total", "osds_up", "osds_in", "pgs_total",
	"bytes_used", "bytes_avail", "bytes_total",
	"read_ops_sec", "write_ops_sec", "read_bytes_sec", "write_bytes_sec",
}

var cephOSDMetricColumns = []string{
	"time", "cluster_id", "osd_id", "osd_name", "host",
	"status_up", "status_in", "crush_weight",
}

var cephPoolMetricColumns = []string{
	"time", "cluster_id", "pool_id", "pool_name",
	"size", "min_size", "pg_num",
	"bytes_used", "percent_used",
	"read_ops_sec", "write_ops_sec", "read_bytes_sec", "write_bytes_sec",
}

func batchInsertCephClusterMetrics(ctx context.Context, copier CopyFromer, ts time.Time, snapshots []cephClusterMetricSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	src := pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
		s := snapshots[i]
		return []any{
			ts, s.ClusterID, s.HealthStatus,
			s.OSDsTotal, s.OSDsUp, s.OSDsIn, s.PGsTotal,
			s.BytesUsed, s.BytesAvail, s.BytesTotal,
			s.ReadOpsSec, s.WriteOpsSec, s.ReadBytesSec, s.WritBytesSec,
		}, nil
	})

	n, err := copier.CopyFrom(ctx, pgx.Identifier{"ceph_cluster_metrics"}, cephClusterMetricColumns, src)
	if err != nil {
		return 0, fmt.Errorf("copy ceph cluster metrics: %w", err)
	}
	return n, nil
}

func batchInsertCephOSDMetrics(ctx context.Context, copier CopyFromer, ts time.Time, snapshots []cephOSDMetricSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	src := pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
		s := snapshots[i]
		return []any{
			ts, s.ClusterID, s.OSDID, s.OSDName, s.Host,
			s.StatusUp, s.StatusIn, s.CrushWeight,
		}, nil
	})

	n, err := copier.CopyFrom(ctx, pgx.Identifier{"ceph_osd_metrics"}, cephOSDMetricColumns, src)
	if err != nil {
		return 0, fmt.Errorf("copy ceph OSD metrics: %w", err)
	}
	return n, nil
}

func batchInsertCephPoolMetrics(ctx context.Context, copier CopyFromer, ts time.Time, snapshots []cephPoolMetricSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	src := pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
		s := snapshots[i]
		return []any{
			ts, s.ClusterID, s.PoolID, s.PoolName,
			s.Size, s.MinSize, s.PGNum,
			s.BytesUsed, s.PercentUsed,
			s.ReadOpsSec, s.WriteOpsSec, s.ReadBytesSec, s.WritBytesSec,
		}, nil
	})

	n, err := copier.CopyFrom(ctx, pgx.Identifier{"ceph_pool_metrics"}, cephPoolMetricColumns, src)
	if err != nil {
		return 0, fmt.Errorf("copy ceph pool metrics: %w", err)
	}
	return n, nil
}
