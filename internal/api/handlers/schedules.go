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
	"github.com/bigjakk/nexara/internal/cronspec"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// All four routes are declared in internal/api/registry_schedules.go, which
// states their cluster-scoped permission (manage:schedule for the three writes,
// view:schedule for the listing) and their parameters. Nothing below re-states
// the permission — but the declaration is NOT the whole check, and the two
// things it cannot do are both here.
//
// The first is the CLUSTER the permission was granted on. A scheduled task is
// addressed by its uuid, and a uuid says nothing about which cluster owns it,
// while the declared Check resolves the cluster from the PATH. So the two
// per-task routes re-read the row and compare — see taskInCluster, which is the
// one place that comparison lives. This is the same hazard registry_metrics.go
// documents for the per-guest and per-node series, and it is fixed the same way.
//
// The second is the cron spec, validated HERE rather than in the declaration
// and deliberately: cronspec.NextValidRun validates the expression and returns
// the next run stored on the row in one call, because an invalid next_run_at
// reads as "due now" and a schedule that can never fire would be claimed and run
// on every scheduler tick.

// ScheduleHandler handles scheduled task CRUD endpoints.
type ScheduleHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
}

// NewScheduleHandler creates a new schedule handler.
func NewScheduleHandler(queries *db.Queries, eventPub *events.Publisher) *ScheduleHandler {
	return &ScheduleHandler{queries: queries, eventPub: eventPub}
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
		CreatedAt:    t.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:    t.UpdatedAt.Format(time.RFC3339Nano),
	}
	if t.LastRunAt.Valid {
		s := t.LastRunAt.Time.Format(time.RFC3339Nano)
		r.LastRunAt = &s
	}
	if t.NextRunAt.Valid {
		s := t.NextRunAt.Time.Format(time.RFC3339Nano)
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

// validScheduleActions is the action vocabulary. It is the same set the
// declaration's Enum carries in internal/api/registry_schedules.go —
// TestScheduleActionVocabulary pins the two against each other, because they
// are two copies of one list and a copy nothing compares is a copy that rots.
var validScheduleActions = map[string]bool{
	"snapshot": true,
	"reboot":   true,
}

// ScheduleActionKeys returns the accepted actions, sorted. Exported for the
// guard in package api that compares them against the declared Enum; package
// handlers cannot import package api, so the comparison reads this from the
// other side.
func ScheduleActionKeys() []string { return slices.Sorted(maps.Keys(validScheduleActions)) }

// scheduleParamsJSON renders the opaque per-action options back to the JSON the
// column stores.
//
// An absent value becomes `{}`, which is what the column has always held for a
// task that needs no options — a silent nil would store SQL NULL against a NOT
// NULL jsonb column.
func scheduleParamsJSON(p *apischema.Params) (json.RawMessage, error) {
	if !p.Has("params") {
		return json.RawMessage(`{}`), nil
	}
	raw, err := json.Marshal(p.Object("params"))
	if err != nil {
		// Unreachable for a value that arrived as JSON, but a silent nil here
		// would store NULL over what the caller sent.
		return nil, fiber.NewError(fiber.StatusBadRequest, "Invalid params")
	}
	return raw, nil
}

// Create handles POST /api/v1/clusters/:cluster_id/schedules.
func (h *ScheduleHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	schedule := p.String("schedule")
	// One call, so the next run stored is the one the validation computed.
	// It matters on this table: an invalid next_run_at reads as "due now", so
	// a schedule that can never fire would be claimed and run on every tick.
	nextRun, err := cronspec.NextValidRun(schedule, time.Now())
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	nextRunAt := pgtype.Timestamptz{Time: nextRun, Valid: true}

	params, err := scheduleParamsJSON(p)
	if err != nil {
		return err
	}

	action := p.String("action")
	resourceType := p.String("resource_type")
	resourceID := p.String("resource_id")

	task, err := h.queries.InsertScheduledTask(c.Context(), db.InsertScheduledTaskParams{
		ClusterID:    clusterID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Node:         p.String("node"),
		Action:       action,
		Schedule:     schedule,
		Params:       params,
		Enabled:      p.Bool("enabled"),
		NextRunAt:    nextRunAt,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create schedule")
	}

	details, _ := json.Marshal(map[string]string{
		"action":        action,
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"schedule":      schedule,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "schedule", task.ID.String(), "schedule_created", details)

	return c.Status(fiber.StatusCreated).JSON(toScheduleResponse(task))
}

// List handles GET /api/v1/clusters/:cluster_id/schedules.
func (h *ScheduleHandler) List(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	tasks, err := h.queries.ListScheduledTasksByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list schedules")
	}

	resp := make([]scheduleResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = toScheduleResponse(t)
	}

	return RespondItems(c, resp)
}

// taskInCluster resolves a scheduled task and refuses one that belongs to
// another cluster. It is the choke point every per-task route goes through, and
// TestGuard_ScheduledTaskLookupsAreClusterScoped is what keeps it one.
//
// The declared Check authorized the cluster in the PATH; this is what ties the
// TASK to that cluster. Without it a caller holding manage:schedule on cluster A
// can put A in the path and B's task uuid in it — and the uuids are not secret,
// because schedule_created audit rows carry them and view:audit is a default
// Viewer grant.
//
// Both branches answer 404 with the same message, deliberately: a 403 for "this
// task exists but is not yours" would make the endpoint an existence oracle for
// tasks in clusters the caller cannot see. It is the same split guest_snapshots
// makes for a guest of the wrong kind.
//
// It returns only an error: neither caller needs the row, and the WRITE they go
// on to issue carries the cluster predicate itself rather than trusting a value
// read a moment earlier.
func (h *ScheduleHandler) taskInCluster(c fiber.Ctx, taskID, clusterID uuid.UUID) error {
	task, err := h.queries.GetScheduledTask(c.Context(), taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get schedule")
	}
	if task.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}
	return nil
}

// Update handles PUT /api/v1/clusters/:cluster_id/schedules/:id.
func (h *ScheduleHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	taskID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if err := h.taskInCluster(c, taskID, clusterID); err != nil {
		return err
	}

	schedule := p.String("schedule")
	if err := cronspec.ValidateCron(schedule); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	params, err := scheduleParamsJSON(p)
	if err != nil {
		return err
	}

	// The cluster rides on the statement as well as being checked above, and
	// the two are independent on purpose — see the note on this query in
	// queries/scheduled_tasks.sql. A zero-row result means the row moved or
	// vanished between the read and the write; it is the same answer the read
	// would have given a moment earlier.
	rows, err := h.queries.UpdateScheduledTask(c.Context(), db.UpdateScheduledTaskParams{
		ID:        taskID,
		ClusterID: clusterID,
		Schedule:  schedule,
		Params:    params,
		Enabled:   p.Bool("enabled"),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update schedule")
	}
	if rows == 0 {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	details, _ := json.Marshal(map[string]string{
		"schedule": schedule,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "schedule", taskID.String(), "schedule_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// Delete handles DELETE /api/v1/clusters/:cluster_id/schedules/:id.
func (h *ScheduleHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	taskID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if err := h.taskInCluster(c, taskID, clusterID); err != nil {
		return err
	}

	// Scoped on the statement too; see Update for why the two layers are
	// separate.
	rows, err := h.queries.DeleteScheduledTask(c.Context(), db.DeleteScheduledTaskParams{
		ID:        taskID,
		ClusterID: clusterID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete schedule")
	}
	if rows == 0 {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "schedule", taskID.String(), "schedule_deleted", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}
