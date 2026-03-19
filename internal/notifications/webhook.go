package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type webhookConfig struct {
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	BodyTemplate   string            `json:"body_template"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// WebhookDispatcher sends alert notifications via generic HTTP webhook.
type WebhookDispatcher struct{}

func (d *WebhookDispatcher) Type() string { return "webhook" }

var allowedWebhookMethods = map[string]bool{
	"POST": true, "PUT": true, "PATCH": true,
}

func (d *WebhookDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg webhookConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse webhook config: %w", err)
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook config: url required")
	}
	if err := validateExternalURL(ctx, cfg.URL); err != nil {
		return fmt.Errorf("webhook URL validation: %w", err)
	}
	if cfg.Method == "" {
		cfg.Method = "POST"
	}
	if !allowedWebhookMethods[cfg.Method] {
		return fmt.Errorf("webhook method must be POST, PUT, or PATCH")
	}
	timeout := 10
	if cfg.TimeoutSeconds > 0 && cfg.TimeoutSeconds <= 30 {
		timeout = cfg.TimeoutSeconds
	}

	var bodyBytes []byte
	if cfg.BodyTemplate != "" {
		rendered, err := renderTemplate(cfg.BodyTemplate, payload)
		if err != nil {
			return fmt.Errorf("render body template: %w", err)
		}
		bodyBytes = []byte(rendered)
	} else {
		var err error
		bodyBytes, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	client := SafeHTTPClient(time.Duration(timeout) * time.Second)
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
