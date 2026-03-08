package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type telegramConfig struct {
	BotToken        string `json:"bot_token"`
	ChatID          string `json:"chat_id"`
	ParseMode       string `json:"parse_mode"`
	MessageTemplate string `json:"message_template"`
}

var telegramTokenRE = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]+$`)

// TelegramDispatcher sends alert notifications via Telegram Bot API.
type TelegramDispatcher struct{}

func (d *TelegramDispatcher) Type() string { return "telegram" }

func (d *TelegramDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg telegramConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse telegram config: %w", err)
	}
	if cfg.BotToken == "" || cfg.ChatID == "" {
		return fmt.Errorf("telegram config: bot_token and chat_id required")
	}
	if !telegramTokenRE.MatchString(cfg.BotToken) {
		return fmt.Errorf("invalid telegram bot token format")
	}
	if cfg.ParseMode == "" {
		cfg.ParseMode = "HTML"
	}

	text := buildTelegramMessage(payload)
	if cfg.MessageTemplate != "" {
		if rendered, err := renderTemplate(cfg.MessageTemplate, payload); err == nil {
			text = rendered
		}
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	body := map[string]interface{}{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": cfg.ParseMode,
	}

	return postJSON(ctx, url, body)
}

func buildTelegramMessage(p AlertPayload) string {
	return fmt.Sprintf(
		"%s <b>%s Alert</b>\n\n<b>Rule:</b> %s\n<b>Resource:</b> %s\n<b>Metric:</b> %s %s %.2f\n<b>Current:</b> %.2f\n<b>State:</b> %s",
		severityEmoji(p.Severity),
		strings.ToUpper(p.Severity),
		p.RuleName,
		p.ResourceName,
		p.Metric, p.Operator, p.Threshold,
		p.CurrentValue,
		p.State,
	)
}
