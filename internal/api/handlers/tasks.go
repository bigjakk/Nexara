package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// All four routes are declared in internal/api/registry_tasks.go, which states
// their permission and their parameters: Advisory on the listing, Deferred on
// the create and the update, and a global Check on the bulk clear.
//
// What stays here is what a declaration cannot reach: the two requireClusterPerm
// calls whose cluster is resolved at request time — from the body on the create,
// from the task row on the update — and the SQL scoping that IS the listing's
// authorization.

// TaskHandler handles task history CRUD operations.
type TaskHandler struct {
	queries       *db.Queries
	eventPub      *events.Publisher
	retention     time.Duration
	encryptionKey string
	// taskStatus reads a task from its cluster's Proxmox, which Create
	// confirms a task by before filing it: proxmoxTaskStatus, but in tests.
	taskStatus func(c fiber.Ctx, clusterID uuid.UUID, node, upid string) (*proxmox.TaskStatus, error)
}

// NewTaskHandler creates a new TaskHandler. retention is the task-history
// window (TASK_HISTORY_RETENTION) honored by ClearCompleted, matching the
// automatic scheduler sweep.
func NewTaskHandler(queries *db.Queries, eventPub *events.Publisher, retention time.Duration) *TaskHandler {
	h := &TaskHandler{queries: queries, eventPub: eventPub, retention: retention}
	h.taskStatus = h.proxmoxTaskStatus
	return h
}

// WithEncryptionKey sets the key the handler reads a cluster's Proxmox token
// with, where no client is cached.
func (h *TaskHandler) WithEncryptionKey(key string) *TaskHandler {
	h.encryptionKey = key
	return h
}

// taskConfirmTimeout bounds Create's read of the task from Proxmox.
const taskConfirmTimeout = 5 * time.Second

// proxmoxTaskStatus reads a task's status from the node it runs on, through
// its cluster's own Proxmox client.
func (h *TaskHandler) proxmoxTaskStatus(c fiber.Ctx, clusterID uuid.UUID, node, upid string) (*proxmox.TaskStatus, error) {
	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, taskConfirmTimeout)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(c.Context(), taskConfirmTimeout)
	defer cancel()
	return client.GetTaskStatus(ctx, node, upid)
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

// validTaskStatuses is the task_history state vocabulary. Nothing reads it at
// runtime: the declarations' Enums in internal/api/registry_tasks.go are what
// refuse an unknown status with a 400 before a handler runs — so a typo in
// ?status= surfaces instead of silently returning an empty page — and they
// carry the same set, less the "" that the listing's adds for "no filter" and
// the create's for "running". It is kept as the other side of
// TestTaskStatusVocabulary, which reads it through TaskStatusKeys and
// compares it with those Enums.
var validTaskStatuses = map[string]bool{
	"running": true, "completed": true, "failed": true, "stopped": true,
}

// TaskStatusKeys returns the accepted ?status= values, sorted. Exported for the
// guard in package api that compares them against the declared Enums; package
// handlers cannot import package api, so the comparison reads them from the
// other side.
func TaskStatusKeys() []string { return slices.Sorted(maps.Keys(validTaskStatuses)) }

// TaskSortKeys is TaskStatusKeys for the ?sort= whitelist below.
func TaskSortKeys() []string { return slices.Sorted(maps.Keys(taskSortColumns)) }

// taskSortColumns whitelists the ?sort= keys ListTaskHistoryFiltered knows how
// to order on, and defaultTaskSort/defaultTaskOrder are the ordering the page
// opens with (newest first, as it always has).
//
// The SQL matches these on the string, so an unrecognised value would fall
// through to the default order and silently ignore the caller. The ?sort=
// declaration's Enum refuses one with a 400 before List runs, and
// TestTaskSortVocabulary holds that Enum to this map; List still checks
// against it through parseTaskSort, answering 500 if the declaration ever
// stops. Keep in sync with TaskSortKey in the frontend's
// features/tasks/lib/task-columns.ts.
var taskSortColumns = map[string]bool{
	"started": true, "cluster": true, "type": true, "description": true,
	"vm": true, "node": true, "progress": true, "status": true,
}

const (
	defaultTaskSort  = "started"
	defaultTaskOrder = "desc"
)

// parseTaskSort reads the ?sort= / ?order= pair, defaulting both. Returns an
// error naming the offending parameter so a typo is diagnosable from the
// response rather than showing up as a mysteriously unsorted table.
func parseTaskSort(sortBy, order string) (col string, dir string, err error) {
	if sortBy == "" {
		sortBy = defaultTaskSort
	}
	if order == "" {
		order = defaultTaskOrder
	}
	if !taskSortColumns[sortBy] {
		return "", "", fmt.Errorf("invalid sort column %q", sortBy)
	}
	if order != "asc" && order != "desc" {
		return "", "", fmt.Errorf("invalid sort order %q", order)
	}
	return sortBy, order, nil
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
func (h *TaskHandler) List(c fiber.Ctx, p *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "task")
	if err != nil {
		return err
	}

	// The declaration's Enums and Defaults have already applied every rule
	// parseTaskSort makes, so this can no longer fail on a real request. It is
	// still called, and still checked: it is the one place that knows which
	// ORDER BY keys queries/tasks.sql matches, and a declaration that dropped
	// an Enum would otherwise reach the SQL as an unsorted page.
	sortBy, order, err := parseTaskSort(p.String("sort"), p.String("order"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Unsupported task sort")
	}

	listP := db.ListTaskHistoryFilteredParams{
		Limit:   safeconv.Int32(int(p.Int("limit"))),
		Offset:  safeconv.Int32(int(p.Int("offset"))),
		SortBy:  sortBy,
		SortDir: order,
	}
	// Sorting never reaches the count — ORDER BY cannot change how many rows
	// match, and the two queries must stay filter-for-filter identical.
	var countP db.CountTaskHistoryFilteredParams

	// Optional cluster filter — the caller must have view:task on it. Declared
	// as filter_cluster_id with "cluster_id" as its alias; see the declaration
	// for why the name the gate reads cannot be used for a query parameter.
	// An EMPTY value is the "no filter" it was before the registry: not a uuid
	// to parse and not a cluster to authorize, but every cluster the caller
	// can see, exactly as an omitted one.
	if cid := p.String("filter_cluster_id"); cid != "" {
		clusterID, parseErr := parseParamUUID(cid)
		if parseErr != nil {
			return parseErr
		}
		if !access.PermitsCluster(clusterID) {
			return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
		}
		v := pgtype.UUID{Bytes: clusterID, Valid: true}
		listP.ClusterID = v
		countP.ClusterID = v
	}

	// Likewise an empty status is every state, not a state to match.
	if status := p.String("status"); status != "" {
		v := pgtype.Text{String: status, Valid: true}
		listP.Status = v
		countP.Status = v
	}

	// Optional guest filter: comma-separated Proxmox VMIDs, matched against
	// task_history.vmid (parsed from the UPID at insert). Used by the folder
	// detail view to scope tasks to a folder's VMs. The list SHAPE stays a
	// handler rule — see taskVmidsParam for why it is declared as a string.
	// An empty list is no filter, as it was before the registry, not a list
	// for parseVmidsParam to refuse.
	if raw := p.String("vmids"); raw != "" {
		vmids, vmidErr := parseVmidsParam(raw)
		if vmidErr != nil {
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
		// ListTaskHistoryFilteredRow is the table's own shape — the query
		// returns task_history's columns and nothing else — so the conversion
		// is a compile-time assertion that the two stay identical.
		resp = append(resp, mapTaskHistory(db.TaskHistory(t)))
	}
	return RespondList(c, resp, total)
}

// Create creates a new task history record.
func (h *TaskHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid user")
	}

	clusterID, err := parseParamUUID(p.String("task_cluster_id"))
	if err != nil {
		return err
	}

	// Per-cluster gate so a user with manage:task scoped to cluster X
	// cannot insert task records claiming cluster Y. It is here rather than in
	// the declaration because the cluster arrives in the BODY, which middleware
	// runs before reading — see the Deferred reason on this route.
	if err := requireClusterPerm(c, "manage", "task", clusterID); err != nil {
		return err
	}

	// The collector polls the task at this node until it settles
	// (reconcileRunningTasks), so it must be one of the cluster's, and the
	// node the UPID itself says the task runs on — the one Proxmox would
	// answer for. Checked here, after the permission check, rather than by
	// the registry (Endpoint.NodesCheckedByHandler): the cluster is in the
	// body.
	// The UPID's own node is checked whether or not the body names one:
	// task_history.upid is unique across clusters, so a running task of
	// another cluster filed here first would never be recorded there.
	upid := p.String("upid")
	upidNode := extractNodeFromUPID(upid)
	node := p.String("node")
	if upidNode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "The UPID names no node")
	}
	if node != "" && upidNode != node {
		return fiber.NewError(fiber.StatusBadRequest, "node does not match the node the UPID names")
	}
	if err := requireNodeInCluster(c, lookupOf(h.queries), clusterID, upidNode); err != nil {
		return err
	}

	// A task is filed running; how it ended is the collector's, read from
	// Proxmox (ReconcileTaskHistory). The declaration's Default covers an
	// omitted status, and an EMPTY one means running too, as it always has.
	status := p.String("status")
	if status == "" {
		status = "running"
	}
	if status != "running" {
		return fiber.NewError(fiber.StatusBadRequest, "A task is recorded as running; how it ends is the collector's")
	}
	// The task must be one Proxmox runs on that node of the body's cluster: a
	// UPID names its node by name, and clusters share names like pve-01, so
	// another cluster's task would otherwise be filed here, and its own
	// ingest then lose to this row. Its type is what Proxmox reports.
	reported, err := h.taskStatus(c, clusterID, upidNode, upid)
	if err != nil {
		var fe *fiber.Error
		if errors.As(err, &fe) {
			return fe
		}
		var apiErr *proxmox.APIError
		if errors.As(err, &apiErr) && apiErr.IsParameterRejection() || errors.Is(err, proxmox.ErrNotFound) ||
			errors.Is(err, proxmox.ErrInvalidInput) {
			return fiber.NewError(fiber.StatusBadRequest, "Proxmox has no such task on that node of the cluster")
		}
		return fiber.NewError(fiber.StatusBadGateway, "Could not read the task from Proxmox")
	}
	// Nor is a task filed twice: a row under a UPID already on record would
	// stand beside, or over, the verdict the collector recorded for it. On
	// record anywhere: asked only of a UPID the body's cluster runs.
	onRecord, err := h.queries.TaskUPIDOnRecord(c.Context(), upid)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create task record")
	}
	if onRecord {
		return fiber.NewError(fiber.StatusConflict, "The task is already on record")
	}

	// The description is the caller's word, which the task's own would pass
	// for Proxmox's: Proxmox's task status carries none to take instead, so it
	// is kept in the filing's audit entry, as provided_description, beside who
	// provided it.
	task, err := h.queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID: clusterID,
		UserID:    uid,
		Upid:      upid,
		Status:    status,
		Node:      upidNode,
		TaskType:  reported.Type,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fiber.NewError(fiber.StatusConflict, "The task is already on record")
	}
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create task record")
	}

	// Under recorded_upid, not upid: an audit row naming the task's UPID
	// would itself count as the task — a guest's birth, for a create.
	fields := map[string]any{"recorded_upid": upid, "node": upidNode, "task_type": reported.Type}
	if provided := p.String("description"); provided != "" {
		fields["provided_description"] = provided
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "task", upid, "task_recorded", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindTaskCreated, "task", task.ID.String(), "create")

	return c.Status(fiber.StatusCreated).JSON(mapTaskHistory(task))
}

// Update updates a task history record by UPID.
func (h *TaskHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	// Fiber (fasthttp) doesn't auto-decode route params; the frontend
	// URL-encodes the UPID so colons arrive as %3A, etc.
	rawUPID := p.String("upid")
	upid, err := url.PathUnescape(rawUPID)
	if err != nil {
		upid = rawUPID
	}

	// Look up the task to find its cluster, then gate on per-cluster perm. It
	// is here rather than in the declaration because the cluster is a property
	// of the ROW, which middleware has no way to read — see the Deferred reason
	// on this route.
	task, err := h.queries.GetTaskByUpid(c.Context(), upid)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Task not found")
	}
	if err := requireClusterPerm(c, "manage", "task", task.ClusterID); err != nil {
		return err
	}
	// A finished task's verdict, and a task the collector recorded from
	// Proxmox, are the collector's: see UpdateTaskHistory.
	if task.Status != "running" || task.Source == "proxmox" {
		return fiber.NewError(fiber.StatusConflict, "Only a running task that Nexara recorded can be updated")
	}

	// The task stays running: how it ends is the collector's too.
	status := p.String("status")
	if status == "" {
		status = "running"
	}
	if _, finished := p.OptString("finished_at"); status != "running" || finished {
		return fiber.NewError(fiber.StatusBadRequest, "A task stays running here; how it ends is the collector's")
	}
	params := db.UpdateTaskHistoryParams{
		Upid:       upid,
		Status:     status,
		ExitStatus: p.String("exit_status"),
	}
	if progress, supplied := p.OptFloat("progress"); supplied {
		params.Progress = pgtype.Float8{Float64: progress, Valid: true}
	}

	updated, err := h.queries.UpdateTaskHistory(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update task")
	}
	if updated == 0 {
		return fiber.NewError(fiber.StatusConflict, "The task finished before it could be updated")
	}
	h.eventPub.SystemEvent(c.Context(), events.KindTaskUpdate, status)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ClearCompleted deletes all completed/failed tasks across every cluster.
//
// Because the underlying delete is unscoped (and adding a per-user cluster
// filter would require an array-of-uuid SQL parameter), this stays gated on
// global manage:task — i.e. effectively admin-only. A user with manage:task
// scoped only to cluster X cannot wipe history that includes cluster Y.
func (h *TaskHandler) ClearCompleted(c fiber.Ctx, _ *apischema.Params) error {
	if err := h.queries.DeleteCompletedTasks(c.Context(), time.Now().Add(-h.retention)); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to clear tasks")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "task", "all", "clear_completed", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindTaskUpdate, "clear_completed")

	return c.JSON(fiber.Map{"status": "ok"})
}
