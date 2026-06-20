package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
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

func (h *MetricServerHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// ListServers handles GET /clusters/:cluster_id/metric-servers.
func (h *MetricServerHandler) ListServers(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cluster", clusterID); err != nil {
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
	// The InfluxDB token is a write-only credential — never return it on a read.
	for i := range servers {
		servers[i].Token = ""
	}
	return c.JSON(servers)
}

// CreateServer handles POST /clusters/:cluster_id/metric-servers.
func (h *MetricServerHandler) CreateServer(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cluster", clusterID); err != nil {
		return err
	}
	var req proxmox.CreateMetricServerParams
	if err := c.Bind().Body(&req); err != nil {
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "metric_server", req.ID, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetServer handles GET /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) GetServer(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cluster", clusterID); err != nil {
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
	// The InfluxDB token is a write-only credential — never return it on a read.
	if server != nil {
		server.Token = ""
	}
	return c.JSON(server)
}

// UpdateServer handles PUT /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) UpdateServer(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cluster", clusterID); err != nil {
		return err
	}
	serverID := c.Params("server_id")
	var req proxmox.UpdateMetricServerParams
	if err := c.Bind().Body(&req); err != nil {
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "metric_server", serverID, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteServer handles DELETE /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) DeleteServer(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cluster", clusterID); err != nil {
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "metric_server", serverID, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}
