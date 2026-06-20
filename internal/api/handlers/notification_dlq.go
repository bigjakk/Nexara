package handlers

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// NotificationDLQHandler exposes the dead-letter queue produced by the alert
// engine when a dispatcher exhausts its retries or when a channel is
// rate-limited. The replay path delegates back to *notifications.Engine so
// the same retry schedule is honoured.
type NotificationDLQHandler struct {
	queries     *db.Queries
	alertEngine *notifications.Engine
	eventPub    *events.Publisher
}

// NewNotificationDLQHandler constructs a DLQ handler. alertEngine is required
// for replay; nil is tolerated for the read paths so test setups without a
// running engine still work.
func NewNotificationDLQHandler(queries *db.Queries, alertEngine *notifications.Engine, eventPub *events.Publisher) *NotificationDLQHandler {
	return &NotificationDLQHandler{
		queries:     queries,
		alertEngine: alertEngine,
		eventPub:    eventPub,
	}
}

type notificationDLQResponse struct {
	ID           uuid.UUID       `json:"id"`
	ChannelID    string          `json:"channel_id,omitempty"`
	ChannelType  string          `json:"channel_type"`
	ChannelName  string          `json:"channel_name"`
	AlertID      string          `json:"alert_id,omitempty"`
	RuleID       string          `json:"rule_id,omitempty"`
	ClusterID    string          `json:"cluster_id,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	LastError    string          `json:"last_error"`
	AttemptCount int32           `json:"attempt_count"`
	State        string          `json:"state"`
	FailureKind  string          `json:"failure_kind"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

func toNotificationDLQResponse(row db.NotificationDlq) notificationDLQResponse {
	resp := notificationDLQResponse{
		ID:           row.ID,
		ChannelType:  row.ChannelType,
		ChannelName:  row.ChannelName,
		Payload:      row.Payload,
		LastError:    row.LastError,
		AttemptCount: row.AttemptCount,
		State:        row.State,
		FailureKind:  row.FailureKind,
		CreatedAt:    row.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:    row.UpdatedAt.Format(time.RFC3339Nano),
	}
	if row.ChannelID.Valid {
		id, _ := uuid.FromBytes(row.ChannelID.Bytes[:])
		resp.ChannelID = id.String()
	}
	if row.AlertID.Valid {
		id, _ := uuid.FromBytes(row.AlertID.Bytes[:])
		resp.AlertID = id.String()
	}
	if row.RuleID.Valid {
		id, _ := uuid.FromBytes(row.RuleID.Bytes[:])
		resp.RuleID = id.String()
	}
	if row.ClusterID.Valid {
		id, _ := uuid.FromBytes(row.ClusterID.Bytes[:])
		resp.ClusterID = id.String()
	}
	return resp
}

var validDLQStates = map[string]bool{
	"pending":      true,
	"rate_limited": true,
	"retrying":     true,
	"resolved":     true,
	"dismissed":    true,
}

// List returns DLQ entries optionally filtered by state and channel_id.
//
// Although `view:notification_dlq` is a global RBAC permission, individual
// rows carry a denormalised cluster_id (set at write time from the rule's
// cluster). Rows are filtered down to the caller's accessible clusters
// (via the same `accessibleClusters` helper that gates alert history) so
// a Viewer of Cluster A cannot see DLQ entries belonging to Cluster B.
// Rows with a NULL cluster_id (global rules + rule-less test dispatches)
// are visible to anyone with the global permission, since there is no
// cluster boundary to honour.
func (h *NotificationDLQHandler) List(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "notification_dlq"); err != nil {
		return err
	}

	access, err := accessibleClusters(c, "view", "notification_dlq")
	if err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	state := c.Query("state")
	if state != "" && !validDLQStates[state] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid state filter")
	}

	var channelIDPg pgtype.UUID
	if cidStr := c.Query("channel_id"); cidStr != "" {
		cid, perr := uuid.Parse(cidStr)
		if perr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid channel_id")
		}
		channelIDPg = pgtype.UUID{Bytes: cid, Valid: true}
	}

	rows, err := h.queries.ListNotificationDLQ(c.Context(), db.ListNotificationDLQParams{
		State:     state,
		ChannelID: channelIDPg,
		LimitVal:  safeconv.Int32(limit),
		OffsetVal: safeconv.Int32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list DLQ entries")
	}

	out := make([]notificationDLQResponse, 0, len(rows))
	for _, r := range rows {
		// Cross-cluster view guard. Cluster-scoped rows (cluster_id set)
		// require either global access or per-cluster access; rows with
		// no cluster (global rules / test dispatches) require global
		// access only — they have no cluster boundary to honour.
		if r.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(r.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		out = append(out, toNotificationDLQResponse(r))
	}
	return c.JSON(out)
}

// Summary returns counts grouped by state for the DLQ widget.
func (h *NotificationDLQHandler) Summary(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "notification_dlq"); err != nil {
		return err
	}

	row, err := h.queries.CountNotificationDLQByState(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count DLQ entries")
	}
	return c.JSON(fiber.Map{
		"pending":      row.PendingCount,
		"rate_limited": row.RateLimitedCount,
		"retrying":     row.RetryingCount,
		"resolved":     row.ResolvedCount,
		"dismissed":    row.DismissedCount,
	})
}

// Retry re-attempts a failed dispatch. Delegates to the alert engine so the
// same retry schedule is honoured. Requires BOTH `manage:notification_dlq`
// AND `manage:notification_channel` — a replay sends a real notification on
// behalf of a channel the operator may not otherwise be allowed to test or
// modify; co-locating the two checks closes that escalation path.
//
// For cluster-scoped DLQ rows the cluster permission is also checked so an
// operator cross-cluster can't replay another tenant's traffic.
func (h *NotificationDLQHandler) Retry(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_dlq"); err != nil {
		return err
	}
	if err := requirePerm(c, "manage", "notification_channel"); err != nil {
		return err
	}

	if h.alertEngine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Alert engine not available")
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid DLQ ID")
	}

	row, err := h.queries.GetNotificationDLQ(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "DLQ entry not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load DLQ entry")
	}
	if row.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "notification_dlq", uuid.UUID(row.ClusterID.Bytes)); err != nil {
			return err
		}
	}

	if err := h.alertEngine.ReplayDLQ(c.Context(), id); err != nil {
		if h.eventPub != nil {
			h.eventPub.ClusterEvent(c.Context(), "", events.KindAlertFired,
				"notification_dlq", id.String(), "replay_failed")
		}
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"success": false,
			"message": err.Error(),
		})
	}

	auditCtx := row.ClusterID
	AuditLog(c, h.queries, h.eventPub, auditCtx, "notification_dlq", id.String(), "dlq_replayed", nil)
	return c.JSON(fiber.Map{"success": true, "message": "DLQ entry replayed"})
}

// Dismiss marks a DLQ entry as dismissed without retrying. Used when an
// operator decides the failure is no longer actionable (rule deleted,
// channel rotated, etc).
func (h *NotificationDLQHandler) Dismiss(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_dlq"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid DLQ ID")
	}

	row, err := h.queries.GetNotificationDLQ(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "DLQ entry not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load DLQ entry")
	}
	if row.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "notification_dlq", uuid.UUID(row.ClusterID.Bytes)); err != nil {
			return err
		}
	}

	if err := h.queries.DismissNotificationDLQ(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to dismiss DLQ entry")
	}

	AuditLog(c, h.queries, h.eventPub, row.ClusterID, "notification_dlq", id.String(), "dlq_dismissed", nil)
	return c.JSON(fiber.Map{"success": true})
}

// Delete permanently removes a DLQ entry. Provided alongside Dismiss for
// operators who want to keep the table compact.
func (h *NotificationDLQHandler) Delete(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_dlq"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid DLQ ID")
	}

	row, err := h.queries.GetNotificationDLQ(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "DLQ entry not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load DLQ entry")
	}
	if row.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "notification_dlq", uuid.UUID(row.ClusterID.Bytes)); err != nil {
			return err
		}
	}

	if err := h.queries.DeleteNotificationDLQ(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete DLQ entry")
	}

	AuditLog(c, h.queries, h.eventPub, row.ClusterID, "notification_dlq", id.String(), "dlq_deleted", nil)
	return c.SendStatus(fiber.StatusNoContent)
}
