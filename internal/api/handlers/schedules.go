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

// snapshotScheduleParams is the slice of a snapshot task's opaque params this
// layer has an opinion about.
//
// It is deliberately a SUBSET of internal/scheduler's snapshotParams rather
// than a shared type: the column is carried through undescribed (the
// declaration's "params" is a bare apischema.Object) and the scheduler is what
// owns its full shape. The one key checked here is the one that can make the
// task unrunnable.
//
// Two copies of one contract is a drift that fails OPEN, so the two are pinned
// against each other by TestGuard_SnapshotScheduleParamsMatchesTheScheduler.
type snapshotScheduleParams struct {
	SnapName string `json:"snap_name"`
}

// snapshotScheduleGuestKind maps the stored resource_type onto the guest kind
// whose reserved names apply, using the SAME two values the scheduler switches
// on when the task fires (internal/scheduler, executeSnapshot).
//
// The second result is false for anything else. That is not a name the check
// can decide — and it does not need to: a snapshot task whose resource_type is
// neither "vm" nor "ct" is refused wholesale by the scheduler on its first
// fire, so no snap_name of any shape would have made it run. Refusing here on
// the NAME would report the wrong problem, and refusing on the TYPE is a
// separate change from this one.
func snapshotScheduleGuestKind(resourceType string) (snapshotGuestKind, bool) {
	switch resourceType {
	case "vm":
		return qemuSnapshot, true
	case "ct":
		return lxcSnapshot, true
	}
	return "", false
}

// validateSnapshotScheduleParams refuses AT CREATION a snap_name Proxmox would
// refuse at every fire.
//
// The client is what actually stops a bad name reaching Proxmox — see the
// "Snapshot names" block in internal/proxmox/client_guests.go, which is the
// choke point every caller goes through. This is not a second gate on the same
// hazard; it is about WHEN the operator finds out. Without it the row is
// accepted, the schedule looks armed in the UI, and the failure lands on every
// fire as a raw PVE sentence in last_error — a recurring snapshot that silently
// never runs. With it the caller gets a 400 naming the problem while they are
// still looking at the form.
//
// It reads the same json.RawMessage that is about to be STORED rather than the
// parsed body, so what is validated and what is persisted cannot diverge.
func validateSnapshotScheduleParams(action, resourceType string, params json.RawMessage) error {
	if action != "snapshot" {
		return nil
	}
	kind, known := snapshotScheduleGuestKind(resourceType)
	if !known {
		// Passing on rather than guessing a reserved set — the task is
		// unrunnable for a reason that has nothing to do with its name. See
		// snapshotScheduleGuestKind.
		return nil
	}
	var sp snapshotScheduleParams
	if err := json.Unmarshal(params, &sp); err != nil {
		// scheduleParamsJSON produced this a moment ago, so it is JSON; a
		// snap_name of the wrong TYPE is what lands here.
		return fiber.NewError(fiber.StatusBadRequest, "Invalid params: snap_name must be a string")
	}
	if sp.SnapName == "" {
		// Absent is the common case and is not a mistake: executeSnapshot
		// mints "auto-<timestamp>" instead, which is always a legal name.
		return nil
	}
	return snapshotNameError(kind, sp.SnapName)
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

	if err := validateSnapshotScheduleParams(action, resourceType, params); err != nil {
		return err
	}

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
// It returns the row because Update needs two fields the PUT body cannot carry:
// a task's action and the resource it acts on are fixed at creation, so the
// only way to know whether an updated params blob belongs to a snapshot — and
// to which guest kind — is to read them back. Delete ignores the row.
//
// Returning it does NOT make it the authority for the write. Both statements
// carry a cluster_id predicate of their own (queries/scheduled_tasks.sql), and
// that predicate is the PATH's clusterID — never task.ClusterID, which would
// scope the write on a value read out of the row it is about to write.
// TestGuard_ScheduledTaskWritesCarryTheClusterPredicate pins the value for
// exactly that reason, because with the row in scope the wrong one compiles
// and reads plausibly.
func (h *ScheduleHandler) taskInCluster(c fiber.Ctx, taskID, clusterID uuid.UUID) (db.ScheduledTask, error) {
	task, err := h.queries.GetScheduledTask(c.Context(), taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.ScheduledTask{}, fiber.NewError(fiber.StatusNotFound, "Schedule not found")
		}
		return db.ScheduledTask{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get schedule")
	}
	if task.ClusterID != clusterID {
		return db.ScheduledTask{}, fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}
	return task, nil
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

	task, err := h.taskInCluster(c, taskID, clusterID)
	if err != nil {
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

	// The action and the resource come off the stored row: a PUT can change
	// the cron, the params and the enabled flag, but not what the task acts
	// on.
	if err := validateSnapshotScheduleParams(task.Action, task.ResourceType, params); err != nil {
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

	if _, err := h.taskInCluster(c, taskID, clusterID); err != nil {
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
