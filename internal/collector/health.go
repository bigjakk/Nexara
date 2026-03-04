package collector

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// MetricQueries defines DB operations for the health monitor.
type MetricQueries interface {
	MarkNodeOffline(ctx context.Context, id uuid.UUID) error
	MarkNodeOnline(ctx context.Context, id uuid.UUID) error
}

const defaultFailureThreshold = 3

// HealthMonitor tracks consecutive node failures and marks nodes offline.
type HealthMonitor struct {
	mu        sync.Mutex
	failures  map[uuid.UUID]int
	threshold int
	queries   MetricQueries
	publisher *Publisher
	logger    *slog.Logger
}

// NewHealthMonitor creates a HealthMonitor with the default failure threshold.
func NewHealthMonitor(queries MetricQueries, publisher *Publisher, logger *slog.Logger) *HealthMonitor {
	return &HealthMonitor{
		failures:  make(map[uuid.UUID]int),
		threshold: defaultFailureThreshold,
		queries:   queries,
		publisher: publisher,
		logger:    logger,
	}
}

// RecordSuccess resets the failure counter for a node. If the node was previously
// at or above the threshold, it marks the node online again.
func (h *HealthMonitor) RecordSuccess(ctx context.Context, nodeID uuid.UUID) {
	h.mu.Lock()
	prev := h.failures[nodeID]
	delete(h.failures, nodeID)
	h.mu.Unlock()

	if prev >= h.threshold {
		if err := h.queries.MarkNodeOnline(ctx, nodeID); err != nil {
			h.logger.Error("failed to mark node online",
				"node_id", nodeID,
				"error", err,
			)
		}
	}
}

// RecordFailure increments the failure counter for a node. At the threshold,
// it marks the node offline and publishes an alert.
func (h *HealthMonitor) RecordFailure(ctx context.Context, clusterID, nodeID uuid.UUID, nodeName string) {
	h.mu.Lock()
	h.failures[nodeID]++
	count := h.failures[nodeID]
	h.mu.Unlock()

	if count == h.threshold {
		h.logger.Warn("node reached failure threshold, marking offline",
			"node_id", nodeID,
			"node_name", nodeName,
			"failures", count,
		)

		if err := h.queries.MarkNodeOffline(ctx, nodeID); err != nil {
			h.logger.Error("failed to mark node offline",
				"node_id", nodeID,
				"error", err,
			)
		}

		if h.publisher != nil {
			h.publisher.PublishNodeOffline(ctx, clusterID, nodeID, nodeName)
		}
	}
}
