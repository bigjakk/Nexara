package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
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

// auditLog records an audit log entry for backup-related actions.
// Backup actions don't have a cluster context.
func (h *BackupHandler) auditLog(c *fiber.Ctx, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "backup", resourceID, action, details)
}

// createPBSClient creates a PBS client for the given server ID.
func (h *BackupHandler) createPBSClient(c *fiber.Ctx, pbsServerID uuid.UUID) (*proxmox.PBSClient, error) {
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

func parsePBSID(c *fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("pbs_id"))
	if err != nil {
		return uuid.Nil, fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}
	return id, nil
}

// --- Live proxy endpoints ---

// ListDatastores handles GET /api/v1/pbs-servers/:pbs_id/datastores
func (h *BackupHandler) ListDatastores(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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

	return c.JSON(stores)
}

// GetDatastoreStatus handles GET /api/v1/pbs-servers/:pbs_id/datastores/status
func (h *BackupHandler) GetDatastoreStatus(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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

	return c.JSON(status)
}

// TriggerGC handles POST /api/v1/pbs-servers/:pbs_id/datastores/:store/gc
func (h *BackupHandler) TriggerGC(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
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

	h.auditLog(c, store, "gc_triggered", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "gc_triggered")

	return c.JSON(fiber.Map{"upid": upid})
}

type deleteSnapshotRequest struct {
	BackupType string `json:"backup_type"`
	BackupID   string `json:"backup_id"`
	BackupTime int64  `json:"backup_time"`
}

// DeleteSnapshot handles DELETE /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots
func (h *BackupHandler) DeleteSnapshot(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req deleteSnapshotRequest
	if err := c.BodyParser(&req); err != nil {
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
	h.auditLog(c, store+"/"+req.BackupType+"/"+req.BackupID, "snapshot_deleted", details)
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
func (h *BackupHandler) ProtectSnapshot(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req protectSnapshotRequest
	if err := c.BodyParser(&req); err != nil {
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
	h.auditLog(c, store+"/"+req.BackupType+"/"+req.BackupID, action, details)

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
func (h *BackupHandler) UpdateSnapshotNotes(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req updateSnapshotNotesRequest
	if err := c.BodyParser(&req); err != nil {
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

	h.auditLog(c, store+"/"+req.BackupType+"/"+req.BackupID, "snapshot_notes_updated", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}

// GetTaskLog handles GET /api/v1/pbs-servers/:pbs_id/tasks/:upid/log
func (h *BackupHandler) GetTaskLog(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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

	return c.JSON(entries)
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
func (h *BackupHandler) PruneDatastore(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	store := c.Params("store")
	if store == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Datastore name is required")
	}

	var req pruneDatastoreRequest
	if err := c.BodyParser(&req); err != nil {
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
			"store":       store,
			"backup_type": req.BackupType,
			"backup_id":   req.BackupID,
			"keep_last":   req.KeepLast,
			"keep_daily":  req.KeepDaily,
			"keep_weekly": req.KeepWeekly,
			"keep_monthly": req.KeepMonthly,
			"keep_yearly": req.KeepYearly,
		})
		h.auditLog(c, store, "datastore_pruned", details)
		h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "datastore_pruned")
	}

	return c.JSON(results)
}

// GetDatastoreConfig handles GET /api/v1/pbs-servers/:pbs_id/datastores/:store/config
func (h *BackupHandler) GetDatastoreConfig(c *fiber.Ctx) error {
	pbsID, err := parsePBSID(c)
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

	config, err := client.GetDatastoreConfig(c.Context(), store)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(config)
}

// RunSyncJob handles POST /api/v1/pbs-servers/:pbs_id/sync-jobs/:job_id/run
func (h *BackupHandler) RunSyncJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
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

	h.auditLog(c, jobID, "sync_job_triggered", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "sync_job_triggered")

	return c.JSON(fiber.Map{"upid": upid})
}

// RunVerifyJob handles POST /api/v1/pbs-servers/:pbs_id/verify-jobs/:job_id/run
func (h *BackupHandler) RunVerifyJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
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

	h.auditLog(c, jobID, "verify_job_triggered", nil)
	h.eventPub.SystemEvent(c.Context(), events.KindPBSChange, "verify_job_triggered")

	return c.JSON(fiber.Map{"upid": upid})
}

// ListTasks handles GET /api/v1/pbs-servers/:pbs_id/tasks
func (h *BackupHandler) ListTasks(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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

	return c.JSON(tasks)
}

// GetTaskStatus handles GET /api/v1/pbs-servers/:pbs_id/tasks/:upid
func (h *BackupHandler) GetTaskStatus(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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
func (h *BackupHandler) ListSnapshots(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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
		return c.JSON(snaps)
	}

	snaps, err := h.queries.ListPBSSnapshotsByServer(c.Context(), pbsID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list snapshots")
	}

	return c.JSON(snaps)
}

// ListSyncJobs handles GET /api/v1/pbs-servers/:pbs_id/sync-jobs
func (h *BackupHandler) ListSyncJobs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	jobs, err := h.queries.ListPBSSyncJobsByServer(c.Context(), pbsID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list sync jobs")
	}

	return c.JSON(jobs)
}

// ListVerifyJobs handles GET /api/v1/pbs-servers/:pbs_id/verify-jobs
func (h *BackupHandler) ListVerifyJobs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	jobs, err := h.queries.ListPBSVerifyJobsByServer(c.Context(), pbsID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list verify jobs")
	}

	return c.JSON(jobs)
}

// GetDatastoreMetrics handles GET /api/v1/pbs-servers/:pbs_id/metrics
func (h *BackupHandler) GetDatastoreMetrics(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
		return err
	}

	timeframe := c.Query("timeframe", "latest")

	if timeframe == "latest" {
		metrics, err := h.queries.GetLatestPBSDatastoreMetrics(c.Context(), pbsID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to get datastore metrics")
		}
		return c.JSON(metrics)
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

	return c.JSON(metrics)
}

// GetDatastoreRRD handles GET /api/v1/pbs-servers/:pbs_id/datastores/:store/rrd
// Live proxy to PBS RRD — returns IO performance metrics (transfer rate, IOPS).
func (h *BackupHandler) GetDatastoreRRD(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	pbsID, err := parsePBSID(c)
	if err != nil {
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

	return c.JSON(entries)
}

// ListSnapshotsByBackupID handles GET /api/v1/pbs-snapshots?backup_id=XXX
// Returns all PBS snapshots across all servers matching a given backup_id (VMID).
func (h *BackupHandler) ListSnapshotsByBackupID(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	backupID := c.Query("backup_id")
	if backupID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "backup_id query parameter is required")
	}

	snaps, err := h.queries.ListPBSSnapshotsByBackupID(c.Context(), backupID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list snapshots")
	}

	return c.JSON(snaps)
}

// --- Backup Job endpoints (PVE vzdump) ---

// createPVEClient creates a PVE client for the given cluster ID.
func (h *BackupHandler) createPVEClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        60 * time.Second,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}
	return client, nil
}

type triggerBackupRequest struct {
	VMID     string `json:"vmid"`
	Node     string `json:"node"`
	Storage  string `json:"storage"`
	Mode     string `json:"mode"`
	Compress string `json:"compress"`
}

// TriggerBackup handles POST /api/v1/clusters/:cluster_id/backup
func (h *BackupHandler) TriggerBackup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req triggerBackupRequest
	if err := c.BodyParser(&req); err != nil {
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

	details, _ := json.Marshal(map[string]interface{}{
		"vmid":    req.VMID,
		"node":    req.Node,
		"storage": req.Storage,
		"mode":    req.Mode,
		"upid":    upid,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "backup", req.VMID, "backup_triggered", details)

	// Create task_history so backup appears in the Activity panel.
	description := "Backup VMID " + req.VMID + " on " + req.Node
	if req.Storage != "" {
		description += " → " + req.Storage
	}
	uid, _ := c.Locals("user_id").(uuid.UUID)
	_, _ = h.queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID:   clusterID,
		UserID:      uid,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        req.Node,
		TaskType:    "vzdump",
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindTaskCreated, "task", upid, "vzdump")

	return c.JSON(fiber.Map{"upid": upid})
}

// ListBackupJobs handles GET /api/v1/clusters/:cluster_id/backup-jobs
func (h *BackupHandler) ListBackupJobs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	jobs, err := client.ListBackupJobs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(jobs)
}

type backupJobRequest struct {
	Enabled          *int   `json:"enabled"`
	Type             string `json:"type"`
	Schedule         string `json:"schedule"`
	Storage          string `json:"storage"`
	Node             string `json:"node"`
	VMID             string `json:"vmid"`
	Mode             string `json:"mode"`
	Compress         string `json:"compress"`
	MailNotification string `json:"mailnotification"`
	MailTo           string `json:"mailto"`
	Comment          string `json:"comment"`
}

// CreateBackupJob handles POST /api/v1/clusters/:cluster_id/backup-jobs
func (h *BackupHandler) CreateBackupJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req backupJobRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := client.CreateBackupJob(c.Context(), proxmox.BackupJobParams{
		Enabled:          req.Enabled,
		Type:             req.Type,
		Schedule:         req.Schedule,
		Storage:          req.Storage,
		Node:             req.Node,
		VMID:             req.VMID,
		Mode:             req.Mode,
		Compress:         req.Compress,
		MailNotification: req.MailNotification,
		MailTo:           req.MailTo,
		Comment:          req.Comment,
	}); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{Valid: true, Bytes: clusterID}, "backup", "backup-job", "backup_job_created", nil)

	return c.JSON(fiber.Map{"status": "created"})
}

// UpdateBackupJob handles PUT /api/v1/clusters/:cluster_id/backup-jobs/:job_id
func (h *BackupHandler) UpdateBackupJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	jobID := c.Params("job_id")
	if jobID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Job ID is required")
	}

	var req backupJobRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	client, err := h.createPVEClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := client.UpdateBackupJob(c.Context(), jobID, proxmox.BackupJobParams{
		Enabled:          req.Enabled,
		Type:             req.Type,
		Schedule:         req.Schedule,
		Storage:          req.Storage,
		Node:             req.Node,
		VMID:             req.VMID,
		Mode:             req.Mode,
		Compress:         req.Compress,
		MailNotification: req.MailNotification,
		MailTo:           req.MailTo,
		Comment:          req.Comment,
	}); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{Valid: true, Bytes: clusterID}, "backup", jobID, "backup_job_updated", nil)

	return c.JSON(fiber.Map{"status": "updated"})
}

// DeleteBackupJob handles DELETE /api/v1/clusters/:cluster_id/backup-jobs/:job_id
func (h *BackupHandler) DeleteBackupJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
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
func (h *BackupHandler) RunBackupJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
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

	details, _ := json.Marshal(map[string]interface{}{
		"job_id": jobID,
		"upid":   upid,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "backup", jobID, "backup_job_run", details)

	// Create task_history so it appears in the Activity panel.
	description := "Run backup job " + jobID
	uid, _ := c.Locals("user_id").(uuid.UUID)
	_, _ = h.queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID:   clusterID,
		UserID:      uid,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        "",
		TaskType:    "vzdump",
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindTaskCreated, "task", upid, "vzdump")

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
func (h *BackupHandler) RestoreBackup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "backup"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req restoreBackupRequest
	if err := c.BodyParser(&req); err != nil {
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

	// Create PVE client for the target cluster.
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
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
	archive := pveStorageName + ":backup/" + req.BackupType + "/" + req.BackupID + "/" + backupTime.Format("2006-01-02T15:04:05Z")

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	pveClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        60 * time.Second,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	// When overwriting an existing VM, PVE requires the VM to be on the same
	// node and stopped. Validate this and stop it before restoring.
	if req.Force {
		existingVM, vmErr := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
			ClusterID: clusterID,
			Vmid:      safeInt32(req.VMID),
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

	details, _ := json.Marshal(map[string]interface{}{
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
		"upid":                upid,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "backup", strconv.Itoa(req.VMID)+"/"+req.BackupType, "backup_restored", details)

	// Create task_history so restore appears in the Activity panel.
	description := "Restore " + req.BackupType + "/" + req.BackupID + " → VMID " + strconv.Itoa(req.VMID) + " on " + req.TargetNode
	uid, _ := c.Locals("user_id").(uuid.UUID)
	_, _ = h.queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID:   clusterID,
		UserID:      uid,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        req.TargetNode,
		TaskType:    "qmrestore",
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindTaskCreated, "task", upid, "qmrestore")

	// If requested, wait for restore to finish then start the VM in the background.
	if req.StartAfterRestore {
		go func() {
			ctx := context.Background() //nolint:gosec // G118: intentionally detached; Fiber recycles request context
			for i := 0; i < 600; i++ { // up to 10 minutes
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
	VMID            int32  `json:"vmid"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Status          string `json:"status"`
	ClusterID       string `json:"cluster_id"`
	ClusterName     string `json:"cluster_name"`
	LatestBackup    *int64 `json:"latest_backup"`
	BackupCount     int    `json:"backup_count"`
	CoverageStatus  string `json:"coverage_status"` // "recent", "stale", "none"
}

// GetBackupCoverage handles GET /api/v1/backup-coverage
//
// Cross-references three data sources to determine backup coverage:
// 1. PVE storage pools — which clusters have PBS-type storage and which
//    datastore name each maps to (PVE's PBS storage name = PBS datastore name)
// 2. PBS snapshots — keyed by (datastore, backup_id/VMID)
// 3. VMs — matched only against datastores their cluster actually uses
//
// This correctly handles multi-cluster setups where different clusters
// use different PBS datastores, even with overlapping VMIDs.
func (h *BackupHandler) GetBackupCoverage(c *fiber.Ctx) error {
	vms, err := h.queries.ListAllVMs(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list VMs")
	}

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

	return c.JSON(entries)
}
