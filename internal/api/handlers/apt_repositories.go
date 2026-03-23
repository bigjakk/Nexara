package handlers

import (
	"encoding/json"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

var handlePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// AptRepositoryHandler handles APT repository management endpoints.
type AptRepositoryHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewAptRepositoryHandler creates a new AptRepositoryHandler.
func NewAptRepositoryHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *AptRepositoryHandler {
	return &AptRepositoryHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *AptRepositoryHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *AptRepositoryHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "apt_repository", resourceID, action, details)
}

// ListRepositories handles GET /clusters/:cluster_id/nodes/:node/apt/repositories.
func (h *AptRepositoryHandler) ListRepositories(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "apt_repository"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	nodeName := c.Params("node")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	repos, err := pxClient.GetNodeAptRepositories(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(repos)
}

type toggleRepoRequest struct {
	Path    string `json:"path"`
	Index   int    `json:"index"`
	Enabled bool   `json:"enabled"`
	Digest  string `json:"digest"`
}

// ToggleRepository handles PUT /clusters/:cluster_id/nodes/:node/apt/repositories.
func (h *AptRepositoryHandler) ToggleRepository(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "apt_repository"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	nodeName := c.Params("node")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}
	var req toggleRepoRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Path == "" || len(req.Path) > 256 {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid file path")
	}
	if req.Index < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "Index must be non-negative")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.SetNodeAptRepository(c.Context(), nodeName, req.Path, req.Index, req.Enabled, req.Digest); err != nil {
		return mapProxmoxError(err)
	}

	action := "disabled"
	if req.Enabled {
		action = "enabled"
	}
	details, _ := json.Marshal(map[string]interface{}{
		"node":    nodeName,
		"path":    req.Path,
		"index":   req.Index,
		"enabled": req.Enabled,
	})
	h.auditLog(c, clusterID, nodeName, action, details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindAptRepoChange, "apt_repository", nodeName, action)

	return c.JSON(fiber.Map{"status": "ok"})
}

type addStandardRepoRequest struct {
	Handle string `json:"handle"`
	Digest string `json:"digest"`
}

// AddStandardRepository handles POST /clusters/:cluster_id/nodes/:node/apt/repositories.
func (h *AptRepositoryHandler) AddStandardRepository(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "apt_repository"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	nodeName := c.Params("node")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node name is required")
	}
	var req addStandardRepoRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if !handlePattern.MatchString(req.Handle) {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid repository handle")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.AddNodeAptStandardRepository(c.Context(), nodeName, req.Handle, req.Digest); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName, "handle": req.Handle})
	h.auditLog(c, clusterID, nodeName, "added_standard_repo", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindAptRepoChange, "apt_repository", nodeName, "added_standard_repo")

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}
