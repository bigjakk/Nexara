package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type slackConfig struct {
	WebhookURL      string `json:"webhook_url"`
	Channel         string `json:"channel"`
	Username        string `json:"username"`
	IconEmoji       string `json:"icon_emoji"`
	MessageTemplate string `json:"message_template"`
}

// SlackDispatcher sends alert notifications via Slack webhook.
type SlackDispatcher struct{}

func (d *SlackDispatcher) Type() string { return "slack" }

func (d *SlackDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg slackConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse slack config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack config: webhook_url required")
	}

	text := defaultMessage(payload)
	if cfg.MessageTemplate != "" {
		if rendered, err := renderTemplate(cfg.MessageTemplate, payload); err == nil {
			text = rendered
		}
	}

	color := fmt.Sprintf("#%06x", severityColor(payload.Severity))

	body := map[string]interface{}{
		"text": fmt.Sprintf("%s %s", severityEmoji(payload.Severity), text),
		"attachments": []map[string]interface{}{
			{
				"color": color,
				"fields": []map[string]interface{}{
					{"title": "Rule", "value": payload.RuleName, "short": true},
					{"title": "Severity", "value": strings.ToUpper(payload.Severity), "short": true},
					{"title": "Resource", "value": payload.ResourceName, "short": true},
					{"title": "Metric", "value": payload.Metric, "short": true},
					{"title": "Value", "value": fmt.Sprintf("%.2f", payload.CurrentValue), "short": true},
					{"title": "Threshold", "value": fmt.Sprintf("%s %.2f", payload.Operator, payload.Threshold), "short": true},
				},
			},
		},
	}
	if cfg.Channel != "" {
		body["channel"] = cfg.Channel
	}
	if cfg.Username != "" {
		body["username"] = cfg.Username
	}
	if cfg.IconEmoji != "" {
		body["icon_emoji"] = cfg.IconEmoji
	}

	return postJSON(ctx, cfg.WebhookURL, body)
}

// postJSON sends a JSON POST request using a safe HTTP client that blocks
// private/loopback IPs and does not follow redirects.
func postJSON(ctx context.Context, targetURL string, body interface{}) error {
	if err := validateExternalURL(ctx, targetURL); err != nil {
		return fmt.Errorf("URL validation: %w", err)
	}

	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := SafeHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
