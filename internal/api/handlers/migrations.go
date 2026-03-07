package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/migration"
)

// MigrationHandler handles migration job endpoints.
type MigrationHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewMigrationHandler creates a new MigrationHandler.
func NewMigrationHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *MigrationHandler {
	return &MigrationHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

// --- Request / Response types ---

type createMigrationRequest struct {
	SourceClusterID string          `json:"source_cluster_id"`
	TargetClusterID string          `json:"target_cluster_id"`
	SourceNode      string          `json:"source_node"`
	TargetNode      string          `json:"target_node"`
	VMID            int32           `json:"vmid"`
	VMType          string          `json:"vm_type"`
	MigrationType   string          `json:"migration_type"`
	MigrationMode   string          `json:"migration_mode"`
	StorageMap      json.RawMessage `json:"storage_map"`
	NetworkMap      json.RawMessage `json:"network_map"`
	Online          bool            `json:"online"`
	BWLimitKiB      int32           `json:"bwlimit_kib"`
	DeleteSource    bool            `json:"delete_source"`
	TargetVMID      int32           `json:"target_vmid"`
	TargetStorage   string          `json:"target_storage"`
}

type migrationJobResponse struct {
	ID              uuid.UUID       `json:"id"`
	SourceClusterID uuid.UUID       `json:"source_cluster_id"`
	TargetClusterID uuid.UUID       `json:"target_cluster_id"`
	SourceNode      string          `json:"source_node"`
	TargetNode      string          `json:"target_node"`
	VMID            int32           `json:"vmid"`
	VMType          string          `json:"vm_type"`
	MigrationType   string          `json:"migration_type"`
	MigrationMode   string          `json:"migration_mode"`
	StorageMap      json.RawMessage `json:"storage_map"`
	NetworkMap      json.RawMessage `json:"network_map"`
	Online          bool            `json:"online"`
	BWLimitKiB      int32           `json:"bwlimit_kib"`
	DeleteSource    bool            `json:"delete_source"`
	TargetVMID      int32           `json:"target_vmid"`
	TargetStorage   string          `json:"target_storage"`
	Status          string          `json:"status"`
	UPID            string          `json:"upid"`
	Progress        float64         `json:"progress"`
	CheckResults    json.RawMessage `json:"check_results"`
	ErrorMessage    string          `json:"error_message"`
	StartedAt       *string         `json:"started_at"`
	CompletedAt     *string         `json:"completed_at"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

func toMigrationJobResponse(j db.MigrationJob) migrationJobResponse {
	r := migrationJobResponse{
		ID:              j.ID,
		SourceClusterID: j.SourceClusterID,
		TargetClusterID: j.TargetClusterID,
		SourceNode:      j.SourceNode,
		TargetNode:      j.TargetNode,
		VMID:            j.Vmid,
		VMType:          j.VmType,
		MigrationType:   j.MigrationType,
		MigrationMode:   j.MigrationMode,
		StorageMap:      j.StorageMap,
		NetworkMap:      j.NetworkMap,
		Online:          j.Online,
		BWLimitKiB:      j.BwlimitKib,
		DeleteSource:    j.DeleteSource,
		TargetVMID:      j.TargetVmid,
		TargetStorage:   j.TargetStorage,
		Status:          j.Status,
		UPID:            j.Upid,
		Progress:        j.Progress,
		CheckResults:    j.CheckResults,
		ErrorMessage:    j.ErrorMessage,
		CreatedAt:       j.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       j.UpdatedAt.Format(time.RFC3339),
	}
	if j.StartedAt.Valid {
		s := j.StartedAt.Time.Format(time.RFC3339)
		r.StartedAt = &s
	}
	if j.CompletedAt.Valid {
		s := j.CompletedAt.Time.Format(time.RFC3339)
		r.CompletedAt = &s
	}
	return r
}

func (h *MigrationHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})
}

// --- Handlers ---

var validMigrationTypes = map[string]bool{
	migration.TypeIntraCluster: true,
	migration.TypeCrossCluster: true,
}

var validVMTypes = map[string]bool{
	migration.VMTypeQEMU: true,
	migration.VMTypeLXC:  true,
}

var validMigrationModes = map[string]bool{
	migration.ModeLive:    true,
	migration.ModeStorage: true,
	migration.ModeBoth:    true,
}

// Create handles POST /api/v1/migrations.
func (h *MigrationHandler) Create(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	var req createMigrationRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.SourceNode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "source_node is required")
	}
	if req.VMID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "vmid must be positive")
	}
	if !validVMTypes[req.VMType] {
		return fiber.NewError(fiber.StatusBadRequest, "vm_type must be 'qemu' or 'lxc'")
	}
	if !validMigrationTypes[req.MigrationType] {
		return fiber.NewError(fiber.StatusBadRequest, "migration_type must be 'intra-cluster' or 'cross-cluster'")
	}

	// Default migration mode to "live" if not specified.
	if req.MigrationMode == "" {
		req.MigrationMode = migration.ModeLive
	}
	if !validMigrationModes[req.MigrationMode] {
		return fiber.NewError(fiber.StatusBadRequest, "migration_mode must be 'live', 'storage', or 'both'")
	}

	// Migration mode only applies to intra-cluster.
	if req.MigrationType == migration.TypeCrossCluster && req.MigrationMode != migration.ModeLive {
		return fiber.NewError(fiber.StatusBadRequest, "migration_mode 'storage' and 'both' are only supported for intra-cluster migrations")
	}

	// Storage and both modes require target_storage.
	if (req.MigrationMode == migration.ModeStorage || req.MigrationMode == migration.ModeBoth) && req.TargetStorage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "target_storage is required for storage and both migration modes")
	}

	srcClusterID, err := uuid.Parse(req.SourceClusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid source_cluster_id")
	}

	tgtClusterID, err := uuid.Parse(req.TargetClusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid target_cluster_id")
	}

	// For intra-cluster, source and target must be the same cluster.
	if req.MigrationType == migration.TypeIntraCluster {
		if srcClusterID != tgtClusterID {
			return fiber.NewError(fiber.StatusBadRequest, "For intra-cluster migration, source and target cluster must be the same")
		}
		// Storage mode doesn't need a target node (stays on same node).
		if req.MigrationMode == migration.ModeStorage {
			if req.TargetNode == "" {
				req.TargetNode = req.SourceNode
			}
		} else if req.TargetNode == "" {
			return fiber.NewError(fiber.StatusBadRequest, "target_node is required for intra-cluster live migration")
		}
	}

	if req.StorageMap == nil {
		req.StorageMap = json.RawMessage(`{}`)
	}
	if req.NetworkMap == nil {
		req.NetworkMap = json.RawMessage(`{}`)
	}

	// Get user ID from context.
	var createdBy pgtype.UUID
	if userID, ok := c.Locals("user_id").(uuid.UUID); ok {
		createdBy = pgtype.UUID{Bytes: userID, Valid: true}
	}

	job, err := h.queries.CreateMigrationJob(c.Context(), db.CreateMigrationJobParams{
		SourceClusterID: srcClusterID,
		TargetClusterID: tgtClusterID,
		SourceNode:      req.SourceNode,
		TargetNode:      req.TargetNode,
		Vmid:            req.VMID,
		VmType:          req.VMType,
		MigrationType:   req.MigrationType,
		StorageMap:      req.StorageMap,
		NetworkMap:      req.NetworkMap,
		Online:          req.Online,
		BwlimitKib:      req.BWLimitKiB,
		DeleteSource:    req.DeleteSource,
		TargetVmid:      req.TargetVMID,
		CreatedBy:       createdBy,
		MigrationMode:   req.MigrationMode,
		TargetStorage:   req.TargetStorage,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create migration job")
	}

	typeLabel := "VM"
	if req.VMType == migration.VMTypeLXC {
		typeLabel = "CT"
	}
	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"vmid":           req.VMID,
		"vm_type":        typeLabel,
		"migration_type": req.MigrationType,
		"source_node":    req.SourceNode,
		"target_node":    req.TargetNode,
		"online":         req.Online,
	})
	// Use the VM's DB ID so the enriched audit query can resolve name/vmid.
	resourceType := "vm"
	resourceID := job.ID.String() // fallback to job ID
	if vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: srcClusterID,
		Vmid:      req.VMID,
	}); err == nil {
		resourceID = vm.ID.String()
	}
	h.auditLog(c, srcClusterID, resourceType, resourceID, "migrate_created", detailsJSON)

	return c.Status(fiber.StatusCreated).JSON(toMigrationJobResponse(job))
}

// List handles GET /api/v1/migrations.
func (h *MigrationHandler) List(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	limit := int32(50)
	if l := c.QueryInt("limit", 50); l > 0 && l <= 500 {
		limit = int32(l)
	}
	offset := int32(c.QueryInt("offset", 0))

	jobs, err := h.queries.ListMigrationJobs(c.Context(), db.ListMigrationJobsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list migration jobs")
	}

	resp := make([]migrationJobResponse, len(jobs))
	for i, j := range jobs {
		resp[i] = toMigrationJobResponse(j)
	}

	return c.JSON(resp)
}

// Get handles GET /api/v1/migrations/:id.
func (h *MigrationHandler) Get(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}

	return c.JSON(toMigrationJobResponse(job))
}

// RunCheck handles POST /api/v1/migrations/:id/check.
func (h *MigrationHandler) RunCheck(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	orch := migration.NewOrchestrator(h.queries, h.encryptionKey, nil, h.eventPub)
	report, err := orch.RunPreFlight(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	// Look up the job to get the cluster ID for audit logging.
	if job, jobErr := h.queries.GetMigrationJob(c.Context(), jobID); jobErr == nil {
		h.auditLog(c, job.SourceClusterID, "migration", jobID.String(), "preflight_check", nil)
	}

	return c.JSON(report)
}

// Execute handles POST /api/v1/migrations/:id/execute.
func (h *MigrationHandler) Execute(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}

	if job.Status != migration.StatusPending && job.Status != migration.StatusChecking {
		return fiber.NewError(fiber.StatusConflict, "Job is not in a state that can be executed (status: "+job.Status+")")
	}

	// Get user ID to pass to the orchestrator for audit logging.
	var userID uuid.UUID
	if uid, ok := c.Locals("user_id").(uuid.UUID); ok {
		userID = uid
	}

	orch := migration.NewOrchestrator(h.queries, h.encryptionKey, nil, h.eventPub)

	// Launch execution in background goroutine.
	go orch.Execute(context.Background(), jobID, userID)

	h.eventPub.ClusterEvent(c.Context(), job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "started")

	// Audit log with the VM's DB ID.
	resourceID := jobID.String()
	if vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: job.SourceClusterID,
		Vmid:      job.Vmid,
	}); err == nil {
		resourceID = vm.ID.String()
	}
	h.auditLog(c, job.SourceClusterID, "vm", resourceID, "migrate_started", nil)

	return c.JSON(fiber.Map{
		"status":  "started",
		"job_id":  jobID,
		"message": "Migration started. Poll the job status for progress.",
	})
}

// Cancel handles POST /api/v1/migrations/:id/cancel.
func (h *MigrationHandler) Cancel(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}

	orch := migration.NewOrchestrator(h.queries, h.encryptionKey, nil, h.eventPub)
	if err := orch.Cancel(c.Context(), jobID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to cancel migration job")
	}

	resourceIDCancel := jobID.String()
	if vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: job.SourceClusterID,
		Vmid:      job.Vmid,
	}); err == nil {
		resourceIDCancel = vm.ID.String()
	}
	h.auditLog(c, job.SourceClusterID, "vm", resourceIDCancel, "migrate_cancelled", nil)

	return c.JSON(fiber.Map{"status": "cancelled"})
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/migrations.
func (h *MigrationHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	limit := int32(50)
	if l := c.QueryInt("limit", 50); l > 0 && l <= 500 {
		limit = int32(l)
	}
	offset := int32(c.QueryInt("offset", 0))

	jobs, err := h.queries.ListMigrationJobsByCluster(c.Context(), db.ListMigrationJobsByClusterParams{
		SourceClusterID: clusterID,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list migration jobs")
	}

	resp := make([]migrationJobResponse, len(jobs))
	for i, j := range jobs {
		resp[i] = toMigrationJobResponse(j)
	}

	return c.JSON(resp)
}
