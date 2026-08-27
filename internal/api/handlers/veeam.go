package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Read endpoints for the inventory the collector fills in.
//
// SCOPING. Repositories are GLOBAL: one repository holds every cluster's
// backups, so there is no cluster to attribute it to. Everything else is
// per-cluster, resolved through veeam_platforms — a Veeam platformId is one
// Proxmox connection, and it is the only cluster discriminator the API
// exposes.
//
// Until an operator confirms a platform's cluster mapping, cluster_id is NULL
// and those rows are UNATTRIBUTABLE. Unattributable means global-only, not
// visible-to-all: a caller scoped to one cluster must not see rows that might
// belong to another. That is the same posture a standalone PBS server takes,
// and it is what makes this fail closed today — before the mapping exists,
// only a holder of global view:veeam sees any of it.

// veeamScope decides which Veeam rows a caller may see on one server.
type veeamScope struct {
	access clusterAccess
	// platformCluster maps a Veeam platformId to the Nexara cluster an
	// operator mapped it to. A platform absent from the map, or present with
	// no cluster, is unattributable.
	platformCluster map[uuid.UUID]uuid.UUID
}

// permitsPlatform reports whether the caller may see rows carrying this
// platform.
//
// A NULL platform is not "harmless metadata" — a job that has never run has no
// platform yet, and could belong to any cluster. Global-only is the honest
// answer.
func (s veeamScope) permitsPlatform(platform uuid.UUID, valid bool) bool {
	if !valid {
		return s.access.HasGlobal
	}
	clusterID, mapped := s.platformCluster[platform]
	if !mapped {
		return s.access.HasGlobal
	}
	return s.access.PermitsCluster(clusterID)
}

// clusterFor resolves the cluster a platform maps to, as a nullable column
// value — the NULL pgtype.UUID for an unattributable row.
//
// Used to file an audit row under the cluster whose workload an action
// touched. A cluster-scoped operator reads audit_log filtered by cluster, so a
// job run filed globally is invisible to exactly the person whose guests it
// just affected.
func (s veeamScope) clusterFor(platform uuid.UUID, valid bool) pgtype.UUID {
	if !valid {
		return pgtype.UUID{}
	}
	clusterID, mapped := s.platformCluster[platform]
	if !mapped {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: clusterID, Valid: true}
}

// veeamServerIDFromParam parses :id without touching the database, so the
// permission check can run before the lookup.
func veeamServerIDFromParam(c fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return uuid.Nil, fiber.NewError(fiber.StatusBadRequest, "Invalid Veeam server ID")
	}
	return id, nil
}

// scopedPlatforms returns the platform ids a caller may see, and whether the
// listing should be restricted at all.
//
// nil means unrestricted (a global holder). A non-nil slice — including an
// empty one — restricts to exactly those platforms. The nil/non-nil split
// matters: pgx sends a nil slice as SQL NULL, which the query reads as "no
// restriction", while an empty non-nil slice becomes '{}' and matches nothing.
// Returning nil for a caller with no grants would lift the filter for exactly
// the caller who should see least.
func (s veeamScope) scopedPlatforms() []uuid.UUID {
	if s.access.HasGlobal {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(s.platformCluster))
	for platformID, clusterID := range s.platformCluster {
		if s.access.PermitsCluster(clusterID) {
			ids = append(ids, platformID)
		}
	}
	return ids
}

// veeamScopeFor builds the scope for one server.
//
// accessibleClusters IS the permission gate here — there is deliberately no
// requirePerm in front of it. requirePerm resolves a GLOBAL grant only, so
// putting it first would 403 every cluster-scoped Veeam viewer before the
// scoping below ever ran, and would leave permitsPlatform as dead code that
// could only ever answer "yes". PBSHandler.List and TaskHandler.List take the
// same shape for the same reason.
func (h *VeeamHandler) veeamScopeFor(c fiber.Ctx, serverID uuid.UUID) (veeamScope, error) {
	return h.veeamScopeForAction(c, "view", serverID)
}

// veeamScopeForAction is veeamScopeFor for a verb other than view. Job control
// resolves "execute" through exactly the same platform mapping, so the two
// share one implementation rather than growing a second copy of the
// unattributable-is-global rule that could drift from it.
func (h *VeeamHandler) veeamScopeForAction(c fiber.Ctx, action string, serverID uuid.UUID) (veeamScope, error) {
	access, err := accessibleClusters(c, action, "veeam")
	if err != nil {
		return veeamScope{}, err
	}
	// No grant at all is a refusal, not an empty list: without this the
	// server lookup that follows would let an unauthorized caller probe which
	// server ids exist by telling 404 from 200.
	if !access.HasGlobal && len(access.Allowed) == 0 {
		return veeamScope{}, fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	platforms, err := h.queries.ListVeeamPlatformsByServer(c.Context(), serverID)
	if err != nil {
		return veeamScope{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve Veeam platforms")
	}

	mapping := make(map[uuid.UUID]uuid.UUID, len(platforms))
	for _, p := range platforms {
		if p.ClusterID.Valid {
			mapping[p.PlatformID] = uuid.UUID(p.ClusterID.Bytes)
		}
	}
	return veeamScope{access: access, platformCluster: mapping}, nil
}

type veeamRepositoryResponse struct {
	ID            uuid.UUID `json:"id"`
	VeeamID       uuid.UUID `json:"veeam_id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	HostName      string    `json:"host_name"`
	Path          string    `json:"path"`
	CapacityBytes int64     `json:"capacity_bytes"`
	FreeBytes     int64     `json:"free_bytes"`
	UsedBytes     int64     `json:"used_bytes"`
	IsOnline      bool      `json:"is_online"`
	IsOutOfDate   bool      `json:"is_out_of_date"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// ListRepositories handles GET /api/v1/veeam-servers/:id/repositories.
//
// Global scope: a repository is genuinely shared — one holds the backups of
// every cluster the server protects — so there is no cluster to scope it to.
func (h *VeeamHandler) ListRepositories(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "veeam"); err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	repos, err := h.queries.ListVeeamRepositoriesByServer(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam repositories")
	}

	resp := make([]veeamRepositoryResponse, len(repos))
	for i, r := range repos {
		resp[i] = veeamRepositoryResponse{
			ID:            r.ID,
			VeeamID:       r.VeeamID,
			Name:          r.Name,
			Type:          r.RepoType,
			HostName:      r.HostName,
			Path:          r.Path,
			CapacityBytes: r.CapacityBytes,
			FreeBytes:     r.FreeBytes,
			UsedBytes:     r.UsedBytes,
			IsOnline:      r.IsOnline,
			IsOutOfDate:   r.IsOutOfDate,
			LastSeenAt:    r.LastSeenAt,
		}
	}
	return RespondItems(c, resp)
}

type veeamRepositoryMetricResponse struct {
	Time          time.Time `json:"time"`
	CapacityBytes int64     `json:"capacity_bytes"`
	FreeBytes     int64     `json:"free_bytes"`
	UsedBytes     int64     `json:"used_bytes"`
}

// GetRepositoryMetrics handles
// GET /api/v1/veeam-servers/:id/repositories/:repository_id/metrics.
func (h *VeeamHandler) GetRepositoryMetrics(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "veeam"); err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	repositoryID, err := uuid.Parse(c.Params("repository_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid repository ID")
	}

	since := time.Now().Add(-parseVeeamRange(c.Query("range")))
	rows, err := h.queries.GetVeeamRepositoryMetrics(c.Context(), db.GetVeeamRepositoryMetricsParams{
		VeeamServerID:     server.ID,
		RepositoryVeeamID: repositoryID,
		Bucket:            since,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load repository metrics")
	}

	resp := make([]veeamRepositoryMetricResponse, len(rows))
	for i, r := range rows {
		resp[i] = veeamRepositoryMetricResponse{
			Time:          r.Time,
			CapacityBytes: r.CapacityBytes,
			FreeBytes:     r.FreeBytes,
			UsedBytes:     r.UsedBytes,
		}
	}
	return RespondItems(c, resp)
}

// parseVeeamRange maps the ?range= shorthand to a lookback window, defaulting
// to 7 days. Anything unrecognised gets the default rather than an error: a
// chart with a sensible window beats a 400.
func parseVeeamRange(raw string) time.Duration {
	switch raw {
	case "24h":
		return 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	case "90d":
		return 90 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

type veeamJobResponse struct {
	ID              uuid.UUID  `json:"id"`
	VeeamID         uuid.UUID  `json:"veeam_id"`
	Name            string     `json:"name"`
	JobType         string     `json:"job_type"`
	Workload        string     `json:"workload"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	LastResult      string     `json:"last_result"`
	LastRun         *time.Time `json:"last_run"`
	NextRun         *time.Time `json:"next_run"`
	NextRunPolicy   string     `json:"next_run_policy"`
	RepositoryName  string     `json:"repository_name"`
	ObjectsCount    int32      `json:"objects_count"`
	ProgressPercent int32      `json:"progress_percent"`
	Bottleneck      string     `json:"bottleneck"`
	Duration        string     `json:"duration"`
	ProcessingRate  string     `json:"processing_rate"`
	ProcessedSize   int64      `json:"processed_size"`
	ReadSize        int64      `json:"read_size"`
	TransferredSize int64      `json:"transferred_size"`
	ClusterID       *uuid.UUID `json:"cluster_id"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
}

// ListJobs handles GET /api/v1/veeam-servers/:id/jobs.
func (h *VeeamHandler) ListJobs(c fiber.Ctx) error {
	// Authorize FIRST. Looking the server up before the permission check let
	// an unauthorized caller tell 404 from 403 and so probe which server ids
	// exist, which is the oracle veeamScopeFor's own refusal exists to close.
	serverID, err := veeamServerIDFromParam(c)
	if err != nil {
		return err
	}
	scope, err := h.veeamScopeFor(c, serverID)
	if err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	jobs, err := h.queries.ListVeeamJobsByServer(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam jobs")
	}

	resp := make([]veeamJobResponse, 0, len(jobs))
	for _, j := range jobs {
		platform := uuid.UUID(j.PlatformID.Bytes)
		if !scope.permitsPlatform(platform, j.PlatformID.Valid) {
			continue
		}
		row := veeamJobResponse{
			ID:              j.ID,
			VeeamID:         j.VeeamID,
			Name:            j.Name,
			JobType:         j.JobType,
			Workload:        j.Workload,
			Description:     j.Description,
			Status:          j.Status,
			LastResult:      j.LastResult,
			LastRun:         optionalTime(j.LastRun),
			NextRun:         optionalTime(j.NextRun),
			NextRunPolicy:   j.NextRunPolicy,
			RepositoryName:  j.RepositoryName,
			ObjectsCount:    j.ObjectsCount,
			ProgressPercent: j.ProgressPercent,
			Bottleneck:      j.Bottleneck,
			Duration:        j.Duration,
			ProcessingRate:  j.ProcessingRate,
			ProcessedSize:   j.ProcessedSize,
			ReadSize:        j.ReadSize,
			TransferredSize: j.TransferredSize,
			LastSeenAt:      j.LastSeenAt,
		}
		if j.PlatformID.Valid {
			if clusterID, ok := scope.platformCluster[platform]; ok {
				row.ClusterID = &clusterID
			}
		}
		resp = append(resp, row)
	}
	return RespondItems(c, resp)
}

type veeamSessionResponse struct {
	ID              uuid.UUID  `json:"id"`
	VeeamID         uuid.UUID  `json:"veeam_id"`
	Name            string     `json:"name"`
	State           string     `json:"state"`
	Result          string     `json:"result"`
	ResultMessage   string     `json:"result_message"`
	Algorithm       string     `json:"algorithm"`
	Bottleneck      string     `json:"bottleneck"`
	Duration        string     `json:"duration"`
	ProcessingRate  string     `json:"processing_rate"`
	ProcessedSize   int64      `json:"processed_size"`
	ReadSize        int64      `json:"read_size"`
	TransferredSize int64      `json:"transferred_size"`
	ProgressPercent int32      `json:"progress_percent"`
	CreationTime    time.Time  `json:"creation_time"`
	EndTime         *time.Time `json:"end_time"`
	InitiatedBy     string     `json:"initiated_by"`
	NexaraInitiated bool       `json:"nexara_initiated"`
	ClusterID       *uuid.UUID `json:"cluster_id"`
}

// maxVeeamSessionRows bounds one listing. Sessions accumulate at roughly one
// row per job run, so a few hundred covers weeks of history for a real
// install without handing the browser an unbounded response.
const maxVeeamSessionRows = 500

// ListSessions handles GET /api/v1/veeam-servers/:id/sessions.
func (h *VeeamHandler) ListSessions(c fiber.Ctx) error {
	// Authorize FIRST. Looking the server up before the permission check let
	// an unauthorized caller tell 404 from 403 and so probe which server ids
	// exist, which is the oracle veeamScopeFor's own refusal exists to close.
	serverID, err := veeamServerIDFromParam(c)
	if err != nil {
		return err
	}
	scope, err := h.veeamScopeFor(c, serverID)
	if err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	sessions, err := h.queries.ListVeeamSessionsByServer(c.Context(), db.ListVeeamSessionsByServerParams{
		VeeamServerID: server.ID,
		PlatformIds:   scope.scopedPlatforms(),
		RowLimit:      maxVeeamSessionRows,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam sessions")
	}

	resp := make([]veeamSessionResponse, 0, len(sessions))
	for _, s := range sessions {
		platform := uuid.UUID(s.PlatformID.Bytes)
		if !scope.permitsPlatform(platform, s.PlatformID.Valid) {
			continue
		}
		row := veeamSessionResponse{
			ID:              s.ID,
			VeeamID:         s.VeeamID,
			Name:            s.Name,
			State:           s.State,
			Result:          s.Result,
			ResultMessage:   s.ResultMessage,
			Algorithm:       s.Algorithm,
			Bottleneck:      s.Bottleneck,
			Duration:        s.Duration,
			ProcessingRate:  s.ProcessingRate,
			ProcessedSize:   s.ProcessedSize,
			ReadSize:        s.ReadSize,
			TransferredSize: s.TransferredSize,
			ProgressPercent: s.ProgressPercent,
			CreationTime:    s.CreationTime,
			EndTime:         optionalTime(s.EndTime),
			InitiatedBy:     s.InitiatedBy,
			NexaraInitiated: s.NexaraInitiated,
		}
		if s.PlatformID.Valid {
			if clusterID, ok := scope.platformCluster[platform]; ok {
				row.ClusterID = &clusterID
			}
		}
		resp = append(resp, row)
	}
	return RespondItems(c, resp)
}

type veeamBackupObjectResponse struct {
	ID                 uuid.UUID  `json:"id"`
	VeeamObjectID      uuid.UUID  `json:"veeam_object_id"`
	SmbiosUUID         string     `json:"smbios_uuid"`
	Name               string     `json:"name"`
	ObjectType         string     `json:"object_type"`
	RestorePointsCount int32      `json:"restore_points_count"`
	SizeBytes          int64      `json:"size_bytes"`
	LastRunFailed      bool       `json:"last_run_failed"`
	ClusterID          *uuid.UUID `json:"cluster_id"`
	LastSeenAt         time.Time  `json:"last_seen_at"`
}

// ListBackupObjects handles GET /api/v1/veeam-servers/:id/backup-objects.
//
// One row per guest — the collector folds Veeam's (guest × backup) listing.
// Two rows can still share a NAME without being the same guest: that is a
// rebuilt guest whose replacement reused its name, and telling them apart is
// exactly what the SMBIOS uuid is for.
func (h *VeeamHandler) ListBackupObjects(c fiber.Ctx) error {
	// Authorize FIRST. Looking the server up before the permission check let
	// an unauthorized caller tell 404 from 403 and so probe which server ids
	// exist, which is the oracle veeamScopeFor's own refusal exists to close.
	serverID, err := veeamServerIDFromParam(c)
	if err != nil {
		return err
	}
	scope, err := h.veeamScopeFor(c, serverID)
	if err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	objects, err := h.queries.ListVeeamBackupObjectsByServer(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam backup objects")
	}

	resp := make([]veeamBackupObjectResponse, 0, len(objects))
	for _, o := range objects {
		platform := uuid.UUID(o.PlatformID.Bytes)
		if !scope.permitsPlatform(platform, o.PlatformID.Valid) {
			continue
		}
		row := veeamBackupObjectResponse{
			ID:                 o.ID,
			VeeamObjectID:      o.VeeamObjectID,
			SmbiosUUID:         o.SmbiosUuid,
			Name:               o.Name,
			ObjectType:         o.ObjectType,
			RestorePointsCount: o.RestorePointsCount,
			SizeBytes:          o.SizeBytes,
			LastRunFailed:      o.LastRunFailed,
			LastSeenAt:         o.LastSeenAt,
		}
		if o.PlatformID.Valid {
			if clusterID, ok := scope.platformCluster[platform]; ok {
				row.ClusterID = &clusterID
			}
		}
		resp = append(resp, row)
	}
	return RespondItems(c, resp)
}

type veeamRestorePointResponse struct {
	ID            uuid.UUID `json:"id"`
	VeeamID       uuid.UUID `json:"veeam_id"`
	Name          string    `json:"name"`
	PointType     string    `json:"point_type"`
	MalwareStatus string    `json:"malware_status"`
	GuestOSFamily string    `json:"guest_os_family"`
	CreationTime  time.Time `json:"creation_time"`
	SizeBytes     int64     `json:"size_bytes"`
	SupportsFLR   bool      `json:"supports_flr"`
}

// ListRestorePoints handles
// GET /api/v1/veeam-servers/:id/backup-objects/:object_id/restore-points.
//
// Scoped through the parent object, so a caller who cannot see the guest
// cannot enumerate its recovery history either.
func (h *VeeamHandler) ListRestorePoints(c fiber.Ctx) error {
	// Authorize FIRST. Looking the server up before the permission check let
	// an unauthorized caller tell 404 from 403 and so probe which server ids
	// exist, which is the oracle veeamScopeFor's own refusal exists to close.
	serverID, err := veeamServerIDFromParam(c)
	if err != nil {
		return err
	}
	scope, err := h.veeamScopeFor(c, serverID)
	if err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	objectID, err := uuid.Parse(c.Params("object_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid backup object ID")
	}

	objects, err := h.queries.ListVeeamBackupObjectsByServer(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve the backup object")
	}

	var parent *db.VeeamBackupObject
	for i := range objects {
		if objects[i].ID == objectID {
			parent = &objects[i]
			break
		}
	}
	if parent == nil {
		return fiber.NewError(fiber.StatusNotFound, "Backup object not found")
	}
	// 404 rather than 403 for a caller who may not see it: distinguishing the
	// two would confirm the guest exists on a cluster they have no access to.
	if !scope.permitsPlatform(uuid.UUID(parent.PlatformID.Bytes), parent.PlatformID.Valid) {
		return fiber.NewError(fiber.StatusNotFound, "Backup object not found")
	}

	points, err := h.queries.ListVeeamRestorePointsByObject(c.Context(), parent.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list restore points")
	}

	resp := make([]veeamRestorePointResponse, len(points))
	for i, p := range points {
		resp[i] = veeamRestorePointResponse{
			ID:            p.ID,
			VeeamID:       p.VeeamID,
			Name:          p.Name,
			PointType:     p.PointType,
			MalwareStatus: p.MalwareStatus,
			GuestOSFamily: p.GuestOsFamily,
			CreationTime:  p.CreationTime,
			SizeBytes:     p.SizeBytes,
			SupportsFLR:   p.SupportsFlr,
		}
	}
	return RespondItems(c, resp)
}

// optionalTime unwraps a nullable timestamp into a pointer, so an absent value
// serializes as JSON null rather than as the year 1 — "never ran" and "ran at
// 0001-01-01" read very differently in a UI.
func optionalTime(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}
