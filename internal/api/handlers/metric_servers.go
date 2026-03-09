package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// MetricServerHandler handles metric server configuration endpoints.
type MetricServerHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewMetricServerHandler creates a new MetricServerHandler.
func NewMetricServerHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *MetricServerHandler {
	return &MetricServerHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *MetricServerHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *MetricServerHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// ListServers handles GET /clusters/:cluster_id/metric-servers.
func (h *MetricServerHandler) ListServers(c *fiber.Ctx) error {
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
	servers, err := pxClient.GetMetricServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get metric servers")
	}
	return c.JSON(servers)
}

// CreateServer handles POST /clusters/:cluster_id/metric-servers.
func (h *MetricServerHandler) CreateServer(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreateMetricServerParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.ID == "" || req.Type == "" || req.Server == "" || req.Port == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "ID, type, server, and port are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateMetricServer(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create metric server")
	}
	details, _ := json.Marshal(map[string]string{"id": req.ID, "type": req.Type})
	h.auditLog(c, clusterID, "metric_server", req.ID, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetServer handles GET /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) GetServer(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	serverID := c.Params("server_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	server, err := pxClient.GetMetricServer(c.Context(), serverID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get metric server")
	}
	return c.JSON(server)
}

// UpdateServer handles PUT /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) UpdateServer(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	serverID := c.Params("server_id")
	var req proxmox.UpdateMetricServerParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateMetricServer(c.Context(), serverID, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to update metric server")
	}
	details, _ := json.Marshal(map[string]string{"id": serverID})
	h.auditLog(c, clusterID, "metric_server", serverID, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteServer handles DELETE /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) DeleteServer(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	serverID := c.Params("server_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteMetricServer(c.Context(), serverID); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete metric server")
	}
	details, _ := json.Marshal(map[string]string{"id": serverID})
	h.auditLog(c, clusterID, "metric_server", serverID, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}
