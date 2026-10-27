package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// A node's firewall rule has no id of its own either: its position is its only
// address and moves with every insert above it. The audit rows file under the rule
// set of the node's scope, "node/<node>" — firewall_rule, as the cluster's rules
// (cluster) and a guest's (vm/<vmid>) do — with the position in the details.
func nodeFirewallScope(node string) string { return "node/" + node }

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
	details := firewallRuleAudit(rule, nil)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_rule", nodeFirewallScope(nodeName), "firewall_rule_created", details)
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
	if err := pxClient.UpdateNodeFirewallRule(c.Context(), nodeName, pos, rule, firewallRuleDigest(p)); err != nil {
		return mapFirewallRuleError(err)
	}
	details := firewallRuleAudit(rule, &pos)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_rule", nodeFirewallScope(nodeName), "firewall_rule_updated", details)
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
	if err := pxClient.DeleteNodeFirewallRule(c.Context(), nodeName, pos, firewallRuleDigest(p)); err != nil {
		return mapFirewallRuleError(err)
	}
	details, _ := json.Marshal(map[string]int{"position": pos})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_rule", nodeFirewallScope(nodeName), "firewall_rule_deleted", details)
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
