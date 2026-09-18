package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
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

// nodeFirewallRuleFromParams reads the rule body both the create and the
// update route declare, in the shape the Proxmox client takes. The
// declarations themselves are createNodeFirewallRuleParams and
// updateNodeFirewallRuleParams in internal/api/registry_nodes.go.
//
// Every key is read with a string literal so that
// registry_paramkey_guard_test.go can check it against each route's own
// schema — the reason internal/api/handlers/params.go takes values rather
// than keys everywhere else.
func nodeFirewallRuleFromParams(p *apischema.Params) proxmox.FirewallRuleParams {
	return proxmox.FirewallRuleParams{
		Type:    p.String("type"),
		Action:  p.String("action"),
		Source:  p.String("source"),
		Dest:    p.String("dest"),
		Sport:   p.String("sport"),
		Dport:   p.String("dport"),
		Proto:   p.String("proto"),
		Enable:  int(p.Int("enable")),
		Comment: p.String("comment"),
		Macro:   p.String("macro"),
		Log:     p.String("log"),
		Iface:   p.String("iface"),
	}
}

// nodeFirewallRuleAudit renders a rule for the audit log.
//
// It exists rather than marshalling the proxmox.FirewallRuleParams value
// directly because that type carries `omitempty` on eight of its twelve
// fields, while the request struct it replaced carried none. Marshalling
// the params value would silently drop source, dest, sport, dport, proto,
// comment, macro, log and iface from the audit row whenever they are
// empty — so a row recorded after this migration would not be comparable
// with one recorded before it, and on an UPDATE "the caller cleared dport"
// would become indistinguishable from "the caller never sent it".
func nodeFirewallRuleAudit(rule proxmox.FirewallRuleParams) []byte {
	details, _ := json.Marshal(map[string]any{
		"type":    rule.Type,
		"action":  rule.Action,
		"source":  rule.Source,
		"dest":    rule.Dest,
		"sport":   rule.Sport,
		"dport":   rule.Dport,
		"proto":   rule.Proto,
		"enable":  rule.Enable,
		"comment": rule.Comment,
		"macro":   rule.Macro,
		"log":     rule.Log,
		"iface":   rule.Iface,
	})
	return details
}

// CreateNodeFirewallRule handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules.
func (h *NodeHandler) CreateNodeFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	// type and action are REQUIRED by the schema on this route and optional on
	// the update one, which is the split the handlers already had: Proxmox's
	// rule update keeps the existing direction and action when they are
	// omitted, so only a create has to name them.
	rule := nodeFirewallRuleFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateNodeFirewallRule(c.Context(), nodeName, rule); err != nil {
		return mapProxmoxError(err)
	}
	details := nodeFirewallRuleAudit(rule)
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
	rule := nodeFirewallRuleFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateNodeFirewallRule(c.Context(), nodeName, pos, rule); err != nil {
		return mapFirewallRuleError(err)
	}
	details := nodeFirewallRuleAudit(rule)
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
	// here at 5000 and floored nowhere, so a negative one went straight to
	// Proxmox; the declaration bounds it at both ends instead.
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
