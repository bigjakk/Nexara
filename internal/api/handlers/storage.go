package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

// StorageHandler handles storage pool read endpoints.
type StorageHandler struct {
	queries *db.Queries
}

// NewStorageHandler creates a new storage handler.
func NewStorageHandler(queries *db.Queries) *StorageHandler {
	return &StorageHandler{queries: queries}
}

type storageResponse struct {
	ID        uuid.UUID `json:"id"`
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	Storage   string    `json:"storage"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Active    bool      `json:"active"`
	Enabled   bool      `json:"enabled"`
	Shared    bool      `json:"shared"`
	Total     int64     `json:"total"`
	Used      int64     `json:"used"`
	Avail     int64     `json:"avail"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toStorageResponse(s db.StoragePool) storageResponse {
	return storageResponse{
		ID:         s.ID,
		ClusterID:  s.ClusterID,
		NodeID:     s.NodeID,
		Storage:    s.Storage,
		Type:       s.Type,
		Content:    s.Content,
		Active:     s.Active,
		Enabled:    s.Enabled,
		Shared:     s.Shared,
		Total:      s.Total,
		Used:       s.Used,
		Avail:      s.Avail,
		LastSeenAt: s.LastSeenAt,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/storage.
func (h *StorageHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pools, err := h.queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list storage pools")
	}

	resp := make([]storageResponse, len(pools))
	for i, p := range pools {
		resp[i] = toStorageResponse(p)
	}

	return c.JSON(resp)
}
