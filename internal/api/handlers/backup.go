package handlers

import (
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// BackupHandler handles PBS backup management endpoints.
type BackupHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewBackupHandler creates a new backup handler.
func NewBackupHandler(queries *db.Queries, encryptionKey string) *BackupHandler {
	return &BackupHandler{queries: queries, encryptionKey: encryptionKey}
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
		return mapProxmoxError(err)
	}

	return c.JSON(status)
}

// TriggerGC handles POST /api/v1/pbs-servers/:pbs_id/datastores/:store/gc
func (h *BackupHandler) TriggerGC(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{"upid": upid})
}

type deleteSnapshotRequest struct {
	BackupType string `json:"backup_type"`
	BackupID   string `json:"backup_id"`
	BackupTime int64  `json:"backup_time"`
}

// DeleteSnapshot handles DELETE /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots
func (h *BackupHandler) DeleteSnapshot(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{"status": "deleted"})
}

// RunSyncJob handles POST /api/v1/pbs-servers/:pbs_id/sync-jobs/:job_id/run
func (h *BackupHandler) RunSyncJob(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{"upid": upid})
}

// RunVerifyJob handles POST /api/v1/pbs-servers/:pbs_id/verify-jobs/:job_id/run
func (h *BackupHandler) RunVerifyJob(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{"upid": upid})
}

// ListTasks handles GET /api/v1/pbs-servers/:pbs_id/tasks
func (h *BackupHandler) ListTasks(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return mapProxmoxError(err)
	}

	return c.JSON(tasks)
}

// GetTaskStatus handles GET /api/v1/pbs-servers/:pbs_id/tasks/:upid
func (h *BackupHandler) GetTaskStatus(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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

// --- Restore endpoint ---

type restoreBackupRequest struct {
	PBSServerID string `json:"pbs_server_id"`
	BackupType  string `json:"backup_type"`
	BackupID    string `json:"backup_id"`
	BackupTime  int64  `json:"backup_time"`
	Datastore   string `json:"datastore"`
	TargetNode  string `json:"target_node"`
	VMID        int    `json:"vmid"`
	Storage     string `json:"storage"`
}

// RestoreBackup handles POST /api/v1/clusters/:cluster_id/restore
func (h *BackupHandler) RestoreBackup(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	// Get PBS server info to build the archive string.
	pbsID, err := uuid.Parse(req.PBSServerID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid pbs_server_id")
	}

	server, err := h.queries.GetPBSServer(c.Context(), pbsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	// Build the archive path: pbs://store/ns/type/id/timestamp
	datastore := req.Datastore
	if datastore == "" {
		datastore = "default"
	}
	archive := "pbs://" + server.Name + "/" + datastore + "/" + req.BackupType + "/" + req.BackupID + "/" + strconv.FormatInt(req.BackupTime, 10)

	// Create PVE client for the target cluster.
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

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

	params := proxmox.RestoreParams{
		VMID:    req.VMID,
		Archive: archive,
		Storage: req.Storage,
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

	return c.JSON(fiber.Map{
		"upid":   upid,
		"status": "restoring",
	})
}
