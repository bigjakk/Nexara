package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

// NodeHandler handles node read endpoints.
type NodeHandler struct {
	queries *db.Queries
}

// NewNodeHandler creates a new node handler.
func NewNodeHandler(queries *db.Queries) *NodeHandler {
	return &NodeHandler{queries: queries}
}

type nodeResponse struct {
	ID         uuid.UUID `json:"id"`
	ClusterID  uuid.UUID `json:"cluster_id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	CpuCount   int32     `json:"cpu_count"`
	MemTotal   int64     `json:"mem_total"`
	DiskTotal  int64     `json:"disk_total"`
	PveVersion string    `json:"pve_version"`
	Uptime     int64     `json:"uptime"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toNodeResponse(n db.Node) nodeResponse {
	return nodeResponse{
		ID:         n.ID,
		ClusterID:  n.ClusterID,
		Name:       n.Name,
		Status:     n.Status,
		CpuCount:   n.CpuCount,
		MemTotal:   n.MemTotal,
		DiskTotal:  n.DiskTotal,
		PveVersion: n.PveVersion,
		Uptime:     n.Uptime,
		LastSeenAt: n.LastSeenAt,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/nodes.
func (h *NodeHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}

	resp := make([]nodeResponse, len(nodes))
	for i, n := range nodes {
		resp[i] = toNodeResponse(n)
	}

	return c.JSON(resp)
}
