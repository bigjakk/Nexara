package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

// AuditHandler handles audit log endpoints.
type AuditHandler struct {
	queries *db.Queries
}

// NewAuditHandler creates a new audit handler.
func NewAuditHandler(queries *db.Queries) *AuditHandler {
	return &AuditHandler{queries: queries}
}

type auditLogResponse struct {
	ID              uuid.UUID `json:"id"`
	ClusterID       *string   `json:"cluster_id"`
	UserID          uuid.UUID `json:"user_id"`
	ResourceType    string    `json:"resource_type"`
	ResourceID      string    `json:"resource_id"`
	Action          string    `json:"action"`
	Details         string    `json:"details"`
	CreatedAt       string    `json:"created_at"`
	UserEmail       string    `json:"user_email"`
	UserDisplayName string    `json:"user_display_name"`
	ClusterName     string    `json:"cluster_name"`
	ResourceVMID    int32     `json:"resource_vmid"`
	ResourceName    string    `json:"resource_name"`
}

func toEnrichedAuditResponse(a db.ListAuditLogEnrichedRow) auditLogResponse {
	resp := auditLogResponse{
		ID:              a.ID,
		UserID:          a.UserID,
		ResourceType:    a.ResourceType,
		ResourceID:      a.ResourceID,
		Action:          a.Action,
		Details:         string(a.Details),
		CreatedAt:       a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UserEmail:       a.UserEmail,
		UserDisplayName: a.UserDisplayName,
		ClusterName:     a.ClusterName,
		ResourceVMID:    a.ResourceVmid,
		ResourceName:    a.ResourceName,
	}
	if a.ClusterID.Valid {
		s := uuid.UUID(a.ClusterID.Bytes).String()
		resp.ClusterID = &s
	}
	return resp
}

type auditListResponse struct {
	Items []auditLogResponse `json:"items"`
	Total int64              `json:"total"`
}

// List handles GET /api/v1/audit-log.
func (h *AuditHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)
	if limit > 200 {
		limit = 200
	}

	var clusterFilter pgtype.UUID
	if cid := c.Query("cluster_id"); cid != "" {
		parsed, err := uuid.Parse(cid)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id filter")
		}
		clusterFilter = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	var resourceFilter pgtype.Text
	if rt := c.Query("resource_type"); rt != "" {
		resourceFilter = pgtype.Text{String: rt, Valid: true}
	}

	items, err := h.queries.ListAuditLogEnriched(c.Context(), db.ListAuditLogEnrichedParams{
		Limit:        int32(limit),
		Offset:       int32(offset),
		ClusterID:    clusterFilter,
		ResourceType: resourceFilter,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log")
	}

	total, err := h.queries.CountAuditLog(c.Context(), db.CountAuditLogParams{
		ClusterID:    clusterFilter,
		ResourceType: resourceFilter,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count audit log")
	}

	resp := auditListResponse{
		Items: make([]auditLogResponse, len(items)),
		Total: total,
	}
	for i, a := range items {
		resp.Items[i] = toEnrichedAuditResponse(a)
	}

	return c.JSON(resp)
}

// ListRecent handles GET /api/v1/audit-log/recent — returns the 50 most recent entries.
func (h *AuditHandler) ListRecent(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	items, err := h.queries.ListRecentAuditLogEnriched(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list recent activity")
	}

	resp := make([]auditLogResponse, len(items))
	for i, a := range items {
		resp[i] = toRecentAuditResponse(a)
	}

	return c.JSON(resp)
}

func toRecentAuditResponse(a db.ListRecentAuditLogEnrichedRow) auditLogResponse {
	resp := auditLogResponse{
		ID:              a.ID,
		UserID:          a.UserID,
		ResourceType:    a.ResourceType,
		ResourceID:      a.ResourceID,
		Action:          a.Action,
		Details:         string(a.Details),
		CreatedAt:       a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UserEmail:       a.UserEmail,
		UserDisplayName: a.UserDisplayName,
		ClusterName:     a.ClusterName,
		ResourceVMID:    a.ResourceVmid,
		ResourceName:    a.ResourceName,
	}
	if a.ClusterID.Valid {
		s := uuid.UUID(a.ClusterID.Bytes).String()
		resp.ClusterID = &s
	}
	return resp
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/audit-log.
func (h *AuditHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	limit := c.QueryInt("limit", 50)
	if limit > 200 {
		limit = 200
	}

	items, err := h.queries.ListAuditLogEnriched(c.Context(), db.ListAuditLogEnrichedParams{
		Limit:     int32(limit),
		Offset:    0,
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log")
	}

	resp := make([]auditLogResponse, len(items))
	for i, a := range items {
		resp[i] = toEnrichedAuditResponse(a)
	}

	return c.JSON(resp)
}
