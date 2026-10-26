package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// GuestHistoryHandler serves a guest's History tab: everything that happened
// to one guest, newest first. The route is declared in
// internal/api/registry_guest_history.go and the timeline is assembled in SQL
// (queries/guest_history.sql), which says what each entry is and where it
// comes from.
type GuestHistoryHandler struct {
	queries *db.Queries
}

// NewGuestHistoryHandler creates a new guest history handler.
func NewGuestHistoryHandler(queries *db.Queries) *GuestHistoryHandler {
	return &GuestHistoryHandler{queries: queries}
}

// The outcomes an entry can show. Empty is "not a task", or a task whose end
// was never recorded — a Nexara task pruned from task_history before
// migrations/000115 kept them.
const (
	guestOutcomeRunning = "running"
	guestOutcomeOK      = "ok"
	guestOutcomeFailed  = "failed"
)

type guestHistoryEntry struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurred_at"`
	Category   string `json:"category"`
	Source     string `json:"source"`
	Action     string `json:"action"`
	// ResourceType is the audit row's, for an audit entry; "task" or "status"
	// otherwise.
	ResourceType string `json:"resource_type"`
	// VMID is the guest the row itself names. It differs from the path's only
	// on a clone INTO this guest, which names its source.
	VMID            int32    `json:"vmid"`
	UserID          string   `json:"user_id"`
	UserEmail       string   `json:"user_email"`
	UserDisplayName string   `json:"user_display_name"`
	Details         string   `json:"details"`
	UPID            string   `json:"upid"`
	Node            string   `json:"node"`
	Description     string   `json:"description"`
	TaskType        string   `json:"task_type"`
	Outcome         string   `json:"outcome"`
	ExitStatus      string   `json:"exit_status"`
	Progress        *float64 `json:"progress,omitempty"`
	FinishedAt      *string  `json:"finished_at"`
	FromStatus      string   `json:"from_status,omitempty"`
	ToStatus        string   `json:"to_status,omitempty"`
	// LastSeenAt is, for a status change, when the guest was last seen in
	// FromStatus: the change happened after it and by OccurredAt.
	LastSeenAt *string `json:"last_seen_at,omitempty"`
}

// guestTaskOutcome is how a task ended, from the task row when there is one
// and otherwise from the status the collector recorded when it ingested the
// task. That one is Proxmox's own status field: "RUNNING" while the task ran,
// then its exit status — so "RUNNING" or nothing means the end was never seen.
//
// proxmox.TaskSucceeded is the single success rule ("OK", "OK (with
// warnings)" and "WARNINGS: N" succeed); task_history's own completed/failed
// were derived from it already.
func guestTaskOutcome(taskStatus, taskExit, recorded string) string {
	switch taskStatus {
	case "running":
		return guestOutcomeRunning
	case "completed":
		return guestOutcomeOK
	case "failed":
		return guestOutcomeFailed
	case "stopped":
		// Some writers store Proxmox's raw state rather than its verdict.
		if proxmox.TaskSucceeded(taskExit) {
			return guestOutcomeOK
		}
		return guestOutcomeFailed
	case "":
		r := strings.TrimSpace(recorded)
		if r == "" || strings.EqualFold(r, "running") {
			return ""
		}
		if proxmox.TaskSucceeded(r) {
			return guestOutcomeOK
		}
		return guestOutcomeFailed
	}
	return ""
}

// guestHistoryCutoff picks where a guest's timeline starts, from its lifecycle
// events newest first (ListGuestLifecycleEvents). The newest birth starts it,
// inclusive, so the create is the first entry. A newer destroy starts it after
// the destroy, exclusive: Proxmox reuses the lowest free VMID, so everything
// before it belongs to the previous guest that held the number.
//
// A destroy that failed, or is still running, ended nothing and is passed
// over. One whose end was never recorded counts: it is far likelier to be an
// old destroy whose task row was pruned than one that failed.
//
// No event at all means no cutoff — the guest's whole recorded history.
func guestHistoryCutoff(events []db.ListGuestLifecycleEventsRow) (after pgtype.Timestamptz, inclusive bool) {
	for _, e := range events {
		at := pgtype.Timestamptz{Time: e.CreatedAt, Valid: true}
		if !e.IsDestroy {
			return at, true
		}
		switch guestTaskOutcome(e.TaskStatus, e.TaskExitStatus, e.RecordedStatus) {
		case guestOutcomeFailed, guestOutcomeRunning:
			continue
		}
		return at, false
	}
	return pgtype.Timestamptz{}, false
}

// guestHistoryQueryTimeout bounds one request's two queries. Every view:audit
// holder can ask for any guest, a busy guest's history is the whole of its
// audit trail, and Fiber's request context is never cancelled by a client that
// went away.
const guestHistoryQueryTimeout = 15 * time.Second

// guestHistoryScope decides which of the timeline's parts a caller sees
// beyond the audit rows the route's view:audit gate already admits them to.
// Two parts belong to other permissions, and are FILTERED on them rather than
// gated, so a caller is shown what they may see instead of being refused the
// rest:
//
//   - a task with no audit row comes from task_history alone, which is
//     view:task's;
//   - a status change is the inventory's run state, which is view:vm's for a
//     VM (qemu) and view:container's for a container (lxc).
//
// can is hasClusterPerm on the path's cluster. Any lookup that fails fails
// the request, rather than serving a page that silently left rows out.
func guestHistoryScope(can func(action, resource string) (bool, error)) (includeTasks bool, statusTypes []string, err error) {
	if includeTasks, err = can("view", "task"); err != nil {
		return false, nil, err
	}
	statusTypes = []string{}
	for _, kind := range []struct{ resource, guestType string }{{"vm", "qemu"}, {"container", "lxc"}} {
		allowed, permErr := can("view", kind.resource)
		if permErr != nil {
			return false, nil, permErr
		}
		if allowed {
			statusTypes = append(statusTypes, kind.guestType)
		}
	}
	return includeTasks, statusTypes, nil
}

// List returns one page of a guest's history. The route's gate is view:audit,
// because the audit log is what the timeline is made of; guestHistoryScope
// says what else the caller sees. A task's verdict is served at view:audit, as
// the activity feed serves it (ListRecentAuditLogEnriched); the exit text from
// its task record is Proxmox's own words, task data, and only reaches a
// view:task holder. (An ingested task's audit details carry the status the
// collector saw, at view:audit, exactly as the audit page shows them.)
func (h *GuestHistoryHandler) List(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vmid := safeconv.Int32(int(p.Int("vmid")))

	includeTasks, statusTypes, err := guestHistoryScope(func(action, resource string) (bool, error) {
		return hasClusterPerm(c, action, resource, clusterID)
	})
	if err != nil {
		return err
	}
	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	params := db.ListGuestHistoryParams{
		ClusterID:        clusterID,
		Vmid:             vmid,
		IncludeTasks:     includeTasks,
		StatusGuestTypes: statusTypes,
		RowLimit:         safeconv.Int32(int(p.Int("limit"))),
		RowOffset:        safeconv.Int32(int(p.Int("offset"))),
	}
	// An empty filter is no filter, as on the audit and task listings.
	if since := p.String("start_time"); since != "" {
		t, parseErr := time.Parse(time.RFC3339, since)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid start_time (use RFC3339)")
		}
		params.Since = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if category := p.String("category"); category != "" {
		params.Category = pgtype.Text{String: category, Valid: true}
	}
	if source := p.String("source"); source != "" {
		params.Source = pgtype.Text{String: source, Valid: true}
	}

	ctx, cancel := context.WithTimeout(c.Context(), guestHistoryQueryTimeout)
	defer cancel()
	events, err := h.queries.ListGuestLifecycleEvents(ctx, db.ListGuestLifecycleEventsParams{
		ClusterID: ClusterUUID(clusterID),
		Vmid:      vmid,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the guest's history")
	}
	params.After, params.AfterInclusive = guestHistoryCutoff(events)

	rows, err := h.queries.ListGuestHistory(ctx, params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the guest's history")
	}

	// Every row carries the same total; a page past the end carries none, and
	// reports 0 — the client snaps back to a page that exists.
	var total int64
	items := make([]guestHistoryEntry, 0, len(rows))
	for _, r := range rows {
		total = r.Total
		items = append(items, toGuestHistoryEntry(r, visible, includeTasks))
	}
	return RespondList(c, items, total)
}

// toGuestHistoryEntry maps one row. showExitText is view:task — see List.
func toGuestHistoryEntry(r db.ListGuestHistoryRow, visible map[string]bool, showExitText bool) guestHistoryEntry {
	e := guestHistoryEntry{
		ID:              r.EntryID,
		Kind:            r.Kind,
		OccurredAt:      r.OccurredAt.Format(time.RFC3339Nano),
		Category:        r.Category,
		Source:          r.Source,
		Action:          r.Action,
		ResourceType:    r.ResourceType,
		VMID:            r.Vmid,
		UserEmail:       r.UserEmail,
		UserDisplayName: r.UserDisplayName,
		// The same redaction every audit read applies. No guest row names a
		// reserved setting today; the rule belongs to the read, not to what
		// the rows happen to hold.
		Details:     auditDetailsFor(r.ResourceType, r.ResourceID, string(r.Details), visible),
		UPID:        r.Upid,
		Node:        r.Node,
		Description: r.TaskDescription,
		TaskType:    r.TaskType,
		FromStatus:  r.FromStatus,
		ToStatus:    r.ToStatus,
	}
	if r.UserID.Valid {
		e.UserID = uuid.UUID(r.UserID.Bytes).String()
	}
	if e.Node == "" {
		e.Node = r.TaskNode
	}
	if e.TaskType == "" {
		e.TaskType = taskTypeFromUPID(r.Upid)
	}
	if r.Upid != "" {
		e.Outcome = guestTaskOutcome(r.TaskStatus, r.TaskExitStatus, r.RecordedStatus)
		e.ExitStatus = r.TaskExitStatus
		if r.TaskStatus == "" && e.Outcome != "" {
			e.ExitStatus = r.RecordedStatus
		}
	}
	if r.TaskProgress.Valid {
		v := r.TaskProgress.Float64
		e.Progress = &v
	}
	if r.TaskFinishedAt.Valid {
		s := r.TaskFinishedAt.Time.Format(time.RFC3339Nano)
		e.FinishedAt = &s
	}
	if r.LastSeenAt.Valid {
		s := r.LastSeenAt.Time.Format(time.RFC3339Nano)
		e.LastSeenAt = &s
	}
	if !showExitText {
		e.ExitStatus = ""
	}
	// upid, node and the ingested status are lifted out of details in SQL, so
	// a redacted row's would pass the redaction by — the reason
	// auditGuestIdentity exists for the audit page's derived fields.
	if auditDetailsRedacted(r.ResourceType, r.ResourceID, visible) {
		e.UPID, e.Node, e.TaskType, e.Description = "", "", "", ""
		e.Outcome, e.ExitStatus, e.Progress, e.FinishedAt = "", "", nil, nil
	}
	return e
}
