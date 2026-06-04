package handlers

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/migration"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// MigrationConcurrencyLimit caps the number of in-flight user-initiated
// migrations the API server will run as detached goroutines. Excess
// requests are rejected with 429 Too Many Requests so the operator gets
// immediate feedback rather than queuing forever in memory.
const MigrationConcurrencyLimit = 4

// MigrationHandler handles migration job endpoints.
type MigrationHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	// shutdownCtx is the parent for the detached migration goroutine so a
	// graceful SIGTERM aborts in-flight Proxmox calls instead of orphaning
	// the goroutine. Falls back to context.Background() if nil for tests.
	shutdownCtx context.Context
	// slots is a buffered-channel semaphore that caps the number of
	// concurrent in-flight migration goroutines. A non-blocking send on
	// Execute reserves a slot; the goroutine releases on exit.
	slots chan struct{}
}

// NewMigrationHandler creates a new MigrationHandler. shutdownCtx should be
// the per-server context cancelled on SIGTERM; nil falls back to
// context.Background().
func NewMigrationHandler(shutdownCtx context.Context, queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *MigrationHandler {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	return &MigrationHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		shutdownCtx:   shutdownCtx,
		slots:         make(chan struct{}, MigrationConcurrencyLimit),
	}
}

// newOrchestrator builds a migration.Orchestrator wired with the per-server
// Proxmox client cache stashed in fiber Locals by the API middleware.
// Falls back to a cache-less orchestrator if the middleware didn't run
// (e.g. partial-construction tests).
func (h *MigrationHandler) newOrchestrator(c *fiber.Ctx) *migration.Orchestrator {
	orch := migration.NewOrchestrator(h.queries, h.encryptionKey, nil, h.eventPub)
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		orch.SetProxmoxCache(cache)
	}
	return orch
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
		CreatedAt:       j.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:       j.UpdatedAt.Format(time.RFC3339Nano),
	}
	if j.StartedAt.Valid {
		s := j.StartedAt.Time.Format(time.RFC3339Nano)
		r.StartedAt = &s
	}
	if j.CompletedAt.Valid {
		s := j.CompletedAt.Time.Format(time.RFC3339Nano)
		r.CompletedAt = &s
	}
	return r
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

	// Caller needs manage:migration on BOTH source and target clusters.
	if err := requireClusterPerm(c, "manage", "migration", srcClusterID); err != nil {
		return err
	}
	if srcClusterID != tgtClusterID {
		if err := requireClusterPerm(c, "manage", "migration", tgtClusterID); err != nil {
			return err
		}
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(srcClusterID), resourceType, resourceID, "migrate_created", detailsJSON)

	return c.Status(fiber.StatusCreated).JSON(toMigrationJobResponse(job))
}

// List handles GET /api/v1/migrations.
func (h *MigrationHandler) List(c *fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "migration")
	if err != nil {
		return err
	}

	limit := safeconv.Int32(50)
	if l := c.QueryInt("limit", 50); l > 0 && l <= 500 {
		limit = safeconv.Int32(l)
	}
	offset := safeconv.Int32(c.QueryInt("offset", 0))

	jobs, err := h.queries.ListMigrationJobs(c.Context(), db.ListMigrationJobsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list migration jobs")
	}

	resp := make([]migrationJobResponse, 0, len(jobs))
	for _, j := range jobs {
		// A migration straddles two clusters; require visibility on at least
		// one to know it exists, the same as the cluster-detail pages.
		if !access.PermitsCluster(j.SourceClusterID) && !access.PermitsCluster(j.TargetClusterID) {
			continue
		}
		resp = append(resp, toMigrationJobResponse(j))
	}

	return c.JSON(resp)
}

// Get handles GET /api/v1/migrations/:id.
func (h *MigrationHandler) Get(c *fiber.Ctx) error {
	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}

	if err := requireClusterPerm(c, "view", "migration", job.SourceClusterID); err != nil {
		// Allow target-cluster operators to see the job too.
		if errTgt := requireClusterPerm(c, "view", "migration", job.TargetClusterID); errTgt != nil {
			return err
		}
	}

	return c.JSON(toMigrationJobResponse(job))
}

// RunCheck handles POST /api/v1/migrations/:id/check.
func (h *MigrationHandler) RunCheck(c *fiber.Ctx) error {
	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}
	if err := requireClusterPerm(c, "manage", "migration", job.SourceClusterID); err != nil {
		return err
	}
	if job.TargetClusterID != job.SourceClusterID {
		if err := requireClusterPerm(c, "manage", "migration", job.TargetClusterID); err != nil {
			return err
		}
	}

	orch := h.newOrchestrator(c)
	report, err := orch.RunPreFlight(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(report)
}

// Execute handles POST /api/v1/migrations/:id/execute.
func (h *MigrationHandler) Execute(c *fiber.Ctx) error {
	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}

	if err := requireClusterPerm(c, "manage", "migration", job.SourceClusterID); err != nil {
		return err
	}
	if job.TargetClusterID != job.SourceClusterID {
		if err := requireClusterPerm(c, "manage", "migration", job.TargetClusterID); err != nil {
			return err
		}
	}

	if job.Status != migration.StatusPending && job.Status != migration.StatusChecking {
		return fiber.NewError(fiber.StatusConflict, "Job is not in a state that can be executed (status: "+job.Status+")")
	}

	// Get user ID to pass to the orchestrator for audit logging.
	var userID uuid.UUID
	if uid, ok := c.Locals("user_id").(uuid.UUID); ok {
		userID = uid
	}

	// Reserve a concurrency slot up-front. Non-blocking — if the cap is
	// already saturated we reject with 429 rather than queue indefinitely.
	select {
	case h.slots <- struct{}{}:
	default:
		return fiber.NewError(fiber.StatusTooManyRequests,
			"too many migrations in flight (max "+strconv.Itoa(MigrationConcurrencyLimit)+"); wait for one to finish before starting another")
	}

	orch := h.newOrchestrator(c)

	// Launch execution in a background goroutine rooted in shutdownCtx so a
	// graceful shutdown aborts the migration cleanly. Slot is released on
	// exit, including on panic.
	go func() {
		defer func() { <-h.slots }()
		orch.Execute(h.shutdownCtx, jobID, userID)
	}()

	h.eventPub.ClusterEvent(c.Context(), job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "started")

	// Audit log with the VM's DB ID.
	resourceID := jobID.String()
	if vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: job.SourceClusterID,
		Vmid:      job.Vmid,
	}); err == nil {
		resourceID = vm.ID.String()
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(job.SourceClusterID), "vm", resourceID, "migrate_started", nil)

	return c.JSON(fiber.Map{
		"status":  "started",
		"job_id":  jobID,
		"message": "Migration started. Poll the job status for progress.",
	})
}

// Cancel handles POST /api/v1/migrations/:id/cancel.
func (h *MigrationHandler) Cancel(c *fiber.Ctx) error {
	jobID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid migration job ID")
	}

	job, err := h.queries.GetMigrationJob(c.Context(), jobID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Migration job not found")
	}

	if err := requireClusterPerm(c, "manage", "migration", job.SourceClusterID); err != nil {
		return err
	}
	if job.TargetClusterID != job.SourceClusterID {
		if err := requireClusterPerm(c, "manage", "migration", job.TargetClusterID); err != nil {
			return err
		}
	}

	orch := h.newOrchestrator(c)
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(job.SourceClusterID), "vm", resourceIDCancel, "migrate_cancelled", nil)

	return c.JSON(fiber.Map{"status": "cancelled"})
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/migrations.
func (h *MigrationHandler) ListByCluster(c *fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "migration", clusterID); err != nil {
		return err
	}

	limit := safeconv.Int32(50)
	if l := c.QueryInt("limit", 50); l > 0 && l <= 500 {
		limit = safeconv.Int32(l)
	}
	offset := safeconv.Int32(c.QueryInt("offset", 0))

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
