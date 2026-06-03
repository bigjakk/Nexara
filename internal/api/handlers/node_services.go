package handlers

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

// --- Node Services ---

// ListNodeServices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/services.
func (h *NodeHandler) ListNodeServices(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
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
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
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
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "service_" + action,
		UPID:         upid,
		Description:  action + " service " + service + " on " + nodeName,
		Extra:        map[string]any{"service": service, "action": action},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- Node Syslog ---

// GetNodeSyslog handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/syslog.
func (h *NodeHandler) GetNodeSyslog(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	// Default start to -1 (fetch newest entries) unless explicitly provided.
	startStr := c.Query("start")
	start := -1
	if startStr != "" {
		start, _ = strconv.Atoi(startStr)
	}
	limit, _ := strconv.Atoi(c.Query("limit", "500"))
	if limit > 5000 {
		limit = 5000
	}
	since := c.Query("since")
	if since == "" {
		// Default to today to avoid Proxmox scanning the entire journal,
		// which can hang on nodes with large log files.
		since = time.Now().UTC().Format("2006-01-02")
	}
	until := c.Query("until")
	service := c.Query("service")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, total, err := pxClient.GetNodeSyslog(c.Context(), nodeName, start, limit, since, until, service)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to get node syslog: %v", err))
	}
	return c.JSON(fiber.Map{"entries": entries, "total": total})
}
