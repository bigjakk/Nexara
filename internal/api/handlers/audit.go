package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/reports"
	"github.com/bigjakk/nexara/internal/safeconv"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// AuditHandler handles audit log endpoints.
type AuditHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
}

// NewAuditHandler creates a new audit handler.
func NewAuditHandler(queries *db.Queries, eventPub *events.Publisher) *AuditHandler {
	return &AuditHandler{queries: queries, eventPub: eventPub}
}

type auditLogResponse struct {
	ID              uuid.UUID  `json:"id"`
	ClusterID       *string    `json:"cluster_id"`
	UserID          *uuid.UUID `json:"user_id"`
	ResourceType    string     `json:"resource_type"`
	ResourceID      string     `json:"resource_id"`
	Action          string     `json:"action"`
	Details         string     `json:"details"`
	CreatedAt       string     `json:"created_at"`
	Source          string     `json:"source"`
	UserEmail       string     `json:"user_email"`
	UserDisplayName string     `json:"user_display_name"`
	ClusterName     string     `json:"cluster_name"`
	ResourceVMID    int32      `json:"resource_vmid"`
	ResourceName    string     `json:"resource_name"`
	// Server-authoritative task status, populated only for entries backed by a
	// task_history row (Nexara-dispatched tasks). nil for non-task or external
	// entries — the client falls back to the entry's own details for those.
	TaskStatus     *string  `json:"task_status,omitempty"`
	TaskExitStatus *string  `json:"task_exit_status,omitempty"`
	TaskProgress   *float64 `json:"task_progress,omitempty"`
}

type auditListResponse struct {
	Items []auditLogResponse `json:"items"`
	Total int64              `json:"total"`
}

// reservedSettingVisibility resolves, once per request, which reserved setting
// keys this caller may see the details of: those whose owning endpoint they
// could have read the setting from directly.
//
// It is the read half of the reservation in settings.go. An entry about a
// reserved key carries that key's value — the syslog forwarding entries name
// the collector the audit stream is sent to, and whether its transport is
// verified — while view:audit belongs to the default Viewer role and every
// audit read here serves cluster-less rows to anyone holding it globally.
// Without this, recording the change would hand a Viewer the value that
// GET /api/v1/settings/<key> refuses them.
func reservedSettingVisibility(c fiber.Ctx) (map[string]bool, error) {
	visible := make(map[string]bool, len(reservedGlobalSettings))
	for key, owner := range reservedGlobalSettings {
		allowed, err := hasGlobalPerm(c, owner.Action, owner.Resource)
		if err != nil {
			return nil, err
		}
		visible[key] = allowed
	}
	return visible, nil
}

// auditDetailsFor returns the details a caller may see for one audit row.
//
// Redacted rather than dropped: who changed which setting, and when, is the
// part of the record every view:audit holder is entitled to — it is the value
// alone that is owned. The replacement names the permission to ask for, so a
// reader can tell a redaction from an entry that carried nothing.
func auditDetailsFor(resourceType, resourceID, details string, visible map[string]bool) string {
	if resourceType != settingResourceType || visible[resourceID] {
		return details
	}
	owner, reserved := reservedGlobalSettings[resourceID]
	if !reserved {
		return details
	}
	// The interpolated halves come from reservedGlobalSettings, a table of
	// literals, never from the request — so this needs no escaping.
	return `{"redacted":true,"requires":"` + owner.Action + `:` + owner.Resource + `"}`
}

func toAdvancedAuditResponse(a db.ListAuditLogAdvancedRow, visible map[string]bool) auditLogResponse {
	resp := auditLogResponse{
		ID:              a.ID,
		ResourceType:    a.ResourceType,
		ResourceID:      a.ResourceID,
		Action:          a.Action,
		Details:         auditDetailsFor(a.ResourceType, a.ResourceID, string(a.Details), visible),
		CreatedAt:       a.CreatedAt.Format(time.RFC3339Nano),
		Source:          a.Source,
		UserEmail:       a.UserEmail.String,
		UserDisplayName: a.UserDisplayName.String,
		ClusterName:     a.ClusterName,
		ResourceVMID:    a.ResourceVmid,
		ResourceName:    a.ResourceName,
	}
	if a.UserID.Valid {
		u := uuid.UUID(a.UserID.Bytes)
		resp.UserID = &u
	}
	if a.ClusterID.Valid {
		s := uuid.UUID(a.ClusterID.Bytes).String()
		resp.ClusterID = &s
	}
	return resp
}

// parseAuditFilters extracts all filter params from the request query string
// and stamps the caller's view:audit cluster scope onto both param structs.
//
// The scope is applied here, in the one place both structs are built, rather
// than left to each caller: List and Export share this function, and params
// that left it unscoped are the bug being fixed — a Total counting every
// cluster's entries, and an export silently trimmed to whatever survived a
// global LIMIT. Stamped before any parse can fail, so no return path — error
// or not — hands back a struct that would read across clusters.
//
// query is false when the caller holds no view:audit grant anywhere. The
// params are correct regardless ('{}' matches nothing); the flag only lets the
// caller skip a round-trip that could not come back with a row.
func (h *AuditHandler) parseAuditFilters(c fiber.Ctx, access clusterAccess) (listP db.ListAuditLogAdvancedParams, countP db.CountAuditLogAdvancedParams, query bool, err error) {
	limit := fiber.Query[int](c, "limit", 50)
	offset := fiber.Query[int](c, "offset", 0)
	if limit < 1 {
		limit = 1
	} else if limit > 200 {
		limit = 200
	}
	// Clamped low as well as high: safeconv.Int32 only bounds the int32 range,
	// so a negative ?limit= reached Postgres as `LIMIT -1` and came back a 500.
	if offset < 0 {
		offset = 0
	}

	query = applyAuditListScope(access, &listP, &countP)
	listP.Limit = safeconv.Int32(limit)
	listP.Offset = safeconv.Int32(offset)

	if cid := c.Query("cluster_id"); cid != "" {
		parsed, err := uuid.Parse(cid)
		if err != nil {
			return listP, countP, query, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id filter")
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
			return listP, countP, query, fiber.NewError(fiber.StatusBadRequest, "Invalid user_id filter")
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

	if src := c.Query("source"); src != "" {
		v := pgtype.Text{String: src, Valid: true}
		listP.Source = v
		countP.Source = v
	}

	if st := c.Query("start_time"); st != "" {
		t, err := time.Parse(time.RFC3339, st)
		if err != nil {
			return listP, countP, query, fiber.NewError(fiber.StatusBadRequest, "Invalid start_time (use RFC3339)")
		}
		v := pgtype.Timestamptz{Time: t, Valid: true}
		listP.StartTime = v
		countP.StartTime = v
	}

	if et := c.Query("end_time"); et != "" {
		t, err := time.Parse(time.RFC3339, et)
		if err != nil {
			return listP, countP, query, fiber.NewError(fiber.StatusBadRequest, "Invalid end_time (use RFC3339)")
		}
		v := pgtype.Timestamptz{Time: t, Valid: true}
		listP.EndTime = v
		countP.EndTime = v
	}

	return listP, countP, query, nil
}

// applyAuditListScope stamps the caller's view:audit scope onto BOTH the list
// and the count params. Taking the two together is the whole point: a Total
// computed under a wider scope than the Items is precisely the leak this fixes
// — it counted every cluster's entries and paginated over them — and unlike a
// row, a number no per-row guard can repair.
//
// NULL-cluster rows are global entries (settings changes, logins); see the note
// on ListAuditLogAdvanced in queries/audit_log.sql for why the clause excludes
// them from a scoped caller without a predicate of its own.
func applyAuditListScope(access clusterAccess, listP *db.ListAuditLogAdvancedParams, countP *db.CountAuditLogAdvancedParams) bool {
	ids, query := clusterScopeFilter(access)
	listP.AccessibleClusterIds = ids
	countP.AccessibleClusterIds = ids
	return query
}

// List handles GET /api/v1/audit-log.
func (h *AuditHandler) List(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "audit")
	if err != nil {
		return err
	}

	listP, countP, query, err := h.parseAuditFilters(c, access)
	if err != nil {
		return err
	}

	// If a cluster filter was supplied, the user must have access to it.
	if listP.ClusterID.Valid && !access.PermitsCluster(uuid.UUID(listP.ClusterID.Bytes)) {
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	if !query {
		return c.JSON(auditListResponse{Items: []auditLogResponse{}})
	}

	items, err := h.queries.ListAuditLogAdvanced(c.Context(), listP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log")
	}

	total, err := h.queries.CountAuditLogAdvanced(c.Context(), countP)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to count audit log")
	}

	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	resp := auditListResponse{
		Items: make([]auditLogResponse, 0, len(items)),
		Total: total,
	}
	for _, a := range items {
		// Defense-in-depth: the SQL scope above already restricts rows to the
		// caller's clusters, and excludes the NULL-cluster global entries from
		// anyone without global view:audit. Kept so a future edit to the query
		// cannot silently reopen either leak.
		if a.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(a.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		resp.Items = append(resp.Items, toAdvancedAuditResponse(a, visible))
	}

	return c.JSON(resp)
}

// ListRecent handles GET /api/v1/audit-log/recent — returns the 50 most recent entries.
func (h *AuditHandler) ListRecent(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "audit")
	if err != nil {
		return err
	}

	// Scoped in SQL rather than trimmed afterwards: LIMIT 50 over every
	// cluster's entries, trimmed after, hands a cluster-scoped user whatever
	// survives of the newest 50 global rows — usually a near-empty feed.
	scope, query := clusterScopeFilter(access)
	var items []db.ListRecentAuditLogEnrichedRow
	if query {
		items, err = h.queries.ListRecentAuditLogEnriched(c.Context(), scope)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list recent activity")
		}
	}

	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	resp := make([]auditLogResponse, 0, len(items))
	for _, a := range items {
		// Defense-in-depth, as in List.
		if a.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(a.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		resp = append(resp, toRecentAuditResponse(a, visible))
	}

	return c.JSON(resp)
}

func toRecentAuditResponse(a db.ListRecentAuditLogEnrichedRow, visible map[string]bool) auditLogResponse {
	resp := auditLogResponse{
		ID:              a.ID,
		ResourceType:    a.ResourceType,
		ResourceID:      a.ResourceID,
		Action:          a.Action,
		Details:         auditDetailsFor(a.ResourceType, a.ResourceID, string(a.Details), visible),
		CreatedAt:       a.CreatedAt.Format(time.RFC3339Nano),
		Source:          a.Source,
		UserEmail:       a.UserEmail.String,
		UserDisplayName: a.UserDisplayName.String,
		ClusterName:     a.ClusterName,
		ResourceVMID:    a.ResourceVmid,
		ResourceName:    a.ResourceName,
	}
	if a.UserID.Valid {
		u := uuid.UUID(a.UserID.Bytes)
		resp.UserID = &u
	}
	if a.ClusterID.Valid {
		s := uuid.UUID(a.ClusterID.Bytes).String()
		resp.ClusterID = &s
	}
	// task_status is "" when no task_history row matched the entry's upid.
	if a.TaskStatus != "" {
		ts := a.TaskStatus
		es := a.TaskExitStatus
		resp.TaskStatus = &ts
		resp.TaskExitStatus = &es
	}
	if a.TaskProgress.Valid {
		p := a.TaskProgress.Float64
		resp.TaskProgress = &p
	}
	return resp
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/audit-log.
func (h *AuditHandler) ListByCluster(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "audit", clusterID); err != nil {
		return err
	}

	limit := fiber.Query[int](c, "limit", 50)
	if limit < 1 {
		limit = 1
	} else if limit > 200 {
		limit = 200
	}

	// Scoped too, though requireClusterPerm above has already authorized this
	// exact cluster and the ClusterID filter pins the result set to it. The
	// stamp is a no-op against either of those — a global caller scopes to nil,
	// and a cluster-scoped one to a set containing the very cluster being
	// filtered on (rbac.go: a global grant satisfies a cluster check, so
	// passing requireClusterPerm implies one or the other). It is here so this
	// endpoint is not the one audit read whose safety rests on a single lock in
	// a query the other three now share.
	access, err := accessibleClusters(c, "view", "audit")
	if err != nil {
		return err
	}

	items, err := h.queries.ListAuditLogAdvanced(c.Context(), db.ListAuditLogAdvancedParams{
		Limit:                int32(limit), //nolint:gosec // bounds checked above (1-200)
		Offset:               0,
		ClusterID:            pgtype.UUID{Bytes: clusterID, Valid: true},
		AccessibleClusterIds: access.ScopedIDs(),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log")
	}

	// A reserved setting key is global, so its entries carry no cluster_id and
	// this cluster-filtered query cannot return one. Applied anyway, so that
	// every path over these rows answers to one rule rather than to a property
	// of the current WHERE clause.
	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	resp := make([]auditLogResponse, 0, len(items))
	for _, a := range items {
		// Defense-in-depth, as in List. Cannot drop a row today — every row
		// carries the one authorized cluster_id — which is the point: the four
		// audit reads now answer to the same rule, so none of them is the one
		// that quietly stops checking.
		if a.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(a.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		resp = append(resp, toAdvancedAuditResponse(a, visible))
	}

	return c.JSON(resp)
}

// ListActions handles GET /api/v1/audit-log/actions — returns distinct action values.
func (h *AuditHandler) ListActions(c fiber.Ctx) error {
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
func (h *AuditHandler) ListUsers(c fiber.Ctx) error {
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
func (h *AuditHandler) Export(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "audit")
	if err != nil {
		return err
	}

	format := c.Query("format", "json")
	if format != "json" && format != "csv" && format != "syslog" {
		return fiber.NewError(fiber.StatusBadRequest, "format must be 'json', 'csv', or 'syslog'")
	}

	// Parse same filters but override limit for export (max 10000). The scope
	// rides along on listP, and it matters more here than on a page: the
	// 10000-row cap applied over every cluster's entries and trimmed after
	// would silently drop a scoped user's accessible rows that fall beyond the
	// newest 10000 global ones, producing a short export that nothing in the
	// file marks as incomplete.
	listP, _, query, err := h.parseAuditFilters(c, access)
	if err != nil {
		return err
	}
	listP.Limit = 10000
	listP.Offset = 0

	if listP.ClusterID.Valid && !access.PermitsCluster(uuid.UUID(listP.ClusterID.Bytes)) {
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	var rawItems []db.ListAuditLogAdvancedRow
	if query {
		rawItems, err = h.queries.ListAuditLogAdvanced(c.Context(), listP)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list audit log for export")
		}
	}

	// Defense-in-depth, as in List.
	items := make([]db.ListAuditLogAdvancedRow, 0, len(rawItems))
	for _, a := range rawItems {
		if a.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(a.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		items = append(items, a)
	}

	// An export is the same disclosure as a listing, in a file. Resolved here
	// so all three formats share one answer.
	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("20060102-150405")

	switch format {
	case "csv":
		return h.exportCSV(c, items, timestamp, visible)
	case "syslog":
		return h.exportSyslog(c, items, timestamp, visible)
	default:
		return h.exportJSON(c, items, timestamp, visible)
	}
}

func (h *AuditHandler) exportJSON(c fiber.Ctx, items []db.ListAuditLogAdvancedRow, timestamp string, visible map[string]bool) error {
	resp := make([]auditLogResponse, len(items))
	for i, a := range items {
		resp[i] = toAdvancedAuditResponse(a, visible)
	}

	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-log-%s.json", timestamp))
	return c.JSON(resp)
}

func (h *AuditHandler) exportCSV(c fiber.Ctx, items []db.ListAuditLogAdvancedRow, timestamp string, visible map[string]bool) error {
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-log-%s.csv", timestamp))

	var buf strings.Builder
	w := csv.NewWriter(&buf)

	// Header row.
	_ = reports.WriteSafeCSVRow(w, []string{
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
		userName := a.UserDisplayName.String
		if userName == "" {
			userName = a.UserEmail.String
		}
		if userName == "" {
			userName = "(deleted user)"
		}
		_ = reports.WriteSafeCSVRow(w, []string{
			a.CreatedAt.Format(time.RFC3339Nano),
			clusterName,
			userName,
			a.UserEmail.String,
			a.ResourceType,
			a.ResourceID,
			resourceName,
			vmid,
			a.Action,
			auditDetailsFor(a.ResourceType, a.ResourceID, string(a.Details), visible),
		})
	}

	w.Flush()
	return c.SendString(buf.String())
}

// exportSyslog outputs audit entries in RFC 5424 syslog format for SIEM integration.
func (h *AuditHandler) exportSyslog(c fiber.Ctx, items []db.ListAuditLogAdvancedRow, timestamp string, visible map[string]bool) error {
	c.Set("Content-Type", "text/plain; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-log-%s.log", timestamp))

	var buf strings.Builder
	for _, a := range items {
		// RFC 5424: <PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID SD MSG
		// PRI: facility=local0 (16), severity derived from action
		severity := syslogSeverity(a.Action)
		pri := 16*8 + severity // facility local0 = 16

		userName := a.UserDisplayName.String
		if userName == "" {
			userName = a.UserEmail.String
		}
		if userName == "" {
			userName = "(deleted user)"
		}

		clusterName := a.ClusterName
		if clusterName == "" {
			clusterName = "system"
		}

		// Shared with the live forwarder so the two renderings cannot drift. It
		// bounds and escapes every value by the RFC 5424 SD-PARAM rules, which
		// is what stops a details blob or a resource id from opening a field,
		// closing the element or ending the record. See proxsyslog.FormatAuditSD.
		sd := proxsyslog.FormatAuditSD(
			userName, clusterName, a.ResourceType, a.ResourceID, a.Action,
			auditDetailsFor(a.ResourceType, a.ResourceID, string(a.Details), visible),
		)

		// One fewer "-" than before: the fields moved out of MSG into
		// STRUCTURED-DATA, which occupies that slot.
		line := fmt.Sprintf("<%d>1 %s nexara audit - - %s\n",
			pri,
			a.CreatedAt.Format(time.RFC3339Nano),
			sd,
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

// --- Syslog forwarding config ---

const syslogSettingKey = "syslog_forwarding"

// The audit actions these endpoints record.
//
// Both are filed under the settings resource type with the setting key as the
// resource id: the config is a row in `settings`, and recording the probe
// against that same id means one filter over the audit log tells the whole
// story of a destination — every change, and every test that preceded it.
const (
	syslogUpdatedAction = "syslog_forwarding_updated"
	syslogTestedAction  = "syslog_forwarding_tested"
)

// Bounds on the caller-supplied strings a syslog audit detail carries: the host
// and protocol from the config itself, and the probe's error text, which wraps
// net's own message around that same host. Neither endpoint caps the host it
// accepts, so without these one request writes a body-limit-sized string into
// audit_log. They bound the size of a row, not the number of them — nothing
// rate-limits the probe.
const (
	maxSyslogAuditValueLen = 256
	maxSyslogAuditErrorLen = 512
)

// syslogAuditConfig is the part of a forwarding config that a change or a probe
// records: where the audit stream goes, over what, and whether it goes at all.
//
// It is a struct of its own rather than proxsyslog.Config, and that is the
// point. Marshalling the config itself would copy every field the forwarder
// ever grows — a TLS client key, a collector token — into a table with a wider
// read audience than the settings row, on the day the field landed and with
// nothing to catch it. Adding a field here is a deliberate act.
// TestGuard_SyslogAuditFieldsClassified fails when proxsyslog.Config gains one
// that is neither recorded here nor listed as deliberately omitted.
type syslogAuditConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Facility int    `json:"facility"`
	// Recorded because turning it on downgrades the transport carrying the
	// audit stream — the same class of change as pointing it somewhere else.
	TLSSkipVerify bool `json:"tls_skip_verify"`
}

// toSyslogAuditConfig is the single point where a forwarding config crosses
// into an audit detail, which makes it the one place the caps have to be
// applied. Every recorded config goes through here — the previous, the new, and
// a probe's target.
func toSyslogAuditConfig(cfg proxsyslog.Config) syslogAuditConfig {
	return syslogAuditConfig{
		Enabled:       cfg.Enabled,
		Host:          auditTruncate(cfg.Host, maxSyslogAuditValueLen),
		Port:          cfg.Port,
		Protocol:      auditTruncate(cfg.Protocol, maxSyslogAuditValueLen),
		Facility:      cfg.Facility,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}
}

// syslogAuditDetails is what a change to the forwarding config records — enough
// to see where the audit stream used to go and where it goes now without
// reading the settings row back, which by then holds only the new value.
type syslogAuditDetails struct {
	// Previous is the config this write replaced, null when none was stored.
	// PreviousUnavailable separates that from a stored config that could not be
	// read back, so an absent "before" is never guesswork.
	Previous            *syslogAuditConfig `json:"previous"`
	PreviousUnavailable bool               `json:"previous_unavailable,omitempty"`
	New                 syslogAuditConfig  `json:"new"`
}

// syslogTestAuditDetails is what a forwarding probe records: the target as the
// caller submitted it, and what came back.
type syslogTestAuditDetails struct {
	Target  syslogAuditConfig `json:"target"`
	Success bool              `json:"success"`
	Error   string            `json:"error,omitempty"`
}

// defaultSyslogConfig is what the endpoints report before anything has been
// saved: forwarding off, and the RFC 5424 defaults the forwarder itself falls
// back to.
func defaultSyslogConfig() proxsyslog.Config {
	return proxsyslog.Config{Port: 514, Protocol: "udp", Facility: 16}
}

// storedSyslogConfig reads the saved forwarding config. found is false when
// nothing has been saved yet, which is not an error — that is what
// defaultSyslogConfig is for. Anything else is an error, deliberately: a config
// that exists but cannot be read is not the same as none, and neither caller
// may treat it as such.
func (h *AuditHandler) storedSyslogConfig(c fiber.Ctx) (proxsyslog.Config, bool, error) {
	setting, err := h.queries.GetSetting(c.Context(), db.GetSettingParams{
		Key:   syslogSettingKey,
		Scope: "global",
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return proxsyslog.Config{}, false, nil
	}
	if err != nil {
		return proxsyslog.Config{}, false, fmt.Errorf("read syslog config: %w", err)
	}

	var cfg proxsyslog.Config
	if err := json.Unmarshal(setting.Value, &cfg); err != nil {
		return proxsyslog.Config{}, false, fmt.Errorf("decode syslog config: %w", err)
	}
	return cfg, true, nil
}

// previousSyslogAudit reads the config a write is about to replace, in the
// shape the audit detail records it. A nil config with unavailable false means
// nothing was ever stored; unavailable true means a stored config could not be
// read back, which the record says rather than passing off as "none".
//
// The read is its own statement, not part of the write's transaction. Two
// concurrent updates can therefore each record the value they read, and the one
// that lost the race names a "previous" that was already gone. The stored config
// is correct either way; only that field of the record is approximate, and both
// entries still show up.
func (h *AuditHandler) previousSyslogAudit(c fiber.Ctx) (prev *syslogAuditConfig, unavailable bool) {
	cfg, found, err := h.storedSyslogConfig(c)
	if err != nil {
		return nil, true
	}
	if !found {
		return nil, false
	}
	audited := toSyslogAuditConfig(cfg)
	return &audited, false
}

// syslogTestAuditError renders a failed probe for the audit detail. The message
// embeds the caller's own host and protocol, so it is bounded like any other
// caller-supplied string.
func syslogTestAuditError(err error) string {
	if err == nil {
		return ""
	}
	return auditTruncate(err.Error(), maxSyslogAuditErrorLen)
}

// GetSyslogConfig handles GET /api/v1/audit-log/syslog-config.
func (h *AuditHandler) GetSyslogConfig(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "audit"); err != nil {
		return err
	}

	cfg, found, err := h.storedSyslogConfig(c)
	if err != nil {
		// Not folded into the defaults below. Showing "disabled, udp, 514" for a
		// config that exists but could not be read invites the operator to save
		// that straight back over a live one.
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read syslog config")
	}
	if !found {
		return c.JSON(defaultSyslogConfig())
	}

	return c.JSON(cfg)
}

// UpdateSyslogConfig handles PUT /api/v1/audit-log/syslog-config.
func (h *AuditHandler) UpdateSyslogConfig(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "audit"); err != nil {
		return err
	}

	var cfg proxsyslog.Config
	if err := c.Bind().Body(&cfg); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Validate.
	if cfg.Enabled {
		if cfg.Host == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Host is required when enabled")
		}
		if cfg.Port < 1 || cfg.Port > 65535 {
			return fiber.NewError(fiber.StatusBadRequest, "Port must be between 1 and 65535")
		}
		proto := strings.ToLower(cfg.Protocol)
		if proto != "udp" && proto != "tcp" && proto != "tls" {
			return fiber.NewError(fiber.StatusBadRequest, "Protocol must be 'udp', 'tcp', or 'tls'")
		}
		cfg.Protocol = proto
	}
	if cfg.Facility < 0 || cfg.Facility > 23 {
		return fiber.NewError(fiber.StatusBadRequest, "Facility must be between 0 and 23")
	}
	if cfg.Facility == 0 {
		cfg.Facility = 16 // default local0
	}
	if cfg.Port == 0 {
		cfg.Port = 514
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "udp"
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to marshal config")
	}

	previous, previousUnavailable := h.previousSyslogAudit(c)

	_, err = h.queries.UpsertSetting(c.Context(), db.UpsertSettingParams{
		Key:   syslogSettingKey,
		Value: data,
		Scope: "global",
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to save syslog config")
	}

	// Where the audit log itself is sent — and whether it is sent at all — is
	// exactly what an audit log exists to record. Redirecting the stream to
	// another collector, or setting enabled:false to stop it, is otherwise a
	// change that leaves no trace anywhere.
	//
	// Placed after the write and before the reconfigure, and both halves
	// matter. After the write, so a save the database rejected records nothing.
	// Before Configure, so the entry AuditLog forwards still travels over the
	// *outgoing* connection: the collector being redirected away from, or
	// switched off, is told so. Reconfigure first and that notice goes to the
	// new destination, or — on a disable — nowhere at all.
	details, _ := json.Marshal(syslogAuditDetails{ // strings, ints and bools — cannot fail
		Previous:            previous,
		PreviousUnavailable: previousUnavailable,
		New:                 toSyslogAuditConfig(cfg),
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{},
		settingResourceType, syslogSettingKey, syslogUpdatedAction, details)

	// Reconfigure the live forwarder.
	if fwd := h.eventPub.SyslogForwarder(); fwd != nil {
		// Forward is asynchronous, so the audit entry above is only queued at
		// this point. Drain it before reconfiguring, or it races Configure and
		// usually loses — the notice would then reach the new destination, or
		// on a disable go nowhere, which is precisely what the ordering above
		// exists to prevent. Bounded so an unreachable collector delays this
		// request only, never the ones that merely wrote an audit row.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		fwd.Flush(flushCtx)
		cancel()

		if err := fwd.Configure(cfg); err != nil {
			return c.Status(fiber.StatusOK).JSON(fiber.Map{
				"saved":   true,
				"warning": fmt.Sprintf("Config saved but forwarder failed to connect: %v", err),
			})
		}
	}

	return c.JSON(cfg)
}

// TestSyslog handles POST /api/v1/audit-log/syslog-test.
func (h *AuditHandler) TestSyslog(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "audit"); err != nil {
		return err
	}

	var cfg proxsyslog.Config
	if err := c.Bind().Body(&cfg); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if cfg.Host == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fiber.NewError(fiber.StatusBadRequest, "Port must be between 1 and 65535")
	}
	if cfg.Port == 0 {
		cfg.Port = 514
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "udp"
	}
	if cfg.Facility == 0 {
		cfg.Facility = 16
	}

	fwd := proxsyslog.NewForwarder(nil)
	probeErr := fwd.Test(cfg)

	// Recorded whether or not the probe connected. This endpoint opens an
	// outbound connection to a host the caller names and writes to it, so the
	// attempt is the thing worth seeing — a refused one no less than a
	// successful one, since a run of them is how a host would be swept. The
	// outcome rides in the details rather than in the action so that both land
	// under one filter, alongside the config changes for the same key.
	details, _ := json.Marshal(syslogTestAuditDetails{ // strings, ints and bools — cannot fail
		Target:  toSyslogAuditConfig(cfg),
		Success: probeErr == nil,
		Error:   syslogTestAuditError(probeErr),
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{},
		settingResourceType, syslogSettingKey, syslogTestedAction, details)

	if probeErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   probeErr.Error(),
		})
	}

	return c.JSON(fiber.Map{"success": true})
}
