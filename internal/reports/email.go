package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

type smtpConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
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

	// Resolve DNS and verify host does not point to a private address (SSRF protection).
	ips, err := net.DefaultResolver.LookupHost(ctx, cfg.Host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve SMTP host: %w", err)
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil && isPrivateOrReserved(ip) {
			return fmt.Errorf("SMTP host resolves to a private/loopback address")
		}
	}

	resolvedAddr := fmt.Sprintf("%s:%d", ips[0], cfg.Port)

	errCh := make(chan error, 1)
	go func() {
		var auth smtp.Auth
		if cfg.Username != "" {
			auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		}
		errCh <- smtp.SendMail(resolvedAddr, auth, cfg.From, toAddrs, []byte(msg))
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("smtp send: %w", err)
		}
		logger.Info("report email sent", "channel_id", channelID, "recipients", len(toAddrs))
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return fmt.Errorf("smtp send timed out")
	}
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func isPrivateOrReserved(ip net.IP) bool {
	// Normalize IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
