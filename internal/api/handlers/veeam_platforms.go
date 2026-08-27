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

// Platform mapping — the operator-confirmed link between a Veeam "platform"
// (one Proxmox connection, identified by a platformId that every backup
// object, restore point and session from that cluster carries) and a Nexara
// cluster.
//
// This mapping is load-bearing for authorization, not just for display. Every
// cluster-scoped Veeam permission resolves through it: until a platform is
// mapped, its rows are unattributable and only a holder of GLOBAL view:veeam
// sees them at all. Getting it wrong in the other direction would show one
// cluster's backups to a viewer scoped to another, which is why nothing
// derives it automatically except the single unambiguous case — an install
// with exactly one active cluster, mapped by UpsertVeeamPlatform when the
// platform is first discovered and never re-applied afterwards. Veeam exposes
// no field naming the Nexara cluster, so any richer guess would be exactly
// that.

type veeamPlatformResponse struct {
	PlatformID  uuid.UUID  `json:"platform_id"`
	DisplayName string     `json:"display_name"`
	ClusterID   *uuid.UUID `json:"cluster_id"`
	ClusterName string     `json:"cluster_name"`
	ObjectCount int64      `json:"object_count"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
}

// ListPlatforms handles GET /api/v1/veeam-servers/:id/platforms.
//
// Global view:veeam, like the repository listing: the answer spans clusters by
// construction — it is the list of clusters this server protects, including
// ones the caller may hold no grant on — so there is no cluster to scope it
// to, and a partial answer would make the mapping UI silently incomplete.
func (h *VeeamHandler) ListPlatforms(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "veeam"); err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	rows, err := h.queries.ListVeeamPlatformsWithCluster(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam platforms")
	}

	resp := make([]veeamPlatformResponse, len(rows))
	for i, r := range rows {
		resp[i] = veeamPlatformResponse{
			PlatformID:  r.PlatformID,
			DisplayName: r.DisplayName,
			ClusterName: r.ClusterName.String,
			ObjectCount: r.ObjectCount,
			LastSeenAt:  r.LastSeenAt,
		}
		if r.ClusterID.Valid {
			id := uuid.UUID(r.ClusterID.Bytes)
			resp[i].ClusterID = &id
		}
	}
	return RespondItems(c, resp)
}

// mapPlatformRequest carries the cluster to attach, or null to detach.
//
// A pointer so "cluster_id": null and an omitted key are both expressible and
// both mean detach — the UI's "not mapped" option sends null, and treating a
// missing field as "leave alone" would make the only way to unmap it a
// separate endpoint.
type mapPlatformRequest struct {
	ClusterID *uuid.UUID `json:"cluster_id"`
}

// MapPlatform handles PUT /api/v1/veeam-servers/:id/platforms/:platform_id.
//
// Gated on GLOBAL manage:veeam. Deliberately not on manage:veeam for the
// target cluster: this call decides which cluster a body of backup data is
// attributed to, so a holder scoped to cluster A could otherwise point a
// platform holding cluster B's guests at A and read it.
func (h *VeeamHandler) MapPlatform(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "veeam"); err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}
	platformID, err := uuid.Parse(c.Params("platform_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid platform ID")
	}

	var req mapPlatformRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	clusterName := ""
	target := pgtype.UUID{}
	if req.ClusterID != nil {
		// Resolved before the write so an unknown cluster is a 404 rather
		// than a foreign-key 500, and so the audit row can name the cluster
		// an operator actually chose.
		cluster, cErr := h.queries.GetCluster(c.Context(), *req.ClusterID)
		if cErr != nil {
			if errors.Is(cErr, pgx.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
			}
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve the cluster")
		}
		clusterName = cluster.Name
		target = pgtype.UUID{Bytes: cluster.ID, Valid: true}
	}

	updated, err := h.queries.SetVeeamPlatformCluster(c.Context(), db.SetVeeamPlatformClusterParams{
		VeeamServerID: server.ID,
		PlatformID:    platformID,
		ClusterID:     target,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The platform is discovered by the sync, not created here, so an
			// id nobody has observed is genuinely absent rather than a
			// validation failure.
			return fiber.NewError(fiber.StatusNotFound, "Veeam platform not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to map the Veeam platform")
	}

	// Re-correlate immediately rather than waiting for the next sync tick.
	// This is one idempotent UPDATE against the local database — no Veeam
	// call, nothing that needs the collector leader — and running it here is
	// what makes the mapping produce a visible result on the operator's very
	// next page load instead of up to a sync interval later.
	//
	// Safe to run before the SMBIOS pass has visited the newly attached
	// cluster: correlation's name tier requires an affirmative "this guest
	// has no SMBIOS uuid" record, so guests nobody has scanned yet resolve to
	// unattributed rather than being name-matched. Without that rule this
	// call would match every object on the cluster at once, orphaned backups
	// of replaced machines included, and briefly claim protection that does
	// not exist.
	//
	// A failure here is logged by the sync that will redo it; it must not
	// fail a mapping that has already been written.
	if _, cErr := h.queries.CorrelateVeeamBackupObjects(c.Context(), server.ID); cErr != nil {
		slog.Warn("veeam: re-correlating after a platform mapping failed",
			"veeam_server_id", server.ID, "platform_id", platformID, "error", cErr)
	}

	action := "veeam_platform_unmapped"
	if req.ClusterID != nil {
		action = "veeam_platform_mapped"
	}
	h.audit(c, server, action, map[string]any{
		"platform_id":  platformID.String(),
		"platform":     auditSafe(updated.DisplayName),
		"cluster_id":   clusterIDForAudit(req.ClusterID),
		"cluster_name": clusterName,
	})

	resp := veeamPlatformResponse{
		PlatformID:  updated.PlatformID,
		DisplayName: updated.DisplayName,
		ClusterName: clusterName,
		LastSeenAt:  updated.LastSeenAt,
	}
	if updated.ClusterID.Valid {
		id := uuid.UUID(updated.ClusterID.Bytes)
		resp.ClusterID = &id
	}
	return c.JSON(resp)
}

// clusterIDForAudit renders an optional cluster id for an audit detail blob,
// so an unmap records an explicit null rather than omitting the key and
// reading as "the mapping was not part of this change".
func clusterIDForAudit(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

type veeamInfrastructureResponse struct {
	ID          uuid.UUID  `json:"id"`
	VeeamRef    uuid.UUID  `json:"veeam_ref"`
	Role        string     `json:"role"`
	Name        string     `json:"name"`
	HostName    string     `json:"host_name"`
	IsDisabled  bool       `json:"is_disabled"`
	IsOnline    bool       `json:"is_online"`
	ClusterID   *uuid.UUID `json:"cluster_id"`
	ClusterName string     `json:"cluster_name"`
	VMID        *int32     `json:"vmid"`
	GuestName   string     `json:"guest_name"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
}

// ListInfrastructure handles GET /api/v1/veeam-servers/:id/infrastructure.
//
// The guests that belong to the Veeam deployment rather than to the workload
// it protects: worker appliances, and the VBR server when it runs on the
// cluster it protects. Coverage excludes them, and this is where an operator
// checks what Nexara decided to exclude and why — an exclusion nobody can
// inspect is indistinguishable from a coverage bug.
//
// Global view:veeam, like the repository and platform listings: the answer
// spans every cluster the server protects.
func (h *VeeamHandler) ListInfrastructure(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "veeam"); err != nil {
		return err
	}
	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	rows, err := h.queries.ListVeeamInfrastructureByServer(c.Context(), server.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam infrastructure")
	}

	resp := make([]veeamInfrastructureResponse, len(rows))
	for i, r := range rows {
		resp[i] = veeamInfrastructureResponse{
			ID:          r.ID,
			VeeamRef:    r.VeeamRef,
			Role:        r.Role,
			Name:        r.Name,
			HostName:    r.HostName,
			IsDisabled:  r.IsDisabled,
			IsOnline:    r.IsOnline,
			ClusterName: r.ClusterName.String,
			GuestName:   r.GuestName.String,
			LastSeenAt:  r.LastSeenAt,
		}
		if r.ClusterID.Valid {
			id := uuid.UUID(r.ClusterID.Bytes)
			resp[i].ClusterID = &id
		}
		if r.Vmid.Valid {
			vmid := r.Vmid.Int32
			resp[i].VMID = &vmid
		}
	}
	return RespondItems(c, resp)
}
