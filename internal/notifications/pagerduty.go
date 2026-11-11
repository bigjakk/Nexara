package notifications

import (
	"context"
	"encoding/json"
	"fmt"
)

type pagerdutyConfig struct {
	RoutingKey      string            `json:"routing_key"`
	SeverityMapping map[string]string `json:"severity_mapping"`
}

// PagerDutyDispatcher sends alert notifications via PagerDuty Events API v2.
type PagerDutyDispatcher struct{}

func (d *PagerDutyDispatcher) Type() string { return "pagerduty" }

// pagerdutyDedupKey builds a stable dedup_key for PagerDuty Events API v2.
// PagerDuty groups events sharing a dedup_key into one incident; trigger and
// resolve events for the same alert must share a key for the resolve to land
// on the right incident.
//
// An alert's own id is that key. The resource name is no stable stand-in: a
// UPS, task-failure or Veeam rule's is a sentence carrying live readings, and
// an escalation sends the one stored at creation, so its trigger, escalation
// and resolve each opened or missed an incident of their own.
//
// A notification with no alert (a channel test, a CVE notice, a dead letter
// stored before alert_id) keeps the older (rule, cluster, resource) key, which
// still gives each resource of one rule its own incident.
func pagerdutyDedupKey(payload AlertPayload) string {
	if payload.AlertID != "" {
		return "nexara-alert-" + payload.AlertID
	}
	cluster := payload.ClusterID
	if cluster == "" {
		cluster = "global"
	}
	return fmt.Sprintf("nexara-%s-%s-%s", payload.RuleID, cluster, payload.ResourceName)
}

func (d *PagerDutyDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg pagerdutyConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse pagerduty config: %w", err)
	}
	if cfg.RoutingKey == "" {
		return fmt.Errorf("pagerduty config: routing_key required")
	}

	pdSeverity := payload.Severity
	if mapped, ok := cfg.SeverityMapping[payload.Severity]; ok {
		pdSeverity = mapped
	}

	eventAction := "trigger"
	if payload.State == "resolved" {
		eventAction = "resolve"
	}

	body := map[string]interface{}{
		"routing_key":  cfg.RoutingKey,
		"event_action": eventAction,
		"dedup_key":    pagerdutyDedupKey(payload),
		"payload": map[string]interface{}{
			"summary":   fmt.Sprintf("%s: %s on %s", payload.Severity, payload.RuleName, payload.ResourceName),
			"severity":  pdSeverity,
			"source":    payload.ResourceName,
			"component": payload.Metric,
			"custom_details": map[string]interface{}{
				"metric":        payload.Metric,
				"current_value": payload.CurrentValue,
				"threshold":     payload.Threshold,
				"operator":      payload.Operator,
				"state":         payload.State,
			},
		},
	}

	return postJSON(ctx, "https://events.pagerduty.com/v2/enqueue", body)
}
