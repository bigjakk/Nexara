package handlers

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Orphaned objects, operator overrides, and the per-guest protection card.

type veeamOrphanResponse struct {
	ID                 uuid.UUID  `json:"id"`
	VeeamObjectID      uuid.UUID  `json:"veeam_object_id"`
	SmbiosUUID         string     `json:"smbios_uuid"`
	Name               string     `json:"name"`
	ObjectType         string     `json:"object_type"`
	PlatformName       string     `json:"platform_name"`
	ClusterID          *uuid.UUID `json:"cluster_id"`
	ClusterName        string     `json:"cluster_name"`
	RestorePointsCount int32      `json:"restore_points_count"`
	RestorePointBytes  int64      `json:"restore_point_bytes"`
	LatestRestorePoint *time.Time `json:"latest_restore_point"`
	SizeBytes          int64      `json:"size_bytes"`
	LastRunFailed      bool       `json:"last_run_failed"`
	LastSeenAt         time.Time  `json:"last_seen_at"`
}

// ListOrphanedObjects handles GET /api/v1/veeam-servers/:id/orphaned-objects.
//
// Backup objects whose platform IS mapped to a cluster but which match no
// guest on it: a deleted VM, a template whose name was reused under a new
// uuid, a host rebuilt in place. They are restore points consuming repository
// space for machines that no longer exist in the form that was backed up, and
// the Veeam console does not call them out.
//
// This listing is the other half of correlating on the SMBIOS uuid rather than
// the name. A name-matching coverage view would not have an orphan list at
// all — it would silently report each of these guests as protected, by a
// backup of the machine it replaced.
func (h *VeeamHandler) ListOrphanedObjects(c fiber.Ctx) error {
	// Authorize FIRST, before the server lookup, so an unauthorized caller
	// cannot tell 404 from 403 and probe which server ids exist.
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

	rows, err := h.queries.ListVeeamOrphanedObjects(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list orphaned Veeam objects")
	}

	resp := make([]veeamOrphanResponse, 0, len(rows))
	for _, r := range rows {
		// Every row here has a MAPPED platform by construction, so the scope
		// resolves to a real cluster and a caller scoped elsewhere sees none
		// of it.
		if !scope.permitsPlatform(uuid.UUID(r.PlatformID.Bytes), r.PlatformID.Valid) {
			continue
		}
		row := veeamOrphanResponse{
			ID:                 r.ID,
			VeeamObjectID:      r.VeeamObjectID,
			SmbiosUUID:         r.SmbiosUuid,
			Name:               r.Name,
			ObjectType:         r.ObjectType,
			PlatformName:       r.PlatformName,
			ClusterName:        r.ClusterName.String,
			RestorePointsCount: r.RestorePointsCount,
			RestorePointBytes:  r.RestorePointBytes,
			LatestRestorePoint: optionalTime(r.LatestRestorePoint),
			SizeBytes:          r.SizeBytes,
			LastRunFailed:      r.LastRunFailed,
			LastSeenAt:         r.LastSeenAt,
		}
		if r.ClusterID.Valid {
			id := uuid.UUID(r.ClusterID.Bytes)
			row.ClusterID = &id
		}
		resp = append(resp, row)
	}
	return RespondItems(c, resp)
}

// mapObjectGuestRequest carries the guest to attach, or nulls to hand the row
// back to automatic resolution.
type mapObjectGuestRequest struct {
	ClusterID *uuid.UUID `json:"cluster_id"`
	VMID      *int32     `json:"vmid"`
}

// MapBackupObjectGuest handles
// PUT /api/v1/veeam-servers/:id/backup-objects/:object_id/guest.
//
// The escape hatch for what automatic resolution cannot know: a guest renamed
// since its last backup, one whose SMBIOS uuid was changed, or an orphan an
// operator recognises. The mapping is recorded as match_method 'manual' and no
// sync overwrites it — until the pin goes stale, which
// CorrelateVeeamBackupObjects defines and detects.
//
// The target guest MUST be on the cluster the object's platform is mapped to.
// That is not a convenience restriction: a Veeam platformId IS one Proxmox
// connection, so an object from it cannot belong to a different cluster, and
// allowing one would let a holder scoped to cluster A pull an object carrying
// cluster B's restore-point history onto a guest they can read. It also
// collapses authorization to a single question — manage:veeam on that one
// cluster — instead of a source check and a destination check that can
// disagree.
func (h *VeeamHandler) MapBackupObjectGuest(c fiber.Ctx) error {
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

	var req mapObjectGuestRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	// Both or neither. A cluster with no vmid identifies nothing, and a vmid
	// with no cluster is ambiguous across every cluster the server protects.
	if (req.ClusterID == nil) != (req.VMID == nil) {
		return fiber.NewError(fiber.StatusBadRequest, "cluster_id and vmid must be set together, or both null to clear the mapping")
	}

	object, err := h.queries.GetVeeamBackupObject(c.Context(), db.GetVeeamBackupObjectParams{
		VeeamServerID: server.ID,
		ID:            objectID,
	})
	// 404 rather than 403 for a caller who may not see it: distinguishing the
	// two would confirm the object exists on a cluster they have no access to.
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Backup object not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve the backup object")
	}
	if !scope.permitsPlatform(uuid.UUID(object.PlatformID.Bytes), object.PlatformID.Valid) {
		return fiber.NewError(fiber.StatusNotFound, "Backup object not found")
	}

	// The object's OWN cluster, from its platform mapping — never from its
	// current cluster_id, which a previous manual mapping may itself have set.
	platformCluster, mapped := uuid.Nil, false
	if object.PlatformID.Valid {
		platformCluster, mapped = scope.platformCluster[uuid.UUID(object.PlatformID.Bytes)]
	}
	if !mapped {
		// Nothing is known about which cluster this object belongs to, so
		// there is no cluster-scoped permission to check and no guest that
		// could legitimately be named. 422, not 401 or 403: the request is
		// well-formed and authorized, the server is simply not in a state
		// where it can be honoured.
		return fiber.NewError(fiber.StatusUnprocessableEntity,
			"This backup object's Veeam platform is not mapped to a cluster yet. Map the platform first.")
	}
	if err := requireClusterPerm(c, "manage", "veeam", platformCluster); err != nil {
		return err
	}

	target := pgtype.UUID{}
	targetVMID := pgtype.Int4{}
	guestName := ""
	if req.ClusterID != nil {
		if *req.ClusterID != platformCluster {
			return fiber.NewError(fiber.StatusBadRequest,
				"The guest must be on the cluster this backup object's Veeam platform is mapped to")
		}
		guest, gErr := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
			ClusterID: *req.ClusterID,
			Vmid:      *req.VMID,
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "Guest not found on that cluster")
			}
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve the guest")
		}
		guestName = guest.Name
		target = pgtype.UUID{Bytes: *req.ClusterID, Valid: true}
		targetVMID = pgtype.Int4{Int32: *req.VMID, Valid: true}
	}

	updated, err := h.queries.SetVeeamBackupObjectGuest(c.Context(), db.SetVeeamBackupObjectGuestParams{
		VeeamServerID: server.ID,
		ID:            objectID,
		ClusterID:     target,
		Vmid:          targetVMID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Backup object not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to map the backup object")
	}

	// Clearing hands the row back to automatic resolution, so re-run it now
	// rather than leaving the object unattributed until the next sync tick.
	if req.ClusterID == nil {
		if _, cErr := h.queries.CorrelateVeeamBackupObjects(c.Context(), server.ID); cErr != nil {
			slog.Warn("veeam: re-correlating after clearing a manual mapping failed",
				"veeam_server_id", server.ID, "object_id", objectID, "error", cErr)
		}
	}

	action := "veeam_object_unmapped"
	if req.ClusterID != nil {
		action = "veeam_object_mapped"
	}
	h.audit(c, server, action, map[string]any{
		"object":     auditSafe(updated.Name),
		"object_id":  objectID.String(),
		"cluster_id": clusterIDForAudit(req.ClusterID),
		"vmid":       vmidForAudit(req.VMID),
		"guest_name": guestName,
	})

	return c.JSON(fiber.Map{
		"id":           updated.ID,
		"name":         updated.Name,
		"cluster_id":   clusterIDForAudit(req.ClusterID),
		"vmid":         vmidForAudit(req.VMID),
		"match_method": updated.MatchMethod,
	})
}

// vmidForAudit renders an optional vmid, so clearing a mapping records an
// explicit null rather than a 0 that reads like a real guest.
func vmidForAudit(vmid *int32) any {
	if vmid == nil {
		return nil
	}
	return *vmid
}

type veeamGuestProtectionResponse struct {
	Protected          bool                        `json:"protected"`
	LatestRestorePoint *time.Time                  `json:"latest_restore_point"`
	MalwareStatus      string                      `json:"malware_status"`
	ObjectCount        int64                       `json:"object_count"`
	RestorePointCount  int64                       `json:"restore_point_count"`
	RestorePointBytes  int64                       `json:"restore_point_bytes"`
	MatchMethod        string                      `json:"match_method"`
	LastRunFailed      bool                        `json:"last_run_failed"`
	RestorePoints      []veeamGuestRestorePointRow `json:"restore_points"`
}

type veeamGuestRestorePointRow struct {
	ID            uuid.UUID `json:"id"`
	VeeamID       uuid.UUID `json:"veeam_id"`
	Name          string    `json:"name"`
	PointType     string    `json:"point_type"`
	MalwareStatus string    `json:"malware_status"`
	GuestOSFamily string    `json:"guest_os_family"`
	CreationTime  time.Time `json:"creation_time"`
	SizeBytes     int64     `json:"size_bytes"`
	SupportsFLR   bool      `json:"supports_flr"`
	ObjectName    string    `json:"object_name"`
}

// maxVeeamGuestRestorePoints bounds the history one guest's card renders. A
// guest on daily, weekly and monthly jobs accumulates hundreds of points, and
// the card shows recent recoverability, not an archive index.
const maxVeeamGuestRestorePoints = 100

// GetGuestVeeamProtection handles
// GET /api/v1/clusters/:cluster_id/vms/:vm_id/veeam.
//
// The VM detail page's Veeam card. Takes the guest's UUID like every sibling
// route, and resolves it to the stable (cluster_id, vmid) identity for the
// lookup — the UUID is a request-time handle, never a stored key, because the
// collector re-mints it whenever it churns the guest row.
func (h *VeeamHandler) GetGuestVeeamProtection(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	// view:veeam on the cluster, not view:vm: this is Veeam data, and a
	// caller who may see the guest is not thereby entitled to its backup
	// posture.
	if err := requireClusterPerm(c, "view", "veeam", clusterID); err != nil {
		return err
	}

	vmID, err := uuid.Parse(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}
	vm, err := h.queries.GetVM(c.Context(), vmID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get VM")
	}
	// A guest id from another cluster must not be readable through this
	// cluster's authorization.
	if vm.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "VM not found")
	}

	summary, err := h.queries.GetVeeamGuestProtection(c.Context(), db.GetVeeamGuestProtectionParams{
		ClusterID: clusterID,
		Vmid:      vm.Vmid,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load Veeam protection")
	}

	resp := veeamGuestProtectionResponse{
		// Restore points, not backup objects. A guest whose object survives
		// but whose points have all been pruned is NOT protected, and that
		// state — Veeam knows the guest and can restore nothing — is the one
		// most worth getting right.
		Protected:          summary.RestorePointCount > 0,
		LatestRestorePoint: optionalTime(summary.LatestRestorePoint),
		MalwareStatus:      summary.LatestMalwareStatus,
		ObjectCount:        summary.ObjectCount,
		RestorePointCount:  summary.RestorePointCount,
		RestorePointBytes:  summary.RestorePointBytes,
		MatchMethod:        summary.MatchMethod,
		LastRunFailed:      summary.LastRunFailed,
		RestorePoints:      []veeamGuestRestorePointRow{},
	}

	points, err := h.queries.ListVeeamRestorePointsForGuest(c.Context(), db.ListVeeamRestorePointsForGuestParams{
		ClusterID: clusterID,
		Vmid:      vm.Vmid,
		RowLimit:  maxVeeamGuestRestorePoints,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load restore points")
	}
	for _, p := range points {
		resp.RestorePoints = append(resp.RestorePoints, veeamGuestRestorePointRow{
			ID:            p.ID,
			VeeamID:       p.VeeamID,
			Name:          p.Name,
			PointType:     p.PointType,
			MalwareStatus: p.MalwareStatus,
			GuestOSFamily: p.GuestOsFamily,
			CreationTime:  p.CreationTime,
			SizeBytes:     p.SizeBytes,
			SupportsFLR:   p.SupportsFlr,
			ObjectName:    p.ObjectName,
		})
	}

	return c.JSON(resp)
}
