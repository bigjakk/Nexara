package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
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

func (h *PoolHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// mapPoolError adds pool-specific handling on top of mapProxmoxError, in the
// same shape as mapTemplateError.
//
// PVE reports "this pool is already there" as a plain 500 with a die() string
// rather than a distinguishing status, so it carries no rejection map and
// mapProxmoxError can only call it a gateway failure. It is not one — it is the
// single most likely way CreatePool fails, and it is entirely about the name
// the operator chose, which is what 409 says.
func mapPoolError(err error) error {
	if err == nil {
		return nil
	}
	if proxmox.IsAlreadyExistsError(err) {
		return fiber.NewError(fiber.StatusConflict, "A pool with that ID already exists")
	}
	return mapProxmoxError(err)
}

// CreatePool handles POST /clusters/:cluster_id/pools.
func (h *PoolHandler) CreatePool(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "pool", clusterID); err != nil {
		return err
	}
	var req proxmox.CreatePoolParams
	if err := c.Bind().Body(&req); err != nil {
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
		return mapPoolError(err)
	}
	details, _ := json.Marshal(map[string]string{"poolid": req.PoolID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pool", req.PoolID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", req.PoolID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetPool handles GET /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) GetPool(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "pool", clusterID); err != nil {
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
		return mapProxmoxError(err)
	}
	return c.JSON(pool)
}

// UpdatePool handles PUT /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) UpdatePool(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "pool", clusterID); err != nil {
		return err
	}
	poolID := c.Params("pool_id")
	if poolID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool ID is required")
	}
	var req proxmox.UpdatePoolParams
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateResourcePool(c.Context(), poolID, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"poolid": poolID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pool", poolID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", poolID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeletePool handles DELETE /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) DeletePool(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "pool", clusterID); err != nil {
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
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"poolid": poolID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pool", poolID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", poolID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}
