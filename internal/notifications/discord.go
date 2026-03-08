package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type discordConfig struct {
	WebhookURL      string `json:"webhook_url"`
	Username        string `json:"username"`
	AvatarURL       string `json:"avatar_url"`
	MessageTemplate string `json:"message_template"`
}

// DiscordDispatcher sends alert notifications via Discord webhook.
type DiscordDispatcher struct{}

func (d *DiscordDispatcher) Type() string { return "discord" }

func (d *DiscordDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg discordConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse discord config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("discord config: webhook_url required")
	}

	content := ""
	if cfg.MessageTemplate != "" {
		if rendered, err := renderTemplate(cfg.MessageTemplate, payload); err == nil {
			content = rendered
		}
	}

	body := map[string]interface{}{
		"content": content,
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("%s %s", severityEmoji(payload.Severity), payload.RuleName),
				"description": fmt.Sprintf("**%s** alert on **%s**", strings.ToUpper(payload.Severity), payload.ResourceName),
				"color":       severityColor(payload.Severity),
				"fields": []map[string]interface{}{
					{"name": "Metric", "value": payload.Metric, "inline": true},
					{"name": "Current Value", "value": fmt.Sprintf("%.2f", payload.CurrentValue), "inline": true},
					{"name": "Threshold", "value": fmt.Sprintf("%s %.2f", payload.Operator, payload.Threshold), "inline": true},
					{"name": "State", "value": payload.State, "inline": true},
				},
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	if cfg.Username != "" {
		body["username"] = cfg.Username
	}
	if cfg.AvatarURL != "" {
		body["avatar_url"] = cfg.AvatarURL
	}

	return postJSON(ctx, cfg.WebhookURL, body)
}
