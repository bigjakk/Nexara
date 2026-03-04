package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

// VMHandler handles VM read endpoints.
type VMHandler struct {
	queries *db.Queries
}

// NewVMHandler creates a new VM handler.
func NewVMHandler(queries *db.Queries) *VMHandler {
	return &VMHandler{queries: queries}
}

type vmResponse struct {
	ID        uuid.UUID `json:"id"`
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	Vmid      int32     `json:"vmid"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CpuCount  int32     `json:"cpu_count"`
	MemTotal  int64     `json:"mem_total"`
	DiskTotal int64     `json:"disk_total"`
	Uptime    int64     `json:"uptime"`
	Template  bool      `json:"template"`
	Tags      string    `json:"tags"`
	HaState   string    `json:"ha_state"`
	Pool      string    `json:"pool"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toVMResponse(v db.Vm) vmResponse {
	return vmResponse{
		ID:         v.ID,
		ClusterID:  v.ClusterID,
		NodeID:     v.NodeID,
		Vmid:       v.Vmid,
		Name:       v.Name,
		Type:       v.Type,
		Status:     v.Status,
		CpuCount:   v.CpuCount,
		MemTotal:   v.MemTotal,
		DiskTotal:  v.DiskTotal,
		Uptime:     v.Uptime,
		Template:   v.Template,
		Tags:       v.Tags,
		HaState:    v.HaState,
		Pool:       v.Pool,
		LastSeenAt: v.LastSeenAt,
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,
	}
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/vms.
func (h *VMHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vms, err := h.queries.ListVMsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list VMs")
	}

	resp := make([]vmResponse, len(vms))
	for i, v := range vms {
		resp[i] = toVMResponse(v)
	}

	return c.JSON(resp)
}
