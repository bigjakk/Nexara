package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/rolling"
	"github.com/bigjakk/nexara/internal/safeconv"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// RollingUpdateHandler handles rolling update API endpoints.
type RollingUpdateHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	orchestrator  *rolling.Orchestrator
}

// NewRollingUpdateHandler creates a new RollingUpdateHandler.
func NewRollingUpdateHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher, orchestrator *rolling.Orchestrator) *RollingUpdateHandler {
	return &RollingUpdateHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		orchestrator:  orchestrator,
	}
}

type rollingUpdateJobResponse struct {
	ID                string          `json:"id"`
	ClusterID         string          `json:"cluster_id"`
	Status            string          `json:"status"`
	Parallelism       int32           `json:"parallelism"`
	RebootAfterUpdate bool            `json:"reboot_after_update"`
	AutoRestoreGuests bool            `json:"auto_restore_guests"`
	PackageExcludes   []string        `json:"package_excludes"`
	HAPolicy          string          `json:"ha_policy"`
	HAWarnings        json.RawMessage `json:"ha_warnings"`
	AutoUpgrade       bool            `json:"auto_upgrade"`
	FailureReason     string          `json:"failure_reason"`
	NotifyChannelID   string          `json:"notify_channel_id,omitempty"`
	CreatedBy         string          `json:"created_by"`
	StartedAt         string          `json:"started_at,omitempty"`
	CompletedAt       string          `json:"completed_at,omitempty"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type rollingUpdateNodeResponse struct {
	ID                 string          `json:"id"`
	JobID              string          `json:"job_id"`
	NodeName           string          `json:"node_name"`
	NodeOrder          int32           `json:"node_order"`
	Step               string          `json:"step"`
	FailureReason      string          `json:"failure_reason"`
	SkipReason         string          `json:"skip_reason,omitempty"`
	PackagesJSON       json.RawMessage `json:"packages_json"`
	GuestsJSON         json.RawMessage `json:"guests_json"`
	DrainStartedAt     string          `json:"drain_started_at,omitempty"`
	DrainCompletedAt   string          `json:"drain_completed_at,omitempty"`
	UpgradeConfirmedAt string          `json:"upgrade_confirmed_at,omitempty"`
	RebootStartedAt    string          `json:"reboot_started_at,omitempty"`
	RebootCompletedAt  string          `json:"reboot_completed_at,omitempty"`
	HealthCheckAt      string          `json:"health_check_at,omitempty"`
	RestoreStartedAt   string          `json:"restore_started_at,omitempty"`
	RestoreCompletedAt string          `json:"restore_completed_at,omitempty"`
	UpgradeStartedAt   string          `json:"upgrade_started_at,omitempty"`
	UpgradeCompletedAt string          `json:"upgrade_completed_at,omitempty"`
	UpgradeOutput      string          `json:"upgrade_output,omitempty"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
}

func toJobResponse(j db.RollingUpdateJob) rollingUpdateJobResponse {
	haWarnings := j.HaWarnings
	if len(haWarnings) == 0 || string(haWarnings) == "null" {
		haWarnings = json.RawMessage(`[]`)
	}
	r := rollingUpdateJobResponse{
		ID:                j.ID.String(),
		ClusterID:         j.ClusterID.String(),
		Status:            j.Status,
		Parallelism:       j.Parallelism,
		RebootAfterUpdate: j.RebootAfterUpdate,
		AutoRestoreGuests: j.AutoRestoreGuests,
		PackageExcludes:   j.PackageExcludes,
		HAPolicy:          j.HaPolicy,
		HAWarnings:        haWarnings,
		AutoUpgrade:       j.AutoUpgrade,
		FailureReason:     j.FailureReason,
		CreatedBy:         j.CreatedBy.String(),
		CreatedAt:         j.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:         j.UpdatedAt.Format(time.RFC3339Nano),
	}
	if j.StartedAt.Valid {
		r.StartedAt = j.StartedAt.Time.Format(time.RFC3339Nano)
	}
	if j.CompletedAt.Valid {
		r.CompletedAt = j.CompletedAt.Time.Format(time.RFC3339Nano)
	}
	if r.PackageExcludes == nil {
		r.PackageExcludes = []string{}
	}
	if j.NotifyChannelID.Valid {
		r.NotifyChannelID = uuid.UUID(j.NotifyChannelID.Bytes).String()
	}
	return r
}

func formatTimestamptz(t pgtype.Timestamptz) string {
	if t.Valid {
		return t.Time.Format(time.RFC3339Nano)
	}
	return ""
}

func toRollingNodeResponse(n db.RollingUpdateNode) rollingUpdateNodeResponse {
	return rollingUpdateNodeResponse{
		ID:                 n.ID.String(),
		JobID:              n.JobID.String(),
		NodeName:           n.NodeName,
		NodeOrder:          n.NodeOrder,
		Step:               n.Step,
		FailureReason:      n.FailureReason,
		SkipReason:         n.SkipReason,
		PackagesJSON:       n.PackagesJson,
		GuestsJSON:         n.GuestsJson,
		DrainStartedAt:     formatTimestamptz(n.DrainStartedAt),
		DrainCompletedAt:   formatTimestamptz(n.DrainCompletedAt),
		UpgradeConfirmedAt: formatTimestamptz(n.UpgradeConfirmedAt),
		RebootStartedAt:    formatTimestamptz(n.RebootStartedAt),
		RebootCompletedAt:  formatTimestamptz(n.RebootCompletedAt),
		HealthCheckAt:      formatTimestamptz(n.HealthCheckAt),
		RestoreStartedAt:   formatTimestamptz(n.RestoreStartedAt),
		RestoreCompletedAt: formatTimestamptz(n.RestoreCompletedAt),
		UpgradeStartedAt:   formatTimestamptz(n.UpgradeStartedAt),
		UpgradeCompletedAt: formatTimestamptz(n.UpgradeCompletedAt),
		UpgradeOutput:      n.UpgradeOutput,
		CreatedAt:          n.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:          n.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// ListJobs returns rolling update jobs for a cluster.
func (h *RollingUpdateHandler) ListJobs(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "rolling_update", clusterID); err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	jobs, err := h.queries.ListRollingUpdateJobs(c.Context(), db.ListRollingUpdateJobsParams{
		ClusterID: clusterID,
		Limit:     safeconv.Int32(limit),
		Offset:    safeconv.Int32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list rolling update jobs")
	}

	result := make([]rollingUpdateJobResponse, len(jobs))
	for i, j := range jobs {
		result[i] = toJobResponse(j)
	}

	return c.JSON(result)
}

type createRollingUpdateRequest struct {
	Nodes             []string `json:"nodes"`
	Parallelism       int32    `json:"parallelism"`
	RebootAfterUpdate *bool    `json:"reboot_after_update"`
	AutoRestoreGuests *bool    `json:"auto_restore_guests"`
	PackageExcludes   []string `json:"package_excludes"`
	HAPolicy          string   `json:"ha_policy"`
	AutoUpgrade       *bool    `json:"auto_upgrade"`
	NotifyChannelID   *string  `json:"notify_channel_id"`
}

// CreateJob creates a new rolling update job.
func (h *RollingUpdateHandler) CreateJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	var req createRollingUpdateRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if len(req.Nodes) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "At least one node is required")
	}
	if len(req.Nodes) > 64 {
		return fiber.NewError(fiber.StatusBadRequest, "Too many nodes (max 64)")
	}
	if req.Parallelism <= 0 {
		req.Parallelism = 1
	}
	if req.Parallelism > safeconv.Int32(len(req.Nodes)) {
		req.Parallelism = safeconv.Int32(len(req.Nodes))
	}

	// Validate node names.
	for _, n := range req.Nodes {
		if n == "" || len(n) > 128 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node name")
		}
	}

	// Prevent concurrent jobs for the same cluster.
	hasRunning, err := h.queries.HasRunningJobForCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for running jobs")
	}
	if hasRunning {
		return fiber.NewError(fiber.StatusConflict, "A rolling update job is already active for this cluster")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	rebootAfter := false
	if req.RebootAfterUpdate != nil {
		rebootAfter = *req.RebootAfterUpdate
	}
	autoRestore := true
	if req.AutoRestoreGuests != nil {
		autoRestore = *req.AutoRestoreGuests
	}
	if req.PackageExcludes == nil {
		req.PackageExcludes = []string{}
	}

	haPolicy := req.HAPolicy
	if haPolicy == "" {
		haPolicy = "warn"
	}
	if haPolicy != "strict" && haPolicy != "warn" {
		return fiber.NewError(fiber.StatusBadRequest, "ha_policy must be 'strict' or 'warn'")
	}

	// Run pre-flight checks: HA constraints + capacity analysis.
	var haWarningsJSON json.RawMessage
	client, clientErr := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if clientErr == nil {
		var allConflicts []rolling.HAConflict
		preflightHasErrors := false

		// HA/DRS constraint check.
		report, err := rolling.AnalyzeHAConstraints(c.Context(), client, h.queries, clusterID, req.Nodes)
		if err == nil && report != nil {
			allConflicts = append(allConflicts, report.Conflicts...)
			preflightHasErrors = preflightHasErrors || report.HasErrors
		}

		// Capacity feasibility check — verifies remaining nodes can absorb
		// the workload when each batch of nodes is drained.
		capConflicts, capHasErrors, capErr := rolling.AnalyzeCapacity(c.Context(), client, req.Nodes, req.Parallelism)
		if capErr == nil && len(capConflicts) > 0 {
			allConflicts = append(allConflicts, capConflicts...)
			preflightHasErrors = preflightHasErrors || capHasErrors
		}

		if haPolicy == "strict" && preflightHasErrors {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error":     "preflight_conflict",
				"message":   "Pre-flight checks failed and policy is strict",
				"conflicts": allConflicts,
			})
		}
		haWarningsJSON, _ = json.Marshal(allConflicts)
	}
	if haWarningsJSON == nil {
		haWarningsJSON = json.RawMessage(`[]`)
	}

	autoUpgrade := false
	if req.AutoUpgrade != nil && *req.AutoUpgrade {
		// Verify SSH credentials exist for this cluster.
		hasCreds, err := h.queries.HasClusterSSHCredentials(c.Context(), clusterID)
		if err != nil || !hasCreds {
			return fiber.NewError(fiber.StatusBadRequest, "Auto upgrade requires SSH credentials to be configured for this cluster")
		}
		autoUpgrade = true
	}

	var notifyChannelID pgtype.UUID
	if req.NotifyChannelID != nil && *req.NotifyChannelID != "" {
		parsed, parseErr := uuid.Parse(*req.NotifyChannelID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid notify_channel_id")
		}
		notifyChannelID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	job, err := h.queries.InsertRollingUpdateJob(c.Context(), db.InsertRollingUpdateJobParams{
		ClusterID:         clusterID,
		Parallelism:       req.Parallelism,
		RebootAfterUpdate: rebootAfter,
		AutoRestoreGuests: autoRestore,
		PackageExcludes:   req.PackageExcludes,
		HaPolicy:          haPolicy,
		HaWarnings:        haWarningsJSON,
		AutoUpgrade:       autoUpgrade,
		CreatedBy:         userID,
		NotifyChannelID:   notifyChannelID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create rolling update job")
	}

	// Fetch packages for each node and insert node rows.
	// Reuse client from HA preflight if available, otherwise create one.
	if client == nil {
		client, clientErr = CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	}

	for i, nodeName := range req.Nodes {
		var packagesJSON json.RawMessage
		if clientErr == nil {
			updates, err := client.GetNodeAptUpdates(c.Context(), nodeName)
			if err == nil {
				packagesJSON, _ = json.Marshal(updates)
			}
		}
		if packagesJSON == nil {
			packagesJSON = json.RawMessage(`[]`)
		}

		_, err := h.queries.InsertRollingUpdateNode(c.Context(), db.InsertRollingUpdateNodeParams{
			JobID:        job.ID,
			NodeName:     nodeName,
			NodeOrder:    safeconv.Int32(i),
			PackagesJson: packagesJSON,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to create rolling update node")
		}
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", job.ID.String(), "rolling_update_created", nil)

	return c.Status(fiber.StatusCreated).JSON(toJobResponse(job))
}

// GetJob returns a single rolling update job with node counts.
func (h *RollingUpdateHandler) GetJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	job, err := h.queries.GetRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}

	return c.JSON(toJobResponse(job))
}

// StartJob starts a pending rolling update job.
func (h *RollingUpdateHandler) StartJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	job, err := h.queries.GetRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}
	if job.Status != "pending" {
		return fiber.NewError(fiber.StatusBadRequest, "Job is not in pending status")
	}

	if err := h.queries.StartRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to start job")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "rolling_update_started", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "started")
	}

	job.Status = "running"
	return c.JSON(toJobResponse(job))
}

// CancelJob cancels a rolling update job.
func (h *RollingUpdateHandler) CancelJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	job, err := h.queries.GetRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}
	if job.ClusterID != clusterID {
		// The permission check above covered the URL cluster; make sure the
		// job actually belongs to it.
		return fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}

	if err := h.queries.CancelRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to cancel job")
	}

	// Release everything the job still holds — the DRS pause, the native CRS
	// pause, and HA rules disabled for in-flight nodes. Cancelled jobs drop
	// out of the orchestrator's running-jobs tick, so nothing else would ever
	// restore these. Detached context: the restores call Proxmox and must
	// outlive this request.
	go func() { //nolint:gosec // G118: intentionally detached — cancel-cleanup restores must outlive the request (Fiber recycles the request context)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("rolling-update cancel cleanup panicked", "job_id", jobID, "panic", r)
			}
		}()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		h.orchestrator.CleanupCancelledJob(cleanupCtx, jobID)
	}()

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "rolling_update_cancelled", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "cancelled")
	}

	return c.JSON(fiber.Map{"status": "cancelled"})
}

// PauseJob pauses a running rolling update job.
func (h *RollingUpdateHandler) PauseJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	if err := h.queries.PauseRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to pause job")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "rolling_update_paused", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "paused")
	}

	return c.JSON(fiber.Map{"status": "paused"})
}

// ResumeJob resumes a paused rolling update job.
func (h *RollingUpdateHandler) ResumeJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	if err := h.queries.ResumeRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to resume job")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "rolling_update_resumed", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "resumed")
	}

	return c.JSON(fiber.Map{"status": "running"})
}

// ListNodes returns nodes for a rolling update job.
func (h *RollingUpdateHandler) ListNodes(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	nodes, err := h.queries.ListRollingUpdateNodes(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}

	result := make([]rollingUpdateNodeResponse, len(nodes))
	for i, n := range nodes {
		result[i] = toRollingNodeResponse(n)
	}

	return c.JSON(result)
}

// ConfirmUpgrade confirms that manual upgrade is done on a node.
func (h *RollingUpdateHandler) ConfirmUpgrade(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	nodeID, err := uuid.Parse(c.Params("node_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
	}

	job, err := h.queries.GetRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}

	node, err := h.queries.GetRollingUpdateNode(c.Context(), nodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rolling update node not found")
	}

	if node.JobID != jobID {
		return fiber.NewError(fiber.StatusBadRequest, "Node does not belong to this job")
	}

	if err := h.orchestrator.ConfirmUpgrade(c.Context(), job, node); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	details, _ := json.Marshal(map[string]string{"node": node.NodeName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "node_upgrade_confirmed", details)

	return c.JSON(fiber.Map{"status": "confirmed"})
}

// SkipNode skips a pending node in a rolling update job.
func (h *RollingUpdateHandler) SkipNode(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "rolling_update", clusterID); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	nodeID, err := uuid.Parse(c.Params("node_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
	}

	node, err := h.queries.GetRollingUpdateNode(c.Context(), nodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Node not found")
	}

	if node.JobID != jobID {
		return fiber.NewError(fiber.StatusBadRequest, "Node does not belong to this job")
	}

	if err := h.queries.SkipRollingUpdateNode(c.Context(), db.SkipRollingUpdateNodeParams{
		ID:         nodeID,
		SkipReason: "manually skipped by user",
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to skip node")
	}

	details, _ := json.Marshal(map[string]string{"node": node.NodeName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "node_skipped", details)

	return c.JSON(fiber.Map{"status": "skipped"})
}

// PreviewPackages returns pending apt packages for a specific node.
func (h *RollingUpdateHandler) PreviewPackages(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "rolling_update", clusterID); err != nil {
		return err
	}

	nodeName := c.Params("node")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}

	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}

	updates, err := client.GetNodeAptUpdates(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get package updates")
	}

	return c.JSON(updates)
}

// PreflightHA analyzes HA/DRS constraints and capacity feasibility for a proposed set of nodes.
func (h *RollingUpdateHandler) PreflightHA(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "rolling_update", clusterID); err != nil {
		return err
	}

	var req struct {
		Nodes       []string `json:"nodes"`
		Parallelism int32    `json:"parallelism"`
	}
	if err := c.Bind().Body(&req); err != nil || len(req.Nodes) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "nodes array is required")
	}
	if req.Parallelism <= 0 {
		req.Parallelism = 1
	}

	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to connect to cluster")
	}

	report, err := rolling.AnalyzeHAConstraints(c.Context(), client, h.queries, clusterID, req.Nodes)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to analyze HA constraints")
	}
	if report == nil {
		report = &rolling.HAPreFlightReport{Conflicts: []rolling.HAConflict{}}
	}

	// Capacity feasibility check.
	capConflicts, capHasErrors, capErr := rolling.AnalyzeCapacity(c.Context(), client, req.Nodes, req.Parallelism)
	if capErr == nil && len(capConflicts) > 0 {
		report.Conflicts = append(report.Conflicts, capConflicts...)
		report.HasErrors = report.HasErrors || capHasErrors
	}

	return c.JSON(report)
}

// --- SSH Credential Management ---

type sshCredentialResponse struct {
	ClusterID string `json:"cluster_id"`
	Username  string `json:"username"`
	Port      int32  `json:"port"`
	AuthType  string `json:"auth_type"`
	HasKey    bool   `json:"has_key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// GetSSHCredentials returns SSH credential metadata (never returns the secret).
func (h *RollingUpdateHandler) GetSSHCredentials(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}

	creds, err := h.queries.GetClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return c.JSON(nil) // No credentials configured.
	}

	return c.JSON(sshCredentialResponse{
		ClusterID: creds.ClusterID.String(),
		Username:  creds.Username,
		Port:      creds.Port,
		AuthType:  creds.AuthType,
		HasKey:    creds.EncryptedPrivateKey != "",
		CreatedAt: creds.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: creds.UpdatedAt.Format(time.RFC3339Nano),
	})
}

type upsertSSHCredentialRequest struct {
	Username   string `json:"username"`
	Port       int32  `json:"port"`
	AuthType   string `json:"auth_type"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
}

// UpsertSSHCredentials creates or updates SSH credentials for a cluster.
func (h *RollingUpdateHandler) UpsertSSHCredentials(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}

	var req upsertSSHCredentialRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Username == "" {
		req.Username = "root"
	}
	if len(req.Username) > 64 {
		return fiber.NewError(fiber.StatusBadRequest, "Username too long (max 64)")
	}
	if req.Port <= 0 || req.Port > 65535 {
		req.Port = 22
	}
	if req.AuthType != "password" && req.AuthType != "key" {
		return fiber.NewError(fiber.StatusBadRequest, "auth_type must be 'password' or 'key'")
	}
	if req.AuthType == "password" && req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Password is required for password auth")
	}
	if req.AuthType == "key" && req.PrivateKey == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Private key is required for key auth")
	}

	var encPassword, encKey string
	if req.Password != "" {
		encPassword, err = crypto.Encrypt(req.Password, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt credentials")
		}
	}
	if req.PrivateKey != "" {
		encKey, err = crypto.Encrypt(req.PrivateKey, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt credentials")
		}
	}

	creds, err := h.queries.UpsertClusterSSHCredentials(c.Context(), db.UpsertClusterSSHCredentialsParams{
		ClusterID:           clusterID,
		Username:            req.Username,
		Port:                req.Port,
		AuthType:            req.AuthType,
		EncryptedPassword:   encPassword,
		EncryptedPrivateKey: encKey,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to save SSH credentials")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", clusterID.String(), "ssh_credentials_updated", nil)

	return c.JSON(sshCredentialResponse{
		ClusterID: creds.ClusterID.String(),
		Username:  creds.Username,
		Port:      creds.Port,
		AuthType:  creds.AuthType,
		HasKey:    creds.EncryptedPrivateKey != "",
		CreatedAt: creds.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: creds.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// DeleteSSHCredentials removes SSH credentials for a cluster.
func (h *RollingUpdateHandler) DeleteSSHCredentials(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}

	if err := h.queries.DeleteClusterSSHCredentials(c.Context(), clusterID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete SSH credentials")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", clusterID.String(), "ssh_credentials_deleted", nil)

	return c.JSON(fiber.Map{"status": "deleted"})
}

// TestSSHConnection runs the TOFU host-key flow against a specific node.
//
// Three response shapes:
//   - {success: true} — host key pinned, auth succeeded.
//   - {success: false, host_key_pending: {...}} — host key not yet pinned;
//     UI should show fingerprint and ask the user to confirm + pin.
//   - {success: false, host_key_mismatch: {...}} — pinned key did NOT
//     match the presented one; UI should warn and offer re-pin.
//   - {success: false, message: "..."} — connection or auth failure for
//     reasons unrelated to host-key trust.
func (h *RollingUpdateHandler) TestSSHConnection(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}

	var req struct {
		NodeName string `json:"node_name"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if err := validateNodeName(req.NodeName); err != nil {
		return err
	}

	creds, err := h.queries.GetClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "SSH credentials not configured for this cluster")
	}

	sshHost, hostErr := h.resolveNodeAddress(c, clusterID, req.NodeName)
	if hostErr != nil {
		return hostErr
	}
	if err := guardSSHHost(c.Context(), sshHost); err != nil {
		return err
	}
	sshPort := int(creds.Port)

	// Look up the pinned host key, if any.
	pinned, pinErr := h.queries.GetSSHKnownHost(c.Context(), db.GetSSHKnownHostParams{
		ClusterID: clusterID,
		Host:      sshHost,
		Port:      creds.Port,
	})
	pinnedFound := pinErr == nil

	// Path 1: no pinned key yet — scan and return fingerprint for the
	// user to confirm, do NOT attempt auth.
	if !pinnedFound {
		key, scanErr := sshpkg.ScanHostKey(c.Context(), sshHost, sshPort)
		if scanErr != nil {
			return c.JSON(fiber.Map{
				"success": false,
				"message": scanErr.Error(),
			})
		}
		return c.JSON(fiber.Map{
			"success": false,
			"message": "Host key not yet trusted. Confirm the fingerprint matches the node, then pin it.",
			"host_key_pending": fiber.Map{
				"host":        sshHost,
				"port":        sshPort,
				"fingerprint": sshpkg.FingerprintSHA256(key),
				"public_key":  sshpkg.MarshalAuthorizedKey(key),
			},
		})
	}

	// Path 2: pinned — attempt the connection and surface a typed
	// mismatch error if the remote key has changed.
	knownKey, parseErr := sshpkg.ParseAuthorizedKey(pinned.PublicKey)
	if parseErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Stored host key is corrupt — delete and re-pin")
	}

	password, privateKey, decErr := h.decryptSSHCredentials(creds)
	if decErr != nil {
		return decErr
	}

	sshCfg := sshpkg.Config{
		Host:         sshHost,
		Port:         sshPort,
		Username:     creds.Username,
		Password:     password,
		PrivateKey:   privateKey,
		KnownHostKey: knownKey,
	}

	if err := sshpkg.TestConnection(c.Context(), sshCfg); err != nil {
		var mismatch *sshpkg.HostKeyMismatchError
		if errors.As(err, &mismatch) {
			return c.JSON(fiber.Map{
				"success": false,
				"message": "Host key has changed since it was pinned. Investigate before re-pinning.",
				"host_key_mismatch": fiber.Map{
					"host":                  sshHost,
					"port":                  sshPort,
					"expected_fingerprint":  mismatch.ExpectedFingerprint,
					"presented_fingerprint": mismatch.PresentedFingerprint,
					"presented_public_key":  mismatch.PresentedPublicKey,
				},
			})
		}
		return c.JSON(fiber.Map{
			"success": false,
			"message": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success":     true,
		"message":     "SSH connection successful",
		"fingerprint": pinned.Fingerprint,
	})
}

// --- SSH Known-Host Management ---

type sshKnownHostResponse struct {
	ID          string `json:"id"`
	ClusterID   string `json:"cluster_id"`
	Host        string `json:"host"`
	Port        int32  `json:"port"`
	Fingerprint string `json:"fingerprint"`
	PinnedBy    string `json:"pinned_by,omitempty"`
	PinnedAt    string `json:"pinned_at"`
}

// ListSSHKnownHosts returns the pinned host keys for a cluster.
func (h *RollingUpdateHandler) ListSSHKnownHosts(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}

	rows, err := h.queries.ListSSHKnownHosts(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list pinned host keys")
	}

	out := make([]sshKnownHostResponse, 0, len(rows))
	for _, r := range rows {
		resp := sshKnownHostResponse{
			ID:          r.ID.String(),
			ClusterID:   r.ClusterID.String(),
			Host:        r.Host,
			Port:        r.Port,
			Fingerprint: r.Fingerprint,
			PinnedAt:    r.PinnedAt.Format(time.RFC3339Nano),
		}
		if r.PinnedBy.Valid {
			resp.PinnedBy = uuid.UUID(r.PinnedBy.Bytes).String()
		}
		out = append(out, resp)
	}
	return c.JSON(out)
}

type pinSSHHostKeyRequest struct {
	NodeName            string `json:"node_name"`
	ExpectedFingerprint string `json:"expected_fingerprint"`
}

// PinSSHHostKey runs a fresh host-key scan and stores the result, but only
// after verifying the freshly-scanned fingerprint matches the one the user
// confirmed in the UI. This closes the TOCTOU window between the test
// response and the pin call.
func (h *RollingUpdateHandler) PinSSHHostKey(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}

	var req pinSSHHostKeyRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if err := validateNodeName(req.NodeName); err != nil {
		return err
	}
	if req.ExpectedFingerprint == "" || len(req.ExpectedFingerprint) > 128 {
		return fiber.NewError(fiber.StatusBadRequest, "expected_fingerprint is required (and must be ≤ 128 chars)")
	}

	creds, err := h.queries.GetClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "SSH credentials not configured for this cluster")
	}

	sshHost, hostErr := h.resolveNodeAddress(c, clusterID, req.NodeName)
	if hostErr != nil {
		return hostErr
	}
	if err := guardSSHHost(c.Context(), sshHost); err != nil {
		return err
	}

	key, scanErr := sshpkg.ScanHostKey(c.Context(), sshHost, int(creds.Port))
	if scanErr != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to scan host key: "+scanErr.Error())
	}
	scannedFP := sshpkg.FingerprintSHA256(key)
	if scannedFP != req.ExpectedFingerprint {
		return fiber.NewError(fiber.StatusConflict,
			"Host key changed between confirmation and pin (expected "+req.ExpectedFingerprint+
				", scanned "+scannedFP+"). Re-test the connection and confirm the new fingerprint.")
	}

	pinnedBy := pgtype.UUID{}
	if uid, ok := c.Locals("user_id").(uuid.UUID); ok {
		pinnedBy = pgtype.UUID{Bytes: uid, Valid: true}
	}

	row, err := h.queries.UpsertSSHKnownHost(c.Context(), db.UpsertSSHKnownHostParams{
		ClusterID:   clusterID,
		Host:        sshHost,
		Port:        creds.Port,
		PublicKey:   sshpkg.MarshalAuthorizedKey(key),
		Fingerprint: scannedFP,
		PinnedBy:    pinnedBy,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to pin host key")
	}

	details, _ := json.Marshal(fiber.Map{
		"host":        sshHost,
		"fingerprint": scannedFP,
		"node_name":   req.NodeName,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", row.ID.String(), "ssh_host_key_pinned", details)

	resp := sshKnownHostResponse{
		ID:          row.ID.String(),
		ClusterID:   row.ClusterID.String(),
		Host:        row.Host,
		Port:        row.Port,
		Fingerprint: row.Fingerprint,
		PinnedAt:    row.PinnedAt.Format(time.RFC3339Nano),
	}
	if row.PinnedBy.Valid {
		resp.PinnedBy = uuid.UUID(row.PinnedBy.Bytes).String()
	}
	return c.JSON(resp)
}

// DeleteSSHKnownHost removes a pinned host key entry. The next connection
// to that host will fail closed until re-pinned.
func (h *RollingUpdateHandler) DeleteSSHKnownHost(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "ssh_credentials", clusterID); err != nil {
		return err
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}

	if err := h.queries.DeleteSSHKnownHostByID(c.Context(), db.DeleteSSHKnownHostByIDParams{
		ID:        id,
		ClusterID: clusterID,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete pinned host key")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", id.String(), "ssh_host_key_unpinned", nil)
	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- helpers ---

// validateNodeName accepts only the character set Proxmox itself uses for
// node names: ASCII letters, digits, and hyphens, up to 64 chars. This
// prevents control characters from leaking into audit logs or admin UI.
func validateNodeName(s string) error {
	if s == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}
	if len(s) > 64 {
		return fiber.NewError(fiber.StatusBadRequest, "node_name too long (max 64)")
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return fiber.NewError(fiber.StatusBadRequest, "node_name contains unsupported characters")
		}
	}
	return nil
}

// guardSSHHost rejects SSH targets that have no legitimate use case: the
// cloud metadata IP (169.254.169.254), unspecified addresses, and multicast.
// Private/RFC1918/link-local addresses are EXPECTED here — Proxmox nodes are
// almost always reachable on internal networks — so unlike the cluster API
// URL flow we don't apply the warn-and-confirm gate.
func guardSSHHost(ctx context.Context, host string) error {
	err := enforceHostAddressPolicy(ctx, host, true)
	if err == nil {
		return nil
	}
	var pErr *addressPolicyError
	if errors.As(err, &pErr) && pErr.HardReject {
		return fiber.NewError(fiber.StatusBadRequest, pErr.Reason)
	}
	return nil
}

// resolveNodeAddress returns the IP address for a Proxmox node in a cluster,
// or a 400 error if the address is not yet known. The previous silent
// fallback to using the node name as a hostname is removed; an unknown
// address now fails loudly so the user can fix the underlying state.
func (h *RollingUpdateHandler) resolveNodeAddress(c fiber.Ctx, clusterID uuid.UUID, nodeName string) (string, error) {
	addr, err := h.queries.GetNodeAddressByName(c.Context(), db.GetNodeAddressByNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	})
	if err != nil || addr == "" {
		return "", fiber.NewError(fiber.StatusBadRequest,
			"No IP address known for node "+nodeName+" — wait for the collector to report it, then retry.")
	}
	// Defence-in-depth against collector data corruption: reject control
	// characters so a poisoned address can't smuggle CR/LF into log lines.
	for _, r := range addr {
		if r < 0x20 || r == 0x7f {
			return "", fiber.NewError(fiber.StatusInternalServerError, "Stored node address contains control characters")
		}
	}
	return addr, nil
}

// decryptSSHCredentials decrypts the password and private key, returning a
// fiber-friendly error if either fails.
func (h *RollingUpdateHandler) decryptSSHCredentials(creds db.ClusterSshCredential) (password, privateKey string, err error) {
	if creds.EncryptedPassword != "" {
		p, decErr := crypto.Decrypt(creds.EncryptedPassword, h.encryptionKey)
		if decErr != nil {
			return "", "", fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt credentials")
		}
		password = p
	}
	if creds.EncryptedPrivateKey != "" {
		k, decErr := crypto.Decrypt(creds.EncryptedPrivateKey, h.encryptionKey)
		if decErr != nil {
			return "", "", fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt credentials")
		}
		privateKey = k
	}
	return password, privateKey, nil
}
