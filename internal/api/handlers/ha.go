package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// HAHandler handles HA resources, groups, and status endpoints.
type HAHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewHAHandler creates a new HAHandler.
func NewHAHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *HAHandler {
	return &HAHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *HAHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *HAHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

func (h *HAHandler) publishHA(c *fiber.Ctx, clusterID uuid.UUID, resourceID, action string) {
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindHAChange, "ha", resourceID, action)
}

// --- HA Resources ---

// ListResources handles GET /clusters/:cluster_id/ha/resources.
func (h *HAHandler) ListResources(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ha"); err != nil {
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
	resources, err := pxClient.GetHAResources(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get HA resources")
	}
	return c.JSON(resources)
}

// CreateResource handles POST /clusters/:cluster_id/ha/resources.
func (h *HAHandler) CreateResource(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreateHAResourceParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.SID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateHAResource(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create HA resource")
	}
	details, _ := json.Marshal(map[string]string{"sid": req.SID})
	h.auditLog(c, clusterID, "ha_resource", req.SID, "created", details)
	h.publishHA(c, clusterID, req.SID, "resource_created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetResource handles GET /clusters/:cluster_id/ha/resources/:sid.
func (h *HAHandler) GetResource(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	sid := c.Params("sid")
	if sid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	resource, err := pxClient.GetHAResource(c.Context(), sid)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get HA resource")
	}
	return c.JSON(resource)
}

// UpdateResource handles PUT /clusters/:cluster_id/ha/resources/:sid.
func (h *HAHandler) UpdateResource(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	sid := c.Params("sid")
	if sid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	var req proxmox.UpdateHAResourceParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateHAResource(c.Context(), sid, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to update HA resource")
	}
	details, _ := json.Marshal(map[string]string{"sid": sid})
	h.auditLog(c, clusterID, "ha_resource", sid, "updated", details)
	h.publishHA(c, clusterID, sid, "resource_updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteResource handles DELETE /clusters/:cluster_id/ha/resources/:sid.
func (h *HAHandler) DeleteResource(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	sid := c.Params("sid")
	if sid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "SID is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteHAResource(c.Context(), sid); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete HA resource")
	}
	details, _ := json.Marshal(map[string]string{"sid": sid})
	h.auditLog(c, clusterID, "ha_resource", sid, "deleted", details)
	h.publishHA(c, clusterID, sid, "resource_deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- HA Groups ---

// ListGroups handles GET /clusters/:cluster_id/ha/groups.
func (h *HAHandler) ListGroups(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ha"); err != nil {
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
	groups, err := pxClient.GetHAGroups(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get HA groups")
	}
	return c.JSON(groups)
}

// CreateGroup handles POST /clusters/:cluster_id/ha/groups.
func (h *HAHandler) CreateGroup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreateHAGroupParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	if req.Nodes == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Nodes are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateHAGroup(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create HA group")
	}
	details, _ := json.Marshal(map[string]string{"group": req.Group})
	h.auditLog(c, clusterID, "ha_group", req.Group, "created", details)
	h.publishHA(c, clusterID, req.Group, "group_created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateGroup handles PUT /clusters/:cluster_id/ha/groups/:group.
func (h *HAHandler) UpdateGroup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	group := c.Params("group")
	if group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	var req proxmox.UpdateHAGroupParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateHAGroup(c.Context(), group, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to update HA group")
	}
	details, _ := json.Marshal(map[string]string{"group": group})
	h.auditLog(c, clusterID, "ha_group", group, "updated", details)
	h.publishHA(c, clusterID, group, "group_updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteGroup handles DELETE /clusters/:cluster_id/ha/groups/:group.
func (h *HAHandler) DeleteGroup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "ha"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	group := c.Params("group")
	if group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteHAGroup(c.Context(), group); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete HA group")
	}
	details, _ := json.Marshal(map[string]string{"group": group})
	h.auditLog(c, clusterID, "ha_group", group, "deleted", details)
	h.publishHA(c, clusterID, group, "group_deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- HA Status ---

// GetStatus handles GET /clusters/:cluster_id/ha/status.
func (h *HAHandler) GetStatus(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "ha"); err != nil {
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
	status, err := pxClient.GetHAStatus(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get HA status")
	}
	return c.JSON(status)
}
