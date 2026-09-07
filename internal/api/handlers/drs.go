package handlers

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/drs"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// DRSHandler handles DRS configuration and evaluation endpoints.
type DRSHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	// engine is the process-wide DRS engine from the composition root,
	// sharing the Proxmox client cache with the scheduler's evaluations.
	// Evaluate is read-only, so serving it from the API process is safe;
	// EXECUTION is not — see TriggerEvaluate. May be nil in tests.
	engine *drs.Engine
}

// NewDRSHandler creates a new DRS handler. engine comes from the composition
// root (internal/app).
//
// No shutdown context: this handler no longer spawns detached goroutines.
// Manual evaluation used to execute migrations in a background goroutine here;
// it now queues the request for the scheduler leader, which owns the only
// executor.
func NewDRSHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher, engine *drs.Engine) *DRSHandler {
	return &DRSHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		engine:        engine,
	}
}

// --- Request / Response types ---

type drsConfigRequest struct {
	Mode                string          `json:"mode"`
	Weights             json.RawMessage `json:"weights"`
	ImbalanceThreshold  float64         `json:"imbalance_threshold"`
	EvalIntervalSeconds int32           `json:"eval_interval_seconds"`
	IncludeContainers   bool            `json:"include_containers"`
	// A POINTER, unlike the flag above, because this one defaults to TRUE and
	// absent must mean "leave the stored value alone".
	//
	// A plain bool would read an absent key as false, so any client predating
	// the field would silently disarm the protection on its next save and DRS
	// would start migrating Veeam's workers mid-backup with nothing to show
	// why. Coercing absent to TRUE has the mirror fault: it re-arms a flag an
	// operator deliberately turned off, from a stale browser tab saving an
	// unrelated threshold change. Neither is a decision the caller made — see
	// UpsertDRSConfig, which does the preserving.
	ExcludeVeeamWorkers *bool `json:"exclude_veeam_workers"`
}

type drsConfigResponse struct {
	ID                  uuid.UUID       `json:"id"`
	ClusterID           uuid.UUID       `json:"cluster_id"`
	Mode                string          `json:"mode"`
	Enabled             bool            `json:"enabled"`
	Weights             json.RawMessage `json:"weights"`
	ImbalanceThreshold  float64         `json:"imbalance_threshold"`
	EvalIntervalSeconds int32           `json:"eval_interval_seconds"`
	IncludeContainers   bool            `json:"include_containers"`
	ExcludeVeeamWorkers bool            `json:"exclude_veeam_workers"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
	// NativeCRS describes the cluster's Proxmox CRS dynamic load-balancer config
	// (PVE 9.2+). Nil when unknown or unset. When AutoRebalance is true, Nexara
	// DRS automatic migrations are suppressed to avoid conflicting migrations.
	NativeCRS *nativeCRSStatus `json:"native_crs,omitempty"`
}

// nativeCRSStatus mirrors proxmox.CRSSettings for the API response.
type nativeCRSStatus struct {
	HA               string `json:"ha"`
	AutoRebalance    bool   `json:"auto_rebalance"`
	Threshold        int    `json:"threshold"`
	HoldDuration     int    `json:"hold_duration"`
	Margin           int    `json:"margin"`
	Method           string `json:"method"`
	RebalanceOnStart bool   `json:"rebalance_on_start"`
}

// optionalBool maps an absent request field to SQL NULL, which the upsert
// reads as "leave the stored value alone".
func optionalBool(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
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
		IncludeContainers:   c.IncludeContainers,
		ExcludeVeeamWorkers: c.ExcludeVeeamWorkers,
		CreatedAt:           c.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:           c.UpdatedAt.Format(time.RFC3339Nano),
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
		CreatedAt: r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: r.UpdatedAt.Format(time.RFC3339Nano),
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
		CreatedAt:   h.CreatedAt.Format(time.RFC3339Nano),
	}
	if h.ExecutedAt.Valid {
		s := h.ExecutedAt.Time.Format(time.RFC3339Nano)
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
func (h *DRSHandler) GetConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "drs", clusterID); err != nil {
		return err
	}

	var resp drsConfigResponse
	cfg, err := h.queries.GetDRSConfig(c.Context(), clusterID)
	if err != nil {
		// No config means DRS is disabled (default state).
		resp = drsConfigResponse{
			ClusterID:           clusterID,
			Mode:                "disabled",
			Enabled:             false,
			Weights:             json.RawMessage(`{"cpu":0.3,"memory":0.7}`),
			ImbalanceThreshold:  0.25,
			EvalIntervalSeconds: 300,
			IncludeContainers:   false,
			ExcludeVeeamWorkers: true,
		}
	} else {
		resp = toDRSConfigResponse(cfg)
	}

	// Best-effort: surface the cluster's native Proxmox CRS state so the UI can
	// warn about and defer to it. Never fail the config read on this.
	resp.NativeCRS = h.detectNativeCRS(c, clusterID)
	return c.JSON(resp)
}

// detectNativeCRS reads the cluster's Proxmox CRS configuration so the UI can
// warn about (and defer to) the native dynamic load balancer. Returns nil when
// the options can't be fetched or no CRS is configured.
func (h *DRSHandler) detectNativeCRS(c fiber.Ctx, clusterID uuid.UUID) *nativeCRSStatus {
	client, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return nil
	}
	opts, err := client.GetClusterOptions(c.Context())
	if err != nil {
		return nil
	}
	s := proxmox.ParseCRSSettings(opts.CRS)
	if s.HA == "" && !s.AutoRebalance && !s.RebalanceOnStart {
		return nil
	}
	return &nativeCRSStatus{
		HA:               s.HA,
		AutoRebalance:    s.AutoRebalance,
		Threshold:        s.Threshold,
		HoldDuration:     s.HoldDuration,
		Margin:           s.Margin,
		Method:           s.Method,
		RebalanceOnStart: s.RebalanceOnStart,
	}
}

// UpdateConfig handles PUT /api/v1/clusters/:cluster_id/drs/config.
func (h *DRSHandler) UpdateConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "drs", clusterID); err != nil {
		return err
	}

	var req drsConfigRequest
	if err := c.Bind().Body(&req); err != nil {
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
		req.Weights = json.RawMessage(`{"cpu":0.3,"memory":0.7}`)
	}

	// `enabled` is derived from `mode` — the user-facing config no longer
	// exposes a separate toggle. The DB column remains because the
	// rolling-update orchestrator uses it as a runtime pause flag.
	enabled := req.Mode != "disabled"

	cfg, err := h.queries.UpsertDRSConfig(c.Context(), db.UpsertDRSConfigParams{
		ClusterID:           clusterID,
		Mode:                req.Mode,
		Enabled:             enabled,
		Weights:             req.Weights,
		ImbalanceThreshold:  req.ImbalanceThreshold,
		EvalIntervalSeconds: req.EvalIntervalSeconds,
		IncludeContainers:   req.IncludeContainers,
		ExcludeVeeamWorkers: optionalBool(req.ExcludeVeeamWorkers),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update DRS config")
	}

	// The safety flags belong in the audit row. Turning exclude_veeam_workers
	// off is exactly the change an operator has to be able to point at later
	// when explaining a backup job DRS killed, and the STORED value is what to
	// record — the request may have omitted the field entirely.
	details, _ := json.Marshal(map[string]interface{}{
		"mode":                  req.Mode,
		"imbalance_threshold":   req.ImbalanceThreshold,
		"include_containers":    cfg.IncludeContainers,
		"exclude_veeam_workers": cfg.ExcludeVeeamWorkers,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "drs", cfg.ID.String(), "config_update", details)

	return c.JSON(toDRSConfigResponse(cfg))
}

// ListRules handles GET /api/v1/clusters/:cluster_id/drs/rules.
func (h *DRSHandler) ListRules(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "drs", clusterID); err != nil {
		return err
	}

	rules, err := h.queries.ListDRSRules(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list DRS rules")
	}

	resp := make([]drsRuleResponse, len(rules))
	for i, r := range rules {
		resp[i] = toDRSRuleResponse(r)
	}

	return RespondItems(c, resp)
}

// CreateRule handles POST /api/v1/clusters/:cluster_id/drs/rules.
func (h *DRSHandler) CreateRule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "drs", clusterID); err != nil {
		return err
	}

	var req drsRuleRequest
	if err := c.Bind().Body(&req); err != nil {
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "drs_rule", rule.ID.String(), "rule_created", details)

	return c.Status(fiber.StatusCreated).JSON(toDRSRuleResponse(rule))
}

// DeleteRule handles DELETE /api/v1/clusters/:cluster_id/drs/rules/:rule_id.
func (h *DRSHandler) DeleteRule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "drs", clusterID); err != nil {
		return err
	}

	ruleID, err := uuid.Parse(c.Params("rule_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	if err := h.queries.DeleteDRSRule(c.Context(), ruleID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete DRS rule")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "drs_rule", ruleID.String(), "rule_deleted", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}

// TriggerEvaluate handles POST /api/v1/clusters/:cluster_id/drs/evaluate.
func (h *DRSHandler) TriggerEvaluate(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "drs", clusterID); err != nil {
		return err
	}

	if h.engine == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "DRS engine not configured")
	}
	// Evaluate is read-only — it scores nodes and proposes moves without
	// touching Proxmox state — so the API process runs it directly to build
	// the response. Executing those moves is a different matter; see below.
	result, err := h.engine.Evaluate(c.Context(), clusterID)
	if err != nil {
		slog.Error("DRS evaluate failed", "cluster_id", clusterID, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "DRS evaluation failed")
	}

	// If Proxmox's native CRS auto-rebalance suppressed automatic migrations,
	// don't run or record anything — tell the user why.
	if result != nil && result.BlockedByNativeCRS {
		details, _ := json.Marshal(map[string]any{"reason": result.BlockReason})
		AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "drs", clusterID.String(), "evaluate_blocked_native_crs", details)
		return c.JSON(fiber.Map{
			"blocked":         true,
			"block_reason":    result.BlockReason,
			"recommendations": []any{},
			"count":           0,
			"node_scores":     []any{},
			"imbalance":       0,
			"threshold":       0,
		})
	}

	var recommendations []drs.Recommendation
	if result != nil {
		recommendations = result.Recommendations
	}

	// Look up the DRS mode to decide whether to execute or just advise.
	cfg, cfgErr := h.queries.GetDRSConfig(c.Context(), clusterID)

	queued := false
	if len(recommendations) > 0 && cfgErr == nil && cfg.Mode == "automatic" {
		// Automatic mode: queue the execution for the scheduler leader rather
		// than dispatching migrations here.
		//
		// Executing in the API process ran outside the scheduler's leader
		// election and outside its per-cluster interval bookkeeping, so this
		// trigger and the 60s tick could both dispatch a move for the same
		// guest — the loser failing with "VM is locked (migrate)" and writing
		// a spurious failure row. Delegating to a shared in-process executor
		// would not fix it either: leader election is cross-process, and the
		// replica serving this request may not be the leader.
		//
		// The leader picks the request up on its next pass (worst case 60s),
		// bypassing the evaluation interval for that one run.
		if reqErr := h.queries.RequestDRSEvaluation(c.Context(), clusterID); reqErr != nil {
			slog.Error("DRS manual trigger: failed to queue evaluation", "cluster_id", clusterID, "error", reqErr)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to queue DRS evaluation")
		}
		queued = true
	} else {
		// Advisory mode or no config: record as advisory.
		for _, rec := range recommendations {
			_, _ = h.queries.InsertDRSHistory(c.Context(), db.InsertDRSHistoryParams{
				ClusterID:   clusterID,
				SourceNode:  rec.SourceNode,
				TargetNode:  rec.TargetNode,
				VmID:        safeconv.Int32(rec.VMID),
				VmType:      rec.VMType,
				Reason:      rec.Reason,
				ScoreBefore: rec.ScoreBefore,
				ScoreAfter:  rec.ScoreAfter,
				Status:      "advisory",
				ExecutedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		}
	}

	type evalRecommendation struct {
		VMID        int     `json:"vmid"`
		VMType      string  `json:"vm_type"`
		From        string  `json:"from"`
		To          string  `json:"to"`
		Reason      string  `json:"reason"`
		Improvement float64 `json:"improvement"`
	}

	type nodeScoreResponse struct {
		Node    string  `json:"node"`
		Score   float64 `json:"score"`
		CPULoad float64 `json:"cpu_load"`
		MemLoad float64 `json:"mem_load"`
	}

	resp := make([]evalRecommendation, len(recommendations))
	for i, r := range recommendations {
		resp[i] = evalRecommendation{
			VMID:        r.VMID,
			VMType:      r.VMType,
			From:        r.SourceNode,
			To:          r.TargetNode,
			Reason:      r.Reason,
			Improvement: r.ExpectedImprovement,
		}
	}

	// Build node scores for the response.
	var nodeScores []nodeScoreResponse
	var imbalance, threshold float64
	if result != nil {
		for _, s := range result.NodeScores {
			nodeScores = append(nodeScores, nodeScoreResponse{
				Node:    s.Node,
				Score:   s.Score,
				CPULoad: s.CPULoad,
				MemLoad: s.MemLoad,
			})
		}
		imbalance = result.Imbalance
		threshold = result.Threshold
	}

	details, _ := json.Marshal(map[string]interface{}{
		"recommendation_count": len(resp),
		"queued":               queued,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "drs", clusterID.String(), "evaluate_triggered", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindDRSAction, "drs", clusterID.String(), "evaluate_triggered")

	return c.JSON(fiber.Map{
		"blocked":         false,
		"recommendations": resp,
		"count":           len(resp),
		"node_scores":     nodeScores,
		"imbalance":       imbalance,
		"threshold":       threshold,
		// queued=true means an evaluation was queued for the scheduler
		// leader. It re-plans against live state before executing, so these
		// recommendations are a snapshot, not a committed work list — the UI
		// says "queued", never "migrating".
		"queued": queued,
	})
}

// ListHistory handles GET /api/v1/clusters/:cluster_id/drs/history.
func (h *DRSHandler) ListHistory(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "drs", clusterID); err != nil {
		return err
	}

	limit := int32(50)
	if l := fiber.Query[int](c, "limit", 50); l > 0 && l <= 500 {
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

	return RespondItems(c, resp)
}

// createProxmoxClient creates a Proxmox client for the given cluster ID.
func (h *DRSHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
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
func (h *DRSHandler) ListHARules(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "drs", clusterID); err != nil {
		return err
	}

	client, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	haRules, err := client.GetHARules(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]drsRuleResponse, 0, len(haRules))
	for _, entry := range haRules {
		resp = append(resp, haRuleToResponse(clusterID, entry))
	}

	return RespondItems(c, resp)
}

// CreateHARule handles POST /api/v1/clusters/:cluster_id/drs/ha-rules.
func (h *DRSHandler) CreateHARule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "drs", clusterID); err != nil {
		return err
	}

	var req struct {
		RuleName  string   `json:"rule_name"`
		RuleType  string   `json:"rule_type"`
		VMIDs     []int    `json:"vm_ids"`
		NodeNames []string `json:"node_names"`
		Enabled   bool     `json:"enabled"`
	}
	if err := c.Bind().Body(&req); err != nil {
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
		return mapProxmoxError(err)
	}

	haDetails, _ := json.Marshal(map[string]interface{}{"rule_name": req.RuleName, "rule_type": req.RuleType, "vm_ids": req.VMIDs, "ha_type": haRuleType})
	// "created", not "ha_rule_created": the row's resource_type already says
	// ha_rule, and HAHandler.CreateRule has always written the short verb. One
	// resource type, one vocabulary.
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_rule", req.RuleName, "created", haDetails)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok", "rule_name": req.RuleName})
}

// DeleteHARule handles DELETE /api/v1/clusters/:cluster_id/drs/ha-rules/:rule_name.
//
// The DRS page's own door onto the same PVE rules table that the HA tab's
// HAHandler.DeleteRule writes to, so it audits through the same classifier: a
// 200 from PVE's delete_rule is not evidence of a deletion, because that
// endpoint is an unconditional Perl hash delete and answers 200 for a rule that
// was already gone. Two endpoints filing rows under one resource_type must not
// disagree about what happened.
//
// It stays a separate handler rather than delegating to the HA one: this route
// is gated on manage:drs and that one on manage:ha, so delegating would quietly
// change which permission the endpoint requires.
//
// The snapshot read costs one extra round trip to PVE. That is affordable on an
// operator-initiated delete, and it is the only evidence the prior state ever
// leaves behind.
func (h *DRSHandler) DeleteHARule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "drs", clusterID); err != nil {
		return err
	}

	// Read raw, unlike the HA tab's decodePathParam: the DRS client sends the
	// rule name unencoded, so decoding here would corrupt a name containing a
	// literal percent. What matters for the lookup below is that the same
	// string reaches both findHARule and DeleteHARule, and it does.
	ruleName := c.Params("rule_name")
	if ruleName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "rule_name is required")
	}

	client, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	snapshot, snapErr := findHARule(c.Context(), client, ruleName)
	if err := client.DeleteHARule(c.Context(), ruleName); err != nil {
		return mapProxmoxError(err)
	}

	action, priorStateUnknown := classifyHARuleDelete(snapshot, snapErr)
	if priorStateUnknown {
		slog.Warn("DRS HA rule delete: could not read the rules list to snapshot the rule; auditing without its detail",
			"cluster_id", clusterID, "rule", ruleName, "error", snapErr)
	}
	details := haRuleDeleteDetails(c.Context(), h.queries, clusterID, ruleName, snapshot, priorStateUnknown)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "ha_rule", ruleName, action, details)

	return c.JSON(fiber.Map{"status": "ok"})
}
