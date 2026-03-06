package handlers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/drs"
)

// DRSHandler handles DRS configuration and evaluation endpoints.
type DRSHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewDRSHandler creates a new DRS handler.
func NewDRSHandler(queries *db.Queries, encryptionKey string) *DRSHandler {
	return &DRSHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
	}
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
	ID        uuid.UUID       `json:"id"`
	ClusterID uuid.UUID       `json:"cluster_id"`
	RuleType  string          `json:"rule_type"`
	VMIDs     json.RawMessage `json:"vm_ids"`
	NodeNames json.RawMessage `json:"node_names"`
	Enabled   bool            `json:"enabled"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func toDRSRuleResponse(r db.DrsRule) drsRuleResponse {
	return drsRuleResponse{
		ID:        r.ID,
		ClusterID: r.ClusterID,
		RuleType:  r.RuleType,
		VMIDs:     r.VmIds,
		NodeNames: r.NodeNames,
		Enabled:   r.Enabled,
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
			Weights:             json.RawMessage(`{"cpu":0.4,"memory":0.4,"network":0.2}`),
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
		req.Weights = json.RawMessage(`{"cpu":0.4,"memory":0.4,"network":0.2}`)
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

	return c.Status(fiber.StatusCreated).JSON(toDRSRuleResponse(rule))
}

// DeleteRule handles DELETE /api/v1/clusters/:cluster_id/drs/rules/:rule_id.
func (h *DRSHandler) DeleteRule(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	ruleID, err := uuid.Parse(c.Params("rule_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	if err := h.queries.DeleteDRSRule(c.Context(), ruleID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete DRS rule")
	}

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

	engine := drs.NewEngine(h.queries, h.encryptionKey, nil)
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
