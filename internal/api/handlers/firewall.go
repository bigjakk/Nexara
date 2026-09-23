package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// The cluster-wide and per-guest firewall, the aliases, IP sets and security
// groups built on top of it, and the log. The declarations are
// registerFirewallEndpoints in internal/api/registry_firewall.go.

// firewallRuleMissingPhrases is the one die PVE's rule endpoints use for a
// position that is no longer there. Deliberately just the one: "no such alias"
// is a different object on a neighbouring endpoint and must keep its 502.
var firewallRuleMissingPhrases = []string{"no rule at position"}

// mapFirewallRuleError adds position-specific handling on top of
// mapProxmoxError, in the same shape as mapTemplateError.
//
// PVE's rule endpoints die with a plain "no rule at position N" — a bare 500
// with no rejection map — so mapProxmoxError can only call it a gateway
// failure. It is not one: two operators with the same list open will do this to
// each other routinely, and the answer is "your view is stale", which is what
// 404 says. Nexara sends no digest, so PVE's assert_if_modified 409 never
// fires and 404 is the right code here.
func mapFirewallRuleError(err error) error {
	return mapMissingObjectError("No firewall rule at that position — the list may be out of date",
		firewallRuleMissingPhrases, err)
}

// firewallRuleFromParams reads the twelve-field rule body in the shape the
// Proxmox client takes. Four route pairs declare it — the node, cluster,
// guest and security-group rule sets — through firewallRuleBody in
// internal/api/registry_firewall.go.
//
// Every key is read with a string literal so that
// registry_paramkey_guard_test.go can check it against each route's own
// schema — the reason internal/api/handlers/params.go takes values rather
// than keys everywhere else.
func firewallRuleFromParams(p *apischema.Params) proxmox.FirewallRuleParams {
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

// firewallRuleAudit renders a rule for the audit log.
//
// Only the NODE firewall routes use it, even though it lives here with the
// rule body it renders. The cluster, guest and security-group handlers below
// deliberately keep the narrower {action, type} payloads they recorded
// before this migration: widening them would make a row written today
// incomparable with one written last week, which is the same argument this
// function exists for from the other direction.
//
// It exists rather than marshalling the proxmox.FirewallRuleParams value
// directly because that type carries `omitempty` on eight of its twelve
// fields, while the request struct it replaced carried none. Marshalling
// the params value would silently drop source, dest, sport, dport, proto,
// comment, macro, log and iface from the audit row whenever they are
// empty — so a row recorded after this migration would not be comparable
// with one recorded before it, and on an UPDATE "the caller cleared dport"
// would become indistinguishable from "the caller never sent it".
func firewallRuleAudit(rule proxmox.FirewallRuleParams) []byte {
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

// --- Cluster Firewall Endpoints ---

// ListClusterFirewallRules handles GET /api/v1/clusters/:cluster_id/firewall/rules.
func (h *NetworkHandler) ListClusterFirewallRules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	rules, err := pxClient.GetClusterFirewallRules(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, rules)
}

// CreateClusterFirewallRule handles POST /api/v1/clusters/:cluster_id/firewall/rules.
func (h *NetworkHandler) CreateClusterFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// type and action are REQUIRED and non-empty by the schema on this route
	// (firewallRuleBody's create spelling) and optional on the update one,
	// which is the split the handlers already had — including their refusal
	// of an empty value, which is what the declared MinLength restates.
	req := firewallRuleFromParams(p)

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateClusterFirewallRule(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"action": req.Action, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", "cluster", "firewall_rule_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateClusterFirewallRule handles PUT /api/v1/clusters/:cluster_id/firewall/rules/:pos.
func (h *NetworkHandler) UpdateClusterFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// The schema declares :pos as a non-negative integer, so "abc" and "-1"
	// are both a 400 that names the parameter before this runs. The
	// hand-rolled Atoi caught the first and never the second.
	pos := int(p.Int("pos"))
	req := firewallRuleFromParams(p)

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateClusterFirewallRule(c.Context(), pos, req); err != nil {
		return mapFirewallRuleError(err)
	}

	details, _ := json.Marshal(map[string]interface{}{"position": pos, "action": req.Action, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("cluster/rule/%d", pos), "firewall_rule_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteClusterFirewallRule handles DELETE /api/v1/clusters/:cluster_id/firewall/rules/:pos.
func (h *NetworkHandler) DeleteClusterFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pos := int(p.Int("pos"))

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteClusterFirewallRule(c.Context(), pos); err != nil {
		return mapFirewallRuleError(err)
	}

	details, _ := json.Marshal(map[string]interface{}{"position": pos})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("cluster/rule/%d", pos), "firewall_rule_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- VM Firewall Endpoints ---

// resolveVMNode looks up the node name for a VM in the database.
//
// The lookup is CLUSTER-SCOPED — GetVMByClusterAndVmid takes the cluster the
// route's path named — so a VMID that exists on some other cluster is a 404
// here rather than a read of another cluster's guest.
func (h *NetworkHandler) resolveVMNode(c fiber.Ctx, clusterID uuid.UUID, vmid int32) (string, error) {
	vm, err := h.queries.GetVMByClusterAndVmid(c.Context(), db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID,
		Vmid:      vmid,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to look up VM")
	}

	node, err := h.queries.GetNode(c.Context(), vm.NodeID)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to look up node")
	}

	return node.Name, nil
}

// ListVMFirewallRules handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules.
func (h *NetworkHandler) ListVMFirewallRules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// :vm_id is the PROXMOX VMID on this route, not Nexara's row uuid — see
	// firewallVMIDParam in internal/api/registry_firewall.go. The schema
	// declares it as a bounded integer, so the hand-rolled Atoi is gone.
	vmid := int(p.Int("vm_id"))

	nodeName, err := h.resolveVMNode(c, clusterID, safeconv.Int32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	rules, err := pxClient.GetVMFirewallRules(c.Context(), nodeName, vmid)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, rules)
}

// CreateVMFirewallRule handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules.
func (h *NetworkHandler) CreateVMFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vmid := int(p.Int("vm_id"))
	req := firewallRuleFromParams(p)

	nodeName, err := h.resolveVMNode(c, clusterID, safeconv.Int32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateVMFirewallRule(c.Context(), nodeName, vmid, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]interface{}{"vmid": vmid, "node": nodeName, "action": req.Action, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("vm/%d", vmid), "firewall_rule_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateVMFirewallRule handles PUT /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos.
func (h *NetworkHandler) UpdateVMFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vmid := int(p.Int("vm_id"))
	pos := int(p.Int("pos"))
	req := firewallRuleFromParams(p)

	nodeName, err := h.resolveVMNode(c, clusterID, safeconv.Int32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateVMFirewallRule(c.Context(), nodeName, vmid, pos, req); err != nil {
		return mapFirewallRuleError(err)
	}

	details, _ := json.Marshal(map[string]interface{}{"vmid": vmid, "node": nodeName, "position": pos, "action": req.Action, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("vm/%d/rule/%d", vmid, pos), "firewall_rule_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteVMFirewallRule handles DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos.
func (h *NetworkHandler) DeleteVMFirewallRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vmid := int(p.Int("vm_id"))
	pos := int(p.Int("pos"))

	nodeName, err := h.resolveVMNode(c, clusterID, safeconv.Int32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteVMFirewallRule(c.Context(), nodeName, vmid, pos); err != nil {
		return mapFirewallRuleError(err)
	}

	details, _ := json.Marshal(map[string]interface{}{"vmid": vmid, "node": nodeName, "position": pos})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("vm/%d/rule/%d", vmid, pos), "firewall_rule_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Options Endpoints ---

// GetFirewallOptions handles GET /api/v1/clusters/:cluster_id/firewall/options.
func (h *NetworkHandler) GetFirewallOptions(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	opts, err := pxClient.GetClusterFirewallOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(opts)
}

// SetFirewallOptions handles PUT /api/v1/clusters/:cluster_id/firewall/options.
func (h *NetworkHandler) SetFirewallOptions(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	req := proxmox.FirewallOptions{
		// A POINTER, and the OptInt read is what keeps it one: the options
		// card sends {"policy_in": "DROP"} with no enable at all, and a plain
		// read would turn that into enable=0 — silently disabling the
		// cluster firewall on a policy change.
		Enable:      optIntPtr(p.OptInt("enable")),
		PolicyIn:    p.String("policy_in"),
		PolicyOut:   p.String("policy_out"),
		LogLevelIn:  p.String("log_level_in"),
		LogLevelOut: p.String("log_level_out"),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.SetClusterFirewallOptions(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(req)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", "cluster/options", "firewall_options_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Aliases ---

// ListFirewallAliases handles GET /api/v1/clusters/:cluster_id/firewall/aliases.
func (h *NetworkHandler) ListFirewallAliases(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	aliases, err := pxClient.GetFirewallAliases(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, aliases)
}

// CreateFirewallAlias handles POST /api/v1/clusters/:cluster_id/firewall/aliases.
func (h *NetworkHandler) CreateFirewallAlias(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	// name and cidr are both required by the schema, which is the pair the
	// handler refused an empty value for. `rename` is not declared on this
	// route because CreateFirewallAlias never read it.
	req := proxmox.FirewallAliasParams{
		Name:    p.String("name"),
		CIDR:    p.String("cidr"),
		Comment: p.String("comment"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateFirewallAlias(c.Context(), req); err != nil {
		return mapDuplicateNameError("A firewall alias with that name already exists", err)
	}
	details, _ := json.Marshal(map[string]string{"name": req.Name, "cidr": req.CIDR})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_alias", req.Name, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateFirewallAlias handles PUT /api/v1/clusters/:cluster_id/firewall/aliases/:name.
func (h *NetworkHandler) UpdateFirewallAlias(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	// No Name here: the alias being changed comes from the PATH, and the
	// schema declares no body field for it — so a body that named a
	// different alias, which used to be silently dropped, is now a 400.
	req := proxmox.FirewallAliasParams{
		CIDR:    p.String("cidr"),
		Comment: p.String("comment"),
		Rename:  p.String("rename"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateFirewallAlias(c.Context(), name, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_alias", name, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteFirewallAlias handles DELETE /api/v1/clusters/:cluster_id/firewall/aliases/:name.
func (h *NetworkHandler) DeleteFirewallAlias(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallAlias(c.Context(), name); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_alias", name, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall IP Sets ---

// ListFirewallIPSets handles GET /api/v1/clusters/:cluster_id/firewall/ipset.
func (h *NetworkHandler) ListFirewallIPSets(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	sets, err := pxClient.GetFirewallIPSets(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, sets)
}

// CreateFirewallIPSet handles POST /api/v1/clusters/:cluster_id/firewall/ipset.
func (h *NetworkHandler) CreateFirewallIPSet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name, comment := p.String("name"), p.String("comment")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateFirewallIPSet(c.Context(), name, comment); err != nil {
		return mapDuplicateNameError("An IP set with that name already exists", err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_ipset", name, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// DeleteFirewallIPSet handles DELETE /api/v1/clusters/:cluster_id/firewall/ipset/:name.
func (h *NetworkHandler) DeleteFirewallIPSet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallIPSet(c.Context(), name); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_ipset", name, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// ListFirewallIPSetEntries handles GET /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries.
func (h *NetworkHandler) ListFirewallIPSetEntries(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetFirewallIPSetEntries(c.Context(), name)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, entries)
}

// AddFirewallIPSetEntry handles POST /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries.
func (h *NetworkHandler) AddFirewallIPSetEntry(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	req := proxmox.FirewallIPSetEntryParams{
		CIDR: p.String("cidr"),
		// A POINTER: the key is sent only when the caller chose, so
		// "omitted" and "0" stay different requests.
		NoMatch: optIntPtr(p.OptInt("nomatch")),
		Comment: p.String("comment"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.AddFirewallIPSetEntry(c.Context(), name, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"set": name, "cidr": req.CIDR})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_ipset_entry", name+"/"+req.CIDR, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// DeleteFirewallIPSetEntry handles DELETE /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries/:cidr.
func (h *NetworkHandler) DeleteFirewallIPSetEntry(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	// An entry id is a CIDR, so a correct client percent-encodes its slash and
	// Fiber hands back exactly that: "192.0.2.0%2F24". Decoding is what makes
	// the entry Proxmox receives the entry the caller meant — without it
	// DeleteFirewallIPSetEntry escapes the "%" a second time and the request
	// addresses an entry literally named "192.0.2.0%2F24", which is why no
	// CIDR entry could be removed through this route at all. The set NAME is
	// not decoded because its declared rule (pve-object-id) admits no escape.
	cidr, err := accessParam(p.String("cidr"), "IP set entry")
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallIPSetEntry(c.Context(), name, cidr); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"set": name, "cidr": cidr})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_ipset_entry", name+"/"+cidr, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Security Groups ---

// ListSecurityGroups handles GET /api/v1/clusters/:cluster_id/firewall/groups.
func (h *NetworkHandler) ListSecurityGroups(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	groups, err := pxClient.GetFirewallSecurityGroups(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, groups)
}

// CreateSecurityGroup handles POST /api/v1/clusters/:cluster_id/firewall/groups.
func (h *NetworkHandler) CreateSecurityGroup(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.FirewallSecurityGroupParams{
		Group:   p.String("group"),
		Comment: p.String("comment"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateFirewallSecurityGroup(c.Context(), req); err != nil {
		return mapDuplicateNameError("A security group with that name already exists", err)
	}
	details, _ := json.Marshal(map[string]string{"group": req.Group})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_security_group", req.Group, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// DeleteSecurityGroup handles DELETE /api/v1/clusters/:cluster_id/firewall/groups/:group.
func (h *NetworkHandler) DeleteSecurityGroup(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	group := p.String("group")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallSecurityGroup(c.Context(), group); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"group": group})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_security_group", group, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// ListSecurityGroupRules handles GET /api/v1/clusters/:cluster_id/firewall/groups/:group/rules.
func (h *NetworkHandler) ListSecurityGroupRules(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	group := p.String("group")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	rules, err := pxClient.GetSecurityGroupRules(c.Context(), group)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, rules)
}

// CreateSecurityGroupRule handles POST /api/v1/clusters/:cluster_id/firewall/groups/:group/rules.
func (h *NetworkHandler) CreateSecurityGroupRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	group := p.String("group")
	// type and action are OPTIONAL on this route, unlike the cluster and
	// node rule creates: this handler never required them.
	req := firewallRuleFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSecurityGroupRule(c.Context(), group, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"group": group, "action": req.Action})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_security_group_rule", group, "rule_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSecurityGroupRule handles PUT /api/v1/clusters/:cluster_id/firewall/groups/:group/rules/:pos.
func (h *NetworkHandler) UpdateSecurityGroupRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	group := p.String("group")
	pos := int(p.Int("pos"))
	req := firewallRuleFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSecurityGroupRule(c.Context(), group, pos, req); err != nil {
		return mapFirewallRuleError(err)
	}
	details, _ := json.Marshal(map[string]string{"group": group, "pos": strconv.Itoa(pos)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_security_group_rule", group, "rule_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSecurityGroupRule handles DELETE /api/v1/clusters/:cluster_id/firewall/groups/:group/rules/:pos.
func (h *NetworkHandler) DeleteSecurityGroupRule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	group := p.String("group")
	pos := int(p.Int("pos"))
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSecurityGroupRule(c.Context(), group, pos); err != nil {
		return mapFirewallRuleError(err)
	}
	details, _ := json.Marshal(map[string]string{"group": group, "pos": strconv.Itoa(pos)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_security_group_rule", group, "rule_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Log ---

// GetFirewallLog handles GET /api/v1/clusters/:cluster_id/firewall/log.
func (h *NetworkHandler) GetFirewallLog(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	// node is required by the schema and carries the node-name format, which
	// additionally rejects the empty string the hand-rolled c.Query could not
	// tell from absent. Both bounds on limit and start live in the schema
	// now; a negative one used to reach GetNodeFirewallLog, which drops any
	// value that is not positive, so it read like 0.
	nodeName := p.String("node")
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
