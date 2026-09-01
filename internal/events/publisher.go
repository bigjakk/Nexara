package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	proxsyslog "github.com/bigjakk/nexara/internal/syslog"

	"github.com/google/uuid"
)

// Event kinds published through the WebSocket pipeline.
const (
	KindTaskCreated       = "task_created"
	KindTaskUpdate        = "task_update"
	KindAuditEntry        = "audit_entry"
	KindVMStateChange     = "vm_state_change"
	KindInventoryChange   = "inventory_change"
	KindMigrationUpdate   = "migration_update"
	KindDRSAction         = "drs_action"
	KindPBSChange         = "pbs_change"
	KindCVEScan           = "cve_scan"
	KindAlertFired        = "alert_fired"
	KindAlertStateChange  = "alert_state_change"
	KindReportGenerated   = "report_generated"
	KindRollingUpdate     = "rolling_update"
	KindHAChange          = "ha_change"
	KindPoolChange        = "pool_change"
	KindReplicationChange = "replication_change"
	KindACMEChange        = "acme_change"
	KindAptRepoChange     = "apt_repo_change"
	KindVMImport          = "vm_import"
	KindSnapshotChange    = "snapshot_change"
	KindAccessChange      = "access_change"
	KindVeeamChange       = "veeam_change"
	KindVirtioWinChange   = "virtio_win_change"
	KindGuestToolsChange  = "guest_tools_change"
)

// Redis pub/sub channels for non-cluster events. Cluster events use
// "nexara:events:<cluster-uuid>".
//
// The identifier after "nexara:events:" must not contain a colon —
// ws.RedisChannelToClient splits on the first one.
const (
	SystemRedisChannel      = "nexara:events:system"
	SystemAuditRedisChannel = "nexara:events:system-audit"
)

// Event is a lightweight notification pushed through Redis pub/sub.
type Event struct {
	Kind         string `json:"kind"`
	ClusterID    string `json:"cluster_id,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	Action       string `json:"action,omitempty"`
	// Error carries a non-OK completion reason for actions that have a
	// lifecycle (e.g. a Proxmox task that exits with a non-OK exit status).
	// Empty / omitted = success. Clients that care about success-vs-failure
	// correlation (e.g. the mobile task tracker) check this field to
	// distinguish a fired-and-completed event from a fired-and-failed one
	// without having to poll the Proxmox tasks API themselves.
	Error     string `json:"error,omitempty"`
	Timestamp string `json:"timestamp"`
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
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	data, err := json.Marshal(event)
	if err != nil {
		p.logger.Warn("failed to marshal event", "error", err)
		return
	}

	channel := publishChannel(event)
	if err := p.client.Publish(ctx, channel, data).Err(); err != nil {
		p.logger.Warn("failed to publish event", "channel", channel, "error", err)
	}
}

// publishChannel decides which Redis channel an event fans out on, which in
// turn decides which permission a WS subscriber needs to receive it. Extracted
// from Publish so the routing — the entirety of the audit-stream gate — is
// table-testable without Redis. See TestPublishChannelRouting.
func publishChannel(event Event) string {
	// A nil-UUID cluster id is not a cluster — it comes from callers that pass
	// ClusterUUID(uuid.Nil) for a genuinely global resource. Treating it as one
	// would route the event to a cluster room that any holder of a GLOBAL
	// view:cluster grant can join (RBACEngine.HasPermission short-circuits on
	// global scope regardless of the requested id), which is not the audit gate.
	clusterID := event.ClusterID
	if clusterID == uuid.Nil.String() {
		clusterID = ""
	}

	switch {
	case clusterID != "" && event.Kind == KindAuditEntry:
		// Cluster audit entries name what was done to a cluster's resources.
		// The REST audit endpoints require view:audit for that cluster, so the
		// live stream rides its own room with the same gate rather than the
		// view:cluster-gated events room.
		return fmt.Sprintf("nexara:audit:%s", clusterID)
	case clusterID != "":
		return fmt.Sprintf("nexara:events:%s", clusterID)
	case event.Kind == KindAuditEntry:
		// Non-cluster audit entries name the subject of an administrative
		// action — the user whose password changed, the setting that was
		// written, the role that was granted. They ride a separate room the WS
		// layer gates on view:audit, rather than the general system room every
		// authenticated session may join.
		return SystemAuditRedisChannel
	default:
		return SystemRedisChannel
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

// maxEventErrorLength caps the size of the Error field on outbound
// events to keep pathological Proxmox / pgx error messages (which can
// contain multi-megabyte stack traces) from being fanned out verbatim
// to every WS subscriber. Per security review Z4. Callers in the
// codebase publish sanitized short codes today (see task_watcher.go),
// so this is also belt-and-suspenders against future call sites that
// might forget to sanitize first.
const maxEventErrorLength = 512

// ClusterEventWithError publishes a cluster-scoped event carrying a non-OK
// completion reason in the Error field. Use this from task watchers / other
// background completion observers when they detect a failure path that the
// mutation handler could not anticipate (e.g. Proxmox task exit status is
// not "OK"). Clients correlating tasks to their fire-and-forget dispatch
// use the presence of Error to flip their local task state from pending
// to failed instead of pending to success.
//
// `errMsg` should be a sanitized public code (see ErrTaskFailed etc. in
// `internal/api/handlers/task_watcher.go`). The maxEventErrorLength cap
// truncates anything longer to protect against accidentally publishing
// raw pgx / Proxmox error text — both as a defensive measure and so the
// Redis pub/sub fan-out doesn't OOM on a malformed input.
func (p *Publisher) ClusterEventWithError(ctx context.Context, clusterID, kind, resourceType, resourceID, action, errMsg string) {
	if p == nil {
		return
	}
	if len(errMsg) > maxEventErrorLength {
		errMsg = errMsg[:maxEventErrorLength] + "..."
	}
	p.Publish(ctx, Event{
		Kind:         kind,
		ClusterID:    clusterID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Error:        errMsg,
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
