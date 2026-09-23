package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// Both routes are declared in internal/api/registry_guest_snapshots.go, which
// states their permission and their parameters. The listing is Advisory —
// nothing gates it and the filtering below IS the authorization — and the
// resync carries an Alternatives gate that passes on EITHER guest permission.
//
// What stays here is the half neither shape can express: the PRECISE per-class
// check on the resync, which is not knowable until the guest row is read, and
// which answers 404 rather than 403 so a caller holding only the other class
// cannot learn that a guest with this vmid exists.

// maxSnapshotsPerGuest bounds a single guest's snapshot listing before the
// resync upsert loop runs — a plausibility cap, not a product limit.
const maxSnapshotsPerGuest = 1000

// GuestSnapshotHandler serves the central guest snapshot inventory collected
// by the snapshot sync loop (internal/collector/snapshot_sync.go) and offers
// an on-demand per-guest resync so the UI can refresh a single guest without
// waiting for the next collector pass.
type GuestSnapshotHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewGuestSnapshotHandler creates a new guest snapshot handler.
func NewGuestSnapshotHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *GuestSnapshotHandler {
	return &GuestSnapshotHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

type guestSnapshotItem struct {
	ClusterID   uuid.UUID `json:"cluster_id"`
	ClusterName string    `json:"cluster_name"`
	Vmid        int32     `json:"vmid"`
	GuestType   string    `json:"guest_type"`
	// VMID is the internal vms row UUID the frontend uses for detail links.
	// Null when the guest is not currently in inventory (mid-churn or gone) —
	// the snapshot row survives on its stable (cluster_id, vmid) identity.
	VMID        *uuid.UUID `json:"vm_id"`
	VMName      *string    `json:"vm_name"`
	VMStatus    *string    `json:"vm_status"`
	Node        string     `json:"node"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parent      string     `json:"parent"`
	Vmstate     bool       `json:"vmstate"`
	SnapTime    int64      `json:"snap_time"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
}

// List returns every guest snapshot the caller may see, across all clusters,
// oldest first (rows without a snapshot time sort last). QEMU rows require
// view:vm on the row's cluster, LXC rows view:container — the split matters
// because the two resources can be granted independently.
func (h *GuestSnapshotHandler) List(c fiber.Ctx, p *apischema.Params) error {
	vmAccess, err := accessibleClusters(c, "view", "vm")
	if err != nil {
		return err
	}
	ctAccess, err := accessibleClusters(c, "view", "container")
	if err != nil {
		return err
	}

	// Optional cluster filter — the caller must hold either guest permission
	// on it (per-row filtering below still applies the precise one). Declared
	// as filter_cluster_id with "cluster_id" as its alias; see the declaration
	// for why the name the gate reads cannot be used for a query parameter.
	// An empty value is the "no filter" it has always been, not a uuid to
	// parse.
	var clusterFilter uuid.UUID
	if cid := p.String("filter_cluster_id"); cid != "" {
		clusterFilter, err = parseParamUUID(cid)
		if err != nil {
			return err
		}
		if !vmAccess.PermitsCluster(clusterFilter) && !ctAccess.PermitsCluster(clusterFilter) {
			return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
		}
	}

	// Nothing this caller could see — skip the table scan entirely.
	if !vmAccess.HasGlobal && len(vmAccess.Allowed) == 0 &&
		!ctAccess.HasGlobal && len(ctAccess.Allowed) == 0 {
		return RespondList(c, []guestSnapshotItem{}, 0)
	}

	rows, err := h.queries.ListAllGuestSnapshots(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list snapshots")
	}

	items := filterGuestSnapshotRows(rows, vmAccess, ctAccess, clusterFilter)
	return RespondItems(c, items)
}

// filterGuestSnapshotRows applies the per-row RBAC split and the optional
// cluster filter. Split out from List so the access logic is unit-testable
// without a database.
func filterGuestSnapshotRows(rows []db.ListAllGuestSnapshotsRow, vmAccess, ctAccess clusterAccess, clusterFilter uuid.UUID) []guestSnapshotItem {
	items := make([]guestSnapshotItem, 0, len(rows))
	for _, row := range rows {
		if clusterFilter != uuid.Nil && row.ClusterID != clusterFilter {
			continue
		}
		// guest_type is persisted on the snapshot row (not derived from the
		// LEFT-JOINed vms row) precisely so this split still works for rows
		// whose guest has vanished.
		switch row.GuestType {
		case "qemu":
			if !vmAccess.PermitsCluster(row.ClusterID) {
				continue
			}
		case "lxc":
			if !ctAccess.PermitsCluster(row.ClusterID) {
				continue
			}
		default:
			continue
		}
		items = append(items, mapGuestSnapshotRow(row))
	}
	return items
}

func mapGuestSnapshotRow(row db.ListAllGuestSnapshotsRow) guestSnapshotItem {
	item := guestSnapshotItem{
		ClusterID:   row.ClusterID,
		ClusterName: row.ClusterName,
		Vmid:        row.Vmid,
		GuestType:   row.GuestType,
		Node:        row.Node,
		Name:        row.Name,
		Description: row.Description,
		Parent:      row.Parent,
		Vmstate:     row.Vmstate,
		SnapTime:    row.SnapTime,
		LastSeenAt:  row.LastSeenAt,
	}
	if row.VmID.Valid {
		id := uuid.UUID(row.VmID.Bytes)
		item.VMID = &id
	}
	if row.VmName.Valid {
		item.VMName = &row.VmName.String
	}
	if row.VmStatus.Valid {
		item.VMStatus = &row.VmStatus.String
	}
	return item
}

// Resync refreshes one guest's snapshot inventory straight from Proxmox and
// converges the guest_snapshots rows, so the central page reflects a just-
// completed snapshot task without waiting for the next collector pass. The
// listing is read-only against Proxmox and yields no UPID (no TrackTask),
// and the rows are derived cache state (no audit entry).
func (h *GuestSnapshotHandler) Resync(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// The declared Alternatives gate has already refused a caller holding
	// NEITHER guest permission on this cluster. These two reads are what the
	// PRECISE per-class check below needs, and they are taken BEFORE the guest
	// lookup on purpose: resolving first would make the 404-vs-403 split a
	// vmid-existence oracle.
	canVM, err := hasClusterPerm(c, "view", "vm", clusterID)
	if err != nil {
		return err
	}
	canCT, err := hasClusterPerm(c, "view", "container", clusterID)
	if err != nil {
		return err
	}

	// Resolve on the stable (cluster_id, vmid) identity — central-page rows
	// carry vmid, and the vms UUID may have churned since the row was listed.
	vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID,
		Vmid:      safeconv.Int32(int(p.Int("vmid"))),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Guest not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up guest")
	}

	// Precise per-class gate. 404, not 403: a caller holding only the other
	// guest class must not learn that a guest with this vmid exists.
	resource := "vm"
	if vm.Type == "lxc" {
		resource = "container"
	}
	if (resource == "vm" && !canVM) || (resource == "container" && !canCT) {
		return fiber.NewError(fiber.StatusNotFound, "Guest not found")
	}

	node, err := h.queries.GetNode(c.Context(), vm.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for guest")
	}

	client, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return err
	}

	var snaps []proxmox.Snapshot
	if vm.Type == "lxc" {
		snaps, err = client.ListCTSnapshots(c.Context(), node.Name, int(vm.Vmid))
	} else {
		snaps, err = client.ListVMSnapshots(c.Context(), node.Name, int(vm.Vmid))
	}
	if err != nil {
		return mapProxmoxError(err)
	}
	if len(snaps) == 0 {
		// PVE always returns at least the synthetic "current" entry; a
		// raw-empty 200 is anomalous. Refuse to prune on it — same rule as
		// the collector (internal/collector/snapshot_sync.go).
		return fiber.NewError(fiber.StatusBadGateway, "Proxmox returned an empty snapshot listing")
	}
	if len(snaps) > maxSnapshotsPerGuest {
		// Defence in depth against a broken or hostile PVE endpoint driving
		// an unbounded upsert loop; PVE's practical ceiling is far lower.
		return fiber.NewError(fiber.StatusBadGateway, "Proxmox returned an implausible snapshot listing")
	}

	names := make([]string, 0, len(snaps))
	for _, snap := range snaps {
		if snap.Name == "current" {
			// Synthetic "you are here" entry — never persisted, mirroring the
			// per-VM list handlers and the collector.
			continue
		}
		names = append(names, snap.Name)
		if _, upErr := h.queries.UpsertGuestSnapshot(c.Context(), db.UpsertGuestSnapshotParams{
			ClusterID:   clusterID,
			Vmid:        vm.Vmid,
			Name:        snap.Name,
			GuestType:   vm.Type,
			Node:        node.Name,
			Description: snap.Description,
			Parent:      snap.Parent,
			Vmstate:     snap.VMState != 0,
			SnapTime:    snap.SnapTime,
		}); upErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to store snapshot")
		}
	}
	removed, err := h.queries.DeleteGuestSnapshotsNotInSet(c.Context(), db.DeleteGuestSnapshotsNotInSetParams{
		ClusterID: clusterID,
		Vmid:      vm.Vmid,
		Names:     names,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to prune stale snapshots")
	}
	// The upserts are derived cache, but the prune deletes rows that back
	// the snapshot_age_days alert metric — leave a trace when it acted.
	if removed > 0 {
		details, _ := json.Marshal(map[string]any{
			"vmid":   vm.Vmid,
			"kept":   len(names),
			"pruned": removed,
		})
		AuditLog(c, h.queries, h.eventPub,
			pgtype.UUID{Bytes: clusterID, Valid: true},
			resource, vm.ID.String(), "snapshot_resync", details)
	}

	if h.eventPub != nil {
		h.eventPub.ClusterEvent(c.Context(), clusterID.String(),
			events.KindSnapshotChange, resource, vm.ID.String(), "resync")
	}
	return c.JSON(fiber.Map{"count": len(names)})
}
