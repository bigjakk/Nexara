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
