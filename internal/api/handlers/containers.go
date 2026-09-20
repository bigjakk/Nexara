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

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// ContainerHandler handles LXC container CRUD and lifecycle endpoints.
type ContainerHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewContainerHandler creates a new container handler.
func NewContainerHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ContainerHandler {
	return &ContainerHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

// ContainerStatusActions is the set of power actions
// POST .../containers/:ct_id/status accepts, in the order the API
// documentation lists them.
//
// It is exported so that the endpoint's declaration in
// internal/api/registry_containers.go can use it as the parameter's enum:
// the list that validates the request and the list the switch in
// PerformAction covers are then the same list, and a new action cannot be
// accepted by one and dropped by the other.
//
// "reset" is absent on purpose, and that is the one place this list is NOT
// a copy of VMStatusActions: LXC has no hardware-reset equivalent, so
// Proxmox's CT status endpoint offers no such action.
var ContainerStatusActions = []string{"start", "stop", "shutdown", "reboot", "suspend", "resume"}

// ctCloneParams reads the body shared by clone and clone-to-template.
func ctCloneParams(p *apischema.Params) proxmox.CloneParams {
	return proxmox.CloneParams{
		NewID:   int(p.Int("new_id")),
		Name:    p.String("name"),
		Target:  p.String("target"),
		Full:    p.Bool("full"),
		Storage: p.String("storage"),
	}
}

// Every handler in this file is a registry endpoint: it is declared in
// internal/api/registry_containers.go, which states its parameters and its
// permission, and it receives the validated parameters instead of
// re-parsing the request.
//
// Two things that used to be at the top of each of these functions are
// deliberately absent. The requireClusterPerm call now runs as route
// middleware, attached from the declaration — a handler that forgets it
// can no longer ship. And the body bind is gone: apischema has already
// coerced, format-checked and default-filled every parameter, and rejected
// any the endpoint does not declare, so the hand-rolled "x is required"
// checks that followed each bind are gone with it.

// ListByCluster handles GET /api/v1/clusters/:cluster_id/containers.
func (h *ContainerHandler) ListByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	cts, err := h.queries.ListContainersByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list containers")
	}

	resp := make([]vmResponse, len(cts))
	for i, ct := range cts {
		resp[i] = toVMResponse(ct)
	}

	return RespondItems(c, resp)
}

// GetContainer handles GET /api/v1/clusters/:cluster_id/containers/:ct_id.
func (h *ContainerHandler) GetContainer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, err := h.queries.GetContainer(c.Context(), ctID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Container not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get container")
	}
	// A container id says nothing about which cluster it is in, while the
	// route's permission middleware authorized the cluster in the PATH — so
	// without this, a caller holding view:container on one cluster reads
	// another cluster's inventory row by pairing its own cluster id with a
	// foreign container id. resolveCT makes the same check for every handler
	// that goes through it; this one looks the row up directly, which is why
	// TestGuard_GuestLookupsAreClusterScoped watches it.
	if ct.ClusterID != clusterID {
		// 404 rather than 403: whether a container exists in a cluster the
		// caller cannot see is not theirs to learn.
		return fiber.NewError(fiber.StatusNotFound, "Container not found in this cluster")
	}

	return c.JSON(toVMResponse(ct))
}

// PerformAction handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/status.
func (h *ContainerHandler) PerformAction(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}
	// The schema's enum is ContainerStatusActions, the same list the switch
	// below covers, so an unknown action never reaches here.
	action := p.String("action")

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	var upid string
	switch action {
	case "start":
		upid, err = pxClient.StartCT(c.Context(), node.Name, int(ct.Vmid))
	case "stop":
		upid, err = pxClient.StopCT(c.Context(), node.Name, int(ct.Vmid))
	case "shutdown":
		upid, err = pxClient.ShutdownCT(c.Context(), node.Name, int(ct.Vmid))
	case "reboot":
		upid, err = pxClient.RebootCT(c.Context(), node.Name, int(ct.Vmid))
	case "suspend":
		upid, err = pxClient.SuspendCT(c.Context(), node.Name, int(ct.Vmid))
	case "resume":
		upid, err = pxClient.ResumeCT(c.Context(), node.Name, int(ct.Vmid))
	}
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       action,
		UPID:         upid,
		Description:  guestActionDesc(action, ct),
		Extra:        map[string]any{"vmid": ct.Vmid},
	})

	// Watch the task in the background and update the DB when it completes.
	watchTaskAndUpdateStatus(h.queries, h.eventPub, pxClient, node.Name, upid, ct.ID, cluster.ID, action, "container")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// CloneContainer handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/clone.
func (h *ContainerHandler) CloneContainer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}
	clone := ctCloneParams(p)

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CloneCT(c.Context(), node.Name, int(ct.Vmid), clone)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "clone",
		UPID:         upid,
		Description:  guestActionDesc("clone", ct),
		Extra:        map[string]any{"vmid": ct.Vmid, "new_id": clone.NewID},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "container", ct.ID.String(), "clone")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// MigrateContainer handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/migrate.
func (h *ContainerHandler) MigrateContainer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}
	target := p.String("target")

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MigrateCT(c.Context(), node.Name, int(ct.Vmid), proxmox.MigrateParams{
		Target: target,
		Online: p.Bool("online"),
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "migrate",
		UPID:         upid,
		Description:  guestActionDesc("migrate", ct) + " → " + target,
		Extra:        map[string]any{"vmid": ct.Vmid, "target": target},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindMigrationUpdate, "container", ct.ID.String(), "migrate")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// ConvertToTemplate handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/convert-to-template.
// This converts a stopped container to a template. The operation is irreversible in Proxmox.
func (h *ContainerHandler) ConvertToTemplate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	if ct.Template {
		return fiber.NewError(fiber.StatusConflict, "Container is already a template")
	}

	if ct.Status != "stopped" {
		return fiber.NewError(fiber.StatusConflict, "Container must be stopped before converting to template")
	}

	upid, err := pxClient.ConvertCTToTemplate(c.Context(), node.Name, int(ct.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "convert-to-template",
		UPID:         upid,
		Description:  guestActionDesc("convert-to-template", ct),
		Extra:        map[string]any{"vmid": ct.Vmid},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "container", ct.ID.String(), "convert-to-template")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// CloneToTemplate handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/clone-to-template.
// This clones a container and then automatically converts the clone to a template.
func (h *ContainerHandler) CloneToTemplate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}
	clone := ctCloneParams(p)

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	cloneUpid, err := pxClient.CloneCT(c.Context(), node.Name, int(ct.Vmid), clone)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "clone-to-template",
		UPID:         cloneUpid,
		Description:  guestActionDesc("clone-to-template", ct),
		Extra:        map[string]any{"vmid": ct.Vmid, "new_id": clone.NewID, "clone_to_template": true},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "container", ct.ID.String(), "clone-to-template")

	// Background: poll clone task then convert the clone to template
	targetNode := node.Name
	if clone.Target != "" {
		targetNode = clone.Target
	}
	go h.convertCloneToTemplate(pxClient, targetNode, clone.NewID, cluster.ID.String()) //nolint:gosec // G118: intentionally detached — clone→template conversion must outlive the request (Fiber recycles the request context)

	return c.JSON(vmActionResponse{
		UPID:   cloneUpid,
		Status: "dispatched",
	})
}

// convertCloneToTemplate polls until the cloned CT appears then converts it to a template.
func (h *ContainerHandler) convertCloneToTemplate(pxClient *proxmox.Client, node string, newVMID int, clusterID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("CT clone-to-template watcher panicked",
				"vmid", newVMID, "cluster_id", clusterID, "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cts, err := pxClient.GetContainers(ctx, node)
			if err != nil {
				continue
			}
			found := false
			for _, ct := range cts {
				if ct.VMID == newVMID && ct.Status == "stopped" && ct.Template == 0 {
					found = true
					break
				}
			}
			if !found {
				continue
			}

			_, _ = pxClient.ConvertCTToTemplate(ctx, node, newVMID)
			h.eventPub.ClusterEvent(ctx, clusterID, events.KindInventoryChange, "container", "", "clone-to-template-complete")
			return
		}
	}
}

// DestroyContainer handles DELETE /api/v1/clusters/:cluster_id/containers/:ct_id.
func (h *ContainerHandler) DestroyContainer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DestroyCT(c.Context(), node.Name, int(ct.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "destroy",
		UPID:         upid,
		Description:  guestActionDesc("destroy", ct),
		Extra:        map[string]any{"vmid": ct.Vmid},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "container", ct.ID.String(), "destroy")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// resolveCT loads the container, its node, the cluster, and creates a Proxmox client.
func (h *ContainerHandler) resolveCT(c fiber.Ctx, clusterID, ctID uuid.UUID) (db.Vm, db.Node, db.Cluster, *proxmox.Client, error) {
	var zeroCT db.Vm
	var zeroNode db.Node
	var zeroCluster db.Cluster

	ct, err := h.queries.GetContainer(c.Context(), ctID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "Container not found")
		}
		return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get container")
	}

	if ct.ClusterID != clusterID {
		return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "Container not found in this cluster")
	}

	node, err := h.queries.GetNode(c.Context(), ct.NodeID)
	if err != nil {
		return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for container")
	}

	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return zeroCT, zeroNode, zeroCluster, nil, err
	}

	return ct, node, cluster, pxClient, nil
}

// --- Snapshot handlers ---

// GetSnapshotCapability handles GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshot-capability.
func (h *ContainerHandler) GetSnapshotCapability(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, node, _, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	supported, err := pxClient.GetCTSnapshotFeature(c.Context(), node.Name, int(ct.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := snapshotCapabilityResponse{Supported: supported, BlockingVolumes: []string{}}
	if !supported {
		if config, cfgErr := pxClient.GetCTConfig(c.Context(), node.Name, int(ct.Vmid)); cfgErr == nil {
			resp.BlockingVolumes = snapshotBlockingVolumes(config, storageTypesByName(c, h.queries, clusterID))
		}
	}
	return c.JSON(resp)
}

// ListSnapshots handles GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots.
func (h *ContainerHandler) ListSnapshots(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, node, _, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	snaps, err := pxClient.ListCTSnapshots(c.Context(), node.Name, int(ct.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]snapshotResponse, 0, len(snaps))
	for _, s := range snaps {
		if s.Name == "current" {
			continue
		}
		resp = append(resp, snapshotResponse{
			Name:        s.Name,
			Description: s.Description,
			SnapTime:    s.SnapTime,
			VMState:     s.VMState,
			Parent:      s.Parent,
		})
	}

	return RespondItems(c, resp)
}

// CreateSnapshot handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots.
func (h *ContainerHandler) CreateSnapshot(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	// The declaration states the whole rule — the "pve-configid" format for
	// the shape and MaxLength for SnapshotMaxNameLen — and the schema
	// enforces both before this runs. This re-checks them at the choke
	// point and adds what a declaration cannot express: the names Proxmox
	// reserves, which for a container are "current" and "vzdump" and are
	// NOT the VM's set — a container snapshot may be called "pending".
	// See the "Snapshot names" block in internal/proxmox/client_guests.go
	// for where each rule comes from upstream, and for why the rule itself
	// sits at the client.
	snapName := p.String("snap_name")
	if err := snapshotNameError(lxcSnapshot, snapName); err != nil {
		return err
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateCTSnapshot(c.Context(), node.Name, int(ct.Vmid), proxmox.SnapshotParams{
		SnapName:    snapName,
		Description: p.String("description"),
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "snapshot_create",
		UPID:         upid,
		Description:  guestActionDesc("snapshot_create", ct),
		Extra:        map[string]any{"vmid": ct.Vmid, "snap_name": snapName},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "snapshot_create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DeleteSnapshot handles DELETE /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name.
// The permission is delete, not execute: destroying a snapshot is a
// delete-class action, matching VMHandler.DeleteSnapshot and
// DestroyContainer. The gate now lives in the declaration
// (internal/api/registry_containers.go); the previous inline execute check
// was a copy of the rollback handler's.
func (h *ContainerHandler) DeleteSnapshot(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}
	snapName := p.String("snap_name")

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DeleteCTSnapshot(c.Context(), node.Name, int(ct.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "snapshot_delete",
		UPID:         upid,
		Description:  guestActionDesc("snapshot_delete", ct),
		Extra:        map[string]any{"vmid": ct.Vmid, "snap_name": snapName},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "snapshot_delete")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// RollbackSnapshot handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name/rollback.
func (h *ContainerHandler) RollbackSnapshot(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}
	snapName := p.String("snap_name")

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.RollbackCTSnapshot(c.Context(), node.Name, int(ct.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "snapshot_rollback",
		UPID:         upid,
		Description:  guestActionDesc("snapshot_rollback", ct),
		Extra:        map[string]any{"vmid": ct.Vmid, "snap_name": snapName},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "snapshot_rollback")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Create Container handler ---

// CreateContainer handles POST /api/v1/clusters/:cluster_id/containers.
func (h *ContainerHandler) CreateContainer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// extra carries whatever Proxmox LXC config keys the schema does not
	// name — features, cpulimit, arch, onboot — as a flat object.
	extra, err := stringMap("extra", p.Object("extra"))
	if err != nil {
		return err
	}

	vmid := int(p.Int("vmid"))
	node := p.String("node")
	hostname := p.String("hostname")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateCT(c.Context(), node, proxmox.CreateCTParams{
		VMID:         vmid,
		Hostname:     hostname,
		OSTemplate:   p.String("ostemplate"),
		Storage:      p.String("storage"),
		RootFS:       p.String("rootfs"),
		Memory:       int(p.Int("memory")),
		Swap:         int(p.Int("swap")),
		Cores:        int(p.Int("cores")),
		Net0:         p.String("net0"),
		Password:     p.String("password"),
		SSHKeys:      p.String("ssh_keys"),
		Unprivileged: p.Bool("unprivileged"),
		Start:        p.Bool("start"),
		Description:  p.String("description"),
		Tags:         p.String("tags"),
		Pool:         p.String("pool"),
		Nameserver:   p.String("nameserver"),
		Searchdomain: p.String("searchdomain"),
		Extra:        extra,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "container",
		ResourceID:   strconv.Itoa(vmid),
		ResourceName: hostname,
		Action:       "create",
		UPID:         upid,
		Description:  "create CT " + strconv.Itoa(vmid),
		Extra:        map[string]any{"vmid": vmid},
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindInventoryChange, "container", strconv.Itoa(vmid), "create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Container Config handlers ---

// GetContainerConfig handles GET /api/v1/clusters/:cluster_id/containers/:ct_id/config.
func (h *ContainerHandler) GetContainerConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, node, _, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	config, err := pxClient.GetCTConfig(c.Context(), node.Name, int(ct.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(config)
}

// ResizeDisk handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/disks/resize.
func (h *ContainerHandler) ResizeDisk(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	if err := pxClient.ResizeCTDisk(c.Context(), node.Name, int(ct.Vmid), proxmox.DiskResizeParams{
		Disk: p.String("disk"),
		Size: p.String("size"),
	}); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "container", ct.ID.String(), "disk_resize", nil)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "disk_resize")

	return c.JSON(vmActionResponse{
		UPID:   "",
		Status: "ok",
	})
}

// MoveVolume handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/volumes/move.
func (h *ContainerHandler) MoveVolume(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	// Same spec the VM path uses; CTParams drops the format, which LXC has no
	// concept of.
	//
	// Still validated here, not only in the schema: the bounds live in
	// DiskMoveSpec, and this is the choke point every caller of MoveVolume
	// goes through. The schema's job is that the two required fields are
	// named in the 400 when they are missing — which is why "volume" is
	// declared under the name this endpoint takes rather than the "disk"
	// the spec calls it.
	spec := proxmox.DiskMoveSpec{
		Disk:          p.String("volume"),
		TargetStorage: p.String("storage"),
		DeleteSource:  p.Bool("delete"),
		BWLimitKiB:    int(p.Int("bwlimit_kib")),
	}
	if err := spec.Validate(); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MoveCTVolume(c.Context(), node.Name, int(ct.Vmid), spec.CTParams())
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "container",
		ResourceID:   ct.ID.String(),
		ResourceName: ct.Name,
		Action:       "volume_move",
		UPID:         upid,
		Description:  "Move volume " + spec.Summary(),
		Extra:        withVMID(spec.AuditExtra(), ct.Vmid),
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "volume_move")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// SetContainerConfig handles PUT /api/v1/clusters/:cluster_id/containers/:ct_id/config.
func (h *ContainerHandler) SetContainerConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, ctID, err := containerIDs(p)
	if err != nil {
		return err
	}

	fields, err := stringMap("fields", p.Object("fields"))
	if err != nil {
		return err
	}
	// The schema requires the parameter; it cannot require the object to
	// hold anything, and a config write with nothing in it would be a
	// no-op Proxmox round trip that still writes an audit row claiming a
	// change.
	if len(fields) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "fields map is required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	if err := pxClient.SetContainerConfig(c.Context(), node.Name, int(ct.Vmid), fields); err != nil {
		return mapProxmoxError(err)
	}

	configDetails, _ := json.Marshal(map[string]interface{}{"fields": fields})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "container", ct.ID.String(), "config_update", configDetails)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "config_update")

	return c.JSON(fiber.Map{"status": "ok"})
}

// createProxmoxClient creates a Proxmox client for the given cluster.
func (h *ContainerHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}
