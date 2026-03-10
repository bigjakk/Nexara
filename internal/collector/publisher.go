package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Publisher publishes metric summaries and alerts to Redis.
type Publisher struct {
	client *redis.Client
	logger *slog.Logger
}

// NewPublisher creates a new Redis Publisher.
func NewPublisher(client *redis.Client, logger *slog.Logger) *Publisher {
	return &Publisher{
		client: client,
		logger: logger,
	}
}

// clusterMetricSummary is the JSON payload published to Redis.
type clusterMetricSummary struct {
	ClusterID   uuid.UUID            `json:"cluster_id"`
	CollectedAt string               `json:"collected_at"`
	NodeCount   int                  `json:"node_count"`
	VMCount     int                  `json:"vm_count"`
	Nodes       []nodeMetricSnapshot `json:"nodes"`
	VMs         []vmMetricSnapshot   `json:"vms"`
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
	summary := clusterMetricSummary{
		ClusterID:   result.ClusterID,
		CollectedAt: result.CollectedAt.UTC().Format("2006-01-02T15:04:05Z"),
		NodeCount:   len(result.NodeMetrics),
		VMCount:     len(result.VMMetrics),
		Nodes:       result.NodeMetrics,
		VMs:         result.VMMetrics,
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
