package handlers

import (
	"net/url"
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
	if t.FinishedAt.Valid {
		s := t.FinishedAt.Time.Format(time.RFC3339Nano)
		resp.FinishedAt = &s
	}
	return resp
}

type taskListResponse struct {
	Items []taskResponse `json:"items"`
	Total int64          `json:"total"`
}

// validTaskStatuses bounds the ?status= filter to the known task_history states
// so a typo surfaces as a 400 rather than silently returning an empty page.
var validTaskStatuses = map[string]bool{
	"running": true, "completed": true, "failed": true, "stopped": true,
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

	total, err := h.queries.CountTaskHistoryFiltered(c.Context(), countP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count tasks")
	}

	tasks, err := h.queries.ListTaskHistoryFiltered(c.Context(), listP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list tasks")
	}

	resp := taskListResponse{
		Items: make([]taskResponse, 0, len(tasks)),
		Total: total,
	}
	for _, t := range tasks {
		// Per-row guard for the unfiltered case: a user without global view:task
		// only sees rows for clusters they can access.
		if !access.PermitsCluster(t.ClusterID) {
			continue
		}
		resp.Items = append(resp.Items, mapTaskHistory(t))
	}
	return c.JSON(resp)
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
