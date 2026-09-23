package handlers

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// ListNodeFirewallRules handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules.
func (h *NodeHandler) ListNodeFirewallRules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	rules, err := pxClient.GetNodeFirewallRules(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, rules)
}

// CreateNodeFirewallRule handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules.
func (h *NodeHandler) CreateNodeFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	// type and action are REQUIRED and non-empty by the schema on this route
	// and optional on the update one, which is the split the handlers already
	// had: Proxmox's rule update keeps the existing direction and action when
	// they are omitted, and firewallRuleToForm omits an empty one rather than
	// sending it, so only a create has to name them.
	rule := firewallRuleFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateNodeFirewallRule(c.Context(), nodeName, rule); err != nil {
		return mapProxmoxError(err)
	}
	details := firewallRuleAudit(rule)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "node", nodeName, "create_firewall_rule", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// UpdateNodeFirewallRule handles PUT /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos.
func (h *NodeHandler) UpdateNodeFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	// The schema declares :pos as a non-negative integer, so "abc" and "-1" are
	// both a 400 that names the parameter before this runs. The hand-rolled
	// Atoi caught the first and never the second.
	pos := int(p.Int("pos"))
	rule := firewallRuleFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateNodeFirewallRule(c.Context(), nodeName, pos, rule); err != nil {
		return mapFirewallRuleError(err)
	}
	details := firewallRuleAudit(rule)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "node", nodeName, "update_firewall_rule", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteNodeFirewallRule handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos.
func (h *NodeHandler) DeleteNodeFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pos := int(p.Int("pos"))
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteNodeFirewallRule(c.Context(), nodeName, pos); err != nil {
		return mapFirewallRuleError(err)
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "node", nodeName, "delete_firewall_rule", nil)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetNodeFirewallLog handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/log.
func (h *NodeHandler) GetNodeFirewallLog(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	// Both bounds live in the route's parameter schema now. limit was CLAMPED
	// here at 5000 and floored nowhere — a negative one reached
	// GetNodeFirewallLog, which drops any limit that is not positive, so it
	// read like 0; the declaration bounds it at both ends instead.
	limit, start := int(p.Int("limit")), int(p.Int("start"))
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetNodeFirewallLog(c.Context(), nodeName, limit, start)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, entries)
}
