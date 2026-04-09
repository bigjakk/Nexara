package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
)

// AlertHandler handles alert rules, history, notification channels, and maintenance windows.
type AlertHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	registry      *notifications.Registry
}

// NewAlertHandler creates a new AlertHandler.
func NewAlertHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher, registry *notifications.Registry) *AlertHandler {
	return &AlertHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		registry:      registry,
	}
}

func (h *AlertHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, nil)
}

func (h *AlertHandler) auditLogGlobal(c *fiber.Ctx, resourceType, resourceID, action string) {
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, resourceType, resourceID, action, nil)
}

// --- Response types ---

type alertRuleResponse struct {
	ID              uuid.UUID       `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Enabled         bool            `json:"enabled"`
	Severity        string          `json:"severity"`
	Metric          string          `json:"metric"`
	Operator        string          `json:"operator"`
	Threshold       float64         `json:"threshold"`
	DurationSeconds int32           `json:"duration_seconds"`
	ScopeType       string          `json:"scope_type"`
	ClusterID       string          `json:"cluster_id,omitempty"`
	NodeID          string          `json:"node_id,omitempty"`
	VMID            string          `json:"vm_id,omitempty"`
	CooldownSeconds int32           `json:"cooldown_seconds"`
	EscalationChain json.RawMessage `json:"escalation_chain"`
	MessageTemplate string          `json:"message_template"`
	CreatedBy       uuid.UUID       `json:"created_by"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

func toAlertRuleResponse(r db.AlertRule) alertRuleResponse {
	resp := alertRuleResponse{
		ID:              r.ID,
		Name:            r.Name,
		Description:     r.Description,
		Enabled:         r.Enabled,
		Severity:        r.Severity,
		Metric:          r.Metric,
		Operator:        r.Operator,
		Threshold:       r.Threshold,
		DurationSeconds: r.DurationSeconds,
		ScopeType:       r.ScopeType,
		CooldownSeconds: r.CooldownSeconds,
		EscalationChain: r.EscalationChain,
		MessageTemplate: r.MessageTemplate,
		CreatedBy:       r.CreatedBy,
		CreatedAt:       r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       r.UpdatedAt.Format(time.RFC3339),
	}
	if r.ClusterID.Valid {
		id, _ := uuid.FromBytes(r.ClusterID.Bytes[:])
		resp.ClusterID = id.String()
	}
	if r.NodeID.Valid {
		id, _ := uuid.FromBytes(r.NodeID.Bytes[:])
		resp.NodeID = id.String()
	}
	if r.VmID.Valid {
		id, _ := uuid.FromBytes(r.VmID.Bytes[:])
		resp.VMID = id.String()
	}
	return resp
}

type alertHistoryResponse struct {
	ID              uuid.UUID `json:"id"`
	RuleID          uuid.UUID `json:"rule_id"`
	State           string    `json:"state"`
	Severity        string    `json:"severity"`
	ClusterID       string    `json:"cluster_id,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	VMID            string    `json:"vm_id,omitempty"`
	ResourceName    string    `json:"resource_name"`
	Metric          string    `json:"metric"`
	CurrentValue    float64   `json:"current_value"`
	Threshold       float64   `json:"threshold"`
	Message         string    `json:"message"`
	EscalationLevel int32     `json:"escalation_level"`
	ChannelID       string    `json:"channel_id,omitempty"`
	PendingAt       string    `json:"pending_at"`
	FiredAt         string    `json:"fired_at,omitempty"`
	AcknowledgedAt  string    `json:"acknowledged_at,omitempty"`
	AcknowledgedBy  string    `json:"acknowledged_by,omitempty"`
	ResolvedAt      string    `json:"resolved_at,omitempty"`
	ResolvedBy      string    `json:"resolved_by,omitempty"`
	CreatedAt       string    `json:"created_at"`
}

func toAlertHistoryResponse(a db.AlertHistory) alertHistoryResponse {
	resp := alertHistoryResponse{
		ID:              a.ID,
		RuleID:          a.RuleID,
		State:           a.State,
		Severity:        a.Severity,
		ResourceName:    a.ResourceName,
		Metric:          a.Metric,
		CurrentValue:    a.CurrentValue,
		Threshold:       a.Threshold,
		Message:         a.Message,
		EscalationLevel: a.EscalationLevel,
		PendingAt:       a.PendingAt.Format(time.RFC3339),
		CreatedAt:       a.CreatedAt.Format(time.RFC3339),
	}
	if a.ClusterID.Valid {
		id, _ := uuid.FromBytes(a.ClusterID.Bytes[:])
		resp.ClusterID = id.String()
	}
	if a.NodeID.Valid {
		id, _ := uuid.FromBytes(a.NodeID.Bytes[:])
		resp.NodeID = id.String()
	}
	if a.VmID.Valid {
		id, _ := uuid.FromBytes(a.VmID.Bytes[:])
		resp.VMID = id.String()
	}
	if a.ChannelID.Valid {
		id, _ := uuid.FromBytes(a.ChannelID.Bytes[:])
		resp.ChannelID = id.String()
	}
	if a.FiredAt.Valid {
		resp.FiredAt = a.FiredAt.Time.Format(time.RFC3339)
	}
	if a.AcknowledgedAt.Valid {
		resp.AcknowledgedAt = a.AcknowledgedAt.Time.Format(time.RFC3339)
	}
	if a.AcknowledgedBy.Valid {
		id, _ := uuid.FromBytes(a.AcknowledgedBy.Bytes[:])
		resp.AcknowledgedBy = id.String()
	}
	if a.ResolvedAt.Valid {
		resp.ResolvedAt = a.ResolvedAt.Time.Format(time.RFC3339)
	}
	if a.ResolvedBy.Valid {
		id, _ := uuid.FromBytes(a.ResolvedBy.Bytes[:])
		resp.ResolvedBy = id.String()
	}
	return resp
}

type alertSummaryResponse struct {
	FiringCount       int64 `json:"firing_count"`
	PendingCount      int64 `json:"pending_count"`
	AcknowledgedCount int64 `json:"acknowledged_count"`
	CriticalFiring    int64 `json:"critical_firing"`
	WarningFiring     int64 `json:"warning_firing"`
	InfoFiring        int64 `json:"info_firing"`
}

type notificationChannelResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	ChannelType string    `json:"channel_type"`
	Enabled     bool      `json:"enabled"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

func toNotificationChannelResponse(ch db.NotificationChannel) notificationChannelResponse {
	return notificationChannelResponse{
		ID:          ch.ID,
		Name:        ch.Name,
		ChannelType: ch.ChannelType,
		Enabled:     ch.Enabled,
		CreatedBy:   ch.CreatedBy,
		CreatedAt:   ch.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   ch.UpdatedAt.Format(time.RFC3339),
	}
}

type maintenanceWindowResponse struct {
	ID          uuid.UUID `json:"id"`
	ClusterID   uuid.UUID `json:"cluster_id"`
	NodeID      string    `json:"node_id,omitempty"`
	Description string    `json:"description"`
	StartsAt    string    `json:"starts_at"`
	EndsAt      string    `json:"ends_at"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

func toMaintenanceWindowResponse(w db.MaintenanceWindow) maintenanceWindowResponse {
	resp := maintenanceWindowResponse{
		ID:          w.ID,
		ClusterID:   w.ClusterID,
		Description: w.Description,
		StartsAt:    w.StartsAt.Format(time.RFC3339),
		EndsAt:      w.EndsAt.Format(time.RFC3339),
		CreatedBy:   w.CreatedBy,
		CreatedAt:   w.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   w.UpdatedAt.Format(time.RFC3339),
	}
	if w.NodeID.Valid {
		id, _ := uuid.FromBytes(w.NodeID.Bytes[:])
		resp.NodeID = id.String()
	}
	return resp
}

// --- Request types ---

var validSeveritiesAlert = map[string]bool{
	"critical": true, "warning": true, "info": true,
}

var validOperators = map[string]bool{
	">": true, ">=": true, "<": true, "<=": true, "==": true, "!=": true,
}

var validScopeTypes = map[string]bool{
	"cluster": true, "node": true, "vm": true,
}

// validChannelTypes intentionally excludes "expo_push" — push notifications
// are wired in the backend (dispatcher is registered, devices table exists,
// /me/devices endpoints work) but disabled at the channel-creation API
// boundary because Nexara mobile is the only push consumer and it isn't
// shipping the registration flow yet. To re-enable: add "expo_push": true
// here, re-enable the mobile-side hooks in mobile/features/push/, and add
// "Mobile push (Expo)" back to CHANNEL_TYPES in ChannelForm.tsx.
var validChannelTypes = map[string]bool{
	"email": true, "webhook": true, "slack": true, "discord": true,
	"pagerduty": true, "teams": true, "telegram": true,
}

type createAlertRuleRequest struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Enabled         *bool           `json:"enabled"`
	Severity        string          `json:"severity"`
	Metric          string          `json:"metric"`
	Operator        string          `json:"operator"`
	Threshold       *float64        `json:"threshold"`
	DurationSeconds *int32          `json:"duration_seconds"`
	ScopeType       string          `json:"scope_type"`
	ClusterID       string          `json:"cluster_id"`
	NodeID          string          `json:"node_id"`
	VMID            string          `json:"vm_id"`
	CooldownSeconds *int32          `json:"cooldown_seconds"`
	EscalationChain json.RawMessage `json:"escalation_chain"`
	MessageTemplate string          `json:"message_template"`
}

const maxTemplateLen = 4096

const (
	maxNameLen        = 255
	maxDescriptionLen = 1024
	maxDurationSec    = 86400  // 24 hours
	maxCooldownSec    = 604800 // 7 days
)

// validateEscalationChain validates the structure of an escalation chain.
func validateEscalationChain(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var chain []struct {
		ChannelID    string `json:"channel_id"`
		DelayMinutes int    `json:"delay_minutes"`
	}
	if err := json.Unmarshal(data, &chain); err != nil {
		return fmt.Errorf("invalid escalation chain JSON")
	}
	for _, step := range chain {
		if _, err := uuid.Parse(step.ChannelID); err != nil {
			return fmt.Errorf("invalid channel_id in escalation chain")
		}
		if step.DelayMinutes < 0 {
			return fmt.Errorf("delay_minutes must be non-negative")
		}
	}
	return nil
}

type createChannelRequest struct {
	Name        string          `json:"name"`
	ChannelType string          `json:"channel_type"`
	Config      json.RawMessage `json:"config"`
	Enabled     *bool           `json:"enabled"`
}

type createMaintenanceWindowRequest struct {
	NodeID      string `json:"node_id"`
	Description string `json:"description"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
}

// ====== Alert Rules ======

// ListRules lists all alert rules.
func (h *AlertHandler) ListRules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	clusterID := c.Query("cluster_id")
	if clusterID != "" {
		cid, err := uuid.Parse(clusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
		}
		rules, err := h.queries.ListAlertRulesByCluster(c.Context(), db.ListAlertRulesByClusterParams{
			ClusterID: pgtype.UUID{Bytes: cid, Valid: true},
			Limit:     safeInt32(limit),
			Offset:    safeInt32(offset),
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alert rules")
		}
		result := make([]alertRuleResponse, len(rules))
		for i, r := range rules {
			result[i] = toAlertRuleResponse(r)
		}
		return c.JSON(result)
	}

	rules, err := h.queries.ListAlertRules(c.Context(), db.ListAlertRulesParams{
		Limit:  safeInt32(limit),
		Offset: safeInt32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alert rules")
	}

	result := make([]alertRuleResponse, len(rules))
	for i, r := range rules {
		result[i] = toAlertRuleResponse(r)
	}
	return c.JSON(result)
}

// CreateRule creates a new alert rule.
func (h *AlertHandler) CreateRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "alert"); err != nil {
		return err
	}

	var req createAlertRuleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" || len(req.Name) > maxNameLen {
		return fiber.NewError(fiber.StatusBadRequest, "Name is required and must be <= 255 characters")
	}
	if len(req.Description) > maxDescriptionLen {
		return fiber.NewError(fiber.StatusBadRequest, "Description must be <= 1024 characters")
	}
	if !notifications.ValidMetric(req.Metric) {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid metric")
	}
	if !validOperators[req.Operator] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid operator")
	}
	if req.Threshold == nil {
		return fiber.NewError(fiber.StatusBadRequest, "Threshold is required")
	}
	if req.Severity == "" {
		req.Severity = "warning"
	}
	if !validSeveritiesAlert[req.Severity] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid severity")
	}
	if req.ScopeType == "" {
		req.ScopeType = "cluster"
	}
	if !validScopeTypes[req.ScopeType] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scope_type")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	durationSeconds := int32(300)
	if req.DurationSeconds != nil {
		if *req.DurationSeconds < 0 || *req.DurationSeconds > maxDurationSec {
			return fiber.NewError(fiber.StatusBadRequest, "duration_seconds must be between 0 and 86400")
		}
		durationSeconds = *req.DurationSeconds
	}
	cooldownSeconds := int32(3600)
	if req.CooldownSeconds != nil {
		if *req.CooldownSeconds < 0 || *req.CooldownSeconds > maxCooldownSec {
			return fiber.NewError(fiber.StatusBadRequest, "cooldown_seconds must be between 0 and 604800")
		}
		cooldownSeconds = *req.CooldownSeconds
	}
	escalationChain := json.RawMessage("[]")
	if len(req.EscalationChain) > 0 {
		if err := validateEscalationChain(req.EscalationChain); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid escalation_chain: %v", err))
		}
		escalationChain = req.EscalationChain
	}
	if len(req.MessageTemplate) > maxTemplateLen {
		return fiber.NewError(fiber.StatusBadRequest, "message_template must be <= 4096 characters")
	}

	var clusterID, nodeID, vmID pgtype.UUID
	if req.ClusterID != "" {
		cid, err := uuid.Parse(req.ClusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
		}
		clusterID = pgtype.UUID{Bytes: cid, Valid: true}
	}
	if req.NodeID != "" {
		nid, err := uuid.Parse(req.NodeID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node_id")
		}
		nodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}
	if req.VMID != "" {
		vid, err := uuid.Parse(req.VMID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid vm_id")
		}
		vmID = pgtype.UUID{Bytes: vid, Valid: true}
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	rule, err := h.queries.InsertAlertRule(c.Context(), db.InsertAlertRuleParams{
		Name:            req.Name,
		Description:     req.Description,
		Enabled:         enabled,
		Severity:        req.Severity,
		Metric:          req.Metric,
		Operator:        req.Operator,
		Threshold:       *req.Threshold,
		DurationSeconds: durationSeconds,
		ScopeType:       req.ScopeType,
		ClusterID:       clusterID,
		NodeID:          nodeID,
		VmID:            vmID,
		CooldownSeconds: cooldownSeconds,
		EscalationChain: escalationChain,
		CreatedBy:       userID,
		MessageTemplate: req.MessageTemplate,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create alert rule")
	}

	if clusterID.Valid {
		cid, _ := uuid.FromBytes(clusterID.Bytes[:])
		h.auditLog(c, cid, "alert_rule", rule.ID.String(), "alert_rule_created")
	} else {
		h.auditLogGlobal(c, "alert_rule", rule.ID.String(), "alert_rule_created")
	}

	return c.Status(fiber.StatusCreated).JSON(toAlertRuleResponse(rule))
}

// GetRule returns a single alert rule.
func (h *AlertHandler) GetRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	rule, err := h.queries.GetAlertRule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rule not found")
	}

	return c.JSON(toAlertRuleResponse(rule))
}

// UpdateRule updates an existing alert rule.
func (h *AlertHandler) UpdateRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "alert"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	existing, err := h.queries.GetAlertRule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rule not found")
	}

	var req createAlertRuleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Apply defaults from existing.
	name := existing.Name
	if req.Name != "" {
		if len(req.Name) > maxNameLen {
			return fiber.NewError(fiber.StatusBadRequest, "Name must be <= 255 characters")
		}
		name = req.Name
	}
	description := existing.Description
	if req.Description != "" {
		if len(req.Description) > maxDescriptionLen {
			return fiber.NewError(fiber.StatusBadRequest, "Description must be <= 1024 characters")
		}
		description = req.Description
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	severity := existing.Severity
	if req.Severity != "" {
		if !validSeveritiesAlert[req.Severity] {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid severity")
		}
		severity = req.Severity
	}
	metric := existing.Metric
	if req.Metric != "" {
		if !notifications.ValidMetric(req.Metric) {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid metric")
		}
		metric = req.Metric
	}
	operator := existing.Operator
	if req.Operator != "" {
		if !validOperators[req.Operator] {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid operator")
		}
		operator = req.Operator
	}
	threshold := existing.Threshold
	if req.Threshold != nil {
		threshold = *req.Threshold
	}
	durationSeconds := existing.DurationSeconds
	if req.DurationSeconds != nil {
		if *req.DurationSeconds < 0 || *req.DurationSeconds > maxDurationSec {
			return fiber.NewError(fiber.StatusBadRequest, "duration_seconds must be between 0 and 86400")
		}
		durationSeconds = *req.DurationSeconds
	}
	scopeType := existing.ScopeType
	if req.ScopeType != "" {
		if !validScopeTypes[req.ScopeType] {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid scope_type")
		}
		scopeType = req.ScopeType
	}
	cooldownSeconds := existing.CooldownSeconds
	if req.CooldownSeconds != nil {
		if *req.CooldownSeconds < 0 || *req.CooldownSeconds > maxCooldownSec {
			return fiber.NewError(fiber.StatusBadRequest, "cooldown_seconds must be between 0 and 604800")
		}
		cooldownSeconds = *req.CooldownSeconds
	}
	escalationChain := existing.EscalationChain
	if len(req.EscalationChain) > 0 {
		if err := validateEscalationChain(req.EscalationChain); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid escalation_chain: %v", err))
		}
		escalationChain = req.EscalationChain
	}
	messageTemplate := existing.MessageTemplate
	if req.MessageTemplate != "" {
		if len(req.MessageTemplate) > maxTemplateLen {
			return fiber.NewError(fiber.StatusBadRequest, "message_template must be <= 4096 characters")
		}
		messageTemplate = req.MessageTemplate
	}

	clusterID := existing.ClusterID
	if req.ClusterID != "" {
		cid, parseErr := uuid.Parse(req.ClusterID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
		}
		clusterID = pgtype.UUID{Bytes: cid, Valid: true}
	}
	nodeID := existing.NodeID
	if req.NodeID != "" {
		nid, parseErr := uuid.Parse(req.NodeID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node_id")
		}
		nodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}
	vmID := existing.VmID
	if req.VMID != "" {
		vid, parseErr := uuid.Parse(req.VMID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid vm_id")
		}
		vmID = pgtype.UUID{Bytes: vid, Valid: true}
	}

	rule, err := h.queries.UpdateAlertRule(c.Context(), db.UpdateAlertRuleParams{
		ID:              id,
		Name:            name,
		Description:     description,
		Enabled:         enabled,
		Severity:        severity,
		Metric:          metric,
		Operator:        operator,
		Threshold:       threshold,
		DurationSeconds: durationSeconds,
		ScopeType:       scopeType,
		ClusterID:       clusterID,
		NodeID:          nodeID,
		VmID:            vmID,
		CooldownSeconds: cooldownSeconds,
		EscalationChain: escalationChain,
		MessageTemplate: messageTemplate,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update alert rule")
	}

	h.auditLogGlobal(c, "alert_rule", id.String(), "alert_rule_updated")

	return c.JSON(toAlertRuleResponse(rule))
}

// DeleteRule deletes an alert rule.
func (h *AlertHandler) DeleteRule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "alert"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	if _, err := h.queries.GetAlertRule(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rule not found")
	}

	if err := h.queries.DeleteAlertRule(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete alert rule")
	}

	h.auditLogGlobal(c, "alert_rule", id.String(), "alert_rule_deleted")

	return c.SendStatus(fiber.StatusNoContent)
}

// ====== Alert History ======

// ListAlerts lists alert history with optional filters.
func (h *AlertHandler) ListAlerts(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	state := c.Query("state")
	if state != "" {
		validStates := map[string]bool{"pending": true, "firing": true, "acknowledged": true, "resolved": true}
		if !validStates[state] {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid state filter")
		}
	}
	severity := c.Query("severity")
	if severity != "" && !validSeveritiesAlert[severity] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid severity filter")
	}
	clusterIDStr := c.Query("cluster_id")

	var clusterID uuid.UUID
	if clusterIDStr != "" {
		var parseErr error
		clusterID, parseErr = uuid.Parse(clusterIDStr)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
		}
	}

	alerts, err := h.queries.ListAlertHistoryFiltered(c.Context(), db.ListAlertHistoryFilteredParams{
		State:     state,
		Severity:  severity,
		ClusterID: clusterID,
		LimitVal:  safeInt32(limit),
		OffsetVal: safeInt32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alerts")
	}

	result := make([]alertHistoryResponse, len(alerts))
	for i, a := range alerts {
		result[i] = toAlertHistoryResponse(a)
	}
	return c.JSON(result)
}

// GetAlert returns a single alert.
func (h *AlertHandler) GetAlert(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid alert ID")
	}

	alert, err := h.queries.GetAlertHistory(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Alert not found")
	}

	return c.JSON(toAlertHistoryResponse(alert))
}

// GetAlertSummary returns active alert counts.
func (h *AlertHandler) GetAlertSummary(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	summary, err := h.queries.GetAlertSummary(c.Context())
	if err != nil {
		return c.JSON(alertSummaryResponse{})
	}

	return c.JSON(alertSummaryResponse{
		FiringCount:       summary.FiringCount,
		PendingCount:      summary.PendingCount,
		AcknowledgedCount: summary.AcknowledgedCount,
		CriticalFiring:    summary.CriticalFiring,
		WarningFiring:     summary.WarningFiring,
		InfoFiring:        summary.InfoFiring,
	})
}

// AcknowledgeAlert acknowledges a firing alert.
func (h *AlertHandler) AcknowledgeAlert(c *fiber.Ctx) error {
	if err := requirePerm(c, "acknowledge", "alert"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid alert ID")
	}

	alert, err := h.queries.GetAlertHistory(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Alert not found")
	}
	if alert.State != "firing" {
		return fiber.NewError(fiber.StatusConflict, "Alert is not in firing state")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)
	if err := h.queries.AcknowledgeAlert(c.Context(), db.AcknowledgeAlertParams{
		ID:             id,
		AcknowledgedBy: pgtype.UUID{Bytes: userID, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to acknowledge alert")
	}

	h.auditLogGlobal(c, "alert", id.String(), "alert_acknowledged")

	if h.eventPub != nil {
		clusterID := ""
		if alert.ClusterID.Valid {
			cid, _ := uuid.FromBytes(alert.ClusterID.Bytes[:])
			clusterID = cid.String()
		}
		h.eventPub.ClusterEvent(c.Context(), clusterID, events.KindAlertStateChange, "alert", id.String(), "acknowledged")
	}

	return c.JSON(fiber.Map{"status": "acknowledged"})
}

// ResolveAlert resolves a firing or acknowledged alert.
func (h *AlertHandler) ResolveAlert(c *fiber.Ctx) error {
	if err := requirePerm(c, "acknowledge", "alert"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid alert ID")
	}

	alert, err := h.queries.GetAlertHistory(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Alert not found")
	}
	if alert.State != "firing" && alert.State != "acknowledged" {
		return fiber.NewError(fiber.StatusConflict, "Alert cannot be resolved from current state")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)
	if err := h.queries.ResolveAlert(c.Context(), db.ResolveAlertParams{
		ID:         id,
		ResolvedBy: pgtype.UUID{Bytes: userID, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve alert")
	}

	h.auditLogGlobal(c, "alert", id.String(), "alert_resolved")

	if h.eventPub != nil {
		clusterID := ""
		if alert.ClusterID.Valid {
			cid, _ := uuid.FromBytes(alert.ClusterID.Bytes[:])
			clusterID = cid.String()
		}
		h.eventPub.ClusterEvent(c.Context(), clusterID, events.KindAlertStateChange, "alert", id.String(), "resolved")
	}

	return c.JSON(fiber.Map{"status": "resolved"})
}

// ListAlertsByCluster lists alerts for a specific cluster.
func (h *AlertHandler) ListAlertsByCluster(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	alerts, err := h.queries.ListAlertHistoryByCluster(c.Context(), db.ListAlertHistoryByClusterParams{
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
		Limit:     safeInt32(limit),
		Offset:    safeInt32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alerts")
	}

	result := make([]alertHistoryResponse, len(alerts))
	for i, a := range alerts {
		result[i] = toAlertHistoryResponse(a)
	}
	return c.JSON(result)
}

// CountActiveAlertsByCluster returns active alert counts for a cluster.
func (h *AlertHandler) CountActiveAlertsByCluster(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	counts, err := h.queries.CountActiveAlertsByCluster(c.Context(), pgtype.UUID{Bytes: clusterID, Valid: true})
	if err != nil {
		return c.JSON(fiber.Map{"firing": 0, "pending": 0, "acknowledged": 0})
	}

	return c.JSON(fiber.Map{
		"firing":       counts.FiringCount,
		"pending":      counts.PendingCount,
		"acknowledged": counts.AcknowledgedCount,
	})
}

// ====== Notification Channels ======

// ListChannels lists all notification channels.
func (h *AlertHandler) ListChannels(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "notification_channel"); err != nil {
		return err
	}

	channels, err := h.queries.ListNotificationChannels(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list channels")
	}

	result := make([]notificationChannelResponse, len(channels))
	for i, ch := range channels {
		result[i] = toNotificationChannelResponse(ch)
	}
	return c.JSON(result)
}

// CreateChannel creates a new notification channel.
func (h *AlertHandler) CreateChannel(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_channel"); err != nil {
		return err
	}

	var req createChannelRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" || len(req.Name) > maxNameLen {
		return fiber.NewError(fiber.StatusBadRequest, "Name is required and must be <= 255 characters")
	}
	if !validChannelTypes[req.ChannelType] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid channel_type")
	}
	if len(req.Config) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "Config is required")
	}

	encrypted, err := crypto.Encrypt(string(req.Config), h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt config")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	ch, err := h.queries.InsertNotificationChannel(c.Context(), db.InsertNotificationChannelParams{
		Name:             req.Name,
		ChannelType:      req.ChannelType,
		ConfigEncrypted:  encrypted,
		Enabled:          enabled,
		CreatedBy:        userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create channel")
	}

	h.auditLogGlobal(c, "notification_channel", ch.ID.String(), "channel_created")

	return c.Status(fiber.StatusCreated).JSON(toNotificationChannelResponse(ch))
}

// GetChannel returns a single notification channel.
func (h *AlertHandler) GetChannel(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "notification_channel"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid channel ID")
	}

	ch, err := h.queries.GetNotificationChannel(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	return c.JSON(toNotificationChannelResponse(ch))
}

// UpdateChannel updates a notification channel.
func (h *AlertHandler) UpdateChannel(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_channel"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid channel ID")
	}

	existing, err := h.queries.GetNotificationChannel(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	var req createChannelRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	name := existing.Name
	if req.Name != "" {
		if len(req.Name) > maxNameLen {
			return fiber.NewError(fiber.StatusBadRequest, "Name must be <= 255 characters")
		}
		name = req.Name
	}
	channelType := existing.ChannelType
	if req.ChannelType != "" {
		if !validChannelTypes[req.ChannelType] {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid channel_type")
		}
		channelType = req.ChannelType
	}
	configEncrypted := existing.ConfigEncrypted
	if len(req.Config) > 0 {
		enc, encErr := crypto.Encrypt(string(req.Config), h.encryptionKey)
		if encErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt config")
		}
		configEncrypted = enc
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	ch, err := h.queries.UpdateNotificationChannel(c.Context(), db.UpdateNotificationChannelParams{
		ID:              id,
		Name:            name,
		ChannelType:     channelType,
		ConfigEncrypted: configEncrypted,
		Enabled:         enabled,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update channel")
	}

	h.auditLogGlobal(c, "notification_channel", id.String(), "channel_updated")

	return c.JSON(toNotificationChannelResponse(ch))
}

// DeleteChannel deletes a notification channel.
func (h *AlertHandler) DeleteChannel(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_channel"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid channel ID")
	}

	if _, err := h.queries.GetNotificationChannel(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	if err := h.queries.DeleteNotificationChannel(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete channel")
	}

	h.auditLogGlobal(c, "notification_channel", id.String(), "channel_deleted")

	return c.SendStatus(fiber.StatusNoContent)
}

// TestChannel sends a test notification through a channel.
func (h *AlertHandler) TestChannel(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "notification_channel"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid channel ID")
	}

	ch, err := h.queries.GetNotificationChannel(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	if h.registry == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Notification dispatchers not available")
	}

	dispatcher, ok := h.registry.Get(ch.ChannelType)
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("No dispatcher for channel type: %s", ch.ChannelType))
	}

	configJSON, err := crypto.Decrypt(ch.ConfigEncrypted, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt channel config")
	}

	payload := notifications.AlertPayload{
		RuleName:        "Test Alert Rule",
		RuleID:          "00000000-0000-0000-0000-000000000000",
		Severity:        "warning",
		State:           "firing",
		Metric:          "cpu_usage",
		Operator:        ">",
		Threshold:       90.0,
		CurrentValue:    95.5,
		ResourceName:    "test-node-01",
		NodeName:        "test-node-01",
		ClusterID:       "test-cluster",
		Message:         "This is a test notification from Nexara.",
		FiredAt:         time.Now().UTC().Format(time.RFC3339),
		EscalationLevel: 0,
	}

	if err := dispatcher.Send(c.Context(), json.RawMessage(configJSON), payload); err != nil {
		// Log the full error for debugging; return generic message to client.
		h.eventPub.ClusterEvent(c.Context(), "", events.KindAlertFired, "notification_channel", id.String(), "test_failed")
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"success": false,
			"message": "Test notification failed. Check server logs for details.",
		})
	}

	h.auditLogGlobal(c, "notification_channel", id.String(), "channel_tested")

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Test notification sent successfully",
	})
}

// ====== Maintenance Windows ======

// ListMaintenanceWindows lists maintenance windows for a cluster.
func (h *AlertHandler) ListMaintenanceWindows(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "maintenance_window"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	windows, err := h.queries.ListMaintenanceWindows(c.Context(), db.ListMaintenanceWindowsParams{
		ClusterID: clusterID,
		Limit:     safeInt32(limit),
		Offset:    safeInt32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list maintenance windows")
	}

	result := make([]maintenanceWindowResponse, len(windows))
	for i, w := range windows {
		result[i] = toMaintenanceWindowResponse(w)
	}
	return c.JSON(result)
}

// CreateMaintenanceWindow creates a new maintenance window.
func (h *AlertHandler) CreateMaintenanceWindow(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "maintenance_window"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	var req createMaintenanceWindowRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if len(req.Description) > maxDescriptionLen {
		return fiber.NewError(fiber.StatusBadRequest, "Description must be <= 1024 characters")
	}

	startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid starts_at format (use RFC3339)")
	}
	endsAt, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid ends_at format (use RFC3339)")
	}
	if !endsAt.After(startsAt) {
		return fiber.NewError(fiber.StatusBadRequest, "ends_at must be after starts_at")
	}

	var nodeID pgtype.UUID
	if req.NodeID != "" {
		nid, parseErr := uuid.Parse(req.NodeID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node_id")
		}
		nodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	window, err := h.queries.InsertMaintenanceWindow(c.Context(), db.InsertMaintenanceWindowParams{
		ClusterID:   clusterID,
		NodeID:      nodeID,
		Description: req.Description,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		CreatedBy:   userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create maintenance window")
	}

	h.auditLog(c, clusterID, "maintenance_window", window.ID.String(), "maintenance_window_created")

	return c.Status(fiber.StatusCreated).JSON(toMaintenanceWindowResponse(window))
}

// UpdateMaintenanceWindow updates a maintenance window.
func (h *AlertHandler) UpdateMaintenanceWindow(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "maintenance_window"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid window ID")
	}

	existing, err := h.queries.GetMaintenanceWindow(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	// Verify the maintenance window belongs to the cluster in the URL.
	if existing.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	var req createMaintenanceWindowRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	description := existing.Description
	if req.Description != "" {
		if len(req.Description) > maxDescriptionLen {
			return fiber.NewError(fiber.StatusBadRequest, "Description must be <= 1024 characters")
		}
		description = req.Description
	}
	startsAt := existing.StartsAt
	if req.StartsAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, req.StartsAt)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid starts_at format")
		}
		startsAt = parsed
	}
	endsAt := existing.EndsAt
	if req.EndsAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, req.EndsAt)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid ends_at format")
		}
		endsAt = parsed
	}
	if !endsAt.After(startsAt) {
		return fiber.NewError(fiber.StatusBadRequest, "ends_at must be after starts_at")
	}
	nodeID := existing.NodeID
	if req.NodeID != "" {
		nid, parseErr := uuid.Parse(req.NodeID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node_id")
		}
		nodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}

	window, err := h.queries.UpdateMaintenanceWindow(c.Context(), db.UpdateMaintenanceWindowParams{
		ID:          id,
		Description: description,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		NodeID:      nodeID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update maintenance window")
	}

	h.auditLog(c, existing.ClusterID, "maintenance_window", id.String(), "maintenance_window_updated")

	return c.JSON(toMaintenanceWindowResponse(window))
}

// DeleteMaintenanceWindow deletes a maintenance window.
func (h *AlertHandler) DeleteMaintenanceWindow(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "maintenance_window"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid window ID")
	}

	existing, err := h.queries.GetMaintenanceWindow(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	// Verify the maintenance window belongs to the cluster in the URL.
	if existing.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	if err := h.queries.DeleteMaintenanceWindow(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete maintenance window")
	}

	h.auditLog(c, existing.ClusterID, "maintenance_window", id.String(), "maintenance_window_deleted")

	return c.SendStatus(fiber.StatusNoContent)
}
