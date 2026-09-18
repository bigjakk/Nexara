package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Firewall rule templates: a saved set of rules Nexara keeps in its OWN
// database, plus the one route that copies a saved set into a cluster's
// firewall. The declarations are registerFirewallTemplateEndpoints in
// internal/api/registry_firewall_templates.go — FOUR of the six, because the
// two writes carry a JSON array of objects that apischema cannot describe.
//
// CreateTemplate and UpdateTemplate are therefore still legacy, and still
// carry their hand-placed requirePerm call. See
// TestFirewallTemplateWritesAreStillLegacy, which pins that on purpose.

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
		CreatedAt:   t.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   t.UpdatedAt.Format(time.RFC3339Nano),
	}
}

type createTemplateRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
}

// ListTemplates handles GET /api/v1/firewall-templates.
func (h *NetworkHandler) ListTemplates(c fiber.Ctx, _ *apischema.Params) error {
	templates, err := h.queries.ListFirewallTemplates(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list firewall templates")
	}

	resp := make([]templateResponse, len(templates))
	for i, t := range templates {
		resp[i] = toTemplateResponse(t)
	}

	return RespondItems(c, resp)
}

// GetTemplate handles GET /api/v1/firewall-templates/:id.
func (h *NetworkHandler) GetTemplate(c fiber.Ctx, p *apischema.Params) error {
	templateID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
//
// Still a LEGACY route, so it keeps its hand-placed permission check: its
// body carries `rules`, an array of objects, and apischema's Property.Items
// is restricted to scalar element types. See
// registerFirewallTemplateEndpoints for the full reasoning.
func (h *NetworkHandler) CreateTemplate(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	var req createTemplateRequest
	if err := c.Bind().Body(&req); err != nil {
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
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "firewall_template", tmpl.ID.String(), "template_created", details)

	return c.Status(fiber.StatusCreated).JSON(toTemplateResponse(tmpl))
}

// UpdateTemplate handles PUT /api/v1/firewall-templates/:id.
//
// Still a LEGACY route, for the reason CreateTemplate gives.
func (h *NetworkHandler) UpdateTemplate(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "network"); err != nil {
		return err
	}

	templateID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid template ID")
	}

	var req createTemplateRequest
	if err := c.Bind().Body(&req); err != nil {
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
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "firewall_template", templateID.String(), "template_updated", details)

	return c.JSON(toTemplateResponse(tmpl))
}

// DeleteTemplate handles DELETE /api/v1/firewall-templates/:id.
func (h *NetworkHandler) DeleteTemplate(c fiber.Ctx, p *apischema.Params) error {
	templateID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if err := h.queries.DeleteFirewallTemplate(c.Context(), templateID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete template")
	}

	details, _ := json.Marshal(map[string]string{"template_id": templateID.String()})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "firewall_template", templateID.String(), "template_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplyTemplate handles POST /api/v1/clusters/:cluster_id/firewall-templates/:id/apply.
func (h *NetworkHandler) ApplyTemplate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	templateID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "firewall_template", templateID.String(), "template_applied", details)

	return c.JSON(fiber.Map{
		"status":  "ok",
		"applied": applied,
		"total":   len(rules),
	})
}
