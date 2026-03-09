package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// PoolHandler handles resource pool endpoints.
type PoolHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewPoolHandler creates a new PoolHandler.
func NewPoolHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *PoolHandler {
	return &PoolHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *PoolHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *PoolHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// ListPools handles GET /clusters/:cluster_id/pools.
func (h *PoolHandler) ListPools(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "pool"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pools, err := pxClient.GetResourcePools(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get resource pools")
	}
	return c.JSON(pools)
}

// CreatePool handles POST /clusters/:cluster_id/pools.
func (h *PoolHandler) CreatePool(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "pool"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreatePoolParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.PoolID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool ID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateResourcePool(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create resource pool")
	}
	details, _ := json.Marshal(map[string]string{"poolid": req.PoolID})
	h.auditLog(c, clusterID, "pool", req.PoolID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", req.PoolID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetPool handles GET /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) GetPool(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "pool"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	poolID := c.Params("pool_id")
	if poolID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool ID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pool, err := pxClient.GetResourcePool(c.Context(), poolID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get resource pool")
	}
	return c.JSON(pool)
}

// UpdatePool handles PUT /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) UpdatePool(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "pool"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	poolID := c.Params("pool_id")
	if poolID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool ID is required")
	}
	var req proxmox.UpdatePoolParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateResourcePool(c.Context(), poolID, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to update resource pool")
	}
	details, _ := json.Marshal(map[string]string{"poolid": poolID})
	h.auditLog(c, clusterID, "pool", poolID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", poolID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeletePool handles DELETE /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) DeletePool(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "pool"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	poolID := c.Params("pool_id")
	if poolID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool ID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteResourcePool(c.Context(), poolID); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete resource pool")
	}
	details, _ := json.Marshal(map[string]string{"poolid": poolID})
	h.auditLog(c, clusterID, "pool", poolID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", poolID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}
