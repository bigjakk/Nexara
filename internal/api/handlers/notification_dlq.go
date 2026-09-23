package handlers

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// All five routes are declared in internal/api/registry_notification_dlq.go,
// which states their GLOBAL permission (view:notification_dlq for the two
// reads, manage:notification_dlq for the three writes) and their parameters.
//
// What stays here is what the declaration cannot express: the SECOND global
// permission a replay needs (manage:notification_channel — Permissions has no
// "all of" shape), and the per-row cluster check on an entry that names one,
// which is only knowable after the row is read.

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

// validDLQStates is the accepted ?state= vocabulary. It is the same set the
// declaration's Enum carries in internal/api/registry_notification_dlq.go,
// less the "" no-filter sentinel the Enum adds — TestNotificationDLQStateVocabulary
// pins the two against each other, because they are two copies of one list and
// a copy nothing compares is a copy that rots.
var validDLQStates = map[string]bool{
	"pending":      true,
	"rate_limited": true,
	"retrying":     true,
	"resolved":     true,
	"dismissed":    true,
}

// DLQStateKeys returns the accepted ?state= values, sorted. Exported for the
// guard in package api that compares them against the declared Enum; package
// handlers cannot import package api, so the comparison reads this from the
// other side.
func DLQStateKeys() []string { return slices.Sorted(maps.Keys(validDLQStates)) }

// List returns DLQ entries optionally filtered by state and channel_id.
//
// This endpoint is global-only, and that is stated by its DECLARATION rather
// than by anything in this body: internal/api/registry_notification_dlq.go
// declares globalCheck("view", "notification_dlq"), which mounts
// handlers.RequirePermission as middleware. A cluster-scoped grant satisfies no
// global check (internal/auth/rbac.go), so a Viewer of one cluster is refused
// outright rather than served a filtered listing.
//
// Which makes the per-row cluster guard further down unreachable today — every
// caller the declared gate lets through holds a global grant, so
// accessibleClusters returns HasGlobal and the guard keeps every row. It is kept
// because rows do carry a denormalised cluster_id (set at write time from the
// rule's cluster), so the row-level rule is worth stating and worth having
// already correct.
//
// It is NOT, however, a licence to widen the gate on its own. Opening this to
// cluster-scoped callers means scoping ListNotificationDLQ in SQL as well:
// LIMIT/OFFSET are applied across every cluster's rows and the guard trims
// afterwards, so a scoped caller would page through the global rowset and get
// short pages with holes. queries/audit_log.sql shows the shape that fixes it.
func (h *NotificationDLQHandler) List(c fiber.Ctx, p *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "notification_dlq")
	if err != nil {
		return err
	}

	// An empty channel_id is the "every channel" it has always been, not a
	// uuid to parse; the same goes for an empty state, which
	// ListNotificationDLQ reads as no filter.
	var channelIDPg pgtype.UUID
	if cidStr := p.String("channel_id"); cidStr != "" {
		cid, perr := parseParamUUID(cidStr)
		if perr != nil {
			return perr
		}
		channelIDPg = pgtype.UUID{Bytes: cid, Valid: true}
	}

	rows, err := h.queries.ListNotificationDLQ(c.Context(), db.ListNotificationDLQParams{
		State:     p.String("state"),
		ChannelID: channelIDPg,
		LimitVal:  safeconv.Int32(int(p.Int("limit"))),
		OffsetVal: safeconv.Int32(int(p.Int("offset"))),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list DLQ entries")
	}

	out := make([]notificationDLQResponse, 0, len(rows))
	for _, r := range rows {
		// Cross-cluster view guard, unreachable while the DECLARED gate is
		// global-only (see the doc comment): cluster-scoped rows require
		// global or per-cluster access, and rows with no cluster (global
		// rules / test dispatches) require global access, which every caller
		// here has. Retained so the rule is already right if that changes.
		if r.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(r.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		out = append(out, toNotificationDLQResponse(r))
	}
	return RespondItems(c, out)
}

// Summary returns counts grouped by state for the DLQ widget.
func (h *NotificationDLQHandler) Summary(c fiber.Ctx, _ *apischema.Params) error {
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
func (h *NotificationDLQHandler) Retry(c fiber.Ctx, p *apischema.Params) error {
	// The declared Check has already required manage:notification_dlq. This is
	// the SECOND global permission the replay needs, which Permissions has no
	// "all of" shape for — see registerNotificationDLQEndpoints.
	if err := requirePerm(c, "manage", "notification_channel"); err != nil {
		return err
	}

	if h.alertEngine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Alert engine not available")
	}

	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
func (h *NotificationDLQHandler) Dismiss(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
func (h *NotificationDLQHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
