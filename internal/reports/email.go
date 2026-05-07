package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/notifications"
)

type smtpConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	TLS      *bool    `json:"tls,omitempty"`
}

func (c smtpConfig) useSTARTTLS() bool {
	if c.TLS == nil {
		return true
	}
	return *c.TLS
}

// SendReportEmail sends a generated report via an existing SMTP notification channel.
func SendReportEmail(ctx context.Context, queries *db.Queries, encryptionKey string, channelID uuid.UUID, recipients []string, subject, htmlBody string, logger *slog.Logger) error {
	channel, err := queries.GetNotificationChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("get email channel: %w", err)
	}
	if channel.ChannelType != "email" {
		return fmt.Errorf("channel %s is type %s, not email", channelID, channel.ChannelType)
	}

	configJSON, err := crypto.Decrypt(channel.ConfigEncrypted, encryptionKey)
	if err != nil {
		return fmt.Errorf("decrypt channel config: %w", err)
	}

	var cfg smtpConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("parse smtp config: %w", err)
	}
	if cfg.Host == "" {
		return fmt.Errorf("smtp host not configured")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}

	// Use provided recipients, fall back to channel's default To list.
	toAddrs := recipients
	if len(toAddrs) == 0 {
		toAddrs = cfg.To
	}
	if len(toAddrs) == 0 {
		return fmt.Errorf("no email recipients specified")
	}

	// Sanitize headers to prevent CRLF injection.
	safeFrom := sanitizeHeader(cfg.From)
	safeTo := sanitizeHeader(strings.Join(toAddrs, ", "))
	safeSubject := sanitizeHeader(subject)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"\r\n\r\n%s",
		safeFrom, safeTo, safeSubject, htmlBody)

	if err := notifications.SendSMTPMessage(ctx, notifications.SMTPSendOptions{
		Host:        cfg.Host,
		Port:        cfg.Port,
		Username:    cfg.Username,
		Password:    cfg.Password,
		From:        cfg.From,
		To:          toAddrs,
		Message:     []byte(msg),
		UseSTARTTLS: cfg.useSTARTTLS(),
	}); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}

	logger.Info("report email sent", "channel_id", channelID, "recipients", len(toAddrs))
	return nil
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}
