package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// Event kinds published through the WebSocket pipeline.
const (
	KindTaskCreated      = "task_created"
	KindTaskUpdate       = "task_update"
	KindAuditEntry       = "audit_entry"
	KindVMStateChange    = "vm_state_change"
	KindInventoryChange  = "inventory_change"
	KindMigrationUpdate  = "migration_update"
	KindDRSAction        = "drs_action"
	KindPBSChange        = "pbs_change"
	KindCVEScan          = "cve_scan"
	KindAlertFired       = "alert_fired"
	KindAlertStateChange = "alert_state_change"
	KindReportGenerated  = "report_generated"
	KindRollingUpdate    = "rolling_update"
	KindHAChange         = "ha_change"
	KindPoolChange       = "pool_change"
	KindReplicationChange = "replication_change"
	KindACMEChange       = "acme_change"
	KindAptRepoChange    = "apt_repo_change"
)

// Event is a lightweight notification pushed through Redis pub/sub.
type Event struct {
	Kind         string `json:"kind"`
	ClusterID    string `json:"cluster_id,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	Action       string `json:"action,omitempty"`
	Timestamp    string `json:"timestamp"`
}

// Publisher publishes events to Redis for fan-out via the WS pipeline.
type Publisher struct {
	client    *redis.Client
	logger    *slog.Logger
	syslogFwd *proxsyslog.Forwarder
}

// NewPublisher creates a new event publisher.
func NewPublisher(client *redis.Client, logger *slog.Logger) *Publisher {
	return &Publisher{
		client: client,
		logger: logger,
	}
}

// SetSyslogForwarder attaches a syslog forwarder to the publisher.
// When set, audit events will also be forwarded to the configured syslog server.
func (p *Publisher) SetSyslogForwarder(fwd *proxsyslog.Forwarder) {
	if p == nil {
		return
	}
	p.syslogFwd = fwd
}

// SyslogForwarder returns the attached syslog forwarder (may be nil).
func (p *Publisher) SyslogForwarder() *proxsyslog.Forwarder {
	if p == nil {
		return nil
	}
	return p.syslogFwd
}

// Publish sends an event to Redis. It is nil-safe and fire-and-forget.
func (p *Publisher) Publish(ctx context.Context, event Event) {
	if p == nil || p.client == nil {
		return
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(event)
	if err != nil {
		p.logger.Warn("failed to marshal event", "error", err)
		return
	}

	var channel string
	if event.ClusterID != "" {
		channel = fmt.Sprintf("nexara:events:%s", event.ClusterID)
	} else {
		channel = "nexara:events:system"
	}

	if err := p.client.Publish(ctx, channel, data).Err(); err != nil {
		p.logger.Warn("failed to publish event", "channel", channel, "error", err)
	}
}

// ClusterEvent publishes a cluster-scoped event.
func (p *Publisher) ClusterEvent(ctx context.Context, clusterID, kind, resourceType, resourceID, action string) {
	if p == nil {
		return
	}
	p.Publish(ctx, Event{
		Kind:         kind,
		ClusterID:    clusterID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
	})
}

// SystemEvent publishes a non-cluster event (e.g. task_created).
func (p *Publisher) SystemEvent(ctx context.Context, kind, action string) {
	if p == nil {
		return
	}
	p.Publish(ctx, Event{
		Kind:   kind,
		Action: action,
	})
}
