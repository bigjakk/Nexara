package handlers

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
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

// validCTActions is the set of allowed container status actions.
var validCTActions = map[string]bool{
	"start":    true,
	"stop":     true,
	"shutdown": true,
	"reboot":   true,
	"suspend":  true,
	"resume":   true,
}

type ctActionRequest struct {
	Action string `json:"action"`
}

type ctCloneRequest struct {
	NewID   int    `json:"new_id"`
	Name    string `json:"name"`
	Target  string `json:"target"`
	Full    bool   `json:"full"`
	Storage string `json:"storage"`
}

type ctMigrateRequest struct {
	Target string `json:"target"`
	Online bool   `json:"online"`
}

type ctVolumeMoveRequest struct {
	Volume  string `json:"volume"`
	Storage string `json:"storage"`
	Delete  bool   `json:"delete"`
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/containers.
func (h *ContainerHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	cts, err := h.queries.ListContainersByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list containers")
	}

	resp := make([]vmResponse, len(cts))
	for i, ct := range cts {
		resp[i] = toVMResponse(ct)
	}

	return c.JSON(resp)
}

// GetContainer handles GET /api/v1/clusters/:cluster_id/containers/:ct_id.
func (h *ContainerHandler) GetContainer(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	ct, err := h.queries.GetContainer(c.Context(), ctID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Container not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get container")
	}

	return c.JSON(toVMResponse(ct))
}

// PerformAction handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/status.
func (h *ContainerHandler) PerformAction(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	var req ctActionRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if !validCTActions[req.Action] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid action; must be one of: start, stop, shutdown, reboot, suspend, resume")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	var upid string
	switch req.Action {
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

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), req.Action)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), req.Action)

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// CloneContainer handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/clone.
func (h *ContainerHandler) CloneContainer(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	var req ctCloneRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.NewID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "new_id is required and must be positive")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CloneCT(c.Context(), node.Name, int(ct.Vmid), proxmox.CloneParams{
		NewID:   req.NewID,
		Name:    req.Name,
		Target:  req.Target,
		Full:    req.Full,
		Storage: req.Storage,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "clone")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "container", ct.ID.String(), "clone")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// MigrateContainer handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/migrate.
func (h *ContainerHandler) MigrateContainer(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	var req ctMigrateRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Target == "" {
		return fiber.NewError(fiber.StatusBadRequest, "target node is required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MigrateCT(c.Context(), node.Name, int(ct.Vmid), proxmox.MigrateParams{
		Target: req.Target,
		Online: req.Online,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "migrate")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindMigrationUpdate, "container", ct.ID.String(), "migrate")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DestroyContainer handles DELETE /api/v1/clusters/:cluster_id/containers/:ct_id.
func (h *ContainerHandler) DestroyContainer(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DestroyCT(c.Context(), node.Name, int(ct.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "destroy")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "container", ct.ID.String(), "destroy")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// resolveCT loads the container, its node, the cluster, and creates a Proxmox client.
func (h *ContainerHandler) resolveCT(c *fiber.Ctx, clusterID, ctID uuid.UUID) (db.Vm, db.Node, db.Cluster, *proxmox.Client, error) {
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

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return zeroCT, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return ct, node, cluster, pxClient, nil
}

// --- Snapshot handlers ---

// ListSnapshots handles GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots.
func (h *ContainerHandler) ListSnapshots(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
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

	return c.JSON(resp)
}

// CreateSnapshot handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots.
func (h *ContainerHandler) CreateSnapshot(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	var req snapshotRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.SnapName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "snap_name is required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateCTSnapshot(c.Context(), node.Name, int(ct.Vmid), proxmox.SnapshotParams{
		SnapName:    req.SnapName,
		Description: req.Description,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "snapshot_create")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "snapshot_create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DeleteSnapshot handles DELETE /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name.
func (h *ContainerHandler) DeleteSnapshot(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	snapName := c.Params("snap_name")
	if snapName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Snapshot name is required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DeleteCTSnapshot(c.Context(), node.Name, int(ct.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "snapshot_delete")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "snapshot_delete")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// RollbackSnapshot handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name/rollback.
func (h *ContainerHandler) RollbackSnapshot(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	snapName := c.Params("snap_name")
	if snapName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Snapshot name is required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.RollbackCTSnapshot(c.Context(), node.Name, int(ct.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "snapshot_rollback")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "snapshot_rollback")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Create Container handler ---

type createCTRequest struct {
	VMID         int               `json:"vmid"`
	Hostname     string            `json:"hostname"`
	Node         string            `json:"node"`
	OSTemplate   string            `json:"ostemplate"`
	Storage      string            `json:"storage"`
	RootFS       string            `json:"rootfs"`
	Memory       int               `json:"memory"`
	Swap         int               `json:"swap"`
	Cores        int               `json:"cores"`
	Net0         string            `json:"net0"`
	Password     string            `json:"password"`
	SSHKeys      string            `json:"ssh_keys"`
	Unprivileged bool              `json:"unprivileged"`
	Start        bool              `json:"start"`
	Description  string            `json:"description"`
	Tags         string            `json:"tags"`
	Pool         string            `json:"pool"`
	Nameserver   string            `json:"nameserver"`
	Searchdomain string            `json:"searchdomain"`
	Extra        map[string]string `json:"extra"`
}

// CreateContainer handles POST /api/v1/clusters/:cluster_id/containers.
func (h *ContainerHandler) CreateContainer(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req createCTRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.VMID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "vmid is required and must be positive")
	}
	if req.Node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node is required")
	}
	if req.OSTemplate == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ostemplate is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateCT(c.Context(), req.Node, proxmox.CreateCTParams{
		VMID:         req.VMID,
		Hostname:     req.Hostname,
		OSTemplate:   req.OSTemplate,
		Storage:      req.Storage,
		RootFS:       req.RootFS,
		Memory:       req.Memory,
		Swap:         req.Swap,
		Cores:        req.Cores,
		Net0:         req.Net0,
		Password:     req.Password,
		SSHKeys:      req.SSHKeys,
		Unprivileged: req.Unprivileged,
		Start:        req.Start,
		Description:  req.Description,
		Tags:         req.Tags,
		Pool:         req.Pool,
		Nameserver:   req.Nameserver,
		Searchdomain: req.Searchdomain,
		Extra:        req.Extra,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, clusterID, "container", strconv.Itoa(req.VMID), "create")
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindInventoryChange, "container", strconv.Itoa(req.VMID), "create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Container Config handlers ---

// MoveVolume handles POST /api/v1/clusters/:cluster_id/containers/:ct_id/volumes/move.
func (h *ContainerHandler) MoveVolume(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	var req ctVolumeMoveRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Volume == "" || req.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "volume and storage are required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MoveCTVolume(c.Context(), node.Name, int(ct.Vmid), proxmox.CTVolumeMoveParams{
		Volume:  req.Volume,
		Storage: req.Storage,
		Delete:  req.Delete,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "container", ct.ID.String(), "volume_move")
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "volume_move")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

type setCTConfigRequest struct {
	Fields map[string]string `json:"fields"`
}

// SetContainerConfig handles PUT /api/v1/clusters/:cluster_id/containers/:ct_id/config.
func (h *ContainerHandler) SetContainerConfig(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ctID, err := uuid.Parse(c.Params("ct_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid container ID")
	}

	var req setCTConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if len(req.Fields) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "fields map is required")
	}

	ct, node, cluster, pxClient, err := h.resolveCT(c, clusterID, ctID)
	if err != nil {
		return err
	}

	if err := pxClient.SetContainerConfig(c.Context(), node.Name, int(ct.Vmid), req.Fields); err != nil {
		return mapProxmoxError(err)
	}

	// Audit log with field details.
	if uid, ok := c.Locals("user_id").(uuid.UUID); ok {
		details, _ := json.Marshal(map[string]interface{}{"fields": req.Fields})
		_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
			ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
			UserID:       uid,
			ResourceType: "container",
			ResourceID:   ct.ID.String(),
			Action:       "config_update",
			Details:      details,
		})
	}
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "container", ct.ID.String(), "config_update")

	return c.JSON(fiber.Map{"status": "ok"})
}

// createProxmoxClient creates a Proxmox client for the given cluster.
func (h *ContainerHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
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

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}

// auditLog writes an audit log entry for container operations.
func (h *ContainerHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(`{}`),
	})
}
