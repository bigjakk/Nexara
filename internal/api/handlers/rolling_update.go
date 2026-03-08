package handlers

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/rolling"
	sshpkg "github.com/proxdash/proxdash/internal/ssh"
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
	if haWarnings == nil {
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
		CreatedAt:         j.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         j.UpdatedAt.Format(time.RFC3339),
	}
	if j.StartedAt.Valid {
		r.StartedAt = j.StartedAt.Time.Format(time.RFC3339)
	}
	if j.CompletedAt.Valid {
		r.CompletedAt = j.CompletedAt.Time.Format(time.RFC3339)
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
		return t.Time.Format(time.RFC3339)
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
		CreatedAt:          n.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          n.UpdatedAt.Format(time.RFC3339),
	}
}

func (h *RollingUpdateHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", resourceID, action, details)
}

// ListJobs returns rolling update jobs for a cluster.
func (h *RollingUpdateHandler) ListJobs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
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
		Limit:     safeInt32(limit),
		Offset:    safeInt32(offset),
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
func (h *RollingUpdateHandler) CreateJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	var req createRollingUpdateRequest
	if err := c.BodyParser(&req); err != nil {
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
	if req.Parallelism > safeInt32(len(req.Nodes)) {
		req.Parallelism = safeInt32(len(req.Nodes))
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

	// Run HA pre-flight check.
	var haWarningsJSON json.RawMessage
	client, clientErr := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if clientErr == nil {
		report, err := rolling.AnalyzeHAConstraints(c.Context(), client, h.queries, clusterID, req.Nodes)
		if err == nil && report != nil {
			if haPolicy == "strict" && report.HasErrors {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{
					"error":     "ha_conflict",
					"message":   "HA constraints would be violated and policy is strict",
					"conflicts": report.Conflicts,
				})
			}
			haWarningsJSON, _ = json.Marshal(report.Conflicts)
		}
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
			NodeOrder:    safeInt32(i),
			PackagesJson: packagesJSON,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to create rolling update node")
		}
	}

	h.auditLog(c, clusterID,job.ID.String(), "rolling_update_created", nil)

	return c.Status(fiber.StatusCreated).JSON(toJobResponse(job))
}

// GetJob returns a single rolling update job with node counts.
func (h *RollingUpdateHandler) GetJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "rolling_update"); err != nil {
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
func (h *RollingUpdateHandler) StartJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
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

	h.auditLog(c, clusterID,jobID.String(), "rolling_update_started", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "started")
	}

	job.Status = "running"
	return c.JSON(toJobResponse(job))
}

// CancelJob cancels a rolling update job.
func (h *RollingUpdateHandler) CancelJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	// Read the job first to check DRS state before cancelling.
	job, err := h.queries.GetRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}

	if err := h.queries.CancelRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to cancel job")
	}

	// Re-enable DRS if it was disabled for this job.
	if job.DrsWasEnabled {
		_ = h.queries.SetDRSEnabled(c.Context(), db.SetDRSEnabledParams{
			ClusterID: clusterID,
			Enabled:   true,
		})
	}

	h.auditLog(c, clusterID,jobID.String(), "rolling_update_cancelled", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "cancelled")
	}

	return c.JSON(fiber.Map{"status": "cancelled"})
}

// PauseJob pauses a running rolling update job.
func (h *RollingUpdateHandler) PauseJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	if err := h.queries.PauseRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to pause job")
	}

	h.auditLog(c, clusterID,jobID.String(), "rolling_update_paused", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "paused")
	}

	return c.JSON(fiber.Map{"status": "paused"})
}

// ResumeJob resumes a paused rolling update job.
func (h *RollingUpdateHandler) ResumeJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid job ID")
	}

	if err := h.queries.ResumeRollingUpdateJob(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to resume job")
	}

	h.auditLog(c, clusterID,jobID.String(), "rolling_update_resumed", nil)

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), "resumed")
	}

	return c.JSON(fiber.Map{"status": "running"})
}

// ListNodes returns nodes for a rolling update job.
func (h *RollingUpdateHandler) ListNodes(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "rolling_update"); err != nil {
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
func (h *RollingUpdateHandler) ConfirmUpgrade(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
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
	h.auditLog(c, clusterID,jobID.String(), "node_upgrade_confirmed", details)

	return c.JSON(fiber.Map{"status": "confirmed"})
}

// SkipNode skips a pending node in a rolling update job.
func (h *RollingUpdateHandler) SkipNode(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
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
	h.auditLog(c, clusterID,jobID.String(), "node_skipped", details)

	return c.JSON(fiber.Map{"status": "skipped"})
}

// PreviewPackages returns pending apt packages for a specific node.
func (h *RollingUpdateHandler) PreviewPackages(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
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

// PreflightHA analyzes HA/DRS constraints for a proposed set of nodes.
func (h *RollingUpdateHandler) PreflightHA(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "rolling_update"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	var req struct {
		Nodes []string `json:"nodes"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.Nodes) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "nodes array is required")
	}

	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to connect to cluster")
	}

	report, err := rolling.AnalyzeHAConstraints(c.Context(), client, h.queries, clusterID, req.Nodes)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to analyze HA constraints")
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
func (h *RollingUpdateHandler) GetSSHCredentials(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ssh_credentials"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
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
		CreatedAt: creds.CreatedAt.Format(time.RFC3339),
		UpdatedAt: creds.UpdatedAt.Format(time.RFC3339),
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
func (h *RollingUpdateHandler) UpsertSSHCredentials(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ssh_credentials"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	var req upsertSSHCredentialRequest
	if err := c.BodyParser(&req); err != nil {
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
		ClusterID:         clusterID,
		Username:          req.Username,
		Port:              req.Port,
		AuthType:          req.AuthType,
		EncryptedPassword: encPassword,
		EncryptedPrivateKey: encKey,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to save SSH credentials")
	}

	h.auditLog(c, clusterID, clusterID.String(), "ssh_credentials_updated", nil)

	return c.JSON(sshCredentialResponse{
		ClusterID: creds.ClusterID.String(),
		Username:  creds.Username,
		Port:      creds.Port,
		AuthType:  creds.AuthType,
		HasKey:    creds.EncryptedPrivateKey != "",
		CreatedAt: creds.CreatedAt.Format(time.RFC3339),
		UpdatedAt: creds.UpdatedAt.Format(time.RFC3339),
	})
}

// DeleteSSHCredentials removes SSH credentials for a cluster.
func (h *RollingUpdateHandler) DeleteSSHCredentials(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ssh_credentials"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	if err := h.queries.DeleteClusterSSHCredentials(c.Context(), clusterID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete SSH credentials")
	}

	h.auditLog(c, clusterID, clusterID.String(), "ssh_credentials_deleted", nil)

	return c.JSON(fiber.Map{"status": "deleted"})
}

// TestSSHConnection tests SSH connectivity to a specific node.
func (h *RollingUpdateHandler) TestSSHConnection(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ssh_credentials"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	var req struct {
		NodeName string `json:"node_name"`
	}
	if err := c.BodyParser(&req); err != nil || req.NodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}

	creds, err := h.queries.GetClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "SSH credentials not configured for this cluster")
	}

	var password, privateKey string
	if creds.EncryptedPassword != "" {
		password, err = crypto.Decrypt(creds.EncryptedPassword, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt credentials")
		}
	}
	if creds.EncryptedPrivateKey != "" {
		privateKey, err = crypto.Decrypt(creds.EncryptedPrivateKey, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt credentials")
		}
	}

	// Resolve node name to IP from stored address.
	sshHost := req.NodeName
	nodeAddr, addrErr := h.queries.GetNodeAddressByName(c.Context(), db.GetNodeAddressByNameParams{
		ClusterID: clusterID,
		Name:      req.NodeName,
	})
	if addrErr == nil && nodeAddr != "" {
		sshHost = nodeAddr
	}

	sshCfg := sshpkg.Config{
		Host:       sshHost,
		Port:       int(creds.Port),
		Username:   creds.Username,
		Password:   password,
		PrivateKey: privateKey,
	}

	if err := sshpkg.TestConnection(c.Context(), sshCfg); err != nil {
		return c.JSON(fiber.Map{
			"success": false,
			"message": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "SSH connection successful",
	})
}
