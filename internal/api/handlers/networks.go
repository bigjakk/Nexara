package handlers

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// NetworkHandler handles network, firewall, and SDN endpoints.
type NetworkHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewNetworkHandler creates a new NetworkHandler.
func NewNetworkHandler(queries *db.Queries, encryptionKey string) *NetworkHandler {
	return &NetworkHandler{queries: queries, encryptionKey: encryptionKey}
}

// createProxmoxClient creates a Proxmox client for the given cluster ID.
func (h *NetworkHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}

// --- Network Interface Endpoints ---

// ListNetworkInterfaces handles GET /clusters/:cluster_id/networks.
func (h *NetworkHandler) ListNetworkInterfaces(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		Node       string                    `json:"node"`
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create network interface")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateNetworkInterface handles PUT /clusters/:cluster_id/networks/:node_name/:iface.
func (h *NetworkHandler) UpdateNetworkInterface(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update network interface")
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteNetworkInterface handles DELETE /clusters/:cluster_id/networks/:node_name/:iface.
func (h *NetworkHandler) DeleteNetworkInterface(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete network interface")
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplyNetworkConfig handles POST /clusters/:cluster_id/networks/:node_name/apply.
func (h *NetworkHandler) ApplyNetworkConfig(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to apply network config")
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// RevertNetworkConfig handles POST /clusters/:cluster_id/networks/:node_name/revert.
func (h *NetworkHandler) RevertNetworkConfig(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revert network config")
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Cluster Firewall Endpoints ---

// ListClusterFirewallRules handles GET /clusters/:cluster_id/firewall/rules.
func (h *NetworkHandler) ListClusterFirewallRules(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create firewall rule")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateClusterFirewallRule handles PUT /clusters/:cluster_id/firewall/rules/:pos.
func (h *NetworkHandler) UpdateClusterFirewallRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteClusterFirewallRule handles DELETE /clusters/:cluster_id/firewall/rules/:pos.
func (h *NetworkHandler) DeleteClusterFirewallRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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

	nodeName, err := h.resolveVMNode(c, clusterID, int32(vmid))
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
	if err := requireAdmin(c); err != nil {
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

	nodeName, err := h.resolveVMNode(c, clusterID, int32(vmid))
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

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateVMFirewallRule handles PUT /clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos.
func (h *NetworkHandler) UpdateVMFirewallRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	nodeName, err := h.resolveVMNode(c, clusterID, int32(vmid))
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

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteVMFirewallRule handles DELETE /clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos.
func (h *NetworkHandler) DeleteVMFirewallRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	nodeName, err := h.resolveVMNode(c, clusterID, int32(vmid))
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

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Firewall Options Endpoints ---

// GetFirewallOptions handles GET /clusters/:cluster_id/firewall/options.
func (h *NetworkHandler) GetFirewallOptions(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN Endpoints ---

// ListSDNZones handles GET /clusters/:cluster_id/sdn/zones.
func (h *NetworkHandler) ListSDNZones(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get SDN zones")
	}

	return c.JSON(zones)
}

// ListSDNVNets handles GET /clusters/:cluster_id/sdn/vnets.
func (h *NetworkHandler) ListSDNVNets(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get SDN VNets")
	}

	return c.JSON(vnets)
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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
	if err := requireAdmin(c); err != nil {
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

	return c.Status(fiber.StatusCreated).JSON(toTemplateResponse(tmpl))
}

// UpdateTemplate handles PUT /api/v1/firewall-templates/:id.
func (h *NetworkHandler) UpdateTemplate(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(toTemplateResponse(tmpl))
}

// DeleteTemplate handles DELETE /api/v1/firewall-templates/:id.
func (h *NetworkHandler) DeleteTemplate(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	templateID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid template ID")
	}

	if err := h.queries.DeleteFirewallTemplate(c.Context(), templateID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete template")
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplyTemplate handles POST /clusters/:cluster_id/firewall-templates/:id/apply.
func (h *NetworkHandler) ApplyTemplate(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
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

	return c.JSON(fiber.Map{
		"status":  "ok",
		"applied": applied,
		"total":   len(rules),
	})
}
