package handlers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/scheduler"
)

// ScheduleHandler handles scheduled task CRUD endpoints.
type ScheduleHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
}

// NewScheduleHandler creates a new schedule handler.
func NewScheduleHandler(queries *db.Queries, eventPub *events.Publisher) *ScheduleHandler {
	return &ScheduleHandler{queries: queries, eventPub: eventPub}
}

type createScheduleRequest struct {
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Node         string          `json:"node"`
	Action       string          `json:"action"`
	Schedule     string          `json:"schedule"`
	Params       json.RawMessage `json:"params"`
	Enabled      bool            `json:"enabled"`
}

type updateScheduleRequest struct {
	Schedule string          `json:"schedule"`
	Params   json.RawMessage `json:"params"`
	Enabled  bool            `json:"enabled"`
}

type scheduleResponse struct {
	ID           uuid.UUID       `json:"id"`
	ClusterID    uuid.UUID       `json:"cluster_id"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Node         string          `json:"node"`
	Action       string          `json:"action"`
	Schedule     string          `json:"schedule"`
	Params       json.RawMessage `json:"params"`
	Enabled      bool            `json:"enabled"`
	LastRunAt    *string         `json:"last_run_at"`
	NextRunAt    *string         `json:"next_run_at"`
	LastStatus   *string         `json:"last_status"`
	LastError    *string         `json:"last_error"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

func toScheduleResponse(t db.ScheduledTask) scheduleResponse {
	r := scheduleResponse{
		ID:           t.ID,
		ClusterID:    t.ClusterID,
		ResourceType: t.ResourceType,
		ResourceID:   t.ResourceID,
		Node:         t.Node,
		Action:       t.Action,
		Schedule:     t.Schedule,
		Params:       t.Params,
		Enabled:      t.Enabled,
		CreatedAt:    t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if t.LastRunAt.Valid {
		s := t.LastRunAt.Time.Format("2006-01-02T15:04:05Z")
		r.LastRunAt = &s
	}
	if t.NextRunAt.Valid {
		s := t.NextRunAt.Time.Format("2006-01-02T15:04:05Z")
		r.NextRunAt = &s
	}
	if t.LastStatus.Valid {
		r.LastStatus = &t.LastStatus.String
	}
	if t.LastError.Valid {
		r.LastError = &t.LastError.String
	}
	return r
}

func (h *ScheduleHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

var validScheduleActions = map[string]bool{
	"snapshot": true,
	"reboot":   true,
}

// Create handles POST /api/v1/clusters/:cluster_id/schedules.
func (h *ScheduleHandler) Create(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "schedule"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req createScheduleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.ResourceType == "" || req.ResourceID == "" || req.Node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "resource_type, resource_id, and node are required")
	}
	if !validScheduleActions[req.Action] {
		return fiber.NewError(fiber.StatusBadRequest, "action must be one of: snapshot, reboot")
	}
	if err := scheduler.ValidateCron(req.Schedule); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if req.Params == nil {
		req.Params = json.RawMessage(`{}`)
	}

	nextRun, cronErr := scheduler.NextRunTime(req.Schedule, time.Now())
	var nextRunAt pgtype.Timestamptz
	if cronErr == nil && !nextRun.IsZero() {
		nextRunAt = pgtype.Timestamptz{Time: nextRun, Valid: true}
	}

	task, err := h.queries.InsertScheduledTask(c.Context(), db.InsertScheduledTaskParams{
		ClusterID:    clusterID,
		ResourceType: req.ResourceType,
		ResourceID:   req.ResourceID,
		Node:         req.Node,
		Action:       req.Action,
		Schedule:     req.Schedule,
		Params:       req.Params,
		Enabled:      req.Enabled,
		NextRunAt:    nextRunAt,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create schedule")
	}

	details, _ := json.Marshal(map[string]string{
		"action":        req.Action,
		"resource_type": req.ResourceType,
		"resource_id":   req.ResourceID,
		"schedule":      req.Schedule,
	})
	h.auditLog(c, clusterID, "schedule", task.ID.String(), "schedule_created", details)

	return c.Status(fiber.StatusCreated).JSON(toScheduleResponse(task))
}

// List handles GET /api/v1/clusters/:cluster_id/schedules.
func (h *ScheduleHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "schedule"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	tasks, err := h.queries.ListScheduledTasksByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list schedules")
	}

	resp := make([]scheduleResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = toScheduleResponse(t)
	}

	return c.JSON(resp)
}

// Update handles PUT /api/v1/clusters/:cluster_id/schedules/:id.
func (h *ScheduleHandler) Update(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "schedule"); err != nil {
		return err
	}

	taskID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid schedule ID")
	}

	var req updateScheduleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if err := scheduler.ValidateCron(req.Schedule); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if req.Params == nil {
		req.Params = json.RawMessage(`{}`)
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	if err := h.queries.UpdateScheduledTask(c.Context(), db.UpdateScheduledTaskParams{
		ID:       taskID,
		Schedule: req.Schedule,
		Params:   req.Params,
		Enabled:  req.Enabled,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update schedule")
	}

	details, _ := json.Marshal(map[string]string{
		"schedule": req.Schedule,
	})
	h.auditLog(c, clusterID, "schedule", taskID.String(), "schedule_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// Delete handles DELETE /api/v1/clusters/:cluster_id/schedules/:id.
func (h *ScheduleHandler) Delete(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "schedule"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	taskID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid schedule ID")
	}

	if err := h.queries.DeleteScheduledTask(c.Context(), taskID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete schedule")
	}

	h.auditLog(c, clusterID, "schedule", taskID.String(), "schedule_deleted", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}
