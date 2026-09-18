package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/rolling"
	"github.com/bigjakk/nexara/internal/safeconv"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// RollingUpdateHandler handles rolling update API endpoints.
//
// All 19 routes are declared in internal/api/registry_rolling_update.go, which
// states their permission — view/manage:rolling_update for the jobs,
// manage:ssh_credentials for the credential and host-key routes — and their
// parameters. Nothing below re-checks either.
//
// What the declaration CANNOT check, and what every route carrying an :id keeps
// in the handler, is that the job or host-key row named in the path belongs to
// the cluster the permission was resolved against. See jobInCluster.
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
	DrainGuests       bool            `json:"drain_guests"`
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
	// RebootRequired marks a node whose upgrade landed but which still owes a
	// reboot, because guests were running on it. Only ever set on an in-place
	// job; a drained job reboots or fails.
	RebootRequired bool   `json:"reboot_required"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
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
		DrainGuests:       j.DrainGuests,
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
		RebootRequired:     n.RebootRequired,
		CreatedAt:          n.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:          n.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// rollingIDs reads the two path parameters every route in this domain that
// names ONE row carries: the cluster the permission was resolved against, and
// the row itself.
//
// The second is a job id on the eight job routes and a pinned-host-key id on the
// delete; both are spelled :id, and neither handler needs to tell them apart
// here because what they do with the value differs entirely.
func rollingIDs(p *apischema.Params) (clusterID, rowID uuid.UUID, err error) {
	clusterID, err = parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	rowID, err = parseParamUUID(p.String("id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return clusterID, rowID, nil
}

// jobInCluster loads a rolling update job and refuses one that belongs to a
// different cluster.
//
// This is the check that makes the declared permission mean what it says. Every
// route here is gated cluster-scoped on the cluster in the PATH, and
// GetRollingUpdateJob selects on the id ALONE — so without this a caller holding
// manage:rolling_update on cluster A could name a job belonging to cluster B and
// start, pause, resume, confirm or skip it. CancelJob was the only one that
// checked; the other six now do.
//
// It cannot be hoisted into middleware: the cluster a job belongs to is a column
// on the row, and middleware runs before any query.
//
// Both outcomes answer the SAME 404 with the same message, deliberately.
// Distinguishing "no such job" from "that job is another cluster's" would hand a
// caller an existence oracle over every job on the install.
func (h *RollingUpdateHandler) jobInCluster(c fiber.Ctx, jobID, clusterID uuid.UUID) (db.RollingUpdateJob, error) {
	job, err := h.queries.GetRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return db.RollingUpdateJob{}, fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}
	if job.ClusterID != clusterID {
		return db.RollingUpdateJob{}, fiber.NewError(fiber.StatusNotFound, "Rolling update job not found")
	}
	return job, nil
}

// ListJobs returns rolling update jobs for a cluster.
func (h *RollingUpdateHandler) ListJobs(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	jobs, err := h.queries.ListRollingUpdateJobs(c.Context(), db.ListRollingUpdateJobsParams{
		ClusterID: clusterID,
		Limit:     safeconv.Int32(int(p.Int("limit"))),
		Offset:    safeconv.Int32(int(p.Int("offset"))),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list rolling update jobs")
	}

	result := make([]rollingUpdateJobResponse, len(jobs))
	for i, j := range jobs {
		result[i] = toJobResponse(j)
	}

	return RespondItems(c, result)
}

// CreateJob creates a new rolling update job.
func (h *RollingUpdateHandler) CreateJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// The node list's own bounds — at least one, at most 64, each one a valid
	// node name — are the declaration's now. What stays here is the CROSS-FIELD
	// clamp: parallelism above the number of nodes named is lowered rather than
	// refused, and apischema has no way to say "at most the length of that other
	// parameter".
	nodes := p.Strings("nodes")
	parallelism := safeconv.Int32(int(p.Int("parallelism")))
	if parallelism > safeconv.Int32(len(nodes)) {
		parallelism = safeconv.Int32(len(nodes))
	}

	// Prevent concurrent jobs for the same cluster.
	hasRunning, err := h.queries.HasRunningJobForCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for running jobs")
	}
	if hasRunning {
		return fiber.NewError(fiber.StatusConflict, "A rolling update job is already active for this cluster")
	}

	// A prior job whose cleanup is still pending holds cluster state (paused
	// native CRS, disabled HA rules, stopped passthrough guests). Starting a
	// new job under it is unsafe: the deferred cleanup sweep would restore
	// that state mid-drain — e.g. re-enable the CRS auto-rebalancer while
	// this job is draining a node. The cluster must be reachable to run a
	// job anyway, so try the release synchronously right now and only refuse
	// if something still couldn't be released.
	pendingJobs, err := h.queries.ListCleanupPendingJobsForCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for pending cleanup")
	}
	for _, pj := range pendingJobs {
		if released, _ := h.orchestrator.ReleaseJobState(c.Context(), pj.ID); !released {
			// Deliberately does not name connectivity as the cause. Re-enabling
			// an HA rule puts it back under PVE's feasibility assert, which it
			// escaped while disabled, so a rule that conflicts with one created
			// during the update fails here for good — retrying and waiting for
			// the cluster to come back would never clear it.
			//
			// The remedy is named because this is the only place an operator
			// meets the permanent case: repairing or deleting the conflicting
			// rule lets the next attempt through, and deleting the stuck rule
			// itself makes reenableHARules drop it from the record.
			//
			// It stops short of pointing at the audit log, which records only
			// three of the five release paths — an unbuildable client, a CRS
			// restore and the HA rules; the Nexara-DRS re-enable and the
			// passthrough-guest restarts log at warn level and audit nothing.
			// Sending the operator to a log that may be silent about their
			// actual failure is worse than naming the likely cause.
			return fiber.NewError(fiber.StatusConflict,
				"A previous rolling update still holds cluster state (paused CRS auto-rebalance, disabled HA rules, or stopped guests) and it could not be released. Cleanup keeps retrying in the background, so a cluster that was briefly unreachable clears on its own. If it never clears, an HA rule is likely refusing to re-enable because another rule now conflicts with it — repair or delete that rule in Proxmox, or delete the stuck rule itself, and try again.")
		}
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	rebootAfter := p.Bool("reboot_after_update")
	autoRestore := p.Bool("auto_restore_guests")
	drainGuests := p.Bool("drain_guests")

	// A non-nil slice, because package_excludes is a NOT NULL jsonb column and
	// an omitted list must reach it as [] rather than as null.
	packageExcludes := p.Strings("package_excludes")
	if packageExcludes == nil {
		packageExcludes = []string{}
	}

	// The EMPTY string is what a caller sends for "unspecified" and has always
	// meant warn; the enum owns the rest of the vocabulary now, so there is no
	// third value left to refuse here.
	haPolicy := p.String("ha_policy")
	if haPolicy == "" {
		haPolicy = "warn"
	}

	// Run pre-flight checks: HA constraints + capacity analysis.
	//
	// Both are questions about migration — whether HA/DRS rules would block
	// guests leaving a node, and whether the remaining nodes could absorb them.
	// An in-place job migrates nothing, so neither has an answer worth acting
	// on, and running them anyway does active harm: AnalyzeCapacity on a
	// single-node cluster reports that the (nonexistent) remaining nodes cannot
	// absorb the workload, which under ha_policy "strict" is a 409 refusing the
	// one job shape a single-node cluster can actually run.
	var haWarningsJSON json.RawMessage
	client, clientErr := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if clientErr == nil && drainGuests {
		// HA/DRS constraint check.
		report, haErr := rolling.AnalyzeHAConstraints(c.Context(), client, h.queries, clusterID, nodes)
		// Capacity feasibility check — verifies remaining nodes can absorb
		// the workload when each batch of nodes is drained.
		capConflicts, capHasErrors, capErr := rolling.AnalyzeCapacity(c.Context(), client, nodes, parallelism)
		allConflicts, preflightHasErrors := foldPreflight(report, haErr, capConflicts, capHasErrors, capErr)

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

	autoUpgrade := p.Bool("auto_upgrade")
	if autoUpgrade {
		// Verify SSH credentials exist for this cluster.
		hasCreds, err := h.queries.HasClusterSSHCredentials(c.Context(), clusterID)
		if err != nil || !hasCreds {
			return fiber.NewError(fiber.StatusBadRequest, "Auto upgrade requires SSH credentials to be configured for this cluster")
		}
	}

	var notifyChannelID pgtype.UUID
	// The EMPTY string is the sentinel for "no channel"; the declaration's
	// empty-or-uuid pattern is what keeps it expressible, because every
	// registered format rejects "".
	if channel := p.String("notify_channel_id"); channel != "" {
		parsed, parseErr := parseParamUUID(channel)
		if parseErr != nil {
			return parseErr
		}
		notifyChannelID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	job, err := h.queries.InsertRollingUpdateJob(c.Context(), db.InsertRollingUpdateJobParams{
		ClusterID:         clusterID,
		Parallelism:       parallelism,
		RebootAfterUpdate: rebootAfter,
		AutoRestoreGuests: autoRestore,
		PackageExcludes:   packageExcludes,
		HaPolicy:          haPolicy,
		HaWarnings:        haWarningsJSON,
		AutoUpgrade:       autoUpgrade,
		DrainGuests:       drainGuests,
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

	for i, nodeName := range nodes {
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
func (h *RollingUpdateHandler) GetJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	job, err := h.jobInCluster(c, jobID, clusterID)
	if err != nil {
		return err
	}

	return c.JSON(toJobResponse(job))
}

// StartJob starts a pending rolling update job.
func (h *RollingUpdateHandler) StartJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	job, err := h.jobInCluster(c, jobID, clusterID)
	if err != nil {
		return err
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
func (h *RollingUpdateHandler) CancelJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	if _, err := h.jobInCluster(c, jobID, clusterID); err != nil {
		return err
	}

	rows, err := h.queries.CancelRollingUpdateJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to cancel job")
	}
	if rows == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "Job is not active (already finished or cancelled)")
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
func (h *RollingUpdateHandler) PauseJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	if _, err := h.jobInCluster(c, jobID, clusterID); err != nil {
		return err
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
func (h *RollingUpdateHandler) ResumeJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	if _, err := h.jobInCluster(c, jobID, clusterID); err != nil {
		return err
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
func (h *RollingUpdateHandler) ListNodes(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	if _, err := h.jobInCluster(c, jobID, clusterID); err != nil {
		return err
	}

	nodes, err := h.queries.ListRollingUpdateNodes(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}

	result := make([]rollingUpdateNodeResponse, len(nodes))
	for i, n := range nodes {
		result[i] = toRollingNodeResponse(n)
	}

	return RespondItems(c, result)
}

// ConfirmUpgrade confirms that manual upgrade is done on a node.
func (h *RollingUpdateHandler) ConfirmUpgrade(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	nodeID, err := parseParamUUID(p.String("node_id"))
	if err != nil {
		return err
	}

	job, err := h.jobInCluster(c, jobID, clusterID)
	if err != nil {
		return err
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
func (h *RollingUpdateHandler) SkipNode(c fiber.Ctx, p *apischema.Params) error {
	clusterID, jobID, err := rollingIDs(p)
	if err != nil {
		return err
	}

	nodeID, err := parseParamUUID(p.String("node_id"))
	if err != nil {
		return err
	}

	// The job is loaded for its CLUSTER rather than for its contents: without
	// it, "the node belongs to this job" is satisfied by any job on the
	// install, including one in a cluster the caller holds nothing on.
	if _, err := h.jobInCluster(c, jobID, clusterID); err != nil {
		return err
	}

	node, err := h.queries.GetRollingUpdateNode(c.Context(), nodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Node not found")
	}

	if node.JobID != jobID {
		return fiber.NewError(fiber.StatusBadRequest, "Node does not belong to this job")
	}

	rows, err := h.queries.SkipRollingUpdateNode(c.Context(), db.SkipRollingUpdateNodeParams{
		ID:         nodeID,
		SkipReason: "manually skipped by user",
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to skip node")
	}
	if rows == 0 {
		// The guarded UPDATE matched nothing — the node already started (it
		// may hold drained guests and disabled HA rules) or already finished.
		// Re-read for the message: the step from the pre-check read may be
		// stale (the orchestrator can start the node between read and update).
		step := node.Step
		if fresh, ferr := h.queries.GetRollingUpdateNode(c.Context(), nodeID); ferr == nil {
			step = fresh.Step
		}
		return fiber.NewError(fiber.StatusBadRequest,
			fmt.Sprintf("Node cannot be skipped in step %q — only nodes that have not started can be skipped", step))
	}

	details, _ := json.Marshal(map[string]string{"node": node.NodeName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "rolling_update", jobID.String(), "node_skipped", details)

	return c.JSON(fiber.Map{"status": "skipped"})
}

// PreviewPackages returns pending apt packages for a specific node.
func (h *RollingUpdateHandler) PreviewPackages(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}

	updates, err := client.GetNodeAptUpdates(c.Context(), p.String("node"))
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, updates)
}

// PreflightHA analyzes HA/DRS constraints and capacity feasibility for a proposed set of nodes.
func (h *RollingUpdateHandler) PreflightHA(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	nodes := p.Strings("nodes")
	parallelism := safeconv.Int32(int(p.Int("parallelism")))

	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to connect to cluster")
	}

	report, err := rolling.AnalyzeHAConstraints(c.Context(), client, h.queries, clusterID, nodes)
	if err != nil {
		// The status is what improves: a blanket 500 said "Nexara is broken"
		// for what is usually an unreachable or non-quorate cluster, and this
		// answers 502/403/404 accordingly. The message is carried for the API
		// and the logs — the wizard discards the body and renders a fixed
		// string (usePreflightHA has no onError), so do not read this as
		// something the operator sees.
		return mapProxmoxError(fmt.Errorf("analyze HA constraints: %w", err))
	}
	if report == nil {
		report = &rolling.HAPreFlightReport{Conflicts: []rolling.HAConflict{}}
	}

	capConflicts, capHasErrors, capErr := rolling.AnalyzeCapacity(c.Context(), client, nodes, parallelism)
	// haErr is nil here on purpose: an HA failure already returned above, so
	// only the capacity half can still be unavailable at this point.
	conflicts, hasErrors := foldPreflight(report, nil, capConflicts, capHasErrors, capErr)
	report.Conflicts, report.HasErrors = conflicts, hasErrors

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
func (h *RollingUpdateHandler) GetSSHCredentials(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
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
		CreatedAt: creds.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: creds.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// UpsertSSHCredentials creates or updates SSH credentials for a cluster.
func (h *RollingUpdateHandler) UpsertSSHCredentials(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// A DEFAULT only fills an ABSENT value, and the form sends "" when the
	// username field is cleared — so this normalisation stays here even though
	// the declaration states root as the default.
	username := p.String("username")
	if username == "" {
		username = "root"
	}
	// Likewise the port: the form's number input sends 0 for a cleared field,
	// which has always meant 22. The UPPER bound is the declaration's now, and
	// it refuses rather than substituting, because nobody clears a field to
	// 70000 and answering "we used 22 instead" without saying so is the silent
	// substitution this migration removes elsewhere.
	port := safeconv.Int32(int(p.Int("port")))
	if port <= 0 {
		port = 22
	}

	// WHICH secret is required depends on auth_type, and apischema cannot state
	// that: Requires names a companion a parameter ALWAYS needs, not one it
	// needs only when a sibling holds a particular value. The vocabulary of
	// auth_type itself IS the declaration's, as an enum.
	authType := p.String("auth_type")
	password := p.String("password")
	privateKey := p.String("private_key")
	if authType == "password" && password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Password is required for password auth")
	}
	if authType == "key" && privateKey == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Private key is required for key auth")
	}

	var encPassword, encKey string
	if password != "" {
		encPassword, err = crypto.Encrypt(password, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt credentials")
		}
	}
	if privateKey != "" {
		encKey, err = crypto.Encrypt(privateKey, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt credentials")
		}
	}

	creds, err := h.queries.UpsertClusterSSHCredentials(c.Context(), db.UpsertClusterSSHCredentialsParams{
		ClusterID:           clusterID,
		Username:            username,
		Port:                port,
		AuthType:            authType,
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
func (h *RollingUpdateHandler) DeleteSSHCredentials(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
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
func (h *RollingUpdateHandler) TestSSHConnection(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// The declaration states the node-name format, which is the tightest of the
	// four spellings of this check in the tree. validateNodeName STAYS: it
	// guards the value at the point where it is about to be resolved to an
	// address and dialled, and an exported validator with a single caller is
	// exactly the opt-in-guard shape this codebase has been bitten by.
	nodeName := p.String("node_name")
	if err := validateNodeName(nodeName); err != nil {
		return err
	}

	creds, err := h.queries.GetClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "SSH credentials not configured for this cluster")
	}

	sshHost, hostErr := h.resolveNodeAddress(c, clusterID, nodeName)
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
func (h *RollingUpdateHandler) ListSSHKnownHosts(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
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
	return RespondItems(c, out)
}

// PinSSHHostKey runs a fresh host-key scan and stores the result, but only
// after verifying the freshly-scanned fingerprint matches the one the user
// confirmed in the UI. This closes the TOCTOU window between the test
// response and the pin call.
func (h *RollingUpdateHandler) PinSSHHostKey(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// Same reasoning as TestSSHConnection: the declaration states the format,
	// and validateNodeName stays at the point of use. The fingerprint\'s own
	// "required, at most 128 characters" rule IS the declaration\'s now — it is
	// a bound on a string, which a schema can state exactly — and what remains
	// here is the check no schema could make: that a fresh scan agrees with it.
	nodeName := p.String("node_name")
	if err := validateNodeName(nodeName); err != nil {
		return err
	}
	expectedFingerprint := p.String("expected_fingerprint")

	creds, err := h.queries.GetClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "SSH credentials not configured for this cluster")
	}

	sshHost, hostErr := h.resolveNodeAddress(c, clusterID, nodeName)
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
	if scannedFP != expectedFingerprint {
		return fiber.NewError(fiber.StatusConflict,
			"Host key changed between confirmation and pin (expected "+expectedFingerprint+
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
		"node_name":   nodeName,
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
func (h *RollingUpdateHandler) DeleteSSHKnownHost(c fiber.Ctx, p *apischema.Params) error {
	// The delete is already scoped to the cluster in the WHERE clause — see
	// DeleteSSHKnownHostByID — so an entry belonging to another cluster matches
	// nothing. That is why this route needs no jobInCluster equivalent.
	clusterID, id, err := rollingIDs(p)
	if err != nil {
		return err
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

// preflightUnavailable is what a pre-flight check that could not run records.
//
// It is an error-severity conflict rather than a silent omission because the
// strict policy exists to refuse a job the pre-flight cannot vouch for, and a
// check that failed has vouched for nothing. Recording it also makes the
// failure visible under the permissive policies, where the job proceeds.
func preflightUnavailable(source string, err error) rolling.HAConflict {
	return rolling.HAConflict{
		Source:   source,
		Type:     "preflight_unavailable",
		Severity: "error",
		Message:  fmt.Sprintf("%s pre-flight could not run: %v", source, err),
	}
}

// foldPreflight combines the two pre-flight checks into the conflict list and
// the single boolean the strict gate reads.
//
// Extracted because both call sites got this wrong in the same way, twice. The
// original was `if err == nil && report != nil`, so a check that failed
// contributed nothing and left hasErrors false — the gate answering "all clear"
// precisely when it had learned nothing. The first fix moved the swallow from
// the analyzer into this caller without removing it. A pure function is what
// lets a test say which of those is in force.
func foldPreflight(
	report *rolling.HAPreFlightReport,
	haErr error,
	capConflicts []rolling.HAConflict,
	capHasErrors bool,
	capErr error,
) (conflicts []rolling.HAConflict, hasErrors bool) {
	// Non-nil from the start, never a bare `var`. PreflightHA assigns the
	// result straight onto report.Conflicts and serialises it, and the wizard
	// reads `preflightReport.conflicts.length` with no null guard — so a nil
	// here is a crash on the clean-cluster path, which is the common answer.
	conflicts = []rolling.HAConflict{}
	switch {
	case haErr != nil:
		conflicts = append(conflicts, preflightUnavailable("HA constraint", haErr))
		hasErrors = true
	case report != nil:
		conflicts = append(conflicts, report.Conflicts...)
		hasErrors = report.HasErrors
	}

	switch {
	case capErr != nil:
		conflicts = append(conflicts, preflightUnavailable("Capacity", capErr))
		hasErrors = true
	case len(capConflicts) > 0:
		conflicts = append(conflicts, capConflicts...)
	}
	// Unconditional, deliberately. Reading the gate boolean only when the
	// conflict list happens to be non-empty is the same shape as the bug this
	// function exists to remove: a check's verdict discarded because of an
	// unrelated emptiness. AnalyzeCapacity always sets them together today;
	// this does not depend on that staying true.
	hasErrors = hasErrors || capHasErrors
	return conflicts, hasErrors
}
