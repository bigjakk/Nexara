package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	return postJSON(ctx, url, telegramBody(cfg, payload))
}

// telegramBody is the sendMessage request.
func telegramBody(cfg telegramConfig, payload AlertPayload) map[string]interface{} {
	return map[string]interface{}{
		"chat_id":    cfg.ChatID,
		"text":       telegramText(cfg, payload),
		"parse_mode": cfg.ParseMode,
	}
}

// telegramText is a Telegram message's text: the channel's template, or the
// built-in text with the notification's message under it, escaped in HTML
// mode.
func telegramText(cfg telegramConfig, payload AlertPayload) string {
	if cfg.MessageTemplate != "" {
		if rendered, err := renderTemplate(cfg.MessageTemplate, payload); err == nil {
			return rendered
		}
	}
	var escape func(string) string
	if cfg.ParseMode == "HTML" {
		escape = html.EscapeString
	}
	return withMessage(buildTelegramMessage(payload, escape), payload, escape)
}

// buildTelegramMessage is the built-in text; escape, when set, is applied to
// every value in it, which in HTML mode a rule or resource name could
// otherwise break or inject markup into.
func buildTelegramMessage(p AlertPayload, escape func(string) string) string {
	if escape == nil {
		escape = func(s string) string { return s }
	}
	return fmt.Sprintf(
		"%s <b>%s Alert</b>\n\n<b>Rule:</b> %s\n<b>Resource:</b> %s\n<b>Metric:</b> %s %s %.2f\n<b>Current:</b> %.2f\n<b>State:</b> %s",
		severityEmoji(p.Severity),
		escape(strings.ToUpper(p.Severity)),
		escape(p.RuleName),
		escape(p.ResourceName),
		escape(p.Metric), escape(p.Operator), p.Threshold,
		p.CurrentValue,
		escape(p.State),
	)
}
