package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// ContainerHandler handles LXC container CRUD and lifecycle endpoints.
type ContainerHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewContainerHandler creates a new container handler.
func NewContainerHandler(queries *db.Queries, encryptionKey string) *ContainerHandler {
	return &ContainerHandler{queries: queries, encryptionKey: encryptionKey}
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

// auditLog writes an audit log entry for container operations.
func (h *ContainerHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string) {
	userID, _ := c.Locals("user_id").(string)
	uid, err := uuid.Parse(userID)
	if err != nil {
		return
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    clusterID,
		UserID:       uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(`{}`),
	})
}
