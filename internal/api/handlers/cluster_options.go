package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// ClusterOptionsHandler handles cluster options and datacenter config endpoints.
type ClusterOptionsHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewClusterOptionsHandler creates a new ClusterOptionsHandler.
func NewClusterOptionsHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ClusterOptionsHandler {
	return &ClusterOptionsHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *ClusterOptionsHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *ClusterOptionsHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// GetOptions handles GET /clusters/:cluster_id/options.
func (h *ClusterOptionsHandler) GetOptions(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
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
	opts, err := pxClient.GetClusterOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(opts)
}

// UpdateOptions handles PUT /clusters/:cluster_id/options.
func (h *ClusterOptionsHandler) UpdateOptions(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.UpdateClusterOptionsParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.SetClusterOptions(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"action": "update_options"})
	h.auditLog(c, clusterID, "cluster_options", clusterID.String(), "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetDescription handles GET /clusters/:cluster_id/description.
func (h *ClusterOptionsHandler) GetDescription(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
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
	opts, err := pxClient.GetClusterOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{"description": opts.Description})
}

// UpdateDescription handles PUT /clusters/:cluster_id/description.
func (h *ClusterOptionsHandler) UpdateDescription(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req struct {
		Description string `json:"description"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.SetClusterOptions(c.Context(), proxmox.UpdateClusterOptionsParams{
		Description: &req.Description,
	}); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"action": "update_description"})
	h.auditLog(c, clusterID, "cluster_options", clusterID.String(), "description_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetTags handles GET /clusters/:cluster_id/tags.
func (h *ClusterOptionsHandler) GetTags(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
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
	opts, err := pxClient.GetClusterOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{
		"registered_tags": opts.RegisteredTags,
		"user_tag_access": opts.UserTagAccess,
		"tag_style":       opts.TagStyle,
	})
}

// UpdateTags handles PUT /clusters/:cluster_id/tags.
func (h *ClusterOptionsHandler) UpdateTags(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req struct {
		RegisteredTags *string `json:"registered_tags"`
		UserTagAccess  *string `json:"user_tag_access"`
		TagStyle       *string `json:"tag_style"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.SetClusterOptions(c.Context(), proxmox.UpdateClusterOptionsParams{
		RegisteredTags: req.RegisteredTags,
		UserTagAccess:  req.UserTagAccess,
		TagStyle:       req.TagStyle,
	}); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"action": "update_tags"})
	h.auditLog(c, clusterID, "cluster_options", clusterID.String(), "tags_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetClusterConfig handles GET /clusters/:cluster_id/config.
func (h *ClusterOptionsHandler) GetClusterConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
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
	cfg, err := pxClient.GetClusterConfig(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(cfg)
}

// GetJoinInfo handles GET /clusters/:cluster_id/config/join.
func (h *ClusterOptionsHandler) GetJoinInfo(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
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
	info, err := pxClient.GetClusterJoinInfo(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(info)
}

// ListCorosyncNodes handles GET /clusters/:cluster_id/config/nodes.
func (h *ClusterOptionsHandler) ListCorosyncNodes(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
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
	nodes, err := pxClient.GetCorosyncNodes(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(nodes)
}
