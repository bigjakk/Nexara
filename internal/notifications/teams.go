package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type teamsConfig struct {
	WebhookURL      string `json:"webhook_url"`
	MessageTemplate string `json:"message_template"`
}

// TeamsDispatcher sends alert notifications via Microsoft Teams webhook.
type TeamsDispatcher struct{}

func (d *TeamsDispatcher) Type() string { return "teams" }

func (d *TeamsDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg teamsConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse teams config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("teams config: webhook_url required")
	}

	return postJSON(ctx, cfg.WebhookURL, teamsBody(cfg, payload))
}

// teamsBody is the Teams webhook request.
func teamsBody(cfg teamsConfig, payload AlertPayload) map[string]interface{} {
	text := chatText(cfg.MessageTemplate, payload)

	color := fmt.Sprintf("%06x", severityColor(payload.Severity))

	body := map[string]interface{}{
		"@type":      "MessageCard",
		"@context":   "http://schema.org/extensions",
		"themeColor": color,
		"summary":    fmt.Sprintf("%s alert: %s", strings.ToUpper(payload.Severity), payload.RuleName),
		"sections": []map[string]interface{}{
			{
				"activityTitle": fmt.Sprintf("%s %s", severityEmoji(payload.Severity), payload.RuleName),
				"facts": []map[string]interface{}{
					{"name": "Resource", "value": payload.ResourceName},
					{"name": "Severity", "value": strings.ToUpper(payload.Severity)},
					{"name": "Metric", "value": payload.Metric},
					{"name": "Current Value", "value": fmt.Sprintf("%.2f", payload.CurrentValue)},
					{"name": "Threshold", "value": fmt.Sprintf("%s %.2f", payload.Operator, payload.Threshold)},
					{"name": "State", "value": payload.State},
				},
				"text": text,
			},
		},
	}

	return body
}
