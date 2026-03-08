package handlers

import (
	"encoding/csv"
	"fmt"
	"strings"
	"time"

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

type auditListResponse struct {
	Items []auditLogResponse `json:"items"`
	Total int64              `json:"total"`
}

func toAdvancedAuditResponse(a db.ListAuditLogAdvancedRow) auditLogResponse {
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

// parseAuditFilters extracts all filter params from the request query string.
func (h *AuditHandler) parseAuditFilters(c *fiber.Ctx) (db.ListAuditLogAdvancedParams, db.CountAuditLogAdvancedParams, error) {
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)
	if limit > 200 {
		limit = 200
	}

	var listP db.ListAuditLogAdvancedParams
	var countP db.CountAuditLogAdvancedParams
	listP.Limit = safeInt32(limit)
	listP.Offset = safeInt32(offset)

	if cid := c.Query("cluster_id"); cid != "" {
		parsed, err := uuid.Parse(cid)
		if err != nil {
			return listP, countP, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id filter")
		}
		v := pgtype.UUID{Bytes: parsed, Valid: true}
		listP.ClusterID = v
		countP.ClusterID = v
	}

	if rt := c.Query("resource_type"); rt != "" {
		v := pgtype.Text{String: rt, Valid: true}
		listP.ResourceType = v
		countP.ResourceType = v
	}

	if uid := c.Query("user_id"); uid != "" {
		parsed, err := uuid.Parse(uid)
		if err != nil {
			return listP, countP, fiber.NewError(fiber.StatusBadRequest, "Invalid user_id filter")
		}
		v := pgtype.UUID{Bytes: parsed, Valid: true}
		listP.UserID = v
		countP.UserID = v
	}

	if a := c.Query("action"); a != "" {
		v := pgtype.Text{String: a, Valid: true}
		listP.Action = v
		countP.Action = v
	}

	if st := c.Query("start_time"); st != "" {
		t, err := time.Parse(time.RFC3339, st)
		if err != nil {
			return listP, countP, fiber.NewError(fiber.StatusBadRequest, "Invalid start_time (use RFC3339)")
		}
		v := pgtype.Timestamptz{Time: t, Valid: true}
		listP.StartTime = v
		countP.StartTime = v
	}

	if et := c.Query("end_time"); et != "" {
		t, err := time.Parse(time.RFC3339, et)
		if err != nil {
			return listP, countP, fiber.NewError(fiber.StatusBadRequest, "Invalid end_time (use RFC3339)")
		}
		v := pgtype.Timestamptz{Time: t, Valid: true}
		listP.EndTime = v
		countP.EndTime = v
	}

	return listP, countP, nil
}

// List handles GET /api/v1/audit-log.
func (h *AuditHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	listP, countP, err := h.parseAuditFilters(c)
	if err != nil {
		return err
	}

	items, err := h.queries.ListAuditLogAdvanced(c.Context(), listP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log")
	}

	total, err := h.queries.CountAuditLogAdvanced(c.Context(), countP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count audit log")
	}

	resp := auditListResponse{
		Items: make([]auditLogResponse, len(items)),
		Total: total,
	}
	for i, a := range items {
		resp.Items[i] = toAdvancedAuditResponse(a)
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

	items, err := h.queries.ListAuditLogAdvanced(c.Context(), db.ListAuditLogAdvancedParams{
		Limit:     int32(limit),
		Offset:    0,
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log")
	}

	resp := make([]auditLogResponse, len(items))
	for i, a := range items {
		resp[i] = toAdvancedAuditResponse(a)
	}

	return c.JSON(resp)
}

// ListActions handles GET /api/v1/audit-log/actions — returns distinct action values.
func (h *AuditHandler) ListActions(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	actions, err := h.queries.ListDistinctAuditActions(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list actions")
	}

	return c.JSON(actions)
}

// ListUsers handles GET /api/v1/audit-log/users — returns distinct users in audit log.
func (h *AuditHandler) ListUsers(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	users, err := h.queries.ListDistinctAuditUsers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list users")
	}

	type userRef struct {
		ID          uuid.UUID `json:"id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"display_name"`
	}
	resp := make([]userRef, len(users))
	for i, u := range users {
		resp[i] = userRef{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName}
	}

	return c.JSON(resp)
}

// Export handles GET /api/v1/audit-log/export — exports audit log in CSV, JSON, or syslog format.
func (h *AuditHandler) Export(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "audit"); err != nil {
		return err
	}

	format := c.Query("format", "json")
	if format != "json" && format != "csv" && format != "syslog" {
		return fiber.NewError(fiber.StatusBadRequest, "format must be 'json', 'csv', or 'syslog'")
	}

	// Parse same filters but override limit for export (max 10000).
	listP, _, err := h.parseAuditFilters(c)
	if err != nil {
		return err
	}
	listP.Limit = 10000
	listP.Offset = 0

	items, err := h.queries.ListAuditLogAdvanced(c.Context(), listP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log for export")
	}

	timestamp := time.Now().Format("20060102-150405")

	switch format {
	case "csv":
		return h.exportCSV(c, items, timestamp)
	case "syslog":
		return h.exportSyslog(c, items, timestamp)
	default:
		return h.exportJSON(c, items, timestamp)
	}
}

func (h *AuditHandler) exportJSON(c *fiber.Ctx, items []db.ListAuditLogAdvancedRow, timestamp string) error {
	resp := make([]auditLogResponse, len(items))
	for i, a := range items {
		resp[i] = toAdvancedAuditResponse(a)
	}

	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-log-%s.json", timestamp))
	return c.JSON(resp)
}

func (h *AuditHandler) exportCSV(c *fiber.Ctx, items []db.ListAuditLogAdvancedRow, timestamp string) error {
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-log-%s.csv", timestamp))

	var buf strings.Builder
	w := csv.NewWriter(&buf)

	// Header row.
	_ = w.Write([]string{
		"Timestamp", "Cluster", "User", "User Email",
		"Resource Type", "Resource ID", "Resource Name", "Resource VMID",
		"Action", "Details",
	})

	for _, a := range items {
		clusterName := a.ClusterName
		if clusterName == "" {
			clusterName = "System"
		}
		resourceName := a.ResourceName
		vmid := ""
		if a.ResourceVmid > 0 {
			vmid = fmt.Sprintf("%d", a.ResourceVmid)
		}
		userName := a.UserDisplayName
		if userName == "" {
			userName = a.UserEmail
		}
		_ = w.Write([]string{
			a.CreatedAt.Format("2006-01-02T15:04:05Z"),
			clusterName,
			userName,
			a.UserEmail,
			a.ResourceType,
			a.ResourceID,
			resourceName,
			vmid,
			a.Action,
			string(a.Details),
		})
	}

	w.Flush()
	return c.SendString(buf.String())
}

// exportSyslog outputs audit entries in RFC 5424 syslog format for SIEM integration.
func (h *AuditHandler) exportSyslog(c *fiber.Ctx, items []db.ListAuditLogAdvancedRow, timestamp string) error {
	c.Set("Content-Type", "text/plain; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-log-%s.log", timestamp))

	var buf strings.Builder
	for _, a := range items {
		// RFC 5424: <PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID SD MSG
		// PRI: facility=local0 (16), severity derived from action
		severity := syslogSeverity(a.Action)
		pri := 16*8 + severity // facility local0 = 16

		userName := a.UserDisplayName
		if userName == "" {
			userName = a.UserEmail
		}

		clusterName := a.ClusterName
		if clusterName == "" {
			clusterName = "system"
		}

		msg := fmt.Sprintf("user=%q cluster=%q resource_type=%s resource_id=%s action=%s",
			userName, clusterName, a.ResourceType, a.ResourceID, a.Action)

		// Append details JSON if non-empty.
		details := string(a.Details)
		if details != "" && details != "{}" {
			msg += fmt.Sprintf(" details=%s", details)
		}

		line := fmt.Sprintf("<%d>1 %s proxdash audit - - - %s\n",
			pri,
			a.CreatedAt.Format("2006-01-02T15:04:05Z"),
			msg,
		)
		buf.WriteString(line)
	}

	return c.SendString(buf.String())
}

// syslogSeverity maps action names to syslog severity levels.
// 6=informational, 4=warning, 3=error.
func syslogSeverity(action string) int {
	switch {
	case strings.Contains(action, "error") || strings.Contains(action, "failed") || strings.Contains(action, "fail"):
		return 3 // error
	case strings.Contains(action, "delete") || strings.Contains(action, "destroy") ||
		strings.Contains(action, "disable") || strings.Contains(action, "revoke") ||
		strings.Contains(action, "reset"):
		return 4 // warning
	default:
		return 6 // informational
	}
}
