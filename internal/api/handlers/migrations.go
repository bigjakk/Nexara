package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/migration"
	"github.com/bigjakk/nexara/internal/proxmox"
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
func (h *MigrationHandler) newOrchestrator(c fiber.Ctx) *migration.Orchestrator {
	orch := migration.NewOrchestrator(h.queries, h.encryptionKey, nil, h.eventPub)
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		orch.SetProxmoxCache(cache)
	}
	return orch
}

// --- Request / Response types ---

// hasStorageMapEntries reports whether a storage_map object has at least
// one non-empty target — i.e. the caller selected a per-disk destination.
func hasStorageMapEntries(m map[string]string) bool {
	for _, v := range m {
		if v != "" {
			return true
		}
	}
	return false
}

// migrationMapJSON renders a validated object parameter into the JSONB the
// job row stores.
//
// An empty or absent map becomes "{}" rather than null, which is what the
// column has always held and what the orchestrator's json.Unmarshal into a
// StorageMapping expects. A marshal failure cannot happen for a
// map[string]string, so the error is reported the way parseParamUUID
// reports the same class of impossible mistake.
func migrationMapJSON(m map[string]string) (json.RawMessage, error) {
	if len(m) == 0 {
		return json.RawMessage(`{}`), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
	}
	return raw, nil
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
	DiskFormat      string          `json:"disk_format"`
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
		DiskFormat:      j.DiskFormat,
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

// The migration vocabularies, ordered as the docs should list them. They
// were map[string]bool membership tests inside the create handler; the
// declaration's Enum is the same rule stated one layer earlier, where it
// also reaches the API documentation. Ordered slices rather than maps
// because an Enum is rendered, and a map's iteration order is not one a
// reader can rely on.
var (
	// MigrationTypes is the migration_type vocabulary.
	MigrationTypes = []string{migration.TypeIntraCluster, migration.TypeCrossCluster}
	// MigrationVMTypes is the vm_type vocabulary.
	MigrationVMTypes = []string{migration.VMTypeQEMU, migration.VMTypeLXC}
	// MigrationModes is the migration_mode vocabulary.
	MigrationModes = []string{migration.ModeLive, migration.ModeStorage, migration.ModeBoth}
	// MigrationModeDefault is what an omitted migration_mode means, and is
	// the value the handler used to substitute for one.
	MigrationModeDefault = migration.ModeLive
)

// Create handles POST /api/v1/migrations.
func (h *MigrationHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	srcClusterID, err := parseParamUUID(p.String("source_cluster_id"))
	if err != nil {
		return err
	}
	tgtClusterID, err := parseParamUUID(p.String("target_cluster_id"))
	if err != nil {
		return err
	}

	// Caller needs manage:migration on BOTH source and target clusters.
	// Declared Deferred rather than gated in middleware: the two clusters
	// are named in the body, which no middleware can read.
	if err := requireClusterPerm(c, "manage", "migration", srcClusterID); err != nil {
		return err
	}
	if srcClusterID != tgtClusterID {
		if err := requireClusterPerm(c, "manage", "migration", tgtClusterID); err != nil {
			return err
		}
	}

	storageMap, err := stringMap("storage_map", p.Object("storage_map"))
	if err != nil {
		return err
	}
	networkMap, err := stringMap("network_map", p.Object("network_map"))
	if err != nil {
		return err
	}

	sourceNode := p.String("source_node")
	targetNode := p.String("target_node")
	vmid := safeconv.Int32(int(p.Int("vmid")))
	vmType := p.String("vm_type")
	migrationType := p.String("migration_type")
	// The schema's Enum and Default cover the per-parameter rules the
	// handler used to make; everything below is a CROSS-field rule, which no
	// parameter schema can express.
	migrationMode := p.String("migration_mode")
	targetStorage := p.String("target_storage")
	diskFormat := p.String("disk_format")

	// Migration mode only applies to intra-cluster.
	if migrationType == migration.TypeCrossCluster && migrationMode != migration.ModeLive {
		return fiber.NewError(fiber.StatusBadRequest, "migration_mode 'storage' and 'both' are only supported for intra-cluster migrations")
	}

	// Storage and both modes need a destination: either a single target_storage
	// (move every disk there) or a per-disk storage_map (disk key -> storage).
	if migrationMode == migration.ModeStorage || migrationMode == migration.ModeBoth {
		if targetStorage == "" && !hasStorageMapEntries(storageMap) {
			return fiber.NewError(fiber.StatusBadRequest, "target_storage or a per-disk storage_map is required for storage and both migration modes")
		}
	}

	// Same format allowlist the per-disk move endpoint enforces. Kept in the
	// handler rather than copied into the schema as an Enum, for the reason
	// imageFormatParam gives: "" is a meaningful value here and
	// ValidImageFormat is the choke point that already owns the vocabulary.
	// LXC volumes have no format choice, so reject it rather than silently
	// dropping it.
	if !proxmox.ValidImageFormat(diskFormat) {
		return fiber.NewError(fiber.StatusBadRequest, "disk_format must be one of: raw, qcow2, vmdk")
	}
	if diskFormat != "" && vmType == migration.VMTypeLXC {
		return fiber.NewError(fiber.StatusBadRequest, "disk_format is not supported for containers")
	}
	if diskFormat != "" && migrationMode != migration.ModeStorage && migrationMode != migration.ModeBoth {
		return fiber.NewError(fiber.StatusBadRequest, "disk_format only applies to storage and both migration modes")
	}

	// For intra-cluster, source and target must be the same cluster.
	if migrationType == migration.TypeIntraCluster {
		if srcClusterID != tgtClusterID {
			return fiber.NewError(fiber.StatusBadRequest, "For intra-cluster migration, source and target cluster must be the same")
		}
		// Storage mode doesn't need a target node (stays on same node).
		if migrationMode == migration.ModeStorage {
			if targetNode == "" {
				targetNode = sourceNode
			}
		} else if targetNode == "" {
			return fiber.NewError(fiber.StatusBadRequest, "target_node is required for intra-cluster live migration")
		}
	}

	storageMapJSON, err := migrationMapJSON(storageMap)
	if err != nil {
		return err
	}
	networkMapJSON, err := migrationMapJSON(networkMap)
	if err != nil {
		return err
	}

	// Get user ID from context.
	var createdBy pgtype.UUID
	if userID, ok := c.Locals("user_id").(uuid.UUID); ok {
		createdBy = pgtype.UUID{Bytes: userID, Valid: true}
	}

	online := p.Bool("online")
	job, err := h.queries.CreateMigrationJob(c.Context(), db.CreateMigrationJobParams{
		SourceClusterID: srcClusterID,
		TargetClusterID: tgtClusterID,
		SourceNode:      sourceNode,
		TargetNode:      targetNode,
		Vmid:            vmid,
		VmType:          vmType,
		MigrationType:   migrationType,
		StorageMap:      storageMapJSON,
		NetworkMap:      networkMapJSON,
		Online:          online,
		BwlimitKib:      safeconv.Int32(int(p.Int("bwlimit_kib"))),
		DeleteSource:    p.Bool("delete_source"),
		TargetVmid:      safeconv.Int32(int(p.Int("target_vmid"))),
		CreatedBy:       createdBy,
		MigrationMode:   migrationMode,
		TargetStorage:   targetStorage,
		DiskFormat:      diskFormat,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create migration job")
	}

	typeLabel := "VM"
	if vmType == migration.VMTypeLXC {
		typeLabel = "CT"
	}
	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"vmid":           vmid,
		"vm_type":        typeLabel,
		"migration_type": migrationType,
		"source_node":    sourceNode,
		"target_node":    targetNode,
		"online":         online,
	})
	// Use the VM's DB ID so the enriched audit query can resolve name/vmid.
	resourceType := "vm"
	resourceID := job.ID.String() // fallback to job ID
	if vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: srcClusterID,
		Vmid:      vmid,
	}); err == nil {
		resourceID = vm.ID.String()
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(srcClusterID), resourceType, resourceID, "migrate_created", detailsJSON)

	return c.Status(fiber.StatusCreated).JSON(toMigrationJobResponse(job))
}

// List handles GET /api/v1/migrations.
func (h *MigrationHandler) List(c fiber.Ctx, p *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "migration")
	if err != nil {
		return err
	}

	// Scoped in SQL, not after the fetch: LIMIT/OFFSET run before the per-row
	// trim below, so paging over every cluster's jobs and filtering afterwards
	// hands a scoped caller short pages with holes in them. The query applies
	// the same either-end rule the guard below does.
	scope, query := clusterScopeFilter(access)
	if !query {
		return RespondItems(c, []migrationJobResponse{})
	}

	jobs, err := h.queries.ListMigrationJobs(c.Context(), db.ListMigrationJobsParams{
		// The schema bounds and defaults both of these, so the clamp that
		// answered ?limit=5000 with 50 rows and ?offset=-1 from the top is
		// gone: an out-of-range value is now a 400 that names the field.
		Limit:                safeconv.Int32(int(p.Int("limit"))),
		Offset:               safeconv.Int32(int(p.Int("offset"))),
		AccessibleClusterIds: scope,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list migration jobs")
	}

	resp := make([]migrationJobResponse, 0, len(jobs))
	for _, j := range jobs {
		// Defense-in-depth: a migration straddles two clusters; visibility on
		// at least one is enough to know it exists, the same as the
		// cluster-detail pages, and the same rule the SQL scope applies.
		if !access.PermitsCluster(j.SourceClusterID) && !access.PermitsCluster(j.TargetClusterID) {
			continue
		}
		resp = append(resp, toMigrationJobResponse(j))
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/migrations/:id.
//
// The permission is Deferred rather than declared: a migration straddles
// two clusters and the path names neither, so the clusters to authorize
// only exist once the row is loaded.
func (h *MigrationHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	jobID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
//
// Deferred for the reason Get gives: the clusters live on the job row.
func (h *MigrationHandler) RunCheck(c fiber.Ctx, p *apischema.Params) error {
	jobID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
		slog.Error("migration pre-flight failed", "job_id", jobID, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Pre-flight check failed")
	}

	return c.JSON(report)
}

// Execute handles POST /api/v1/migrations/:id/execute.
//
// Deferred for the reason Get gives: the clusters live on the job row.
func (h *MigrationHandler) Execute(c fiber.Ctx, p *apischema.Params) error {
	jobID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
		defer func() {
			if r := recover(); r != nil {
				slog.Error("migration execution panicked", "job_id", jobID, "panic", r)
			}
		}()
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
//
// Deferred for the reason Get gives: the clusters live on the job row.
func (h *MigrationHandler) Cancel(c fiber.Ctx, p *apischema.Params) error {
	jobID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
func (h *MigrationHandler) ListByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	jobs, err := h.queries.ListMigrationJobsByCluster(c.Context(), db.ListMigrationJobsByClusterParams{
		SourceClusterID: clusterID,
		// Bounded and defaulted by the schema; see List for what that
		// replaced.
		Limit:  safeconv.Int32(int(p.Int("limit"))),
		Offset: safeconv.Int32(int(p.Int("offset"))),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list migration jobs")
	}

	resp := make([]migrationJobResponse, len(jobs))
	for i, j := range jobs {
		resp[i] = toMigrationJobResponse(j)
	}

	return RespondItems(c, resp)
}
