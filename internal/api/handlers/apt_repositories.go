package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// All three routes are declared in internal/api/registry_apt_repositories.go,
// which states their cluster-scoped permission (view:apt_repository for the
// listing, manage:apt_repository for the two writes) and their parameters —
// including the handle pattern this file used to enforce by hand. Nothing below
// re-checks either.

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

func (h *AptRepositoryHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// ListRepositories handles GET /clusters/:cluster_id/nodes/:node/apt/repositories.
func (h *AptRepositoryHandler) ListRepositories(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node")
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

// ToggleRepository handles PUT /clusters/:cluster_id/nodes/:node/apt/repositories.
func (h *AptRepositoryHandler) ToggleRepository(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node")
	path := p.String("path")
	// The declaration bounds index to 0..2147483647 so this narrowing is exact
	// on a 32-bit build too.
	index := int(p.Int("index"))
	enabled := p.Bool("enabled")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.SetNodeAptRepository(c.Context(), nodeName, path, index, enabled, p.String("digest")); err != nil {
		return mapProxmoxError(err)
	}

	action := "disabled"
	if enabled {
		action = "enabled"
	}
	details, _ := json.Marshal(map[string]interface{}{
		"node":    nodeName,
		"path":    path,
		"index":   index,
		"enabled": enabled,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "apt_repository", nodeName, action, details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindAptRepoChange, "apt_repository", nodeName, action)

	return c.JSON(fiber.Map{"status": "ok"})
}

// AddStandardRepository handles POST /clusters/:cluster_id/nodes/:node/apt/repositories.
func (h *AptRepositoryHandler) AddStandardRepository(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node")
	handle := p.String("handle")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.AddNodeAptStandardRepository(c.Context(), nodeName, handle, p.String("digest")); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName, "handle": handle})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "apt_repository", nodeName, "added_standard_repo", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindAptRepoChange, "apt_repository", nodeName, "added_standard_repo")

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}
