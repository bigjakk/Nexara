package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// NetworkHandler handles network, firewall, and SDN endpoints.
type NetworkHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewNetworkHandler creates a new NetworkHandler.
func NewNetworkHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *NetworkHandler {
	return &NetworkHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

// createProxmoxClient creates a Proxmox client for the given cluster ID.
func (h *NetworkHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// auditLog records an audit log entry for a mutating network/firewall/SDN operation.
func (h *NetworkHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// --- Network Interface Endpoints ---

// ListNetworkInterfaces handles GET /clusters/:cluster_id/networks.
func (h *NetworkHandler) ListNetworkInterfaces(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}

	type nodeIfaces struct {
		Node       string                     `json:"node"`
		Interfaces []proxmox.NetworkInterface `json:"interfaces"`
	}

	result := make([]nodeIfaces, 0, len(nodes))
	for _, node := range nodes {
		ifaces, err := pxClient.GetNetworkInterfaces(c.Context(), node.Name)
		if err != nil {
			continue
		}
		result = append(result, nodeIfaces{
			Node:       node.Name,
			Interfaces: ifaces,
		})
	}

	return c.JSON(result)
}

// ListNodeNetworkInterfaces handles GET /clusters/:cluster_id/networks/:node_name.
func (h *NetworkHandler) ListNodeNetworkInterfaces(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	ifaces, err := pxClient.GetNetworkInterfaces(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get network interfaces")
	}

	return c.JSON(ifaces)
}

// CreateNetworkInterface handles POST /clusters/:cluster_id/networks/:node_name.
func (h *NetworkHandler) CreateNetworkInterface(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}

	var req proxmox.CreateNetworkInterfaceParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Iface == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "iface and type are required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateNetworkInterface(c.Context(), nodeName, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to create network interface: %v", err))
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName, "iface": req.Iface, "type": req.Type})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("%s/%s", nodeName, req.Iface), "interface_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateNetworkInterface handles PUT /clusters/:cluster_id/networks/:node_name/:iface.
func (h *NetworkHandler) UpdateNetworkInterface(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	ifaceName := c.Params("iface")
	if nodeName == "" || ifaceName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name and interface name are required")
	}

	var req proxmox.UpdateNetworkInterfaceParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "type is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateNetworkInterface(c.Context(), nodeName, ifaceName, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to update network interface: %v", err))
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName, "iface": ifaceName, "type": req.Type})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("%s/%s", nodeName, ifaceName), "interface_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteNetworkInterface handles DELETE /clusters/:cluster_id/networks/:node_name/:iface.
func (h *NetworkHandler) DeleteNetworkInterface(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	ifaceName := c.Params("iface")
	if nodeName == "" || ifaceName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name and interface name are required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteNetworkInterface(c.Context(), nodeName, ifaceName); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to delete network interface: %v", err))
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName, "iface": ifaceName})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("%s/%s", nodeName, ifaceName), "interface_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplyNetworkConfig handles POST /clusters/:cluster_id/networks/:node_name/apply.
func (h *NetworkHandler) ApplyNetworkConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.ApplyNetworkConfig(c.Context(), nodeName); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to apply network config: %v", err))
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName})
	h.auditLog(c, clusterID, "network", nodeName, "network_applied", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// RevertNetworkConfig handles POST /clusters/:cluster_id/networks/:node_name/revert.
func (h *NetworkHandler) RevertNetworkConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.RevertNetworkConfig(c.Context(), nodeName); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to revert network config: %v", err))
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName})
	h.auditLog(c, clusterID, "network", nodeName, "network_reverted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Cluster Firewall Endpoints ---

// ListClusterFirewallRules handles GET /clusters/:cluster_id/firewall/rules.
func (h *NetworkHandler) ListClusterFirewallRules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	rules, err := pxClient.GetClusterFirewallRules(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get firewall rules")
	}

	return c.JSON(rules)
}

// CreateClusterFirewallRule handles POST /clusters/:cluster_id/firewall/rules.
func (h *NetworkHandler) CreateClusterFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req proxmox.FirewallRuleParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Action == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "action and type are required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateClusterFirewallRule(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create firewall rule: "+err.Error())
	}

	details, _ := json.Marshal(map[string]string{"action": req.Action, "type": req.Type})
	h.auditLog(c, clusterID, "network", "cluster", "firewall_rule_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateClusterFirewallRule handles PUT /clusters/:cluster_id/firewall/rules/:pos.
func (h *NetworkHandler) UpdateClusterFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule position")
	}

	var req proxmox.FirewallRuleParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateClusterFirewallRule(c.Context(), pos, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update firewall rule")
	}

	details, _ := json.Marshal(map[string]interface{}{"position": pos, "action": req.Action, "type": req.Type})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("cluster/rule/%d", pos), "firewall_rule_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteClusterFirewallRule handles DELETE /clusters/:cluster_id/firewall/rules/:pos.
func (h *NetworkHandler) DeleteClusterFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule position")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteClusterFirewallRule(c.Context(), pos); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete firewall rule")
	}

	details, _ := json.Marshal(map[string]interface{}{"position": pos})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("cluster/rule/%d", pos), "firewall_rule_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- VM Firewall Endpoints ---

// resolveVMNode looks up the node name for a VM in the database.
func (h *NetworkHandler) resolveVMNode(c *fiber.Ctx, clusterID uuid.UUID, vmid int32) (string, error) {
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

// ListVMFirewallRules handles GET /clusters/:cluster_id/vms/:vm_id/firewall/rules.
func (h *NetworkHandler) ListVMFirewallRules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmid, err := strconv.Atoi(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	nodeName, err := h.resolveVMNode(c, clusterID, safeInt32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	rules, err := pxClient.GetVMFirewallRules(c.Context(), nodeName, vmid)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get VM firewall rules")
	}

	return c.JSON(rules)
}

// CreateVMFirewallRule handles POST /clusters/:cluster_id/vms/:vm_id/firewall/rules.
func (h *NetworkHandler) CreateVMFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmid, err := strconv.Atoi(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	var req proxmox.FirewallRuleParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Action == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "action and type are required")
	}

	nodeName, err := h.resolveVMNode(c, clusterID, safeInt32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateVMFirewallRule(c.Context(), nodeName, vmid, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create VM firewall rule")
	}

	details, _ := json.Marshal(map[string]interface{}{"vmid": vmid, "node": nodeName, "action": req.Action, "type": req.Type})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("vm/%d", vmid), "firewall_rule_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateVMFirewallRule handles PUT /clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos.
func (h *NetworkHandler) UpdateVMFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmid, err := strconv.Atoi(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule position")
	}

	var req proxmox.FirewallRuleParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	nodeName, err := h.resolveVMNode(c, clusterID, safeInt32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateVMFirewallRule(c.Context(), nodeName, vmid, pos, req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update VM firewall rule")
	}

	details, _ := json.Marshal(map[string]interface{}{"vmid": vmid, "node": nodeName, "position": pos, "action": req.Action, "type": req.Type})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("vm/%d/rule/%d", vmid, pos), "firewall_rule_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteVMFirewallRule handles DELETE /clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos.
func (h *NetworkHandler) DeleteVMFirewallRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmid, err := strconv.Atoi(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule position")
	}

	nodeName, err := h.resolveVMNode(c, clusterID, safeInt32(vmid))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteVMFirewallRule(c.Context(), nodeName, vmid, pos); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete VM firewall rule")
	}

	details, _ := json.Marshal(map[string]interface{}{"vmid": vmid, "node": nodeName, "position": pos})
	h.auditLog(c, clusterID, "network", fmt.Sprintf("vm/%d/rule/%d", vmid, pos), "firewall_rule_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Options Endpoints ---

// GetFirewallOptions handles GET /clusters/:cluster_id/firewall/options.
func (h *NetworkHandler) GetFirewallOptions(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	opts, err := pxClient.GetClusterFirewallOptions(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get firewall options")
	}

	return c.JSON(opts)
}

// SetFirewallOptions handles PUT /clusters/:cluster_id/firewall/options.
func (h *NetworkHandler) SetFirewallOptions(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req proxmox.FirewallOptions
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.SetClusterFirewallOptions(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to set firewall options")
	}

	details, _ := json.Marshal(req)
	h.auditLog(c, clusterID, "network", "cluster/options", "firewall_options_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN Endpoints ---

// ListSDNZones handles GET /clusters/:cluster_id/sdn/zones.
func (h *NetworkHandler) ListSDNZones(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	zones, err := pxClient.GetSDNZones(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(zones)
}

// ListSDNVNets handles GET /clusters/:cluster_id/sdn/vnets.
func (h *NetworkHandler) ListSDNVNets(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	vnets, err := pxClient.GetSDNVNets(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(vnets)
}

// --- SDN CRUD Endpoints ---

// CreateSDNZone handles POST /clusters/:cluster_id/sdn/zones.
func (h *NetworkHandler) CreateSDNZone(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req proxmox.CreateSDNZoneParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Zone == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "zone and type are required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateSDNZone(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"zone": req.Zone, "type": req.Type})
	h.auditLog(c, clusterID, "sdn", req.Zone, "sdn_zone_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNZone handles PUT /clusters/:cluster_id/sdn/zones/:zone.
func (h *NetworkHandler) UpdateSDNZone(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	zone := c.Params("zone")
	if zone == "" {
		return fiber.NewError(fiber.StatusBadRequest, "zone is required")
	}

	var req proxmox.UpdateSDNZoneParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateSDNZone(c.Context(), zone, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"zone": zone})
	h.auditLog(c, clusterID, "sdn", zone, "sdn_zone_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNZone handles DELETE /clusters/:cluster_id/sdn/zones/:zone.
func (h *NetworkHandler) DeleteSDNZone(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	zone := c.Params("zone")
	if zone == "" {
		return fiber.NewError(fiber.StatusBadRequest, "zone is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteSDNZone(c.Context(), zone); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"zone": zone})
	h.auditLog(c, clusterID, "sdn", zone, "sdn_zone_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// CreateSDNVNet handles POST /clusters/:cluster_id/sdn/vnets.
func (h *NetworkHandler) CreateSDNVNet(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req proxmox.CreateSDNVNetParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.VNet == "" || req.Zone == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet and zone are required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateSDNVNet(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": req.VNet, "zone": req.Zone})
	h.auditLog(c, clusterID, "sdn", req.VNet, "sdn_vnet_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNVNet handles PUT /clusters/:cluster_id/sdn/vnets/:vnet.
func (h *NetworkHandler) UpdateSDNVNet(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vnet := c.Params("vnet")
	if vnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet is required")
	}

	var req proxmox.UpdateSDNVNetParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateSDNVNet(c.Context(), vnet, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet})
	h.auditLog(c, clusterID, "sdn", vnet, "sdn_vnet_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNVNet handles DELETE /clusters/:cluster_id/sdn/vnets/:vnet.
func (h *NetworkHandler) DeleteSDNVNet(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vnet := c.Params("vnet")
	if vnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteSDNVNet(c.Context(), vnet); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet})
	h.auditLog(c, clusterID, "sdn", vnet, "sdn_vnet_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ListSDNSubnets handles GET /clusters/:cluster_id/sdn/vnets/:vnet/subnets.
func (h *NetworkHandler) ListSDNSubnets(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vnet := c.Params("vnet")
	if vnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	subnets, err := pxClient.GetSDNSubnets(c.Context(), vnet)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(subnets)
}

// CreateSDNSubnet handles POST /clusters/:cluster_id/sdn/vnets/:vnet/subnets.
func (h *NetworkHandler) CreateSDNSubnet(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vnet := c.Params("vnet")
	if vnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet is required")
	}

	var req proxmox.CreateSDNSubnetParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Subnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "subnet is required")
	}
	if req.Type == "" {
		req.Type = "subnet"
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateSDNSubnet(c.Context(), vnet, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet, "subnet": req.Subnet})
	h.auditLog(c, clusterID, "sdn", fmt.Sprintf("%s/%s", vnet, req.Subnet), "sdn_subnet_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNSubnet handles PUT /clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet.
func (h *NetworkHandler) UpdateSDNSubnet(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vnet := c.Params("vnet")
	subnet := c.Params("subnet")
	if vnet == "" || subnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet and subnet are required")
	}

	var req proxmox.UpdateSDNSubnetParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateSDNSubnet(c.Context(), vnet, subnet, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet, "subnet": subnet})
	h.auditLog(c, clusterID, "sdn", fmt.Sprintf("%s/%s", vnet, subnet), "sdn_subnet_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNSubnet handles DELETE /clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet.
func (h *NetworkHandler) DeleteSDNSubnet(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vnet := c.Params("vnet")
	subnet := c.Params("subnet")
	if vnet == "" || subnet == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vnet and subnet are required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteSDNSubnet(c.Context(), vnet, subnet); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet, "subnet": subnet})
	h.auditLog(c, clusterID, "sdn", fmt.Sprintf("%s/%s", vnet, subnet), "sdn_subnet_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplySDN handles PUT /clusters/:cluster_id/sdn/apply.
func (h *NetworkHandler) ApplySDN(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.ApplySDN(c.Context()); err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, clusterID, "sdn", "cluster", "sdn_applied", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN Controller Endpoints ---

// ListSDNControllers handles GET /clusters/:cluster_id/sdn/controllers.
func (h *NetworkHandler) ListSDNControllers(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	controllers, err := pxClient.GetSDNControllers(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(controllers)
}

// CreateSDNController handles POST /clusters/:cluster_id/sdn/controllers.
func (h *NetworkHandler) CreateSDNController(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	var req proxmox.CreateSDNControllerParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Controller == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "controller and type are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSDNController(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"controller": req.Controller, "type": req.Type})
	h.auditLog(c, clusterID, "sdn", req.Controller, "sdn_controller_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNController handles PUT /clusters/:cluster_id/sdn/controllers/:controller.
func (h *NetworkHandler) UpdateSDNController(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	controller := c.Params("controller")
	if controller == "" {
		return fiber.NewError(fiber.StatusBadRequest, "controller is required")
	}
	var req proxmox.UpdateSDNControllerParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSDNController(c.Context(), controller, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"controller": controller})
	h.auditLog(c, clusterID, "sdn", controller, "sdn_controller_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNController handles DELETE /clusters/:cluster_id/sdn/controllers/:controller.
func (h *NetworkHandler) DeleteSDNController(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	controller := c.Params("controller")
	if controller == "" {
		return fiber.NewError(fiber.StatusBadRequest, "controller is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSDNController(c.Context(), controller); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"controller": controller})
	h.auditLog(c, clusterID, "sdn", controller, "sdn_controller_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN IPAM Endpoints ---

// ListSDNIPAMs handles GET /clusters/:cluster_id/sdn/ipams.
func (h *NetworkHandler) ListSDNIPAMs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	ipams, err := pxClient.GetSDNIPAMs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(ipams)
}

// CreateSDNIPAM handles POST /clusters/:cluster_id/sdn/ipams.
func (h *NetworkHandler) CreateSDNIPAM(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	var req proxmox.CreateSDNIPAMParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.IPAM == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ipam and type are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSDNIPAM(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"ipam": req.IPAM, "type": req.Type})
	h.auditLog(c, clusterID, "sdn", req.IPAM, "sdn_ipam_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNIPAM handles PUT /clusters/:cluster_id/sdn/ipams/:ipam.
func (h *NetworkHandler) UpdateSDNIPAM(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	ipam := c.Params("ipam")
	if ipam == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ipam is required")
	}
	var req proxmox.UpdateSDNIPAMParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSDNIPAM(c.Context(), ipam, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"ipam": ipam})
	h.auditLog(c, clusterID, "sdn", ipam, "sdn_ipam_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNIPAM handles DELETE /clusters/:cluster_id/sdn/ipams/:ipam.
func (h *NetworkHandler) DeleteSDNIPAM(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	ipam := c.Params("ipam")
	if ipam == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ipam is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSDNIPAM(c.Context(), ipam); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"ipam": ipam})
	h.auditLog(c, clusterID, "sdn", ipam, "sdn_ipam_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN DNS Endpoints ---

// ListSDNDNS handles GET /clusters/:cluster_id/sdn/dns.
func (h *NetworkHandler) ListSDNDNS(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	plugins, err := pxClient.GetSDNDNSPlugins(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(plugins)
}

// CreateSDNDNS handles POST /clusters/:cluster_id/sdn/dns.
func (h *NetworkHandler) CreateSDNDNS(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	var req proxmox.CreateSDNDNSParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.DNS == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "dns and type are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSDNDNS(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"dns": req.DNS, "type": req.Type})
	h.auditLog(c, clusterID, "sdn", req.DNS, "sdn_dns_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNDNS handles PUT /clusters/:cluster_id/sdn/dns/:dns.
func (h *NetworkHandler) UpdateSDNDNS(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	dns := c.Params("dns")
	if dns == "" {
		return fiber.NewError(fiber.StatusBadRequest, "dns is required")
	}
	var req proxmox.UpdateSDNDNSParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSDNDNS(c.Context(), dns, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"dns": dns})
	h.auditLog(c, clusterID, "sdn", dns, "sdn_dns_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNDNS handles DELETE /clusters/:cluster_id/sdn/dns/:dns.
func (h *NetworkHandler) DeleteSDNDNS(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	dns := c.Params("dns")
	if dns == "" {
		return fiber.NewError(fiber.StatusBadRequest, "dns is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSDNDNS(c.Context(), dns); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"dns": dns})
	h.auditLog(c, clusterID, "sdn", dns, "sdn_dns_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Template Endpoints ---

type templateResponse struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

func toTemplateResponse(t db.FirewallTemplate) templateResponse {
	return templateResponse{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Rules:       t.Rules,
		CreatedAt:   t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

type createTemplateRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
}

// ListTemplates handles GET /api/v1/firewall-templates.
func (h *NetworkHandler) ListTemplates(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	templates, err := h.queries.ListFirewallTemplates(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list firewall templates")
	}

	resp := make([]templateResponse, len(templates))
	for i, t := range templates {
		resp[i] = toTemplateResponse(t)
	}

	return c.JSON(resp)
}

// GetTemplate handles GET /api/v1/firewall-templates/:id.
func (h *NetworkHandler) GetTemplate(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}

	templateID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid template ID")
	}

	tmpl, err := h.queries.GetFirewallTemplate(c.Context(), templateID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Template not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get template")
	}

	return c.JSON(toTemplateResponse(tmpl))
}

// CreateTemplate handles POST /api/v1/firewall-templates.
func (h *NetworkHandler) CreateTemplate(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	var req createTemplateRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}

	if req.Rules == nil {
		req.Rules = json.RawMessage(`[]`)
	}

	tmpl, err := h.queries.CreateFirewallTemplate(c.Context(), db.CreateFirewallTemplateParams{
		Name:        req.Name,
		Description: req.Description,
		Rules:       req.Rules,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create template")
	}

	details, _ := json.Marshal(map[string]string{"name": req.Name, "template_id": tmpl.ID.String()})
	h.auditLog(c, uuid.Nil, "firewall_template", tmpl.ID.String(), "template_created", details)

	return c.Status(fiber.StatusCreated).JSON(toTemplateResponse(tmpl))
}

// UpdateTemplate handles PUT /api/v1/firewall-templates/:id.
func (h *NetworkHandler) UpdateTemplate(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	templateID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid template ID")
	}

	var req createTemplateRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}

	if req.Rules == nil {
		req.Rules = json.RawMessage(`[]`)
	}

	tmpl, err := h.queries.UpdateFirewallTemplate(c.Context(), db.UpdateFirewallTemplateParams{
		ID:          templateID,
		Name:        req.Name,
		Description: req.Description,
		Rules:       req.Rules,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Template not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update template")
	}

	details, _ := json.Marshal(map[string]string{"name": req.Name, "template_id": templateID.String()})
	h.auditLog(c, uuid.Nil, "firewall_template", templateID.String(), "template_updated", details)

	return c.JSON(toTemplateResponse(tmpl))
}

// DeleteTemplate handles DELETE /api/v1/firewall-templates/:id.
func (h *NetworkHandler) DeleteTemplate(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "network"); err != nil {
		return err
	}

	templateID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid template ID")
	}

	if err := h.queries.DeleteFirewallTemplate(c.Context(), templateID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete template")
	}

	details, _ := json.Marshal(map[string]string{"template_id": templateID.String()})
	h.auditLog(c, uuid.Nil, "firewall_template", templateID.String(), "template_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplyTemplate handles POST /clusters/:cluster_id/firewall-templates/:id/apply.
func (h *NetworkHandler) ApplyTemplate(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	templateID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid template ID")
	}

	tmpl, err := h.queries.GetFirewallTemplate(c.Context(), templateID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Template not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get template")
	}

	var rules []proxmox.FirewallRuleParams
	if err := json.Unmarshal(tmpl.Rules, &rules); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to parse template rules")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	applied := 0
	for _, rule := range rules {
		if err := pxClient.CreateClusterFirewallRule(c.Context(), rule); err != nil {
			continue
		}
		applied++
	}

	details, _ := json.Marshal(map[string]interface{}{
		"template_id":   templateID.String(),
		"template_name": tmpl.Name,
		"applied":       applied,
		"total":         len(rules),
	})
	h.auditLog(c, clusterID, "firewall_template", templateID.String(), "template_applied", details)

	return c.JSON(fiber.Map{
		"status":  "ok",
		"applied": applied,
		"total":   len(rules),
	})
}

// --- Firewall Aliases ---

// ListFirewallAliases handles GET /clusters/:cluster_id/firewall/aliases.
func (h *NetworkHandler) ListFirewallAliases(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	aliases, err := pxClient.GetFirewallAliases(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get firewall aliases")
	}
	return c.JSON(aliases)
}

// CreateFirewallAlias handles POST /clusters/:cluster_id/firewall/aliases.
func (h *NetworkHandler) CreateFirewallAlias(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	var req proxmox.FirewallAliasParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" || req.CIDR == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Name and CIDR are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateFirewallAlias(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create firewall alias")
	}
	details, _ := json.Marshal(map[string]string{"name": req.Name, "cidr": req.CIDR})
	h.auditLog(c, clusterID, "firewall_alias", req.Name, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateFirewallAlias handles PUT /clusters/:cluster_id/firewall/aliases/:name.
func (h *NetworkHandler) UpdateFirewallAlias(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	name := c.Params("name")
	var req proxmox.FirewallAliasParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateFirewallAlias(c.Context(), name, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to update firewall alias")
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	h.auditLog(c, clusterID, "firewall_alias", name, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteFirewallAlias handles DELETE /clusters/:cluster_id/firewall/aliases/:name.
func (h *NetworkHandler) DeleteFirewallAlias(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	name := c.Params("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallAlias(c.Context(), name); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete firewall alias")
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	h.auditLog(c, clusterID, "firewall_alias", name, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall IP Sets ---

// ListFirewallIPSets handles GET /clusters/:cluster_id/firewall/ipset.
func (h *NetworkHandler) ListFirewallIPSets(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	sets, err := pxClient.GetFirewallIPSets(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get IP sets")
	}
	return c.JSON(sets)
}

// CreateFirewallIPSet handles POST /clusters/:cluster_id/firewall/ipset.
func (h *NetworkHandler) CreateFirewallIPSet(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	var req struct {
		Name    string `json:"name"`
		Comment string `json:"comment"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateFirewallIPSet(c.Context(), req.Name, req.Comment); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create IP set")
	}
	details, _ := json.Marshal(map[string]string{"name": req.Name})
	h.auditLog(c, clusterID, "firewall_ipset", req.Name, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// DeleteFirewallIPSet handles DELETE /clusters/:cluster_id/firewall/ipset/:name.
func (h *NetworkHandler) DeleteFirewallIPSet(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	name := c.Params("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallIPSet(c.Context(), name); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete IP set")
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	h.auditLog(c, clusterID, "firewall_ipset", name, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// ListFirewallIPSetEntries handles GET /clusters/:cluster_id/firewall/ipset/:name/entries.
func (h *NetworkHandler) ListFirewallIPSetEntries(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	name := c.Params("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetFirewallIPSetEntries(c.Context(), name)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get IP set entries")
	}
	return c.JSON(entries)
}

// AddFirewallIPSetEntry handles POST /clusters/:cluster_id/firewall/ipset/:name/entries.
func (h *NetworkHandler) AddFirewallIPSetEntry(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	name := c.Params("name")
	var req proxmox.FirewallIPSetEntryParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.CIDR == "" {
		return fiber.NewError(fiber.StatusBadRequest, "CIDR is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.AddFirewallIPSetEntry(c.Context(), name, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to add IP set entry")
	}
	details, _ := json.Marshal(map[string]string{"set": name, "cidr": req.CIDR})
	h.auditLog(c, clusterID, "firewall_ipset_entry", name+"/"+req.CIDR, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// DeleteFirewallIPSetEntry handles DELETE /clusters/:cluster_id/firewall/ipset/:name/entries/:cidr.
func (h *NetworkHandler) DeleteFirewallIPSetEntry(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	name := c.Params("name")
	cidr := c.Params("cidr")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallIPSetEntry(c.Context(), name, cidr); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete IP set entry")
	}
	details, _ := json.Marshal(map[string]string{"set": name, "cidr": cidr})
	h.auditLog(c, clusterID, "firewall_ipset_entry", name+"/"+cidr, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Security Groups ---

// ListSecurityGroups handles GET /clusters/:cluster_id/firewall/groups.
func (h *NetworkHandler) ListSecurityGroups(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	groups, err := pxClient.GetFirewallSecurityGroups(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get security groups")
	}
	return c.JSON(groups)
}

// CreateSecurityGroup handles POST /clusters/:cluster_id/firewall/groups.
func (h *NetworkHandler) CreateSecurityGroup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	var req proxmox.FirewallSecurityGroupParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Group == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateFirewallSecurityGroup(c.Context(), req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create security group")
	}
	details, _ := json.Marshal(map[string]string{"group": req.Group})
	h.auditLog(c, clusterID, "firewall_security_group", req.Group, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// DeleteSecurityGroup handles DELETE /clusters/:cluster_id/firewall/groups/:group.
func (h *NetworkHandler) DeleteSecurityGroup(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	group := c.Params("group")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteFirewallSecurityGroup(c.Context(), group); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete security group")
	}
	details, _ := json.Marshal(map[string]string{"group": group})
	h.auditLog(c, clusterID, "firewall_security_group", group, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// ListSecurityGroupRules handles GET /clusters/:cluster_id/firewall/groups/:group/rules.
func (h *NetworkHandler) ListSecurityGroupRules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	group := c.Params("group")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	rules, err := pxClient.GetSecurityGroupRules(c.Context(), group)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get security group rules")
	}
	return c.JSON(rules)
}

// CreateSecurityGroupRule handles POST /clusters/:cluster_id/firewall/groups/:group/rules.
func (h *NetworkHandler) CreateSecurityGroupRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	group := c.Params("group")
	var req proxmox.FirewallRuleParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSecurityGroupRule(c.Context(), group, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to create security group rule")
	}
	details, _ := json.Marshal(map[string]string{"group": group, "action": req.Action})
	h.auditLog(c, clusterID, "firewall_security_group_rule", group, "rule_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSecurityGroupRule handles PUT /clusters/:cluster_id/firewall/groups/:group/rules/:pos.
func (h *NetworkHandler) UpdateSecurityGroupRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	group := c.Params("group")
	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid position")
	}
	var req proxmox.FirewallRuleParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSecurityGroupRule(c.Context(), group, pos, req); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to update security group rule")
	}
	details, _ := json.Marshal(map[string]string{"group": group, "pos": strconv.Itoa(pos)})
	h.auditLog(c, clusterID, "firewall_security_group_rule", group, "rule_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSecurityGroupRule handles DELETE /clusters/:cluster_id/firewall/groups/:group/rules/:pos.
func (h *NetworkHandler) DeleteSecurityGroupRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	group := c.Params("group")
	pos, err := strconv.Atoi(c.Params("pos"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid position")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSecurityGroupRule(c.Context(), group, pos); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to delete security group rule")
	}
	details, _ := json.Marshal(map[string]string{"group": group, "pos": strconv.Itoa(pos)})
	h.auditLog(c, clusterID, "firewall_security_group_rule", group, "rule_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Log ---

// GetFirewallLog handles GET /clusters/:cluster_id/firewall/log.
func (h *NetworkHandler) GetFirewallLog(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "network"); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	nodeName := c.Query("node")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node query parameter is required")
	}
	limit, _ := strconv.Atoi(c.Query("limit", "500"))
	start, _ := strconv.Atoi(c.Query("start", "0"))
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetNodeFirewallLog(c.Context(), nodeName, limit, start)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to get firewall log")
	}
	return c.JSON(entries)
}
