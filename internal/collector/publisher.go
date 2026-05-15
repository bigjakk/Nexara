package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Publisher publishes metric summaries and alerts to Redis.
type Publisher struct {
	client *redis.Client
	logger *slog.Logger
	rates  *rateState
}

// NewPublisher creates a new Redis Publisher.
func NewPublisher(client *redis.Client, logger *slog.Logger) *Publisher {
	return &Publisher{
		client: client,
		logger: logger,
		rates:  newRateState(),
	}
}

// wsNodeSnapshot is the per-node payload broadcast on the metrics channel.
// Carries both the cumulative counters (kept for historical compatibility and
// time-series storage) and the bytes-per-second rates derived from the prior
// cycle so clients render real throughput on the very first frame.
type wsNodeSnapshot struct {
	NodeID       uuid.UUID `json:"node_id"`
	CPUUsage     float64   `json:"cpu_usage"`
	MemUsed      int64     `json:"mem_used"`
	MemTotal     int64     `json:"mem_total"`
	DiskRead     int64     `json:"disk_read"`
	DiskWrite    int64     `json:"disk_write"`
	NetIn        int64     `json:"net_in"`
	NetOut       int64     `json:"net_out"`
	DiskReadBps  float64   `json:"disk_read_bps"`
	DiskWriteBps float64   `json:"disk_write_bps"`
	NetInBps     float64   `json:"net_in_bps"`
	NetOutBps    float64   `json:"net_out_bps"`
}

// wsVMSnapshot mirrors wsNodeSnapshot for VMs and containers.
type wsVMSnapshot struct {
	VMID         uuid.UUID `json:"vm_id"`
	CPUUsage     float64   `json:"cpu_usage"`
	MemUsed      int64     `json:"mem_used"`
	MemTotal     int64     `json:"mem_total"`
	DiskRead     int64     `json:"disk_read"`
	DiskWrite    int64     `json:"disk_write"`
	NetIn        int64     `json:"net_in"`
	NetOut       int64     `json:"net_out"`
	DiskReadBps  float64   `json:"disk_read_bps"`
	DiskWriteBps float64   `json:"disk_write_bps"`
	NetInBps     float64   `json:"net_in_bps"`
	NetOutBps    float64   `json:"net_out_bps"`
}

// clusterMetricSummary is the JSON payload published to Redis.
type clusterMetricSummary struct {
	ClusterID   uuid.UUID        `json:"cluster_id"`
	CollectedAt string           `json:"collected_at"`
	NodeCount   int              `json:"node_count"`
	VMCount     int              `json:"vm_count"`
	Nodes       []wsNodeSnapshot `json:"nodes"`
	VMs         []wsVMSnapshot   `json:"vms"`
}

// nodeOfflineAlert is the JSON payload published when a node goes offline.
type nodeOfflineAlert struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	NodeName  string    `json:"node_name"`
	Event     string    `json:"event"`
}

// PublishClusterMetrics publishes a metric summary to Redis. Errors are logged, never returned.
func (p *Publisher) PublishClusterMetrics(ctx context.Context, result *ClusterMetricResult) {
	nodeRates, vmRates := p.rates.Apply(result.ClusterID, result.CollectedAt, result.NodeMetrics, result.VMMetrics)

	nodes := make([]wsNodeSnapshot, 0, len(result.NodeMetrics))
	for _, n := range result.NodeMetrics {
		r := nodeRates[n.NodeID]
		nodes = append(nodes, wsNodeSnapshot{
			NodeID:       n.NodeID,
			CPUUsage:     n.CPUUsage,
			MemUsed:      n.MemUsed,
			MemTotal:     n.MemTotal,
			DiskRead:     n.DiskRead,
			DiskWrite:    n.DiskWrite,
			NetIn:        n.NetIn,
			NetOut:       n.NetOut,
			DiskReadBps:  r.DiskReadBps,
			DiskWriteBps: r.DiskWriteBps,
			NetInBps:     r.NetInBps,
			NetOutBps:    r.NetOutBps,
		})
	}

	vms := make([]wsVMSnapshot, 0, len(result.VMMetrics))
	for _, v := range result.VMMetrics {
		r := vmRates[v.VMID]
		vms = append(vms, wsVMSnapshot{
			VMID:         v.VMID,
			CPUUsage:     v.CPUUsage,
			MemUsed:      v.MemUsed,
			MemTotal:     v.MemTotal,
			DiskRead:     v.DiskRead,
			DiskWrite:    v.DiskWrite,
			NetIn:        v.NetIn,
			NetOut:       v.NetOut,
			DiskReadBps:  r.DiskReadBps,
			DiskWriteBps: r.DiskWriteBps,
			NetInBps:     r.NetInBps,
			NetOutBps:    r.NetOutBps,
		})
	}

	summary := clusterMetricSummary{
		ClusterID:   result.ClusterID,
		CollectedAt: result.CollectedAt.UTC().Format(time.RFC3339Nano),
		NodeCount:   len(result.NodeMetrics),
		VMCount:     len(result.VMMetrics),
		Nodes:       nodes,
		VMs:         vms,
	}

	data, err := json.Marshal(summary)
	if err != nil {
		p.logger.Error("failed to marshal metric summary", "error", err)
		return
	}

	channel := fmt.Sprintf("nexara:metrics:%s", result.ClusterID)
	if err := p.client.Publish(ctx, channel, data).Err(); err != nil {
		p.logger.Error("failed to publish metrics to Redis",
			"channel", channel,
			"error", err,
		)
	}
}

// ForgetCluster drops cached per-entity counters for a cluster. Call when a
// cluster is deleted so rate state does not leak.
func (p *Publisher) ForgetCluster(clusterID uuid.UUID) {
	if p == nil || p.rates == nil {
		return
	}
	p.rates.Forget(clusterID)
}

// PublishNodeOffline publishes an alert when a node is marked offline.
func (p *Publisher) PublishNodeOffline(ctx context.Context, clusterID, nodeID uuid.UUID, nodeName string) {
	alert := nodeOfflineAlert{
		ClusterID: clusterID,
		NodeID:    nodeID,
		NodeName:  nodeName,
		Event:     "node_offline",
	}

	data, err := json.Marshal(alert)
	if err != nil {
		p.logger.Error("failed to marshal offline alert", "error", err)
		return
	}

	channel := fmt.Sprintf("nexara:alerts:%s", clusterID)
	if err := p.client.Publish(ctx, channel, data).Err(); err != nil {
		p.logger.Error("failed to publish alert to Redis",
			"channel", channel,
			"error", err,
		)
	}
}
