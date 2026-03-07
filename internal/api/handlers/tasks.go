package handlers

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
)

// auditLog writes an audit log entry for task operations.
func (h *TaskHandler) auditLog(c *fiber.Ctx, clusterID pgtype.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    clusterID,
		UserID:       uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})
}

// TaskHandler handles task history CRUD operations.
type TaskHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
}

// NewTaskHandler creates a new TaskHandler.
func NewTaskHandler(queries *db.Queries, eventPub *events.Publisher) *TaskHandler {
	return &TaskHandler{queries: queries, eventPub: eventPub}
}

type createTaskRequest struct {
	ClusterID   string  `json:"cluster_id"`
	UPID        string  `json:"upid"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Node        string  `json:"node"`
	TaskType    string  `json:"task_type"`
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
		StartedAt:   t.StartedAt.Format(time.RFC3339),
	}
	if t.Progress.Valid {
		resp.Progress = &t.Progress.Float64
	}
	if t.FinishedAt.Valid {
		s := t.FinishedAt.Time.Format(time.RFC3339)
		resp.FinishedAt = &s
	}
	return resp
}

// List returns task history for all users (includes DRS/system tasks).
func (h *TaskHandler) List(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	tasks, err := h.queries.ListAllTaskHistory(c.Context(), 100)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list tasks")
	}

	result := make([]taskResponse, len(tasks))
	for i, t := range tasks {
		result[i] = mapTaskHistory(t)
	}
	return c.JSON(result)
}

// Create creates a new task history record.
func (h *TaskHandler) Create(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid user")
	}

	var req createTaskRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
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
func (h *TaskHandler) Update(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

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

	var req updateTaskRequest
	if err := c.BodyParser(&req); err != nil {
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

// ClearCompleted deletes all completed/failed tasks.
func (h *TaskHandler) ClearCompleted(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	if err := h.queries.DeleteCompletedTasks(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to clear tasks")
	}

	h.auditLog(c, pgtype.UUID{}, "task", "all", "clear_completed", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindTaskUpdate, "clear_completed")

	return c.JSON(fiber.Map{"status": "ok"})
}
