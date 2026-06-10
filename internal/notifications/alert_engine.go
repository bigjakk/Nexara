package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/safeconv"
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
	rateLimiter   *channelRateLimiter
	// shutdownCtx is the parent process context. Detached dispatch
	// goroutines derive their own timeouts from it so a SIGTERM cancels
	// in-flight retries before the pgx pool is torn down — without this,
	// a retry that finishes after pool shutdown would lose its DLQ row.
	shutdownCtx context.Context
}

// NewEngine creates a new alert engine. shutdownCtx is the per-process
// cancellation context (cancel on SIGTERM); pass nil for tests.
func NewEngine(shutdownCtx context.Context, queries *db.Queries, logger *slog.Logger, eventPub *events.Publisher, registry *Registry, encryptionKey string) *Engine {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	return &Engine{
		queries:       queries,
		logger:        logger,
		eventPub:      eventPub,
		registry:      registry,
		encryptionKey: encryptionKey,
		rateLimiter:   newChannelRateLimiter(),
		shutdownCtx:   shutdownCtx,
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
	// Find existing active alert for this rule + resource. The scope params
	// pass through as pgtype.UUID so an absent dimension reaches SQL as NULL
	// (uuid.Nil would match nothing — see the query comment).
	existing, err := e.queries.GetLatestAlertForRule(ctx, db.GetLatestAlertForRuleParams{
		RuleID: rule.ID,
		NodeID: nodeID,
		VmID:   vmID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// A transient lookup failure must not be treated as "no existing
		// alert" — that would insert a duplicate row every tick.
		return fmt.Errorf("lookup existing alert: %w", err)
	}
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
		// nextLevel is an escalation chain index (always small), but guard for gosec G115.
		escalationLevel := safeconv.Int32(nextLevel)
		if err := e.queries.UpdateAlertEscalation(ctx, db.UpdateAlertEscalationParams{
			ID:              alert.ID,
			EscalationLevel: escalationLevel,
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
//
// Three layers protect downstream services:
//
//  1. The per-channel rate-limiter (token bucket keyed by channel_id) drops
//     a flapping rule's notifications into the DLQ as `rate_limited` rather
//     than drowning Slack/PagerDuty.
//  2. The retry/backoff schedule wraps the actual Send() in 3 attempts with
//     1s/4s waits — absorbs transient endpoint outages.
//  3. On final failure (or rate-limit), a row is written to notification_dlq
//     so the operator can review and replay from the UI.
//
// Errors are deliberately swallowed inside the goroutine: an alert evaluation
// tick must not block on a dead Slack endpoint, and the DLQ row is the
// durable record of the failure.
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
		// Persistent failure — surface in DLQ so the operator can rotate the key.
		e.writeDLQ(ctx, channel, rule, alertID, AlertPayload{}, "decrypt channel config: "+err.Error(), 0, "config_error")
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
		FiredAt:         time.Now().UTC().Format(time.RFC3339Nano),
		EscalationLevel: escalationLevel,
	}
	if rule.ClusterID.Valid {
		payload.ClusterID = uuidFromPgtype(rule.ClusterID).String()
	}

	if rule.MessageTemplate != "" {
		if rendered, err := renderTemplate(rule.MessageTemplate, payload); err == nil {
			payload.Message = rendered
		}
	}

	// Per-channel rate-limit: a flapping rule shouldn't drown the channel.
	// Refused dispatches are recorded in the DLQ so the operator sees what
	// got blocked rather than the throttling being silent.
	if !e.rateLimiter.Allow(channelID) {
		e.logger.Warn("notification rate-limited",
			"channel_id", channelID, "channel_type", channel.ChannelType, "rule", rule.Name)
		e.writeDLQ(ctx, channel, rule, alertID, payload,
			"channel rate limit exceeded; flapping rule throttled",
			0, "rate_limited")
		return
	}

	// Retry/backoff + DLQ on final failure happen in a detached goroutine so
	// the evaluation tick is never blocked on a dead endpoint.
	go e.sendWithRetry(channel, rule, alertID, payload, json.RawMessage(configJSON), dispatcher)
}

// sendWithRetry runs the retry schedule for a single dispatch and persists
// either the success (MarkAlertNotificationSent) or the final failure (DLQ
// row). Splits out from dispatchToChannel for unit testing — the goroutine
// is the boundary that makes the goroutine-internal cleanup ctx loadbearing.
func (e *Engine) sendWithRetry(
	channel db.NotificationChannel,
	rule db.AlertRule,
	alertID uuid.UUID,
	payload AlertPayload,
	configJSON json.RawMessage,
	dispatcher Dispatcher,
) {
	// Derive from shutdownCtx so SIGTERM aborts in-flight retries before
	// the pgx pool is torn down. The 60-second cap is well above the
	// 5-second worst-case retry window and dispatcher HTTP timeouts.
	sendCtx, cancel := context.WithTimeout(e.shutdownCtx, 60*time.Second)
	defer cancel()

	attempts, err := retryableSend(sendCtx, func(c context.Context) error {
		return dispatcher.Send(c, configJSON, payload)
	}, RetrySchedule)

	if err == nil {
		e.logger.Info("notification dispatched",
			"channel_id", channel.ID, "channel_type", channel.ChannelType,
			"state", payload.State, "attempts", attempts)
		if alertID != uuid.Nil {
			_ = e.queries.MarkAlertNotificationSent(sendCtx, alertID)
		}
		return
	}

	e.logger.Error("notification dispatch failed",
		"channel_id", channel.ID, "channel_type", channel.ChannelType,
		"attempts", attempts, "error", err)
	e.writeDLQ(sendCtx, channel, rule, alertID, payload, err.Error(), attempts, "send_failed")
}

// writeDLQ persists a failure record. Errors during the DLQ write itself are
// logged loudly — at that point we have nowhere else to put the failure.
//
// cluster_id is denormalised from the rule (or directly via the rule's
// cluster_id) so the API layer can filter DLQ entries by the caller's
// accessible clusters even after the rule/alert FKs are SET NULL on delete.
func (e *Engine) writeDLQ(
	ctx context.Context,
	channel db.NotificationChannel,
	rule db.AlertRule,
	alertID uuid.UUID,
	payload AlertPayload,
	lastErr string,
	attempts int,
	failureKind string,
) {
	state := "pending"
	if failureKind == "rate_limited" {
		state = "rate_limited"
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		e.logger.Error("failed to marshal DLQ payload", "error", err)
		payloadJSON = []byte("{}")
	}

	channelIDPg := pgtype.UUID{}
	if channel.ID != uuid.Nil {
		channelIDPg = pgtype.UUID{Bytes: channel.ID, Valid: true}
	}
	alertIDPg := pgtype.UUID{}
	if alertID != uuid.Nil {
		alertIDPg = pgtype.UUID{Bytes: alertID, Valid: true}
	}
	ruleIDPg := pgtype.UUID{}
	if rule.ID != uuid.Nil {
		ruleIDPg = pgtype.UUID{Bytes: rule.ID, Valid: true}
	}
	// rule.ClusterID is already pgtype.UUID; carry through verbatim so a
	// global (no-cluster) rule lands as NULL and a cluster-scoped rule
	// lands with the correct cluster reference.
	clusterIDPg := rule.ClusterID

	if _, err := e.queries.InsertNotificationDLQ(ctx, db.InsertNotificationDLQParams{
		ChannelID:    channelIDPg,
		ChannelType:  channel.ChannelType,
		ChannelName:  channel.Name,
		AlertID:      alertIDPg,
		RuleID:       ruleIDPg,
		ClusterID:    clusterIDPg,
		Payload:      payloadJSON,
		LastError:    truncateError(lastErr),
		AttemptCount: safeconv.Int32(attempts),
		State:        state,
		FailureKind:  failureKind,
	}); err != nil {
		e.logger.Error("failed to write DLQ entry",
			"channel_id", channel.ID, "channel_type", channel.ChannelType, "error", err)
	}
}

// truncateError caps a stored error message to keep DLQ rows compact.
// 4KB is enough for stack traces with surrounding context but bounds the
// blast radius of an upstream service that returns multi-MB error bodies.
//
// Rewinds to the last UTF-8 rune boundary before the cut so we never
// produce a half-encoded rune — JSONB / TEXT in PostgreSQL would reject
// the insert with `invalid byte sequence for encoding "UTF8"` and we'd
// silently lose the failure record.
func truncateError(s string) string {
	const maxErrLen = 4096
	const marker = "…[truncated]"
	if len(s) <= maxErrLen {
		return s
	}
	end := maxErrLen - len(marker)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + marker
}

// ReplayDLQ retries a single DLQ entry. Looks up the channel referenced by
// the DLQ row (or returns a typed error if it's been deleted), re-decrypts
// the config, and runs the dispatcher with the full retry schedule. On
// success the row is marked `resolved`; on failure the row's attempt
// counter and last_error are updated and the row stays `pending`.
//
// Bypasses the per-channel rate limiter: replay is an explicit operator
// action and shouldn't get throttled by a still-flapping rule.
func (e *Engine) ReplayDLQ(ctx context.Context, dlqID uuid.UUID) error {
	if e.queries == nil || e.registry == nil {
		return fmt.Errorf("alert engine not configured for replay")
	}

	row, err := e.queries.GetNotificationDLQ(ctx, dlqID)
	if err != nil {
		return fmt.Errorf("get DLQ entry: %w", err)
	}
	if row.State == "resolved" || row.State == "dismissed" {
		return fmt.Errorf("DLQ entry already %s", row.State)
	}
	if !row.ChannelID.Valid {
		return fmt.Errorf("channel deleted; cannot replay")
	}

	channelID := uuidFromPgtype(row.ChannelID)
	channel, err := e.queries.GetNotificationChannelEnabled(ctx, channelID)
	if err != nil {
		return fmt.Errorf("channel %s not found or disabled: %w", channelID, err)
	}

	configJSON, err := crypto.Decrypt(channel.ConfigEncrypted, e.encryptionKey)
	if err != nil {
		return fmt.Errorf("decrypt channel config: %w", err)
	}

	dispatcher, ok := e.registry.Get(channel.ChannelType)
	if !ok {
		return fmt.Errorf("no dispatcher registered for channel type %q", channel.ChannelType)
	}

	var payload AlertPayload
	if perr := json.Unmarshal(row.Payload, &payload); perr != nil {
		return fmt.Errorf("unmarshal stored payload: %w", perr)
	}

	// Re-derive trusted rule fields from the live alert_rules row when
	// available. Defends against DLQ-row tampering: a writer with raw DB
	// access cannot rewrite the payload to inject a different rule_name,
	// severity, threshold, etc. into outbound notifications. Fields that
	// have no canonical source (current_value, resource_name, fired_at)
	// remain from the stored payload. If the rule has been deleted, the
	// stored payload is the fallback — by definition the only record left.
	if row.RuleID.Valid {
		ruleID := uuidFromPgtype(row.RuleID)
		if liveRule, rerr := e.queries.GetAlertRule(ctx, ruleID); rerr == nil {
			payload.RuleID = liveRule.ID.String()
			payload.RuleName = liveRule.Name
			payload.Severity = liveRule.Severity
			payload.Metric = liveRule.Metric
			payload.Operator = liveRule.Operator
			payload.Threshold = liveRule.Threshold
			if liveRule.ClusterID.Valid {
				payload.ClusterID = uuidFromPgtype(liveRule.ClusterID).String()
			} else {
				payload.ClusterID = ""
			}
		}
	}
	// Always refresh fired_at to the actual replay time so the downstream
	// service sees a current timestamp rather than the original failure's
	// stale one.
	payload.FiredAt = time.Now().UTC().Format(time.RFC3339Nano)

	attempts, sendErr := retryableSend(ctx, func(c context.Context) error {
		return dispatcher.Send(c, json.RawMessage(configJSON), payload)
	}, RetrySchedule)

	totalAttempts := safeconv.Int32(int(row.AttemptCount) + attempts)
	if sendErr != nil {
		_ = e.queries.UpdateNotificationDLQState(ctx, db.UpdateNotificationDLQStateParams{
			ID:           dlqID,
			State:        "pending",
			LastError:    truncateError(sendErr.Error()),
			AttemptCount: totalAttempts,
		})
		return fmt.Errorf("replay attempts=%d: %w", attempts, sendErr)
	}

	_ = e.queries.MarkNotificationDLQResolved(ctx, dlqID)
	if row.AlertID.Valid {
		_ = e.queries.MarkAlertNotificationSent(ctx, uuidFromPgtype(row.AlertID))
	}
	e.logger.Info("DLQ entry replayed",
		"dlq_id", dlqID, "channel_id", channelID, "attempts", attempts)
	return nil
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
