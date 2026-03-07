package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/drs"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// DRSHandler handles DRS configuration and evaluation endpoints.
type DRSHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewDRSHandler creates a new DRS handler.
func NewDRSHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *DRSHandler {
	return &DRSHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

// auditLog writes an audit log entry. Failures are logged but don't fail the request.
func (h *DRSHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})
}

// --- Request / Response types ---

type drsConfigRequest struct {
	Mode                string          `json:"mode"`
	Enabled             bool            `json:"enabled"`
	Weights             json.RawMessage `json:"weights"`
	ImbalanceThreshold  float64         `json:"imbalance_threshold"`
	EvalIntervalSeconds int32           `json:"eval_interval_seconds"`
}

type drsConfigResponse struct {
	ID                  uuid.UUID       `json:"id"`
	ClusterID           uuid.UUID       `json:"cluster_id"`
	Mode                string          `json:"mode"`
	Enabled             bool            `json:"enabled"`
	Weights             json.RawMessage `json:"weights"`
	ImbalanceThreshold  float64         `json:"imbalance_threshold"`
	EvalIntervalSeconds int32           `json:"eval_interval_seconds"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
}

func toDRSConfigResponse(c db.DrsConfig) drsConfigResponse {
	return drsConfigResponse{
		ID:                  c.ID,
		ClusterID:           c.ClusterID,
		Mode:                c.Mode,
		Enabled:             c.Enabled,
		Weights:             c.Weights,
		ImbalanceThreshold:  c.ImbalanceThreshold,
		EvalIntervalSeconds: c.EvalIntervalSeconds,
		CreatedAt:           c.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:           c.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

type drsRuleRequest struct {
	RuleType  string          `json:"rule_type"`
	VMIDs     json.RawMessage `json:"vm_ids"`
	NodeNames json.RawMessage `json:"node_names"`
	Enabled   bool            `json:"enabled"`
}

type drsRuleResponse struct {
	ID         uuid.UUID       `json:"id"`
	ClusterID  uuid.UUID       `json:"cluster_id"`
	RuleType   string          `json:"rule_type"`
	VMIDs      json.RawMessage `json:"vm_ids"`
	NodeNames  json.RawMessage `json:"node_names"`
	Enabled    bool            `json:"enabled"`
	Source     string          `json:"source"`
	HARuleName string          `json:"ha_rule_name,omitempty"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

func toDRSRuleResponse(r db.DrsRule) drsRuleResponse {
	return drsRuleResponse{
		ID:        r.ID,
		ClusterID: r.ClusterID,
		RuleType:  r.RuleType,
		VMIDs:     r.VmIds,
		NodeNames: r.NodeNames,
		Enabled:   r.Enabled,
		Source:    "manual",
		CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: r.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

type drsHistoryResponse struct {
	ID          uuid.UUID `json:"id"`
	ClusterID   uuid.UUID `json:"cluster_id"`
	SourceNode  string    `json:"source_node"`
	TargetNode  string    `json:"target_node"`
	VMID        int32     `json:"vm_id"`
	VMType      string    `json:"vm_type"`
	Reason      string    `json:"reason"`
	ScoreBefore float64   `json:"score_before"`
	ScoreAfter  float64   `json:"score_after"`
	Status      string    `json:"status"`
	ExecutedAt  *string   `json:"executed_at"`
	CreatedAt   string    `json:"created_at"`
}

func toDRSHistoryResponse(h db.DrsHistory) drsHistoryResponse {
	r := drsHistoryResponse{
		ID:          h.ID,
		ClusterID:   h.ClusterID,
		SourceNode:  h.SourceNode,
		TargetNode:  h.TargetNode,
		VMID:        h.VmID,
		VMType:      h.VmType,
		Reason:      h.Reason,
		ScoreBefore: h.ScoreBefore,
		ScoreAfter:  h.ScoreAfter,
		Status:      h.Status,
		CreatedAt:   h.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if h.ExecutedAt.Valid {
		s := h.ExecutedAt.Time.Format("2006-01-02T15:04:05Z")
		r.ExecutedAt = &s
	}
	return r
}

// --- Handlers ---

var validDRSModes = map[string]bool{
	"disabled":  true,
	"advisory":  true,
	"automatic": true,
}

var validRuleTypes = map[string]bool{
	"affinity":      true,
	"anti-affinity": true,
	"pin":           true,
}

// GetConfig handles GET /api/v1/clusters/:cluster_id/drs/config.
func (h *DRSHandler) GetConfig(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	cfg, err := h.queries.GetDRSConfig(c.Context(), clusterID)
	if err != nil {
		// No config means DRS is disabled (default state).
		return c.JSON(drsConfigResponse{
			ClusterID:           clusterID,
			Mode:                "disabled",
			Enabled:             false,
			Weights:             json.RawMessage(`{"cpu":0.5,"memory":0.5}`),
			ImbalanceThreshold:  0.25,
			EvalIntervalSeconds: 300,
		})
	}

	return c.JSON(toDRSConfigResponse(cfg))
}

// UpdateConfig handles PUT /api/v1/clusters/:cluster_id/drs/config.
func (h *DRSHandler) UpdateConfig(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req drsConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if !validDRSModes[req.Mode] {
		return fiber.NewError(fiber.StatusBadRequest, "mode must be one of: disabled, advisory, automatic")
	}

	if req.ImbalanceThreshold <= 0 || req.ImbalanceThreshold > 1 {
		return fiber.NewError(fiber.StatusBadRequest, "imbalance_threshold must be between 0 and 1")
	}

	if req.EvalIntervalSeconds < 60 {
		return fiber.NewError(fiber.StatusBadRequest, "eval_interval_seconds must be at least 60")
	}

	if req.Weights == nil {
		req.Weights = json.RawMessage(`{"cpu":0.5,"memory":0.5}`)
	}

	cfg, err := h.queries.UpsertDRSConfig(c.Context(), db.UpsertDRSConfigParams{
		ClusterID:           clusterID,
		Mode:                req.Mode,
		Enabled:             req.Enabled,
		Weights:             req.Weights,
		ImbalanceThreshold:  req.ImbalanceThreshold,
		EvalIntervalSeconds: req.EvalIntervalSeconds,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update DRS config")
	}

	details, _ := json.Marshal(map[string]interface{}{"mode": req.Mode, "enabled": req.Enabled, "imbalance_threshold": req.ImbalanceThreshold})
	h.auditLog(c, clusterID, "drs", cfg.ID.String(), "config_update", details)

	return c.JSON(toDRSConfigResponse(cfg))
}

// ListRules handles GET /api/v1/clusters/:cluster_id/drs/rules.
func (h *DRSHandler) ListRules(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	rules, err := h.queries.ListDRSRules(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list DRS rules")
	}

	resp := make([]drsRuleResponse, len(rules))
	for i, r := range rules {
		resp[i] = toDRSRuleResponse(r)
	}

	return c.JSON(resp)
}

// CreateRule handles POST /api/v1/clusters/:cluster_id/drs/rules.
func (h *DRSHandler) CreateRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req drsRuleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if !validRuleTypes[req.RuleType] {
		return fiber.NewError(fiber.StatusBadRequest, "rule_type must be one of: affinity, anti-affinity, pin")
	}

	if req.VMIDs == nil {
		req.VMIDs = json.RawMessage(`[]`)
	}
	if req.NodeNames == nil {
		req.NodeNames = json.RawMessage(`[]`)
	}

	rule, err := h.queries.InsertDRSRule(c.Context(), db.InsertDRSRuleParams{
		ClusterID: clusterID,
		RuleType:  req.RuleType,
		VmIds:     req.VMIDs,
		NodeNames: req.NodeNames,
		Enabled:   req.Enabled,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create DRS rule")
	}

	details, _ := json.Marshal(map[string]interface{}{"rule_type": req.RuleType, "vm_ids": req.VMIDs, "node_names": req.NodeNames})
	h.auditLog(c, clusterID, "drs_rule", rule.ID.String(), "rule_created", details)

	return c.Status(fiber.StatusCreated).JSON(toDRSRuleResponse(rule))
}

// DeleteRule handles DELETE /api/v1/clusters/:cluster_id/drs/rules/:rule_id.
func (h *DRSHandler) DeleteRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, _ := uuid.Parse(c.Params("cluster_id"))

	ruleID, err := uuid.Parse(c.Params("rule_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	if err := h.queries.DeleteDRSRule(c.Context(), ruleID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete DRS rule")
	}

	h.auditLog(c, clusterID, "drs_rule", ruleID.String(), "rule_deleted", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}

// TriggerEvaluate handles POST /api/v1/clusters/:cluster_id/drs/evaluate.
func (h *DRSHandler) TriggerEvaluate(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	engine := drs.NewEngine(h.queries, h.encryptionKey, slog.Default())
	recommendations, err := engine.Evaluate(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	// Record advisory results.
	for _, rec := range recommendations {
		_, _ = h.queries.InsertDRSHistory(c.Context(), db.InsertDRSHistoryParams{
			ClusterID:   clusterID,
			SourceNode:  rec.SourceNode,
			TargetNode:  rec.TargetNode,
			VmID:        int32(rec.VMID),
			VmType:      rec.VMType,
			Reason:      rec.Reason,
			ScoreBefore: rec.ScoreBefore,
			ScoreAfter:  rec.ScoreAfter,
			Status:      "advisory",
			ExecutedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
	}

	type evalResponse struct {
		VMID       int     `json:"vmid"`
		VMType     string  `json:"vm_type"`
		From       string  `json:"from"`
		To         string  `json:"to"`
		Reason     string  `json:"reason"`
		Improvement float64 `json:"improvement"`
	}

	resp := make([]evalResponse, len(recommendations))
	for i, r := range recommendations {
		resp[i] = evalResponse{
			VMID:        r.VMID,
			VMType:      r.VMType,
			From:        r.SourceNode,
			To:          r.TargetNode,
			Reason:      r.Reason,
			Improvement: r.ExpectedImprovement,
		}
	}

	details, _ := json.Marshal(map[string]interface{}{"recommendation_count": len(resp)})
	h.auditLog(c, clusterID, "drs", clusterID.String(), "evaluate_triggered", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindDRSAction, "drs", clusterID.String(), "evaluate_triggered")

	return c.JSON(fiber.Map{
		"recommendations": resp,
		"count":           len(resp),
	})
}

// ListHistory handles GET /api/v1/clusters/:cluster_id/drs/history.
func (h *DRSHandler) ListHistory(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	limit := int32(50)
	if l := c.QueryInt("limit", 50); l > 0 && l <= 500 {
		limit = int32(l)
	}

	history, err := h.queries.ListDRSHistory(c.Context(), db.ListDRSHistoryParams{
		ClusterID: clusterID,
		Limit:     limit,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list DRS history")
	}

	resp := make([]drsHistoryResponse, len(history))
	for i, h := range history {
		resp[i] = toDRSHistoryResponse(h)
	}

	return c.JSON(resp)
}

// createProxmoxClient creates a Proxmox client for the given cluster ID.
func (h *DRSHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}

// haRuleToResponse converts a Proxmox HA rule entry to a DRS rule response.
func haRuleToResponse(clusterID uuid.UUID, entry proxmox.HARuleEntry) drsRuleResponse {
	// Map Proxmox HA rule type to DRS rule type.
	var ruleType string
	switch entry.Type {
	case "node-affinity":
		ruleType = "pin"
	case "resource-affinity":
		if entry.Affinity == "negative" {
			ruleType = "anti-affinity"
		} else {
			ruleType = "affinity"
		}
	default:
		ruleType = entry.Type
	}

	// Parse resources SIDs ("vm:100,ct:101") into VMID list.
	var vmIDs []int
	for _, res := range strings.Split(entry.Resources, ",") {
		res = strings.TrimSpace(res)
		parts := strings.SplitN(res, ":", 2)
		if len(parts) == 2 {
			if id, err := strconv.Atoi(parts[1]); err == nil {
				vmIDs = append(vmIDs, id)
			}
		}
	}

	// Parse nodes for node-affinity rules.
	var nodeNames []string
	if entry.Nodes != "" {
		for _, n := range strings.Split(entry.Nodes, ",") {
			n = strings.TrimSpace(n)
			// Strip priority suffix (e.g. "node1:100" → "node1").
			if idx := strings.Index(n, ":"); idx >= 0 {
				n = n[:idx]
			}
			if n != "" {
				nodeNames = append(nodeNames, n)
			}
		}
	}

	vmIDsJSON, _ := json.Marshal(vmIDs)
	nodeNamesJSON, _ := json.Marshal(nodeNames)

	return drsRuleResponse{
		ClusterID:  clusterID,
		RuleType:   ruleType,
		VMIDs:      vmIDsJSON,
		NodeNames:  nodeNamesJSON,
		Enabled:    entry.Disable == 0,
		Source:     "ha",
		HARuleName: entry.Rule,
	}
}

// ListHARules handles GET /api/v1/clusters/:cluster_id/drs/ha-rules.
func (h *DRSHandler) ListHARules(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	client, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	haRules, err := client.GetHARules(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to fetch HA rules: %v", err))
	}

	resp := make([]drsRuleResponse, 0, len(haRules))
	for _, entry := range haRules {
		resp = append(resp, haRuleToResponse(clusterID, entry))
	}

	return c.JSON(resp)
}

// CreateHARule handles POST /api/v1/clusters/:cluster_id/drs/ha-rules.
func (h *DRSHandler) CreateHARule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req struct {
		RuleName  string   `json:"rule_name"`
		RuleType  string   `json:"rule_type"`
		VMIDs     []int    `json:"vm_ids"`
		NodeNames []string `json:"node_names"`
		Enabled   bool     `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.RuleName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "rule_name is required for HA rules")
	}
	if !validRuleTypes[req.RuleType] {
		return fiber.NewError(fiber.StatusBadRequest, "rule_type must be one of: affinity, anti-affinity, pin")
	}
	if len(req.VMIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "vm_ids is required")
	}

	// Convert VMIDs to Proxmox SID format: "vm:100,vm:101".
	sids := make([]string, len(req.VMIDs))
	for i, id := range req.VMIDs {
		sids[i] = "vm:" + strconv.Itoa(id)
	}

	// Map DRS rule type back to Proxmox HA format.
	var haRuleType string
	params := proxmox.CreateHARuleParams{
		Rule:      req.RuleName,
		Resources: strings.Join(sids, ","),
	}

	switch req.RuleType {
	case "pin":
		haRuleType = "node-affinity"
		if len(req.NodeNames) > 0 {
			params.Nodes = strings.Join(req.NodeNames, ",")
		}
	case "affinity":
		haRuleType = "resource-affinity"
		params.Affinity = "positive"
	case "anti-affinity":
		haRuleType = "resource-affinity"
		params.Affinity = "negative"
	}

	client, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := client.CreateHARule(c.Context(), haRuleType, params); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to create HA rule: %v", err))
	}

	haDetails, _ := json.Marshal(map[string]interface{}{"rule_name": req.RuleName, "rule_type": req.RuleType, "vm_ids": req.VMIDs, "ha_type": haRuleType})
	h.auditLog(c, clusterID, "ha_rule", req.RuleName, "ha_rule_created", haDetails)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok", "rule_name": req.RuleName})
}

// DeleteHARule handles DELETE /api/v1/clusters/:cluster_id/drs/ha-rules/:rule_name.
func (h *DRSHandler) DeleteHARule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	ruleName := c.Params("rule_name")
	if ruleName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "rule_name is required")
	}

	client, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := client.DeleteHARule(c.Context(), ruleName); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to delete HA rule: %v", err))
	}

	h.auditLog(c, clusterID, "ha_rule", ruleName, "ha_rule_deleted", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}
