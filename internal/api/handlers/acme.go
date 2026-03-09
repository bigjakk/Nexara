package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// ACMEHandler handles ACME certificate management endpoints.
type ACMEHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewACMEHandler creates a new ACMEHandler.
func NewACMEHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ACMEHandler {
	return &ACMEHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *ACMEHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *ACMEHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// --- ACME Accounts ---

// ListAccounts handles GET /clusters/:cluster_id/acme/accounts.
func (h *ACMEHandler) ListAccounts(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	accounts, err := pxClient.GetACMEAccounts(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(accounts)
}

// CreateAccount handles POST /clusters/:cluster_id/acme/accounts.
func (h *ACMEHandler) CreateAccount(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreateACMEAccountParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Contact == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Contact email is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateACMEAccount(c.Context(), req)
	if err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": req.Name, "contact": req.Contact})
	h.auditLog(c, clusterID, "acme_account", req.Name, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_account", req.Name, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"upid": upid})
}

// GetAccount handles GET /clusters/:cluster_id/acme/accounts/:name.
func (h *ACMEHandler) GetAccount(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	name := c.Params("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	account, err := pxClient.GetACMEAccount(c.Context(), name)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(account)
}

// UpdateAccount handles PUT /clusters/:cluster_id/acme/accounts/:name.
func (h *ACMEHandler) UpdateAccount(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	name := c.Params("name")
	var req proxmox.UpdateACMEAccountParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateACMEAccount(c.Context(), name, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	h.auditLog(c, clusterID, "acme_account", name, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_account", name, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteAccount handles DELETE /clusters/:cluster_id/acme/accounts/:name.
func (h *ACMEHandler) DeleteAccount(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	name := c.Params("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteACMEAccount(c.Context(), name); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	h.auditLog(c, clusterID, "acme_account", name, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_account", name, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- ACME Plugins ---

// ListPlugins handles GET /clusters/:cluster_id/acme/plugins.
func (h *ACMEHandler) ListPlugins(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	plugins, err := pxClient.GetACMEPlugins(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(plugins)
}

// CreatePlugin handles POST /clusters/:cluster_id/acme/plugins.
func (h *ACMEHandler) CreatePlugin(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreateACMEPluginParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.ID == "" || req.Type == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ID and type are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateACMEPlugin(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": req.ID, "type": req.Type})
	h.auditLog(c, clusterID, "acme_plugin", req.ID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_plugin", req.ID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdatePlugin handles PUT /clusters/:cluster_id/acme/plugins/:plugin_id.
func (h *ACMEHandler) UpdatePlugin(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pluginID := c.Params("plugin_id")
	var req proxmox.UpdateACMEPluginParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateACMEPlugin(c.Context(), pluginID, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": pluginID})
	h.auditLog(c, clusterID, "acme_plugin", pluginID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_plugin", pluginID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeletePlugin handles DELETE /clusters/:cluster_id/acme/plugins/:plugin_id.
func (h *ACMEHandler) DeletePlugin(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pluginID := c.Params("plugin_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteACMEPlugin(c.Context(), pluginID); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": pluginID})
	h.auditLog(c, clusterID, "acme_plugin", pluginID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_plugin", pluginID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- ACME Challenge Schema, Directories & TOS ---

// ListChallengeSchema handles GET /clusters/:cluster_id/acme/challenge-schema.
func (h *ACMEHandler) ListChallengeSchema(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := pxClient.GetACMEChallengeSchemaRaw(c.Context(), &raw); err != nil {
		return mapProxmoxError(err)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// ListDirectories handles GET /clusters/:cluster_id/acme/directories.
func (h *ACMEHandler) ListDirectories(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	dirs, err := pxClient.GetACMEDirectories(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(dirs)
}

// GetTOS handles GET /clusters/:cluster_id/acme/tos.
func (h *ACMEHandler) GetTOS(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	tos, err := pxClient.GetACMETOS(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{"url": tos})
}

// --- Node ACME Config ---

// GetNodeACMEConfig handles GET /clusters/:cluster_id/nodes/:node/acme-config.
func (h *ACMEHandler) GetNodeACMEConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	node := c.Params("node")
	if node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	cfg, err := pxClient.GetNodeACMEConfig(c.Context(), node)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(cfg)
}

// SetNodeACMEConfig handles PUT /clusters/:cluster_id/nodes/:node/acme-config.
func (h *ACMEHandler) SetNodeACMEConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	node := c.Params("node")
	if node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}
	var req proxmox.NodeACMEConfig
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.SetNodeACMEConfig(c.Context(), node, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"node": node})
	h.auditLog(c, clusterID, "acme_config", node, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Node Certificates ---

// ListNodeCertificates handles GET /clusters/:cluster_id/nodes/:node/certificates.
func (h *ACMEHandler) ListNodeCertificates(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	node := c.Params("node")
	if node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	certs, err := pxClient.GetNodeCertificates(c.Context(), node)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(certs)
}

// OrderNodeCertificate handles POST /clusters/:cluster_id/nodes/:node/certificates/order.
func (h *ACMEHandler) OrderNodeCertificate(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	node := c.Params("node")
	var req struct {
		Force bool `json:"force"`
	}
	_ = c.BodyParser(&req)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.OrderNodeCertificate(c.Context(), node, req.Force)
	if err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"node": node})
	h.auditLog(c, clusterID, "certificate", node, "ordered", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "certificate", node, "ordered")
	return c.JSON(fiber.Map{"upid": upid})
}

// RenewNodeCertificate handles PUT /clusters/:cluster_id/nodes/:node/certificates/renew.
func (h *ACMEHandler) RenewNodeCertificate(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	node := c.Params("node")
	var req struct {
		Force bool `json:"force"`
	}
	_ = c.BodyParser(&req)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.RenewNodeCertificate(c.Context(), node, req.Force)
	if err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"node": node})
	h.auditLog(c, clusterID, "certificate", node, "renewed", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "certificate", node, "renewed")
	return c.JSON(fiber.Map{"upid": upid})
}

// RevokeNodeCertificate handles DELETE /clusters/:cluster_id/nodes/:node/certificates/revoke.
func (h *ACMEHandler) RevokeNodeCertificate(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "certificate"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	node := c.Params("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.RevokeNodeCertificate(c.Context(), node)
	if err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"node": node})
	h.auditLog(c, clusterID, "certificate", node, "revoked", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "certificate", node, "revoked")
	return c.JSON(fiber.Map{"upid": upid})
}
