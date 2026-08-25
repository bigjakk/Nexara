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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// BackupHandler handles PBS backup management endpoints.
type BackupHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewBackupHandler creates a new backup handler.
func NewBackupHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *BackupHandler {
	return &BackupHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

// createPBSClient creates a PBS client for the given server ID. Routes
// through the per-server Proxmox client cache when one is available
// (set on the request via api/middleware), falling back to per-call
// construction otherwise.
func (h *BackupHandler) createPBSClient(c fiber.Ctx, pbsServerID uuid.UUID) (*proxmox.PBSClient, error) {
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		client, err := cache.GetPBS(c.Context(), pbsServerID)
		if err == nil {
			return client, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create PBS client")
	}

	server, err := h.queries.GetPBSServer(c.Context(), pbsServerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	tokenSecret, err := crypto.Decrypt(server.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt PBS credentials")
	}

	client, err := proxmox.NewPBSClient(proxmox.ClientConfig{
		BaseURL:        server.ApiUrl,
		TokenID:        server.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: server.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create PBS client")
	}

	return client, nil
}

func parsePBSID(c fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("pbs_id"))
	if err != nil {
		return uuid.Nil, fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}
	return id, nil
}

// requirePBSPerm gates an operation on a PBS server. If the PBS server is
// linked to a cluster, the caller must have (action, "backup") on that cluster
// — so a user with cluster-scoped backup rights cannot drive a PBS bound to a
// different cluster. Standalone PBS servers (no cluster_id) fall back to the
// global requirePerm.
//
// It returns the server's cluster so the caller can attribute its audit row to
// the same cluster it was just authorized against. That value is why this
// returns anything at all: the authorization here is the one place that already
// holds the binding, and every PBS datastore mutation below used to audit with
// a NULL cluster instead. A NULL cluster_id marks a GLOBAL audit entry, which
// the scoped audit reads hand only to holders of global view:audit — so a
// cluster-scoped operator could trigger a GC or delete a snapshot and then not
// find their own action in the audit log. Invalid is correct only for a
// standalone PBS, which genuinely belongs to no cluster.
func (h *BackupHandler) requirePBSPerm(c fiber.Ctx, pbsID uuid.UUID, action string) (pgtype.UUID, error) {
	server, err := h.queries.GetPBSServer(c.Context(), pbsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return pgtype.UUID{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}
	if server.ClusterID.Valid {
		return server.ClusterID, requireClusterPerm(c, action, "backup", uuid.UUID(server.ClusterID.Bytes))
	}
	return pgtype.UUID{}, requirePerm(c, action, "backup")
}

// --- Live proxy endpoints ---

// ListDatastores handles GET /api/v1/pbs-servers/:pbs_id/datastores
func (h *BackupHandler) ListDatastores(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	stores, err := client.GetDatastores(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, stores)
}

// GetDatastoreStatus handles GET /api/v1/pbs-servers/:pbs_id/datastores/status
func (h *BackupHandler) GetDatastoreStatus(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	status, err := client.GetDatastoreStatus(c.Context())
	if err != nil {
		slog.Warn("PBS GetDatastoreStatus failed", "pbs_id", pbsID, "error", err)
		return mapProxmoxError(err)
	}

	return RespondItems(c, status)
}

// TriggerGC handles POST /api/v1/pbs-servers/:pbs_id/datastores/:store/gc
func (h *BackupHandler) TriggerGC(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "manage")
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	upid, err := client.TriggerGC(c.Context(), store)
	if err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, cluster, "backup", store, "gc_triggered", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "gc_triggered")

	return c.JSON(fiber.Map{"upid": upid})
}

type deleteSnapshotRequest struct {
	BackupType string `json:"backup_type"`
	BackupID   string `json:"backup_id"`
	BackupTime int64  `json:"backup_time"`
}

// DeleteSnapshot handles DELETE /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots
func (h *BackupHandler) DeleteSnapshot(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "delete")
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req deleteSnapshotRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.BackupType == "" || req.BackupID == "" || req.BackupTime == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "backup_type, backup_id, and backup_time are required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	if err := client.DeleteSnapshot(c.Context(), store, req.BackupType, req.BackupID, req.BackupTime); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]interface{}{
		"store":       store,
		"backup_type": req.BackupType,
		"backup_id":   req.BackupID,
		"backup_time": req.BackupTime,
	})
	AuditLog(c, h.queries, h.eventPub, cluster, "backup", store+"/"+req.BackupType+"/"+req.BackupID, "snapshot_deleted", details)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "snapshot_deleted")

	return c.JSON(fiber.Map{"status": "deleted"})
}

type protectSnapshotRequest struct {
	BackupType string `json:"backup_type"`
	BackupID   string `json:"backup_id"`
	BackupTime int64  `json:"backup_time"`
	Protected  bool   `json:"protected"`
}

// ProtectSnapshot handles PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/protect
func (h *BackupHandler) ProtectSnapshot(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "manage")
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req protectSnapshotRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.BackupType == "" || req.BackupID == "" || req.BackupTime == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "backup_type, backup_id, and backup_time are required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	if err := client.ProtectSnapshot(c.Context(), store, req.BackupType, req.BackupID, req.BackupTime, req.Protected); err != nil {
		return mapProxmoxError(err)
	}

	action := "snapshot_protected"
	if !req.Protected {
		action = "snapshot_unprotected"
	}
	details, _ := json.Marshal(map[string]interface{}{
		"store":       store,
		"backup_type": req.BackupType,
		"backup_id":   req.BackupID,
		"backup_time": req.BackupTime,
		"protected":   req.Protected,
	})
	AuditLog(c, h.queries, h.eventPub, cluster, "backup", store+"/"+req.BackupType+"/"+req.BackupID, action, details)

	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, action)

	return c.JSON(fiber.Map{"status": "ok"})
}

type updateSnapshotNotesRequest struct {
	BackupType string `json:"backup_type"`
	BackupID   string `json:"backup_id"`
	BackupTime int64  `json:"backup_time"`
	Comment    string `json:"comment"`
}

// UpdateSnapshotNotes handles PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/notes
func (h *BackupHandler) UpdateSnapshotNotes(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "manage")
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req updateSnapshotNotesRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.BackupType == "" || req.BackupID == "" || req.BackupTime == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "backup_type, backup_id, and backup_time are required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	if err := client.UpdateSnapshotNotes(c.Context(), store, req.BackupType, req.BackupID, req.BackupTime, req.Comment); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, cluster, "backup", store+"/"+req.BackupType+"/"+req.BackupID, "snapshot_notes_updated", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}

// GetTaskLog handles GET /api/v1/pbs-servers/:pbs_id/tasks/:upid/log
func (h *BackupHandler) GetTaskLog(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	upid := c.Params("upid")
	if upid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "UPID is required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	entries, err := client.GetTaskLog(c.Context(), upid)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, entries)
}

type pruneDatastoreRequest struct {
	BackupType  string `json:"backup_type"`
	BackupID    string `json:"backup_id"`
	DryRun      bool   `json:"dry_run"`
	KeepLast    int    `json:"keep_last"`
	KeepDaily   int    `json:"keep_daily"`
	KeepWeekly  int    `json:"keep_weekly"`
	KeepMonthly int    `json:"keep_monthly"`
	KeepYearly  int    `json:"keep_yearly"`
}

// PruneDatastore handles POST /api/v1/pbs-servers/:pbs_id/datastores/:store/prune
func (h *BackupHandler) PruneDatastore(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "manage")
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req pruneDatastoreRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	results, err := client.PruneDatastore(c.Context(), store, proxmox.PBSPruneParams{
		BackupType:  req.BackupType,
		BackupID:    req.BackupID,
		DryRun:      req.DryRun,
		KeepLast:    req.KeepLast,
		KeepDaily:   req.KeepDaily,
		KeepWeekly:  req.KeepWeekly,
		KeepMonthly: req.KeepMonthly,
		KeepYearly:  req.KeepYearly,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	if !req.DryRun {
		details, _ := json.Marshal(map[string]interface{}{
			"store":        store,
			"backup_type":  req.BackupType,
			"backup_id":    req.BackupID,
			"keep_last":    req.KeepLast,
			"keep_daily":   req.KeepDaily,
			"keep_weekly":  req.KeepWeekly,
			"keep_monthly": req.KeepMonthly,
			"keep_yearly":  req.KeepYearly,
		})
		AuditLog(c, h.queries, h.eventPub, cluster, "backup", store, "datastore_pruned", details)
		h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "datastore_pruned")
	}

	return RespondItems(c, results)
}

// GetDatastoreConfig handles GET /api/v1/pbs-servers/:pbs_id/datastores/:store/config
func (h *BackupHandler) GetDatastoreConfig(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	config, err := client.GetDatastoreConfig(c.Context(), store)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(config)
}

// RunSyncJob handles POST /api/v1/pbs-servers/:pbs_id/sync-jobs/:job_id/run
func (h *BackupHandler) RunSyncJob(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "manage")
	if err != nil {
		return err
	}

	jobID := c.Params("job_id")
	if jobID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Job ID is required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	upid, err := client.RunSyncJob(c.Context(), jobID)
	if err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, cluster, "backup", jobID, "sync_job_triggered", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "sync_job_triggered")

	return c.JSON(fiber.Map{"upid": upid})
}

// RunVerifyJob handles POST /api/v1/pbs-servers/:pbs_id/verify-jobs/:job_id/run
func (h *BackupHandler) RunVerifyJob(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	cluster, err := h.requirePBSPerm(c, pbsID, "manage")
	if err != nil {
		return err
	}

	jobID := c.Params("job_id")
	if jobID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Job ID is required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	upid, err := client.RunVerifyJob(c.Context(), jobID)
	if err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, cluster, "backup", jobID, "verify_job_triggered", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "verify_job_triggered")

	return c.JSON(fiber.Map{"upid": upid})
}

// ListTasks handles GET /api/v1/pbs-servers/:pbs_id/tasks
func (h *BackupHandler) ListTasks(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, pErr := strconv.Atoi(l); pErr == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	tasks, err := client.GetTasks(c.Context(), limit)
	if err != nil {
		slog.Warn("PBS GetTasks failed", "pbs_id", pbsID, "error", err)
		return mapProxmoxError(err)
	}

	return RespondItems(c, tasks)
}

// GetTaskStatus handles GET /api/v1/pbs-servers/:pbs_id/tasks/:upid
func (h *BackupHandler) GetTaskStatus(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	upid := c.Params("upid")
	if upid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "UPID is required")
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	status, err := client.GetTaskStatus(c.Context(), upid)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(status)
}

// --- DB-backed endpoints ---

// ListSnapshots handles GET /api/v1/pbs-servers/:pbs_id/snapshots
func (h *BackupHandler) ListSnapshots(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	datastore := c.Query("datastore")
	if datastore != "" {
		snaps, err := h.queries.ListPBSSnapshotsByDatastore(c.Context(), db.ListPBSSnapshotsByDatastoreParams{
			PbsServerID: pbsID,
			Datastore:   datastore,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list snapshots")
		}
		return RespondItems(c, snaps)
	}

	snaps, err := h.queries.ListPBSSnapshotsByServer(c.Context(), pbsID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list snapshots")
	}

	return RespondItems(c, snaps)
}

// ListSyncJobs handles GET /api/v1/pbs-servers/:pbs_id/sync-jobs
func (h *BackupHandler) ListSyncJobs(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	jobs, err := h.queries.ListPBSSyncJobsByServer(c.Context(), pbsID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list sync jobs")
	}

	return RespondItems(c, jobs)
}

// ListPruneJobs handles GET /api/v1/pbs-servers/:pbs_id/prune-jobs
//
// Read live from PBS rather than from a collected table, like the datastore
// config beside it: prune jobs have no local mirror the way sync and verify
// jobs do, and the one consumer is a per-datastore panel.
//
// ?store= narrows to one datastore, applied here rather than passed upstream
// (see proxmox.GetPruneJobs for why). It is a convenience for API callers, not
// a boundary: anyone past the permission gate can omit it and get every job,
// exactly as ListSyncJobs already returns.
func (h *BackupHandler) ListPruneJobs(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	jobs, err := client.GetPruneJobs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, filterPruneJobsByStore(jobs, c.Query("store")))
}

// filterPruneJobsByStore narrows a job list to one datastore. An empty store
// means "no filter", so a caller that omits the parameter gets everything.
// Always returns a non-nil slice: RespondItems renders nil as [], but an
// explicit empty slice keeps that from depending on the envelope helper.
func filterPruneJobsByStore(jobs []proxmox.PBSPruneJob, store string) []proxmox.PBSPruneJob {
	if store == "" {
		return jobs
	}
	filtered := make([]proxmox.PBSPruneJob, 0, len(jobs))
	for _, j := range jobs {
		if j.Store == store {
			filtered = append(filtered, j)
		}
	}
	return filtered
}

// ListVerifyJobs handles GET /api/v1/pbs-servers/:pbs_id/verify-jobs
func (h *BackupHandler) ListVerifyJobs(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	jobs, err := h.queries.ListPBSVerifyJobsByServer(c.Context(), pbsID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list verify jobs")
	}

	return RespondItems(c, jobs)
}

// GetDatastoreMetrics handles GET /api/v1/pbs-servers/:pbs_id/metrics
func (h *BackupHandler) GetDatastoreMetrics(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	timeframe := c.Query("timeframe", "latest")

	if timeframe == "latest" {
		metrics, err := h.queries.GetLatestPBSDatastoreMetrics(c.Context(), pbsID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to get datastore metrics")
		}
		return RespondItems(c, metrics)
	}

	now := time.Now()
	var start time.Time
	switch timeframe {
	case "1h":
		start = now.Add(-1 * time.Hour)
	case "6h":
		start = now.Add(-6 * time.Hour)
	case "24h":
		start = now.Add(-24 * time.Hour)
	case "7d":
		start = now.Add(-7 * 24 * time.Hour)
	default:
		start = now.Add(-1 * time.Hour)
	}

	metrics, err := h.queries.GetPBSDatastoreMetricsHistory(c.Context(), db.GetPBSDatastoreMetricsHistoryParams{
		PbsServerID: pbsID,
		Time:        start,
		Time_2:      now,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get datastore metrics history")
	}

	return RespondItems(c, metrics)
}

// GetDatastoreRRD handles GET /api/v1/pbs-servers/:pbs_id/datastores/:store/rrd
// Live proxy to PBS RRD — returns IO performance metrics (transfer rate, IOPS).
func (h *BackupHandler) GetDatastoreRRD(c fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}
	if _, err := h.requirePBSPerm(c, pbsID, "view"); err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	timeframe := c.Query("timeframe", "hour")
	cf := c.Query("cf", "AVERAGE")

	client, err := h.createPBSClient(c, pbsID)
	if err != nil {
		return err
	}

	entries, err := client.GetDatastoreRRD(c.Context(), store, timeframe, cf)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, entries)
}

// ListSnapshotsByBackupID handles GET /api/v1/pbs-snapshots?backup_id=XXX
// Returns all PBS snapshots across all servers matching a given backup_id (VMID),
// filtered to PBS servers whose cluster the caller has view:backup on.
// Standalone PBS servers (no cluster_id) are visible only to callers with the
// global view:backup grant.
func (h *BackupHandler) ListSnapshotsByBackupID(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "backup")
	if err != nil {
		return err
	}

	backupID := c.Query("backup_id")
	if backupID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "backup_id query parameter is required")
	}

	// Build the set of PBS servers the caller can see, mapped to whether
	// they're cluster-bound (and which cluster).
	servers, err := h.queries.ListPBSServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}
	allowedPBS := make(map[uuid.UUID]bool, len(servers))
	for _, s := range servers {
		if s.ClusterID.Valid {
			if access.PermitsCluster(uuid.UUID(s.ClusterID.Bytes)) {
				allowedPBS[s.ID] = true
			}
		} else if access.HasGlobal {
			allowedPBS[s.ID] = true
		}
	}

	snaps, err := h.queries.ListPBSSnapshotsByBackupID(c.Context(), backupID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list snapshots")
	}

	filtered := snaps[:0]
	for _, sn := range snaps {
		if allowedPBS[sn.PbsServerID] {
			filtered = append(filtered, sn)
		}
	}

	return RespondItems(c, filtered)
}

// --- Backup Job endpoints (PVE vzdump) ---

// createPVEClient creates a PVE client for the given cluster ID. Routes
// through the per-server Proxmox client cache when one is available.
func (h *BackupHandler) createPVEClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, 60*time.Second)
}

type triggerBackupRequest struct {
	VMID     string `json:"vmid"`
	Node     string `json:"node"`
	Storage  string `json:"storage"`
	Mode     string `json:"mode"`
	Compress string `json:"compress"`
}

// TriggerBackup handles POST /api/v1/clusters/:cluster_id/backup
func (h *BackupHandler) TriggerBackup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "backup", clusterID); err != nil {
		return err
	}

	var req triggerBackupRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.VMID == "" || req.Node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vmid and node are required")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := client.TriggerBackup(c.Context(), req.Node, proxmox.BackupParams{
		VMID:     req.VMID,
		Storage:  req.Storage,
		Mode:     req.Mode,
		Compress: req.Compress,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	description := "Backup VMID " + req.VMID + " on " + req.Node
	if req.Storage != "" {
		description += " → " + req.Storage
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         req.Node,
		ResourceType: "backup",
		ResourceID:   req.VMID,
		Action:       "backup_triggered",
		UPID:         upid,
		TaskType:     "vzdump",
		Description:  description,
		Extra: map[string]any{
			"vmid":    req.VMID,
			"storage": req.Storage,
			"mode":    req.Mode,
		},
	})

	return c.JSON(fiber.Map{"upid": upid})
}

// ListBackupJobs handles GET /api/v1/clusters/:cluster_id/backup-jobs
func (h *BackupHandler) ListBackupJobs(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "backup", clusterID); err != nil {
		return err
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	jobs, err := client.ListBackupJobs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, jobs)
}

type backupJobRequest struct {
	Enabled  *int   `json:"enabled"`
	Type     string `json:"type"`
	Schedule string `json:"schedule"`
	Storage  string `json:"storage"`
	// Node and Comment are pointers because "" is a meaningful value for them
	// — "run on any node", "no comment" — and PVE only unsets a property that
	// is named in the delete list. A client that omits the field entirely
	// leaves the job's current value alone.
	Node             *string `json:"node"`
	VMID             string  `json:"vmid"`
	All              *int    `json:"all"`
	Exclude          string  `json:"exclude"`
	Pool             string  `json:"pool"`
	Mode             string  `json:"mode"`
	Compress         string  `json:"compress"`
	MailNotification string  `json:"mailnotification"`
	MailTo           string  `json:"mailto"`
	Comment          *string `json:"comment"`
}

// backupSelectionKeys are the vzdump properties that decide which guests a job
// backs up. A job carries one selection, so switching between them has to clear
// the ones the job no longer uses.
var backupSelectionKeys = []string{"all", "vmid", "exclude", "pool"}

// selectionKeys reports which selection properties this request sets. "exclude"
// is the one combination PVE allows: an exclusion list names guests to skip
// from an all-guests job, so it travels with all=1. A nil result means the
// request names no selection at all — an update then leaves the job's current
// selection untouched.
//
// The precedence matches how vzdump itself resolves a config that carries more
// than one (all, then pool, then the vmid list), so what Nexara displays for a
// hand-edited job is what PVE will actually back up.
func (r backupJobRequest) selectionKeys() map[string]bool {
	switch {
	case r.Exclude != "":
		return map[string]bool{"all": true, "exclude": true}
	case r.All != nil && *r.All != 0:
		return map[string]bool{"all": true}
	case r.Pool != "":
		return map[string]bool{"pool": true}
	case r.VMID != "":
		return map[string]bool{"vmid": true}
	}
	return nil
}

// clearedProperties lists the job properties to unset (PVE's "delete"
// parameter) so the job ends up matching this request rather than a mix of it
// and whatever the job carried before.
func (r backupJobRequest) clearedProperties() []string {
	var cleared []string
	if active := r.selectionKeys(); active != nil {
		for _, k := range backupSelectionKeys {
			if !active[k] {
				cleared = append(cleared, k)
			}
		}
	}
	if r.Node != nil && *r.Node == "" {
		cleared = append(cleared, "node")
	}
	if r.Comment != nil && *r.Comment == "" {
		cleared = append(cleared, "comment")
	}
	return cleared
}

func (r backupJobRequest) toParams() proxmox.BackupJobParams {
	params := proxmox.BackupJobParams{
		Enabled:          r.Enabled,
		Type:             r.Type,
		Schedule:         r.Schedule,
		Storage:          r.Storage,
		Mode:             r.Mode,
		Compress:         r.Compress,
		MailNotification: r.MailNotification,
		MailTo:           r.MailTo,
	}
	if r.Node != nil {
		params.Node = *r.Node
	}
	if r.Comment != nil {
		params.Comment = *r.Comment
	}
	// Send only the selection the request actually asked for. Copying every
	// field through would let a request naming two selections set and delete
	// the same property in one call.
	active := r.selectionKeys()
	if active["all"] {
		all := 1
		params.All = &all
	}
	if active["exclude"] {
		params.Exclude = r.Exclude
	}
	if active["pool"] {
		params.Pool = r.Pool
	}
	if active["vmid"] {
		params.VMID = r.VMID
	}
	return params
}

// selectionSummary renders the guest selection for the audit row, or "" when
// the request names none — an update then leaves the job's selection alone, and
// claiming otherwise would put a change in the audit trail that never happened.
func (r backupJobRequest) selectionSummary() string {
	active := r.selectionKeys()
	switch {
	case active == nil:
		return ""
	case active["exclude"]:
		return "all guests except " + r.Exclude
	case active["pool"]:
		return "pool " + r.Pool
	case active["vmid"]:
		return "vmids " + r.VMID
	default:
		return "all guests"
	}
}

// auditDetails summarises a backup job for the audit row. PVE assigns the job
// ID on create, so without this the audit trail records only that "a job" was
// created. Fields the request left unspecified are omitted rather than
// defaulted, so a partial update is not recorded as having set them.
func (r backupJobRequest) auditDetails() json.RawMessage {
	fields := map[string]any{}
	if r.Schedule != "" {
		fields["schedule"] = r.Schedule
	}
	if r.Storage != "" {
		fields["storage"] = r.Storage
	}
	if r.Node != nil && *r.Node != "" {
		fields["node"] = *r.Node
	}
	if r.Mode != "" {
		fields["mode"] = r.Mode
	}
	if r.Enabled != nil {
		fields["enabled"] = *r.Enabled != 0
	}
	if selection := r.selectionSummary(); selection != "" {
		fields["selection"] = selection
	}
	details, _ := json.Marshal(fields)
	return details
}

// CreateBackupJob handles POST /api/v1/clusters/:cluster_id/backup-jobs
func (h *BackupHandler) CreateBackupJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "backup", clusterID); err != nil {
		return err
	}

	var req backupJobRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := client.CreateBackupJob(c.Context(), req.toParams()); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{Valid: true, Bytes: clusterID}, "backup", "backup-job", "backup_job_created", req.auditDetails())

	return c.JSON(fiber.Map{"status": "created"})
}

// UpdateBackupJob handles PUT /api/v1/clusters/:cluster_id/backup-jobs/:job_id
func (h *BackupHandler) UpdateBackupJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "backup", clusterID); err != nil {
		return err
	}

	jobID := c.Params("job_id")
	if jobID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Job ID is required")
	}

	var req backupJobRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	params := req.toParams()
	params.Delete = req.clearedProperties()
	if err := client.UpdateBackupJob(c.Context(), jobID, params); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{Valid: true, Bytes: clusterID}, "backup", jobID, "backup_job_updated", req.auditDetails())

	return c.JSON(fiber.Map{"status": "updated"})
}

// DeleteBackupJob handles DELETE /api/v1/clusters/:cluster_id/backup-jobs/:job_id
func (h *BackupHandler) DeleteBackupJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "delete", "backup", clusterID); err != nil {
		return err
	}

	jobID := c.Params("job_id")
	if jobID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Job ID is required")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := client.DeleteBackupJob(c.Context(), jobID); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{Valid: true, Bytes: clusterID}, "backup", jobID, "backup_job_deleted", nil)

	return c.JSON(fiber.Map{"status": "deleted"})
}

// RunBackupJob handles POST /api/v1/clusters/:cluster_id/backup-jobs/:job_id/run
func (h *BackupHandler) RunBackupJob(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "backup", clusterID); err != nil {
		return err
	}

	jobID := c.Params("job_id")
	if jobID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Job ID is required")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := client.RunBackupJob(c.Context(), jobID)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		ResourceType: "backup",
		ResourceID:   jobID,
		Action:       "backup_job_run",
		UPID:         upid,
		TaskType:     "vzdump",
		Description:  "Run backup job " + jobID,
		Extra:        map[string]any{"job_id": jobID},
	})

	return c.JSON(fiber.Map{"upid": upid})
}

// --- Restore endpoint ---

type restoreBackupRequest struct {
	PBSServerID       string `json:"pbs_server_id"`
	BackupType        string `json:"backup_type"`
	BackupID          string `json:"backup_id"`
	BackupTime        int64  `json:"backup_time"`
	Datastore         string `json:"datastore"`
	TargetNode        string `json:"target_node"`
	VMID              int    `json:"vmid"`
	Storage           string `json:"storage"`
	Force             bool   `json:"force"`
	Unique            bool   `json:"unique"`
	StartAfterRestore bool   `json:"start_after_restore"`
}

// RestoreBackup handles POST /api/v1/clusters/:cluster_id/restore
func (h *BackupHandler) RestoreBackup(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "backup", clusterID); err != nil {
		return err
	}

	var req restoreBackupRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.PBSServerID == "" || req.BackupType == "" || req.BackupID == "" || req.BackupTime == 0 || req.TargetNode == "" || req.VMID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "pbs_server_id, backup_type, backup_id, backup_time, target_node, and vmid are required")
	}

	// Look up PBS server to validate it exists.
	pbsID, err := uuid.Parse(req.PBSServerID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid pbs_server_id")
	}

	_, err = h.queries.GetPBSServer(c.Context(), pbsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	// Verify the target cluster exists. The actual *Client is fetched
	// from the cache below via CreateProxmoxClient.
	if _, err := h.queries.GetCluster(c.Context(), clusterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	// Find a PVE storage entry of type "pbs" on this cluster to build the archive string.
	storagePools, err := h.queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list cluster storage")
	}
	var pveStorageName string
	for _, sp := range storagePools {
		if sp.Type == "pbs" {
			pveStorageName = sp.Storage
			break
		}
	}
	if pveStorageName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "No PBS storage configured on this PVE cluster. Add the PBS server as storage in PVE first (Datacenter > Storage > Add > Proxmox Backup Server).")
	}

	// Build the archive path: <pve-storage>:backup/<type>/<id>/<ISO-timestamp>
	backupTime := time.Unix(req.BackupTime, 0).UTC()
	archive := pveStorageName + ":backup/" + req.BackupType + "/" + req.BackupID + "/" + backupTime.Format(time.RFC3339Nano)

	pveClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, 60*time.Second)
	if err != nil {
		return err
	}

	// When overwriting an existing VM, PVE requires the VM to be on the same
	// node and stopped. Validate this and stop it before restoring.
	if req.Force {
		existingVM, vmErr := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
			ClusterID: clusterID,
			Vmid:      safeconv.Int32(req.VMID),
		})
		if vmErr != nil && !errors.Is(vmErr, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to check existing VM")
		}
		if vmErr == nil {
			// VM exists — force restore must target the node where the VM lives.
			existingNode, nodeErr := h.queries.GetNode(c.Context(), existingVM.NodeID)
			if nodeErr != nil {
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up VM node")
			}
			// Auto-correct to the correct node.
			req.TargetNode = existingNode.Name

			// Stop the VM before overwriting.
			var stopUpid string
			var stopErr error
			switch req.BackupType {
			case "vm":
				stopUpid, stopErr = pveClient.StopVM(c.Context(), req.TargetNode, req.VMID)
			case "ct":
				stopUpid, stopErr = pveClient.StopCT(c.Context(), req.TargetNode, req.VMID)
			}
			if stopErr != nil {
				// VM might already be stopped.
				slog.Debug("stop before force restore failed (may already be stopped)", "vmid", req.VMID, "error", stopErr)
			} else if stopUpid != "" {
				for i := 0; i < 60; i++ {
					time.Sleep(1 * time.Second)
					ts, tsErr := pveClient.GetTaskStatus(c.Context(), req.TargetNode, stopUpid)
					if tsErr != nil {
						break
					}
					if ts.Status == "stopped" {
						break
					}
				}
			}
		}
	}

	params := proxmox.RestoreParams{
		VMID:    req.VMID,
		Archive: archive,
		Storage: req.Storage,
		Force:   req.Force,
		Unique:  req.Unique,
	}

	var upid string
	switch req.BackupType {
	case "vm":
		upid, err = pveClient.RestoreVM(c.Context(), req.TargetNode, params)
	case "ct":
		upid, err = pveClient.RestoreCT(c.Context(), req.TargetNode, params)
	default:
		return fiber.NewError(fiber.StatusBadRequest, "backup_type must be 'vm' or 'ct'")
	}

	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         req.TargetNode,
		ResourceType: "backup",
		ResourceID:   strconv.Itoa(req.VMID) + "/" + req.BackupType,
		Action:       "backup_restored",
		UPID:         upid,
		TaskType:     "qmrestore",
		Description:  "Restore " + req.BackupType + "/" + req.BackupID + " → VMID " + strconv.Itoa(req.VMID) + " on " + req.TargetNode,
		Extra: map[string]any{
			"pbs_server_id":       req.PBSServerID,
			"backup_type":         req.BackupType,
			"backup_id":           req.BackupID,
			"datastore":           req.Datastore,
			"target_node":         req.TargetNode,
			"vmid":                req.VMID,
			"storage":             req.Storage,
			"force":               req.Force,
			"unique":              req.Unique,
			"start_after_restore": req.StartAfterRestore,
			"archive":             archive,
		},
	})

	// If requested, wait for restore to finish then start the VM in the background.
	if req.StartAfterRestore {
		go func() { //nolint:gosec // G118: intentionally detached — start-after-restore watcher must outlive the request (Fiber recycles the request context)
			defer func() {
				if r := recover(); r != nil {
					slog.Error("start-after-restore watcher panicked", "upid", upid, "panic", r)
				}
			}()
			ctx := context.Background() //nolint:gosec // G118: intentionally detached; Fiber recycles request context
			for i := 0; i < 600; i++ {  // up to 10 minutes
				time.Sleep(2 * time.Second)
				ts, tsErr := pveClient.GetTaskStatus(ctx, req.TargetNode, upid)
				if tsErr != nil {
					slog.Error("start-after-restore: failed to poll restore task", "upid", upid, "error", tsErr)
					return
				}
				if ts.Status == "stopped" {
					if ts.ExitStatus != "OK" {
						slog.Info("start-after-restore: restore task failed, skipping start", "upid", upid, "exit", ts.ExitStatus)
						return
					}
					var startUpid string
					var startErr error
					switch req.BackupType {
					case "vm":
						startUpid, startErr = pveClient.StartVM(ctx, req.TargetNode, req.VMID)
					case "ct":
						startUpid, startErr = pveClient.StartCT(ctx, req.TargetNode, req.VMID)
					}
					if startErr != nil {
						slog.Error("start-after-restore: failed to start VM", "vmid", req.VMID, "error", startErr)
						return
					}
					slog.Info("start-after-restore: VM started", "vmid", req.VMID, "upid", startUpid)
					return
				}
			}
			slog.Warn("start-after-restore: timed out waiting for restore", "upid", upid)
		}()
	}

	return c.JSON(fiber.Map{
		"upid":   upid,
		"status": "restoring",
	})
}

// backupCoverageEntry is a single VM's backup coverage info.
type backupCoverageEntry struct {
	VMID           int32  `json:"vmid"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	ClusterID      string `json:"cluster_id"`
	ClusterName    string `json:"cluster_name"`
	LatestBackup   *int64 `json:"latest_backup"`
	BackupCount    int    `json:"backup_count"`
	CoverageStatus string `json:"coverage_status"` // "recent", "stale", "none"
}

// GetBackupCoverage handles GET /api/v1/backup-coverage
//
// Cross-references three data sources to determine backup coverage:
//  1. PVE storage pools — which clusters have PBS-type storage and which
//     datastore name each maps to (PVE's PBS storage name = PBS datastore name)
//  2. PBS snapshots — keyed by (datastore, backup_id/VMID)
//  3. VMs — matched only against datastores their cluster actually uses
//
// This correctly handles multi-cluster setups where different clusters
// use different PBS datastores, even with overlapping VMIDs.
func (h *BackupHandler) GetBackupCoverage(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "backup")
	if err != nil {
		return err
	}

	vms, err := h.queries.ListAllVMs(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list VMs")
	}
	// Restrict the input set to VMs in clusters the user can view.
	filteredVMs := vms[:0]
	for _, vm := range vms {
		if access.PermitsCluster(vm.ClusterID) {
			filteredVMs = append(filteredVMs, vm)
		}
	}
	vms = filteredVMs

	// Step 1: Build cluster → set of PBS datastore names from PVE storage config.
	// PVE's PBS storage pool name IS the datastore name on the PBS server.
	clusterDatastores := make(map[string]map[string]bool) // clusterID → {datastoreName: true}
	clusters, err := h.queries.ListClusters(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list clusters")
	}
	for _, cl := range clusters {
		pools, pErr := h.queries.ListStoragePoolsByCluster(c.Context(), cl.ID)
		if pErr != nil {
			continue
		}
		for _, pool := range pools {
			if pool.Type == "pbs" {
				cid := cl.ID.String()
				if clusterDatastores[cid] == nil {
					clusterDatastores[cid] = make(map[string]bool)
				}
				clusterDatastores[cid][pool.Storage] = true
			}
		}
	}

	// Step 2: Build snapshot map keyed by "datastore:backup_id".
	type backupInfo struct {
		LatestTime int64
		Count      int
	}
	backupMap := make(map[string]*backupInfo)

	servers, err := h.queries.ListPBSServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}
	for _, srv := range servers {
		snaps, sErr := h.queries.ListPBSSnapshotsByServer(c.Context(), srv.ID)
		if sErr != nil {
			continue
		}
		for _, snap := range snaps {
			key := snap.Datastore + ":" + snap.BackupID
			info, ok := backupMap[key]
			if !ok {
				info = &backupInfo{}
				backupMap[key] = info
			}
			info.Count++
			if snap.BackupTime > info.LatestTime {
				info.LatestTime = snap.BackupTime
			}
		}
	}

	// Step 3: Match VMs against only the datastores their cluster uses.
	now := time.Now().Unix()
	staleThreshold := int64(24 * 3600) // 24 hours

	entries := make([]backupCoverageEntry, 0, len(vms))
	for _, vm := range vms {
		vmidStr := strconv.Itoa(int(vm.Vmid))
		entry := backupCoverageEntry{
			VMID:        vm.Vmid,
			Name:        vm.Name,
			Type:        vm.Type,
			Status:      vm.Status,
			ClusterID:   vm.ClusterID.String(),
			ClusterName: vm.ClusterName,
		}

		// Check each datastore this VM's cluster uses for a matching snapshot.
		// Aggregate across datastores (a VM could be backed up to multiple).
		var totalCount int
		var latestTime int64
		for ds := range clusterDatastores[vm.ClusterID.String()] {
			if info, ok := backupMap[ds+":"+vmidStr]; ok && info.Count > 0 {
				totalCount += info.Count
				if info.LatestTime > latestTime {
					latestTime = info.LatestTime
				}
			}
		}

		if totalCount > 0 {
			entry.LatestBackup = &latestTime
			entry.BackupCount = totalCount
			if now-latestTime < staleThreshold {
				entry.CoverageStatus = "recent"
			} else {
				entry.CoverageStatus = "stale"
			}
		} else {
			entry.CoverageStatus = "none"
		}

		entries = append(entries, entry)
	}

	return RespondItems(c, entries)
}
