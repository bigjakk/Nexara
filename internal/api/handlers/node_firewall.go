package handlers

import (
	"encoding/json"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// ListNodeFirewallRules handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules.
func (h *NodeHandler) ListNodeFirewallRules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "firewall"); err != nil {
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
	rules, err := pxClient.GetNodeFirewallRules(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list node firewall rules")
	}
	return c.JSON(rules)
}

type nodeFirewallRuleRequest struct {
	Type    string `json:"type"`
	Action  string `json:"action"`
	Source  string `json:"source"`
	Dest    string `json:"dest"`
	Sport   string `json:"sport"`
	Dport   string `json:"dport"`
	Proto   string `json:"proto"`
	Enable  int    `json:"enable"`
	Comment string `json:"comment"`
	Macro   string `json:"macro"`
	Log     string `json:"log"`
	Iface   string `json:"iface"`
}

func (r nodeFirewallRuleRequest) toParams() proxmox.FirewallRuleParams {
	return proxmox.FirewallRuleParams{
		Type:    r.Type,
		Action:  r.Action,
		Source:  r.Source,
		Dest:    r.Dest,
		Sport:   r.Sport,
		Dport:   r.Dport,
		Proto:   r.Proto,
		Enable:  r.Enable,
		Comment: r.Comment,
		Macro:   r.Macro,
		Log:     r.Log,
		Iface:   r.Iface,
	}
}

// CreateNodeFirewallRule handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules.
func (h *NodeHandler) CreateNodeFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "firewall"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	var req nodeFirewallRuleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Type == "" || req.Action == "" {
		return fiber.NewError(fiber.StatusBadRequest, "type and action are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateNodeFirewallRule(c.Context(), nodeName, req.toParams()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create node firewall rule")
	}
	details, _ := json.Marshal(req)
	h.auditLog(c, clusterID, nodeName, "create_firewall_rule", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// UpdateNodeFirewallRule handles PUT /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos.
func (h *NodeHandler) UpdateNodeFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "firewall"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule position")
	}
	var req nodeFirewallRuleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateNodeFirewallRule(c.Context(), nodeName, pos, req.toParams()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update node firewall rule")
	}
	details, _ := json.Marshal(req)
	h.auditLog(c, clusterID, nodeName, "update_firewall_rule", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteNodeFirewallRule handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos.
func (h *NodeHandler) DeleteNodeFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "firewall"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule position")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteNodeFirewallRule(c.Context(), nodeName, pos); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete node firewall rule")
	}
	h.auditLog(c, clusterID, nodeName, "delete_firewall_rule", nil)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetNodeFirewallLog handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/log.
func (h *NodeHandler) GetNodeFirewallLog(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "firewall"); err != nil {
		return err
	}
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(c.Query("limit", "500"))
	if limit > 5000 {
		limit = 5000
	}
	start, _ := strconv.Atoi(c.Query("start", "0"))
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetNodeFirewallLog(c.Context(), nodeName, limit, start)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node firewall log")
	}
	return c.JSON(entries)
}
