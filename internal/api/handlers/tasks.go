package handlers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// TaskHandler handles task history CRUD operations.
type TaskHandler struct {
	queries   *db.Queries
	eventPub  *events.Publisher
	retention time.Duration
}

// NewTaskHandler creates a new TaskHandler. retention is the task-history
// window (TASK_HISTORY_RETENTION) honored by ClearCompleted, matching the
// automatic scheduler sweep.
func NewTaskHandler(queries *db.Queries, eventPub *events.Publisher, retention time.Duration) *TaskHandler {
	return &TaskHandler{queries: queries, eventPub: eventPub, retention: retention}
}

type createTaskRequest struct {
	ClusterID   string `json:"cluster_id"`
	UPID        string `json:"upid"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Node        string `json:"node"`
	TaskType    string `json:"task_type"`
}

type updateTaskRequest struct {
	Status     string   `json:"status"`
	ExitStatus string   `json:"exit_status"`
	Progress   *float64 `json:"progress"`
	FinishedAt *string  `json:"finished_at"`
}

type taskResponse struct {
	ID          string   `json:"id"`
	ClusterID   string   `json:"cluster_id"`
	UPID        string   `json:"upid"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	ExitStatus  string   `json:"exit_status"`
	Node        string   `json:"node"`
	TaskType    string   `json:"task_type"`
	Source      string   `json:"source"`
	Progress    *float64 `json:"progress"`
	Vmid        *int32   `json:"vmid,omitempty"`
	StartedAt   string   `json:"started_at"`
	FinishedAt  *string  `json:"finished_at,omitempty"`
}

func mapTaskHistory(t db.TaskHistory) taskResponse {
	resp := taskResponse{
		ID:          t.ID.String(),
		ClusterID:   t.ClusterID.String(),
		UPID:        t.Upid,
		Description: t.Description,
		Status:      t.Status,
		ExitStatus:  t.ExitStatus,
		Node:        t.Node,
		TaskType:    t.TaskType,
		Source:      t.Source,
		StartedAt:   t.StartedAt.Format(time.RFC3339Nano),
	}
	if t.Progress.Valid {
		resp.Progress = &t.Progress.Float64
	}
	if t.Vmid.Valid {
		v := t.Vmid.Int32
		resp.Vmid = &v
	}
	if t.FinishedAt.Valid {
		s := t.FinishedAt.Time.Format(time.RFC3339Nano)
		resp.FinishedAt = &s
	}
	return resp
}

// validTaskStatuses bounds the ?status= filter to the known task_history states
// so a typo surfaces as a 400 rather than silently returning an empty page.
var validTaskStatuses = map[string]bool{
	"running": true, "completed": true, "failed": true, "stopped": true,
}

// maxVmidsFilter bounds the ?vmids= list so a hostile query string can't grow
// the SQL ANY() array without limit. Proxmox VMIDs are 100..999999999.
const maxVmidsFilter = 500

// parseVmidsParam parses a comma-separated VMID list ("100,101,205").
func parseVmidsParam(raw string) ([]int32, error) {
	parts := strings.Split(raw, ",")
	if len(parts) > maxVmidsFilter {
		return nil, fmt.Errorf("too many vmids: %d > %d", len(parts), maxVmidsFilter)
	}
	vmids := make([]int32, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 32)
		if err != nil || v < 0 {
			return nil, fmt.Errorf("invalid vmid %q", p)
		}
		vmids = append(vmids, int32(v))
	}
	return vmids, nil
}

// applyTaskListScope stamps the caller's view:task cluster scope onto BOTH the
// list and count query params — the same value on both, so Items and the
// pagination Total can never disagree. Counting without this scope leaked how
// many tasks other clusters have and broke pagination for scoped users. The
// SQL filter is the access control: nil = global = no restriction; a non-nil
// set restricts, and '{}' matches nothing.
//
// The scope is stamped even for a caller holding no grant at all, so ignoring
// the return value stays safe — '{}' is exactly what that caller should match.
// Returning early instead, as this did, left both structs nil, which reaches
// SQL as NULL and means every cluster; it was safe only for as long as the one
// caller kept honouring the bool. The false return is now advice, not a guard:
// permission to skip round-trips that could not come back with a row.
func applyTaskListScope(access clusterAccess, listP *db.ListTaskHistoryFilteredParams, countP *db.CountTaskHistoryFilteredParams) bool {
	scopeIDs, query := clusterScopeFilter(access)
	listP.AccessibleClusterIds = scopeIDs
	countP.AccessibleClusterIds = scopeIDs
	return query
}

// List returns task history with optional cluster_id + status filters and offset
// pagination (mirrors AuditHandler.List). Includes DRS/system tasks. Status is
// served from the reconciled task_history row, so the client need not poll
// Proxmox per entry.
func (h *TaskHandler) List(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "task")
	if err != nil {
		return err
	}

	limit := fiber.Query[int](c, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	offset := fiber.Query[int](c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	listP := db.ListTaskHistoryFilteredParams{
		Limit:  safeconv.Int32(limit),
		Offset: safeconv.Int32(offset),
	}
	var countP db.CountTaskHistoryFilteredParams

	// Optional cluster filter — the caller must have view:task on it.
	if cid := c.Query("cluster_id"); cid != "" {
		clusterID, err := uuid.Parse(cid)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id filter")
		}
		if !access.PermitsCluster(clusterID) {
			return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
		}
		v := pgtype.UUID{Bytes: clusterID, Valid: true}
		listP.ClusterID = v
		countP.ClusterID = v
	}

	if status := c.Query("status"); status != "" {
		if !validTaskStatuses[status] {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid status filter")
		}
		v := pgtype.Text{String: status, Valid: true}
		listP.Status = v
		countP.Status = v
	}

	// Optional guest filter: comma-separated Proxmox VMIDs, matched against
	// task_history.vmid (parsed from the UPID at insert). Used by the folder
	// detail view to scope tasks to a folder's VMs.
	if raw := c.Query("vmids"); raw != "" {
		vmids, err := parseVmidsParam(raw)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid vmids filter")
		}
		listP.Vmids = vmids
		countP.Vmids = vmids
	}

	if !applyTaskListScope(access, &listP, &countP) {
		return RespondList(c, []taskResponse{}, 0)
	}

	total, err := h.queries.CountTaskHistoryFiltered(c.Context(), countP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count tasks")
	}

	tasks, err := h.queries.ListTaskHistoryFiltered(c.Context(), listP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list tasks")
	}

	resp := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		// Defense-in-depth: SQL already restricts rows via
		// accessible_cluster_ids; the per-row guard keeps a future query edit
		// from silently reopening the cross-cluster leak.
		if !access.PermitsCluster(t.ClusterID) {
			continue
		}
		resp = append(resp, mapTaskHistory(t))
	}
	return RespondList(c, resp, total)
}

// Create creates a new task history record.
func (h *TaskHandler) Create(c fiber.Ctx) error {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid user")
	}

	var req createTaskRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
	}

	// Per-cluster gate so a user with manage:task scoped to cluster X
	// cannot insert task records claiming cluster Y.
	if err := requireClusterPerm(c, "manage", "task", clusterID); err != nil {
		return err
	}

	if req.UPID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "upid is required")
	}

	status := req.Status
	if status == "" {
		status = "running"
	}

	task, err := h.queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID:   clusterID,
		UserID:      uid,
		Upid:        req.UPID,
		Description: req.Description,
		Status:      status,
		Node:        req.Node,
		TaskType:    req.TaskType,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create task record")
	}

	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindTaskCreated, "task", task.ID.String(), "create")

	return c.Status(fiber.StatusCreated).JSON(mapTaskHistory(task))
}

// Update updates a task history record by UPID.
func (h *TaskHandler) Update(c fiber.Ctx) error {
	rawUPID := c.Params("upid")
	if rawUPID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "upid is required")
	}
	// Fiber (fasthttp) doesn't auto-decode route params; the frontend
	// URL-encodes the UPID so colons arrive as %3A, etc.
	upid, err := url.PathUnescape(rawUPID)
	if err != nil {
		upid = rawUPID
	}

	// Look up the task to find its cluster, then gate on per-cluster perm.
	task, err := h.queries.GetTaskByUpid(c.Context(), upid)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Task not found")
	}
	if err := requireClusterPerm(c, "manage", "task", task.ClusterID); err != nil {
		return err
	}

	var req updateTaskRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	params := db.UpdateTaskHistoryParams{
		Upid:       upid,
		Status:     req.Status,
		ExitStatus: req.ExitStatus,
	}

	if req.Progress != nil {
		params.Progress = pgtype.Float8{Float64: *req.Progress, Valid: true}
	}

	if req.FinishedAt != nil {
		t, err := time.Parse(time.RFC3339, *req.FinishedAt)
		if err == nil {
			params.FinishedAt = pgtype.Timestamptz{Time: t, Valid: true}
		}
	} else if req.Status == "stopped" {
		params.FinishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}

	if err := h.queries.UpdateTaskHistory(c.Context(), params); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update task")
	}
	h.eventPub.SystemEvent(c.Context(), events.KindTaskUpdate, req.Status)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ClearCompleted deletes all completed/failed tasks across every cluster.
//
// Because the underlying delete is unscoped (and adding a per-user cluster
// filter would require an array-of-uuid SQL parameter), this stays gated on
// global manage:task — i.e. effectively admin-only. A user with manage:task
// scoped only to cluster X cannot wipe history that includes cluster Y.
func (h *TaskHandler) ClearCompleted(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "task"); err != nil {
		return err
	}

	if err := h.queries.DeleteCompletedTasks(c.Context(), time.Now().Add(-h.retention)); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to clear tasks")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "task", "all", "clear_completed", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindTaskUpdate, "clear_completed")

	return c.JSON(fiber.Map{"status": "ok"})
}
