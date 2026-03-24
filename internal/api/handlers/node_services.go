package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

// --- Node Services ---

// ListNodeServices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/services.
func (h *NodeHandler) ListNodeServices(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	services, err := pxClient.GetNodeServices(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list node services")
	}
	return c.JSON(services)
}

// ServiceAction handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/services/:service/:action.
func (h *NodeHandler) ServiceAction(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "node"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	service := c.Params("service")
	action := c.Params("action")
	if service == "" || action == "" {
		return fiber.NewError(fiber.StatusBadRequest, "service and action are required")
	}
	switch action {
	case "start", "stop", "restart", "reload":
		// valid
	default:
		return fiber.NewError(fiber.StatusBadRequest, "action must be start, stop, restart, or reload")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.ServiceAction(c.Context(), nodeName, service, action)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to "+action+" service "+service)
	}
	details, _ := json.Marshal(map[string]string{"service": service, "action": action})
	h.auditLog(c, clusterID, nodeName, "service_"+action, details)
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- Node Syslog ---

// GetNodeSyslog handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/syslog.
func (h *NodeHandler) GetNodeSyslog(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	start, _ := strconv.Atoi(c.Query("start", "0"))
	limit, _ := strconv.Atoi(c.Query("limit", "500"))
	if limit > 5000 {
		limit = 5000
	}
	since := c.Query("since")
	if since == "" {
		// Default to last 24 hours to avoid Proxmox scanning the entire journal,
		// which can hang on nodes with large log files.
		since = time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
	}
	until := c.Query("until")
	service := c.Query("service")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetNodeSyslog(c.Context(), nodeName, start, limit, since, until, service)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to get node syslog: %v", err))
	}
	return c.JSON(entries)
}
