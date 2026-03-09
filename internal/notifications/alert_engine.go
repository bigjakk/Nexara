package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// validMetrics defines the supported metric names for alert rules.
var validMetrics = map[string]bool{
	"cpu_usage":   true,
	"mem_percent": true,
	"disk_read":   true,
	"disk_write":  true,
	"net_in":      true,
	"net_out":     true,
}

// ValidMetric returns true if the metric name is supported.
func ValidMetric(m string) bool {
	return validMetrics[m]
}

// Engine evaluates alert rules against incoming metrics.
type Engine struct {
	queries       *db.Queries
	logger        *slog.Logger
	eventPub      *events.Publisher
	registry      *Registry
	encryptionKey string
}

// NewEngine creates a new alert engine.
func NewEngine(queries *db.Queries, logger *slog.Logger, eventPub *events.Publisher, registry *Registry, encryptionKey string) *Engine {
	return &Engine{
		queries:       queries,
		logger:        logger,
		eventPub:      eventPub,
		registry:      registry,
		encryptionKey: encryptionKey,
	}
}

// Evaluate runs a full evaluation cycle: load rules, check metrics, fire/resolve alerts.
func (e *Engine) Evaluate(ctx context.Context) {
	rules, err := e.queries.ListEnabledAlertRules(ctx)
	if err != nil {
		e.logger.Error("failed to list enabled alert rules", "error", err)
		return
	}

	if len(rules) == 0 {
		return
	}

	// Load active maintenance windows for suppression.
	windows, err := e.queries.ListActiveMaintenanceWindows(ctx)
	if err != nil {
		e.logger.Warn("failed to list maintenance windows", "error", err)
		// Continue — don't skip alerts because of a window lookup failure.
	}

	for _, rule := range rules {
		if err := e.evaluateRule(ctx, rule, windows); err != nil {
			e.logger.Error("rule evaluation failed",
				"rule_id", rule.ID, "rule_name", rule.Name, "error", err)
		}
	}

	// Check escalations for firing alerts that remain unacknowledged.
	e.checkEscalations(ctx)
}

// evaluateRule evaluates a single rule against its target resources.
func (e *Engine) evaluateRule(ctx context.Context, rule db.AlertRule, windows []db.MaintenanceWindow) error {
	switch rule.ScopeType {
	case "node":
		if !rule.NodeID.Valid {
			return fmt.Errorf("node rule %s has no node_id", rule.ID)
		}
		nodeID := uuidFromPgtype(rule.NodeID)
		if e.isInMaintenanceWindow(rule.ClusterID, rule.NodeID, windows) {
			return nil
		}
		return e.evaluateNodeRule(ctx, rule, nodeID)

	case "vm":
		if !rule.VmID.Valid {
			return fmt.Errorf("vm rule %s has no vm_id", rule.ID)
		}
		vmID := uuidFromPgtype(rule.VmID)
		if e.isInMaintenanceWindow(rule.ClusterID, pgtype.UUID{}, windows) {
			return nil
		}
		return e.evaluateVMRule(ctx, rule, vmID)

	case "cluster":
		if !rule.ClusterID.Valid {
			return fmt.Errorf("cluster rule %s has no cluster_id", rule.ID)
		}
		clusterID := uuidFromPgtype(rule.ClusterID)
		// Evaluate for each node in the cluster.
		nodes, err := e.queries.ListNodesByCluster(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("list nodes: %w", err)
		}
		for _, node := range nodes {
			if e.isInMaintenanceWindow(rule.ClusterID, pgtype.UUID{Bytes: node.ID, Valid: true}, windows) {
				continue
			}
			if err := e.evaluateNodeRule(ctx, rule, node.ID); err != nil {
				e.logger.Warn("node evaluation failed",
					"rule_id", rule.ID, "node_id", node.ID, "error", err)
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported scope_type: %s", rule.ScopeType)
	}
}

// evaluateNodeRule evaluates a rule against a single node's metrics.
func (e *Engine) evaluateNodeRule(ctx context.Context, rule db.AlertRule, nodeID uuid.UUID) error {
	since := time.Now().Add(-time.Duration(rule.DurationSeconds) * time.Second)
	metrics, err := e.queries.GetNodeRecentMetrics(ctx, db.GetNodeRecentMetricsParams{
		NodeID: nodeID,
		Time:   since,
	})
	if err != nil {
		return fmt.Errorf("get node metrics: %w", err)
	}

	if len(metrics) == 0 {
		return nil // No data — can't evaluate.
	}

	// Check if ALL data points within the window satisfy the condition.
	allMatch := true
	var latestValue float64
	for i, m := range metrics {
		val := extractNodeMetric(m, rule.Metric)
		if i == 0 {
			latestValue = val
		}
		if !compareValue(val, rule.Operator, rule.Threshold) {
			allMatch = false
			break
		}
	}

	// Get resource name for the alert message.
	resourceName := nodeID.String()
	node, err := e.queries.GetNode(ctx, nodeID)
	if err == nil {
		resourceName = node.Name
	}

	nodeIDPg := pgtype.UUID{Bytes: nodeID, Valid: true}
	return e.handleRuleResult(ctx, rule, allMatch, latestValue, resourceName, nodeIDPg, pgtype.UUID{})
}

// evaluateVMRule evaluates a rule against a single VM's metrics.
func (e *Engine) evaluateVMRule(ctx context.Context, rule db.AlertRule, vmID uuid.UUID) error {
	since := time.Now().Add(-time.Duration(rule.DurationSeconds) * time.Second)
	metrics, err := e.queries.GetVMRecentMetrics(ctx, db.GetVMRecentMetricsParams{
		VmID: vmID,
		Time: since,
	})
	if err != nil {
		return fmt.Errorf("get vm metrics: %w", err)
	}

	if len(metrics) == 0 {
		return nil
	}

	allMatch := true
	var latestValue float64
	for i, m := range metrics {
		val := extractVMMetric(m, rule.Metric)
		if i == 0 {
			latestValue = val
		}
		if !compareValue(val, rule.Operator, rule.Threshold) {
			allMatch = false
			break
		}
	}

	resourceName := vmID.String()
	vm, err := e.queries.GetVM(ctx, vmID)
	if err == nil {
		resourceName = vm.Name
	}

	vmIDPg := pgtype.UUID{Bytes: vmID, Valid: true}
	return e.handleRuleResult(ctx, rule, allMatch, latestValue, resourceName, pgtype.UUID{}, vmIDPg)
}

// handleRuleResult creates, transitions, or auto-resolves alerts based on evaluation.
func (e *Engine) handleRuleResult(ctx context.Context, rule db.AlertRule, conditionMet bool, value float64, resourceName string, nodeID, vmID pgtype.UUID) error {
	// Find existing active alert for this rule + resource.
	existing, err := e.queries.GetLatestAlertForRule(ctx, db.GetLatestAlertForRuleParams{
		RuleID: rule.ID,
		NodeID: uuidFromPgtype(nodeID),
		VmID:   uuidFromPgtype(vmID),
	})
	hasExisting := err == nil && (existing.State == "pending" || existing.State == "firing")

	if conditionMet {
		if hasExisting {
			// Already tracking — check if pending needs transition to firing.
			if existing.State == "pending" {
				elapsed := time.Since(existing.PendingAt)
				if elapsed >= time.Duration(rule.DurationSeconds)*time.Second {
					if err := e.queries.TransitionAlertToFiring(ctx, existing.ID); err != nil {
						return fmt.Errorf("transition to firing: %w", err)
					}
					e.logger.Info("alert fired",
						"rule", rule.Name, "resource", resourceName, "value", value)
					e.publishAlertEvent(ctx, rule, existing.ID, "fired")
					e.dispatchForRule(ctx, rule, existing.ID, value, resourceName, "firing", 0)
				}
			}
			// Already firing — nothing to do (deduplication).
			return nil
		}

		// Check deduplication cooldown.
		if err == nil && existing.State == "resolved" {
			if time.Since(existing.CreatedAt) < time.Duration(rule.CooldownSeconds)*time.Second {
				return nil // Within cooldown — suppress.
			}
		}

		// Create new pending alert.
		msg := fmt.Sprintf("%s %s %s %.2f on %s (current: %.2f)",
			rule.Metric, rule.Operator, formatThreshold(rule.Threshold), rule.Threshold, resourceName, value)

		// Get the first escalation channel if any.
		var channelID pgtype.UUID
		chain := parseEscalationChain(rule.EscalationChain)
		if len(chain) > 0 {
			channelID = pgtype.UUID{Bytes: chain[0].ChannelID, Valid: true}
		}

		alert, insertErr := e.queries.InsertAlertHistory(ctx, db.InsertAlertHistoryParams{
			RuleID:          rule.ID,
			State:           "pending",
			Severity:        rule.Severity,
			ClusterID:       rule.ClusterID,
			NodeID:          nodeID,
			VmID:            vmID,
			ResourceName:    resourceName,
			Metric:          rule.Metric,
			CurrentValue:    value,
			Threshold:       rule.Threshold,
			Message:         msg,
			EscalationLevel: 0,
			ChannelID:       channelID,
		})
		if insertErr != nil {
			return fmt.Errorf("insert alert: %w", insertErr)
		}

		e.logger.Info("alert pending",
			"rule", rule.Name, "resource", resourceName, "alert_id", alert.ID)

		// If duration is 0, immediately fire.
		if rule.DurationSeconds == 0 {
			if err := e.queries.TransitionAlertToFiring(ctx, alert.ID); err != nil {
				return fmt.Errorf("immediate fire: %w", err)
			}
			e.publishAlertEvent(ctx, rule, alert.ID, "fired")
			e.dispatchForRule(ctx, rule, alert.ID, value, resourceName, "firing", 0)
		}

		return nil
	}

	// Condition NOT met — auto-resolve any active alerts.
	if hasExisting {
		if err := e.queries.AutoResolveAlert(ctx, existing.ID); err != nil {
			return fmt.Errorf("auto-resolve: %w", err)
		}
		e.logger.Info("alert auto-resolved",
			"rule", rule.Name, "resource", resourceName, "alert_id", existing.ID)
		e.publishAlertEvent(ctx, rule, existing.ID, "resolved")
		e.dispatchForRule(ctx, rule, existing.ID, value, resourceName, "resolved", int(existing.EscalationLevel))
	}

	return nil
}

// checkEscalations looks for firing alerts that need escalation.
func (e *Engine) checkEscalations(ctx context.Context) {
	alerts, err := e.queries.ListFiringUnacknowledged(ctx)
	if err != nil {
		e.logger.Error("failed to list firing unacknowledged alerts", "error", err)
		return
	}

	for _, alert := range alerts {
		rule, err := e.queries.GetAlertRule(ctx, alert.RuleID)
		if err != nil {
			continue
		}

		chain := parseEscalationChain(rule.EscalationChain)
		if len(chain) == 0 {
			continue
		}

		nextLevel := int(alert.EscalationLevel) + 1
		if nextLevel >= len(chain) {
			continue // Already at max escalation.
		}

		step := chain[nextLevel]
		if !alert.FiredAt.Valid {
			continue
		}

		elapsed := time.Since(alert.FiredAt.Time)
		if elapsed < time.Duration(step.DelayMinutes)*time.Minute {
			continue // Not time to escalate yet.
		}

		channelID := pgtype.UUID{Bytes: step.ChannelID, Valid: true}
		if err := e.queries.UpdateAlertEscalation(ctx, db.UpdateAlertEscalationParams{
			ID:              alert.ID,
			EscalationLevel: int32(nextLevel),
			ChannelID:       channelID,
		}); err != nil {
			e.logger.Error("failed to escalate alert", "alert_id", alert.ID, "error", err)
			continue
		}

		e.logger.Info("alert escalated",
			"alert_id", alert.ID, "level", nextLevel, "channel_id", step.ChannelID)
		e.publishAlertEvent(ctx, rule, alert.ID, "escalated")
		e.dispatchToChannel(ctx, rule, alert.ID, step.ChannelID, alert.CurrentValue, alert.ResourceName, "escalated", nextLevel)
	}
}

// isInMaintenanceWindow checks if a resource is currently in a maintenance window.
func (e *Engine) isInMaintenanceWindow(clusterID pgtype.UUID, nodeID pgtype.UUID, windows []db.MaintenanceWindow) bool {
	for _, w := range windows {
		// Match cluster.
		if clusterID.Valid && w.ClusterID == uuidFromPgtype(clusterID) {
			// If window has a specific node, only match that node.
			if w.NodeID.Valid {
				if nodeID.Valid && uuidFromPgtype(nodeID) == uuidFromPgtype(w.NodeID) {
					return true
				}
			} else {
				// Window covers entire cluster.
				return true
			}
		}
	}
	return false
}

func (e *Engine) publishAlertEvent(ctx context.Context, rule db.AlertRule, alertID uuid.UUID, action string) {
	if e.eventPub == nil {
		return
	}
	clusterID := ""
	if rule.ClusterID.Valid {
		clusterID = uuidFromPgtype(rule.ClusterID).String()
	}
	e.eventPub.ClusterEvent(ctx, clusterID, events.KindAlertFired, "alert", alertID.String(), action)
}

// --- Notification Dispatch ---

// dispatchForRule dispatches a notification using the rule's escalation chain at the given level.
func (e *Engine) dispatchForRule(ctx context.Context, rule db.AlertRule, alertID uuid.UUID, value float64, resourceName, state string, escalationLevel int) {
	chain := parseEscalationChain(rule.EscalationChain)
	if len(chain) == 0 {
		return
	}
	idx := escalationLevel
	if idx >= len(chain) {
		idx = len(chain) - 1
	}
	e.dispatchToChannel(ctx, rule, alertID, chain[idx].ChannelID, value, resourceName, state, escalationLevel)
}

// dispatchToChannel dispatches a notification to a specific channel.
func (e *Engine) dispatchToChannel(ctx context.Context, rule db.AlertRule, alertID uuid.UUID, channelID uuid.UUID, value float64, resourceName, state string, escalationLevel int) {
	if e.registry == nil || channelID == uuid.Nil {
		return
	}

	channel, err := e.queries.GetNotificationChannelEnabled(ctx, channelID)
	if err != nil {
		e.logger.Warn("channel not found or disabled for dispatch", "channel_id", channelID, "error", err)
		return
	}

	configJSON, err := crypto.Decrypt(channel.ConfigEncrypted, e.encryptionKey)
	if err != nil {
		e.logger.Error("failed to decrypt channel config", "channel_id", channelID, "error", err)
		return
	}

	dispatcher, ok := e.registry.Get(channel.ChannelType)
	if !ok {
		e.logger.Warn("no dispatcher for channel type", "type", channel.ChannelType)
		return
	}

	payload := AlertPayload{
		RuleName:        rule.Name,
		RuleID:          rule.ID.String(),
		Severity:        rule.Severity,
		State:           state,
		Metric:          rule.Metric,
		Operator:        rule.Operator,
		Threshold:       rule.Threshold,
		CurrentValue:    value,
		ResourceName:    resourceName,
		NodeName:        resourceName,
		FiredAt:         time.Now().UTC().Format(time.RFC3339),
		EscalationLevel: escalationLevel,
	}
	if rule.ClusterID.Valid {
		payload.ClusterID = uuidFromPgtype(rule.ClusterID).String()
	}

	// Render custom template if set on rule.
	if rule.MessageTemplate != "" {
		if rendered, err := renderTemplate(rule.MessageTemplate, payload); err == nil {
			payload.Message = rendered
		}
	}

	// Dispatch in a goroutine to avoid blocking the evaluation loop.
	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := dispatcher.Send(sendCtx, json.RawMessage(configJSON), payload); err != nil {
			e.logger.Error("notification dispatch failed",
				"channel_id", channelID, "channel_type", channel.ChannelType, "error", err)
		} else {
			e.logger.Info("notification dispatched",
				"channel_id", channelID, "channel_type", channel.ChannelType, "state", state)
			_ = e.queries.MarkAlertNotificationSent(sendCtx, alertID)
		}
	}()
}

// --- Helpers ---

// escalationStep represents one step in an escalation chain.
type escalationStep struct {
	ChannelID    uuid.UUID `json:"channel_id"`
	DelayMinutes int       `json:"delay_minutes"`
}

func parseEscalationChain(data json.RawMessage) []escalationStep {
	var chain []escalationStep
	if len(data) == 0 {
		return nil
	}
	_ = json.Unmarshal(data, &chain)
	return chain
}

func formatThreshold(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// compareValue evaluates: value <op> threshold.
func compareValue(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return false
	}
}

// extractNodeMetric extracts the named metric from a node metrics row.
func extractNodeMetric(m db.GetNodeRecentMetricsRow, metric string) float64 {
	switch metric {
	case "cpu_usage":
		return m.CpuUsage
	case "mem_percent":
		if m.MemTotal == 0 {
			return 0
		}
		return float64(m.MemUsed) / float64(m.MemTotal) * 100
	case "disk_read":
		return float64(m.DiskRead)
	case "disk_write":
		return float64(m.DiskWrite)
	case "net_in":
		return float64(m.NetIn)
	case "net_out":
		return float64(m.NetOut)
	default:
		return 0
	}
}

// extractVMMetric extracts the named metric from a VM metrics row.
func extractVMMetric(m db.GetVMRecentMetricsRow, metric string) float64 {
	switch metric {
	case "cpu_usage":
		return m.CpuUsage
	case "mem_percent":
		if m.MemTotal == 0 {
			return 0
		}
		return float64(m.MemUsed) / float64(m.MemTotal) * 100
	case "disk_read":
		return float64(m.DiskRead)
	case "disk_write":
		return float64(m.DiskWrite)
	case "net_in":
		return float64(m.NetIn)
	case "net_out":
		return float64(m.NetOut)
	default:
		return 0
	}
}

func uuidFromPgtype(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	id, _ := uuid.FromBytes(p.Bytes[:])
	return id
}
